// Package identity извлекает и проверяет node identity из сертификата.
//
// Источник идентичности пира — только URI SAN сертификата
// (przewalski://node/<uuid>), никогда не поля протокола и не CN.
package identity

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// UriScheme и UriPathPrefix описывают формат URI SAN из ADR-0006.
const (
	UriScheme     = "przewalski"
	UriPathPrefix = "/node/"
)

var (
	// ErrNoURISAN — в серте нет URI SAN przewalski://node/<uuid>.
	ErrNoURISAN = errors.New("identity: сертификат не содержит URI SAN przewalski://node/")
	// ErrEmptyNodeID — URI SAN есть, но node_id внутри пуст.
	ErrEmptyNodeID = errors.New("identity: URI SAN не содержит node_id")
)

// NodeIDFromCert извлекает node_id из URI SAN сертификата.
// Ожидается ровно один URI SAN вида przewalski://node/<uuid>.
func NodeIDFromCert(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", ErrNoURISAN
	}
	for _, u := range cert.URIs {
		if u.Scheme != UriScheme {
			continue
		}
		// przewalski://node/<id> парсится как Host="node", Path="/<id>".
		// Подстраховываемся и от opaque-формы przewalski:node/<id>.
		var id string
		switch {
		case u.Host == "node" && strings.HasPrefix(u.Path, "/"):
			id = strings.TrimPrefix(u.Path, "/")
		case u.Opaque != "" && strings.HasPrefix(u.Opaque, "node/"):
			id = strings.TrimPrefix(u.Opaque, "node/")
		default:
			continue
		}
		// Декодируем на случай percent-encoding в Path.
		if decoded, err := url.PathUnescape(id); err == nil {
			id = decoded
		}
		if id == "" {
			return "", ErrEmptyNodeID
		}
		return id, nil
	}
	return "", ErrNoURISAN
}

// VerifyPeerNodeID проверяет, что сертификат пира содержит ожидаемый node_id
// в URI SAN. Идентичность пира узнаётся только из серта, не из протокола.
func VerifyPeerNodeID(cert *x509.Certificate, expectedNodeID string) error {
	if expectedNodeID == "" {
		return errors.New("identity: пустой ожидаемый node_id")
	}
	got, err := NodeIDFromCert(cert)
	if err != nil {
		return err
	}
	if got != expectedNodeID {
		return fmt.Errorf("identity: node_id пира %q не совпадает с ожидаемым %q", got, expectedNodeID)
	}
	return nil
}
