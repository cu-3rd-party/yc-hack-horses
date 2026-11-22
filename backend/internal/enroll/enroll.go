// Package enroll: автоматический enrollment нод (ADR-0007 amended, ADR-0014).
//
// Central-нода с включённым enrollment (конфиг enrollment.enabled +
// enrollment.token) принимает POST /api/v1/enroll: клиент присылает CSR
// (CN=node_id, URI SAN przewalski://node/<node_id>) и enrollment-токен.
// Сервер валидирует токен и CSR, подписывает серт CA-ключом
// (enrollment.ca_key_file), срок жизни leaf-серта — 365 дней, и записывает
// выдачу в on-disk список (задел под revocation list из ADR-0007).
//
// Клиентская сторона (команда `node enroll`): генерация ключа и CSR, POST на
// central, сохранение cert/key/ca. Если серт уже выдан и жив — повторный
// запрос не выполняется; пере-enroll — за 7 дней до конца срока.
package enroll

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
)

// CertDaysDefault — срок жизни выдаваемого enrollment-серта: 365 дней
// (ADR-0007 в редакции amended; leaf-серты enrollment-нод — 1 год).
const CertDaysDefault = 365

// RenewBefore — за сколько до конца срока клиент должен пере-enroll'иться.
const RenewBefore = 7 * 24 * time.Hour

// MinKeyBits — минимальный размер ключа CSR (RSA).
const MinKeyBits = 2048

var (
	// ErrBadToken — неверный enrollment-токен.
	ErrBadToken = errors.New("enroll: неверный enrollment-токен")
	// ErrBadCSR — CSR не прошёл валидацию (подпись, CN, URI SAN, ключ).
	ErrBadCSR = errors.New("enroll: CSR не прошёл валидацию")
	// ErrAlreadyValid — действующий серт уже есть, пере-enroll не требуется.
	ErrAlreadyValid = errors.New("enroll: действующий серт уже выдан")
)

// GenerateKey генерирует RSA-ключ ноды (3072 бит — как в dev-сертификатах).
func GenerateKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 3072)
}

// NodeURI возвращает URI SAN przewalski://node/<node_id> (ADR-0006).
func NodeURI(nodeID string) (*url.URL, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("%w: пустой node_id", ErrBadCSR)
	}
	for _, r := range nodeID {
		if r <= ' ' || r >= 0x7f {
			return nil, fmt.Errorf("%w: node_id %q содержит недопустимые символы", ErrBadCSR, nodeID)
		}
	}
	return url.Parse("przewalski://node/" + nodeID)
}

// NewCSR строит CSR: CN = node_id, URI SAN przewalski://node/<node_id>
// (ADR-0006). Привязка node_id на сервере — к URI SAN, не к CN.
func NewCSR(key *rsa.PrivateKey, nodeID string) ([]byte, error) {
	uri, err := NodeURI(nodeID)
	if err != nil {
		return nil, err
	}
	template := &x509.CertificateRequest{
		Subject:            pkix.Name{CommonName: nodeID, Organization: []string{"Przewalski"}},
		SignatureAlgorithm: x509.SHA256WithRSA,
		URIs:               []*url.URL{uri},
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		return nil, fmt.Errorf("enroll: create CSR: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// ParseCSR разбирает PEM CSR и проверяет: самоподписан корректным ключом,
// CN = node_id, единственный URI SAN przewalski://node/<node_id>, ключ
// достаточно длинный. node_id сервер привязывает к URI SAN (ADR-0006).
func ParseCSR(csrPEM []byte, nodeID string) (*x509.CertificateRequest, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("%w: пустой node_id", ErrBadCSR)
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("%w: не PEM CERTIFICATE REQUEST", ErrBadCSR)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: разбор CSR: %v", ErrBadCSR, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: подпись CSR не совпадает с ключом: %v", ErrBadCSR, err)
	}
	if key, ok := csr.PublicKey.(*rsa.PublicKey); ok && key.N.BitLen() < MinKeyBits {
		return nil, fmt.Errorf("%w: ключ короче %d бит", ErrBadCSR, MinKeyBits)
	}
	if csr.Subject.CommonName != nodeID {
		return nil, fmt.Errorf("%w: CN %q не совпадает с node_id %q", ErrBadCSR, csr.Subject.CommonName, nodeID)
	}
	uri, err := NodeURI(nodeID)
	if err != nil {
		return nil, err
	}
	if len(csr.URIs) != 1 || csr.URIs[0].String() != uri.String() {
		got := ""
		if len(csr.URIs) == 1 {
			got = csr.URIs[0].String()
		}
		return nil, fmt.Errorf("%w: URI SAN %q, ожидается %q", ErrBadCSR, got, uri.String())
	}
	return csr, nil
}

// SignCSR подписывает проверенный CSR доверенным CA: leaf-серт clientAuth,
// срок days дней (<=0 — CertDaysDefault, 365). Возвращает PEM.
func SignCSR(caCert *x509.Certificate, caKey any, csr *x509.CertificateRequest, days int, now time.Time) ([]byte, error) {
	if days <= 0 {
		days = CertDaysDefault
	}
	nodeID, err := identity.NodeIDFromCSR(csr)
	if err != nil {
		return nil, fmt.Errorf("enroll: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: подпись CSR не совпадает с ключом: %v", ErrBadCSR, err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return nil, fmt.Errorf("enroll: serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: nodeID, Organization: []string{"Przewalski"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		URIs:                  csr.URIs,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caCert, csr.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("enroll: sign: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}
