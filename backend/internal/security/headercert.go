// Клиентский сертификат из HTTP-заголовка ingress (режим grpc_multiplex).
//
// Центральная нода за Traefik ingress принимает gRPC на том же порту, что и
// REST (h2c-мультиплекс, network.grpc_multiplex). TLS терминируется на
// ingress, поэтому mTLS-рукопожатие до бэкенда не доходит; вместо этого
// Traefik-middleware passTLSClientCert (pem: true) прокидывает клиентский
// сертификат в заголовок X-Forwarded-Tls-Client-Cert.
//
// Заголовок — доверенный только от ingress: Traefik сам выставляет его
// значение из состояния TLS-соединения (сёрт, предъявленный клиентом),
// значение, подделанное самим клиентом, перезаписывается. Прямой доступ к
// мультиплекс-порту возможен только внутри кластера; подделка заголовка там
// бесполезна без валидного серта, подписанного dev-CA (security.ca_file).
package security

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
)

// ClientCertHeaderName — заголовок, в который Traefik passTLSClientCert
// (pem: true) кладёт PEM клиентского сертификата.
const ClientCertHeaderName = "X-Forwarded-Tls-Client-Cert"

// ErrNoClientCert — в заголовке нет сертификата (анонимное соединение).
var ErrNoClientCert = errors.New("security: заголовок " + ClientCertHeaderName + " не содержит сертификат")

// ParseClientCertHeader разбирает значение заголовка ClientCertHeaderName в
// сертификат. Traefik экранирует переводы строк в PEM (значение заголовка не
// может содержать реальные "\n"), поэтому поддерживаем экранированную форму
// (literal \n / \r) и URL-кодирование.
func ParseClientCertHeader(header string) (*x509.Certificate, error) {
	raw := unescapeHeaderPEM(header)
	if raw == "" {
		return nil, ErrNoClientCert
	}
	block, _ := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("security: %s: не PEM CERTIFICATE", ClientCertHeaderName)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("security: %s: разбор сертификата: %w", ClientCertHeaderName, err)
	}
	return cert, nil
}

// unescapeHeaderPEM восстанавливает переводы строк, экранированные Traefik
// (\n, \r) или URL-кодированием (%0A, %0D, %2B — "+", который в PEM
// base64 встречается только в URL-закодированной форме).
func unescapeHeaderPEM(header string) string {
	s := strings.TrimSpace(header)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "\\n") || strings.Contains(s, "\\r") {
		s = strings.ReplaceAll(s, `\r`, "")
		s = strings.ReplaceAll(s, `\n`, "\n")
		return s
	}
	if !strings.Contains(s, "\n") {
		// Возможно URL-кодированная форма.
		s = strings.ReplaceAll(s, "%0D%0A", "\n")
		s = strings.ReplaceAll(s, "%0A", "\n")
		s = strings.ReplaceAll(s, "%0d%0a", "\n")
		s = strings.ReplaceAll(s, "%0a", "\n")
	}
	return s
}
