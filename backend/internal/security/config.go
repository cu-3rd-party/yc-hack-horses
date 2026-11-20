// Package security — TLS/mTLS-конфигурация, capabilities и central-идентификация.
package security

import "errors"

// Config — TLS-параметры ноды (соответствуют секции security в config.yaml).
// Пакет не зависит от internal/config: транспорт сам переносит значения.
type Config struct {
	// CertFile и KeyFile — клиентский (и серверный для межнодовго gRPC) серт ноды.
	CertFile string
	KeyFile  string
	// CAFile — CA, которому доверяем пиров (mTLS). Может быть пуст: тогда
	// для проверки серверных сертов используется системный пул (см. VerifyCentralIdentity).
	CAFile string
	// CentralHostname — hostname, на котором ожидается central.
	CentralHostname string
}

// CentralHostnameDefault — имя central из ADR-0008.
const CentralHostnameDefault = "yc-hack-horses.cu3rd.ru"

// Validate требует наличия mTLS-материала для межнодовых соединений.
func (c Config) Validate() error {
	if c.CertFile == "" || c.KeyFile == "" {
		return errors.New("security: cert_file и key_file обязательны для mTLS")
	}
	if c.CAFile == "" {
		return errors.New("security: ca_file обязателен — self-signed auto-trust запрещён (ADR-0007)")
	}
	return nil
}
