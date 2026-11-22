// Клиентская сторона enrollment: статус локального серта, генерация CSR,
// запрос к central и сохранение выданного материала.
package enroll

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
	"github.com/google/uuid"
)

// ClientConfig — параметры клиентского enrollment (для команды `node enroll`
// и её вызова из Electron-хоста).
type ClientConfig struct {
	// CentralURL — адрес central (тот же, что sync.central_url).
	CentralURL string
	// DataDir — каталог данных ноды: node.json, cert.pem/key.pem/ca.pem.
	DataDir string
	// CertFile/KeyFile/CAFile — пути сохранения (пусто — по умолчанию в DataDir).
	CertFile string
	KeyFile  string
	CAFile   string
	// HTTPClient — подменяется в тестах; nil — с системным пулом CA
	// (соединение с central защищено публичным серверным сертом, ADR-0008).
	HTTPClient *http.Client
}

// CertPath возвращает путь к клиентскому сертификату.
func (c ClientConfig) CertPath() string {
	if c.CertFile != "" {
		return c.CertFile
	}
	return filepath.Join(c.DataDir, "cert.pem")
}

// KeyPath возвращает путь к ключу клиента.
func (c ClientConfig) KeyPath() string {
	if c.KeyFile != "" {
		return c.KeyFile
	}
	return filepath.Join(c.DataDir, "key.pem")
}

// CAPath возвращает путь к CA, которому доверяет нода.
func (c ClientConfig) CAPath() string {
	if c.CAFile != "" {
		return c.CAFile
	}
	return filepath.Join(c.DataDir, "ca.pem")
}

// Status — состояние enrollment локальной ноды. Отдаётся подкомандой
// `node enroll --status` в JSON — его читает Electron-хост.
type Status struct {
	// Enrolled — серт существует и разбирается.
	Enrolled bool `json:"enrolled"`
	// NodeID — node_id из URI SAN серта (пусто, если серта нет).
	NodeID string `json:"node_id,omitempty"`
	// NotAfter — конец срока действия серта.
	NotAfter *time.Time `json:"not_after,omitempty"`
	// NeedsReenroll — серта нет, он истёк или до конца срока меньше
	// RenewBefore (7 дней).
	NeedsReenroll bool `json:"needs_reenroll"`
	// Reason — человекочитаемая причина NeedsReenroll.
	Reason string `json:"reason,omitempty"`
}

// StatusOf проверяет файл серта (без сети).
func StatusOf(certFile string, now time.Time) Status {
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		return Status{NeedsReenroll: true, Reason: "сертификат не найден"}
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return Status{NeedsReenroll: true, Reason: "файл сертификата не содержит CERTIFICATE"}
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Status{NeedsReenroll: true, Reason: "разбор сертификата: " + err.Error()}
	}
	nodeID, idErr := identity.NodeIDFromCert(cert)
	if idErr != nil {
		return Status{NeedsReenroll: true, Reason: "в сертификате нет URI SAN przewalski://node/"}
	}
	if now.After(cert.NotAfter) {
		return Status{Enrolled: true, NodeID: nodeID, NotAfter: &cert.NotAfter,
			NeedsReenroll: true, Reason: "срок действия сертификата истёк"}
	}
	if cert.NotAfter.Sub(now) < RenewBefore {
		return Status{Enrolled: true, NodeID: nodeID, NotAfter: &cert.NotAfter,
			NeedsReenroll: true, Reason: "до конца срока действия меньше 7 дней"}
	}
	return Status{Enrolled: true, NodeID: nodeID, NotAfter: &cert.NotAfter, NeedsReenroll: false}
}

// Request/Response — формат POST /api/v1/enroll.
type Request struct {
	NodeID string `json:"node_id"`
	CSR    string `json:"csr"`   // PEM CERTIFICATE REQUEST
	Token  string `json:"token"` // enrollment-токен
}

type Response struct {
	NodeCert  string `json:"node_cert"` // PEM CERTIFICATE (leaf)
	CACert    string `json:"ca_cert"`   // PEM CERTIFICATE (CA, для удобства)
	NodeID    string `json:"node_id"`
	ExpiresAt string `json:"expires_at"` // RFC3339
}

// Enroll выполняет полный цикл: проверка текущего серта → генерация ключа и
// CSR → POST → сохранение файлов. Если серт жив и пере-enroll не нужен —
// возвращает текущий статус без сети (ErrAlreadyValid в поле Status).
func Enroll(ctx context.Context, cfg ClientConfig, token string, now time.Time) (Status, error) {
	if st := StatusOf(cfg.CertPath(), now); !st.NeedsReenroll {
		return st, ErrAlreadyValid
	}
	if token == "" {
		return Status{NeedsReenroll: true, Reason: "не задан enrollment-токен"}, ErrBadToken
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: mkdir data dir: %w", err)
	}
	nodeID, err := LoadOrCreateNodeID(cfg.DataDir)
	if err != nil {
		return Status{NeedsReenroll: true}, err
	}
	key, err := GenerateKey()
	if err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: %w", err)
	}
	csrPEM, err := NewCSR(key, nodeID)
	if err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: %w", err)
	}
	resp, err := postEnroll(ctx, cfg, Request{NodeID: nodeID, CSR: string(csrPEM), Token: token})
	if err != nil {
		return Status{NeedsReenroll: true, Reason: "central недоступна: " + err.Error()}, err
	}
	nodePEM, expires, err := decodeCert(resp.NodeCert)
	if err != nil {
		return Status{NeedsReenroll: true}, err
	}
	caPEM, _, err := decodeCert(resp.CACert)
	if err != nil {
		return Status{NeedsReenroll: true}, err
	}
	// Проверяем, что выданный серт действительно за node_id и подписан CA.
	if err := checkIssuedCert(nodePEM, caPEM, nodeID, now); err != nil {
		return Status{NeedsReenroll: true}, err
	}
	if err := os.WriteFile(cfg.KeyPath(), pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600); err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: write key: %w", err)
	}
	if err := os.WriteFile(cfg.CertPath(), nodePEM, 0o644); err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: write cert: %w", err)
	}
	if err := os.WriteFile(cfg.CAPath(), caPEM, 0o644); err != nil {
		return Status{NeedsReenroll: true}, fmt.Errorf("enroll: write ca: %w", err)
	}
	slog.Info("enroll: сертификат получен", "node_id", nodeID, "not_after", expires.Format(time.RFC3339))
	return Status{Enrolled: true, NodeID: nodeID, NotAfter: &expires, NeedsReenroll: false}, nil
}

// checkIssuedCert убеждается, что выдача соответствует запросу: URI SAN —
// наш node_id, серт валиден и подписан переданным CA.
func checkIssuedCert(nodePEM, caPEM []byte, nodeID string, now time.Time) error {
	nodeCert, err := parseCert(nodePEM)
	if err != nil {
		return err
	}
	gotID, err := identity.NodeIDFromCert(nodeCert)
	if err != nil || gotID != nodeID {
		return fmt.Errorf("enroll: выданный серт не соответствует node_id %s", nodeID)
	}
	if now.After(nodeCert.NotAfter) {
		return fmt.Errorf("enroll: central выдала уже истёкший серт")
	}
	caCert, err := parseCert(caPEM)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := nodeCert.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}); err != nil {
		return fmt.Errorf("enroll: выданный серт не проходит проверку против CA: %v", err)
	}
	return nil
}

func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("enroll: не PEM CERTIFICATE")
	}
	return x509.ParseCertificate(block.Bytes)
}

func decodeCert(pemStr string) ([]byte, time.Time, error) {
	pemBytes := []byte(pemStr)
	cert, err := parseCert(pemBytes)
	if err != nil {
		return nil, time.Time{}, err
	}
	return pemBytes, cert.NotAfter, nil
}

// postEnroll отправляет запрос enrollment на central. Ошибка HTTP-статуса
// с телом message возвращается как есть (для показа пользователю).
func postEnroll(ctx context.Context, cfg ClientConfig, req Request) (*Response, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	url := cfg.CentralURL + "/api/v1/enroll"
	client := cfg.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer httpResp.Body.Close() //nolint:errcheck // тело только читается
	var apiErr struct {
		Message string `json:"message"`
	}
	dec := json.NewDecoder(httpResp.Body)
	if httpResp.StatusCode != http.StatusOK {
		_ = dec.Decode(&apiErr)
		if apiErr.Message != "" {
			return nil, fmt.Errorf("enroll: central: %d: %s", httpResp.StatusCode, apiErr.Message)
		}
		return nil, fmt.Errorf("enroll: central: %d", httpResp.StatusCode)
	}
	var resp Response
	if err := dec.Decode(&resp); err != nil {
		return nil, fmt.Errorf("enroll: ответ central: %w", err)
	}
	return &resp, nil
}

// LoadOrCreateNodeID читает <data_dir>/node.json или создаёт новый UUIDv7.
// Формат файла — тот же, что использует app.loadOrCreateNodeID: нода, запущенная
// после enrollment, подхватит тот же идентификатор.
func LoadOrCreateNodeID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "node.json")
	if data, err := os.ReadFile(path); err == nil {
		var stored struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.ID != "" {
			return stored.ID, nil
		}
	}
	uid, err := uuid.NewV7()
	id := uid.String()
	if err != nil {
		return "", fmt.Errorf("enroll: generate node id: %w", err)
	}
	data, _ := json.MarshalIndent(map[string]string{"id": id}, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("enroll: persist node id: %w", err)
	}
	return id, nil
}
