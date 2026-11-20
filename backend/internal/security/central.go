package security

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// ErrNotCentral — соединение не даёт central-возможностей.
var ErrNotCentral = errors.New("security: соединение не является central")

// CentralCapabilities — полномочия central-ноды. В отличие от
// NodeCapabilities выдаются только после VerifyCentralIdentity.
type CentralCapabilities struct {
	// Confirmed — true только при успешной проверке hostname+серта.
	Confirmed bool
	// Hostname — проверенное имя сервера.
	Hostname string
	// ServerCertValid — серверный серт прошёл проверку публичного CA.
	ServerCertValid bool
}

// VerifyCentralIdentity проверяет, что подключение к central действительно
// central (ADR-0008): (1) hostname — central-домен; (2) серверный серт
// валиден против caPool (при nil — системный пул публичных CA);
// (3) DNS SAN серта соответствует hostname.
//
// Возвращает capabilities и ошибку; при любом несовпадении — нулевые
// capabilities и ErrNotCentral.
func VerifyCentralIdentity(serverName string, certChain []*x509.Certificate, caPool *x509.CertPool, now time.Time) (CentralCapabilities, error) {
	hostname := CentralHostnameDefault
	if serverName != "" {
		hostname = serverName
	}
	if hostname != CentralHostnameDefault {
		return CentralCapabilities{}, fmt.Errorf("%w: hostname %q не является central", ErrNotCentral, serverName)
	}
	if len(certChain) == 0 {
		return CentralCapabilities{}, fmt.Errorf("%w: сервер не предъявил сертификат", ErrNotCentral)
	}
	roots := caPool
	if roots == nil {
		var err error
		roots, err = x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
	}
	leaf := certChain[0]
	// Intermediates нужны, если в цепочке не только лист.
	intermediates := x509.NewCertPool()
	for _, c := range certChain[1:] {
		intermediates.AddCert(c)
	}
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		DNSName:       hostname,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if _, err := leaf.Verify(opts); err != nil {
		return CentralCapabilities{}, fmt.Errorf("%w: серверный серт не прошёл проверку: %v", ErrNotCentral, err)
	}
	return CentralCapabilities{
		Confirmed:       true,
		Hostname:        hostname,
		ServerCertValid: true,
	}, nil
}

// RejectLocalCentral защищает от превращения локальной ноды в central через
// конфигурацию: central hostname + публично валидный серверный серт выдаются
// только соединению, у которого серверный серт реально подписан публичным CA
// на central-имя. Локальная нода со self-signed сертом не проходит проверку
// даже если в её конфиге central_hostname совпадает с доменом central.
//
// cfg — конфиг ноды; serverCert — серт, который нода смогла бы предъявить
// в роли «сервера central». Функция всегда отклоняет local-ноду, потому что
// центральные возможности не выдаются конфигом: источником полномочий
// является только криптографическая проверка в VerifyCentralIdentity.
func RejectLocalCentral(serverCert *x509.Certificate, caPool *x509.CertPool, now time.Time) error {
	// Даже при совпадении hostname в конфиге локальный серт не подписан
	// публичным CA на central-имя, поэтому проверка обязана упасть.
	caps, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{serverCert}, caPool, now)
	if err == nil && caps.Confirmed {
		// Теоретически невозможно: local-серт не выпущен публичным CA.
		return errors.New("security: локальный серт unexpectedly прошёл central-проверку")
	}
	return nil
}

// ClientTLSConfigForCentral — клиентская tls.Config для подключения
// к central: ServerName фиксирован (SNI = central hostname), чтобы
// hostname-проверку нельзя было обойти подменой адреса подключения.
func ClientTLSConfigForCentral(cfg Config, caPool *x509.CertPool) (*tls.Config, error) {
	if cfg.CertFile == "" || cfg.KeyFile == "" {
		return nil, errors.New("security: cert_file и key_file обязательны для mTLS")
	}
	pair, err := loadCertPair(cfg)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{pair},
		RootCAs:      caPool,
		ServerName:   CentralHostnameDefault,
	}, nil
}
