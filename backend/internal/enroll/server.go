// Серверная сторона enrollment: выдача клиентских сертификатов доверенным CA
// и on-disk список выданных сертов (задел под revocation list, ADR-0007).
package enroll

import (
	"bytes"
	"crypto"
	"crypto/subtle"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ServerConfig — параметры enrollment-сервиса central-ноды (секция
// enrollment конфига). Включается только на central; на локальных нодах
// enrollment.enabled = false.
type ServerConfig struct {
	// Token — enrollment-токен (env ENROLLMENT_TOKEN или конфиг).
	Token string
	// CAFile — CA-серт, которым подписываются выдачи (тот же, что
	// security.ca_file: клиенты доверяют ему в проверке цепочки).
	CAFile string
	// CAKeyFile — приватный ключ CA (PEM PKCS#1/PKCS#8).
	CAKeyFile string
	// CertDays — срок жизни выдаваемого серта (0 — CertDaysDefault, 365).
	CertDays int
	// DataDir — каталог для issued-списка (выдачи сериализуются в файл
	// enrollment-issued.jsonl — задел под revocation list из ADR-0007).
	DataDir string
	// Clock подменяется в тестах; nil — time.Now.
	Clock func() time.Time
}

// Service — enrollment-сервис central-ноды.
type Service struct {
	cfg    ServerConfig
	caCert *x509.Certificate
	caKey  crypto.PrivateKey

	mu     sync.Mutex
	issued []IssuedRecord
}

// IssuedRecord — запись о выданном серте (issued-список).
type IssuedRecord struct {
	IssuedAt time.Time `json:"issued_at"`
	NodeID   string    `json:"node_id"`
	Serial   string    `json:"serial"`
	NotAfter time.Time `json:"not_after"`
}

// issuedFileName — имя on-disk списка выданных сертов.
const issuedFileName = "enrollment-issued.jsonl"

// ErrNotConfigured — сервис не сконфигурирован (внутренняя ошибка wiring'а).
var ErrNotConfigured = errors.New("enroll: сервис не сконфигурирован")

// NewService загружает CA-серт и ключ, восстанавливает issued-список.
func NewService(cfg ServerConfig) (*Service, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("enroll: пустой enrollment-токен")
	}
	if cfg.CertDays <= 0 {
		cfg.CertDays = CertDaysDefault
	}
	caPEM, err := os.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("enroll: read ca_file: %w", err)
	}
	caCert, err := parseCert(caPEM)
	if err != nil {
		return nil, fmt.Errorf("enroll: ca_file: %w", err)
	}
	keyPEM, err := os.ReadFile(cfg.CAKeyFile)
	if err != nil {
		return nil, fmt.Errorf("enroll: read ca_key_file: %w", err)
	}
	caKey, err := parsePrivateKey(keyPEM)
	if err != nil {
		return nil, fmt.Errorf("enroll: ca_key_file: %w", err)
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, fmt.Errorf("enroll: mkdir data dir: %w", err)
	}
	s := &Service{cfg: cfg, caCert: caCert, caKey: caKey}
	if err := s.loadIssued(); err != nil {
		return nil, err
	}
	return s, nil
}

// parsePrivateKey принимает PKCS#1 и PKCS#8 PEM (как выдаёт openssl genrsa
// и export из разных инструментов).
func parsePrivateKey(pemBytes []byte) (crypto.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("не PEM PRIVATE KEY")
	}
	switch block.Type {
	case "RSA PRIVATE KEY":
		return x509.ParsePKCS1PrivateKey(block.Bytes)
	case "PRIVATE KEY":
		return x509.ParsePKCS8PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		return x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("неизвестный тип PEM: %s", block.Type)
	}
}

// loadIssued читает выданные ранее серты (если файл есть).
func (s *Service) loadIssued() error {
	data, err := os.ReadFile(s.issuedPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("enroll: read issued list: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	for dec.More() {
		var rec IssuedRecord
		if err := dec.Decode(&rec); err != nil {
			return fmt.Errorf("enroll: parse issued list: %w", err)
		}
		s.issued = append(s.issued, rec)
	}
	return nil
}

func (s *Service) issuedPath() string {
	return filepath.Join(s.cfg.DataDir, issuedFileName)
}

// recordIssued дописывает выдачу в on-disk список. Файл — журнал
// (append-only JSONL): платформа revocation list (ADR-0007) построится
// поверх серийных номеров отсюда.
func (s *Service) recordIssued(rec IssuedRecord) error {
	s.mu.Lock()
	s.issued = append(s.issued, rec)
	s.mu.Unlock()
	f, err := os.OpenFile(s.issuedPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("enroll: open issued list: %w", err)
	}
	defer f.Close() //nolint:errcheck // запись уже сделана; закрытие не меняет результат
	if err := json.NewEncoder(f).Encode(rec); err != nil {
		return fmt.Errorf("enroll: append issued list: %w", err)
	}
	return nil
}

// Issued — снимок выданных сертов (для тестов и будущей ревокации).
func (s *Service) Issued() []IssuedRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]IssuedRecord, len(s.issued))
	copy(out, s.issued)
	return out
}

// Enroll валидирует токен и CSR, подписывает серт и возвращает PEM leaf и CA.
// node_id привязывается к URI SAN CSR — бремя совпадения CN/SAN проверяется
// здесь, а не принимается на веру из поля запроса.
func (s *Service) Enroll(nodeID, csrPEM, token string) (nodeCertPEM, caCertPEM []byte, expires time.Time, err error) {
	if s == nil {
		return nil, nil, time.Time{}, ErrNotConfigured
	}
	now := time.Now()
	if s.cfg.Clock != nil {
		now = s.cfg.Clock()
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) != 1 {
		return nil, nil, time.Time{}, ErrBadToken
	}
	csr, err := ParseCSR([]byte(csrPEM), nodeID)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	nodePEM, err := SignCSR(s.caCert, s.caKey, csr, s.cfg.CertDays, now)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	cert, err := parseCert(nodePEM)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if err := s.recordIssued(IssuedRecord{
		IssuedAt: now,
		NodeID:   nodeID,
		Serial:   cert.SerialNumber.Text(16),
		NotAfter: cert.NotAfter,
	}); err != nil {
		return nil, nil, time.Time{}, err
	}
	return nodePEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: s.caCert.Raw}),
		cert.NotAfter, nil
}
