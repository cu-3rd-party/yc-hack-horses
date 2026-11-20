package security

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
)

// MinTLSVersion — минимальная версия TLS для межнодовых соединений.
const MinTLSVersion = tls.VersionTLS12

// loadCertPair читает серт и ключ ноды с диска.
func loadCertPair(cfg Config) (tls.Certificate, error) {
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("security: загрузка cert/key: %w", err)
	}
	return cert, nil
}

// LoadCAPool читает CA-файл; при пустом пути возвращает nil
// (вызывающий решает, использовать ли системный пул).
func LoadCAPool(caFile string) (*x509.CertPool, error) {
	if caFile == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("security: чтение ca_file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errors.New("security: ca_file не содержит ни одного валидного сертификата")
	}
	return pool, nil
}

// verifyChain — полная проверка цепочки: доверие, срок действия, key usage.
func verifyChain(cert *x509.Certificate, caPool *x509.CertPool, now time.Time) error {
	opts := x509.VerifyOptions{
		Roots:       caPool,
		CurrentTime: now,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if _, err := cert.Verify(opts); err != nil {
		return fmt.Errorf("security: проверка цепочки сертификата: %w", err)
	}
	return nil
}

// peerCertVerify — callback для VerifyPeerCertificate: после штатной
// проверки цепочки (VerifyClientCertificate/VerifyConnection) дополнительно
// проверяем URI SAN/node_id. rawCerts — исходные DER-сертикаты пира.
func peerCertVerify(expectedNodeID string) func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
		if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
			if len(rawCerts) == 0 {
				return errors.New("security: пир не предъявил сертификат")
			}
			// Цепочку по каким-то причинам не проверили — проверяем DER сами нельзя
			// без пула; соединение запрещаем (fail closed).
			return errors.New("security: сертификат пира не прошёл проверку цепочки")
		}
		// Callback вызывается после штатной проверки цепочки (ClientCAs +
		// RequireAndVerifyClientCert), поэтому здесь остаётся привязка к node_id.
		leaf := verifiedChains[0][0]
		if expectedNodeID == "" {
			// Без ожидаемого пира: достаточно наличия валидного przewalski URI SAN.
			_, err := identity.NodeIDFromCert(leaf)
			return err
		}
		return identity.VerifyPeerNodeID(leaf, expectedNodeID)
	}
}

// LoadServerTLSConfig строит tls.Config для gRPC-сервера ноды: mTLS
// (клиентский серт обязателен и проверяется), клиент должен предъявить
// серт с przewalski://node/<uuid> SAN. expectedNodeID может быть пуст —
// тогда проверяется только валидность и наличие node_id.
func LoadServerTLSConfig(cfg Config, expectedNodeID string) (*tls.Config, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	caPool, err := LoadCAPool(cfg.CAFile)
	if err != nil {
		return nil, err
	}
	pair, err := loadCertPair(cfg)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{
		MinVersion:            MinTLSVersion,
		Certificates:          []tls.Certificate{pair},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             caPool,
		VerifyPeerCertificate: peerCertVerify(expectedNodeID),
	}
	return tc, nil
}

// LoadClientTLSConfig строит tls.Config для gRPC-клиента ноды: предъявляет
// свой серт (mTLS), проверяет серверный серт против caPool и имя сервера
// (ServerName — DNS SAN или URI SAN). serverName — адрес/hostname сервера.
func LoadClientTLSConfig(cfg Config, serverName string) (*tls.Config, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	caPool, err := LoadCAPool(cfg.CAFile)
	if err != nil {
		return nil, err
	}
	pair, err := loadCertPair(cfg)
	if err != nil {
		return nil, err
	}
	tc := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{pair},
		RootCAs:      caPool,
		ServerName:   serverName,
	}
	return tc, nil
}
