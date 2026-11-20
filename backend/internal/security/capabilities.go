package security

import (
	"crypto/x509"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
)

// NodeCapabilities — полномочия ноды на соединении. Выдаются только
// по результатам проверки сертификата; конфиг и поля протокола полномочий
// не выдают.
type NodeCapabilities uint8

const (
	// CapNone — неизвестный или невалидный серт: доступ к данным запрещён.
	CapNone          NodeCapabilities = 0
	CapSendEvents    NodeCapabilities = 1 << iota // может пушить события
	CapReceiveEvents                              // может тянуть события
	CapRegisterPeers                              // может регистрировать других пиров
)

// Has возвращает true, если набор содержит все перечисленные биты.
func (c NodeCapabilities) Has(other NodeCapabilities) bool {
	return c&other == other
}

// String — человекочитаемое имя набора (для логов).
func (c NodeCapabilities) String() string {
	if c == CapNone {
		return "none"
	}
	names := []struct {
		bit  NodeCapabilities
		name string
	}{
		{CapSendEvents, "send_events"},
		{CapReceiveEvents, "receive_events"},
		{CapRegisterPeers, "register_peers"},
	}
	out := ""
	for _, n := range names {
		if c.Has(n.bit) {
			if out != "" {
				out += ","
			}
			out += n.name
		}
	}
	return out
}

// CapabilitiesForCert вычисляет полномочия по клиентскому сертификату ноды:
// цепочка должна быть доверенной (caPool), серт — действителен в момент now,
// URI SAN должен разбираться в node_id. Любая ошибка — CapNone.
//
// expectedNodeID — опциональная привязка к ожидаемому пиру ("" — не проверять).
func CapabilitiesForCert(cert *x509.Certificate, caPool *x509.CertPool, now time.Time, expectedNodeID string) NodeCapabilities {
	if cert == nil || caPool == nil {
		return CapNone
	}
	if err := verifyChain(cert, caPool, now); err != nil {
		return CapNone
	}
	nodeID, err := identity.NodeIDFromCert(cert)
	if err != nil {
		return CapNone
	}
	if expectedNodeID != "" && nodeID != expectedNodeID {
		return CapNone
	}
	return nodeCapabilitiesFor(nodeID)
}

// nodeCapabilitiesFor выдаёт полномочия известной ноде. Любая нода с валидным
// сертом может отправлять и получать события; register_peers — только нодам,
// перечисленным в регистре (в V0 — central, см. ADR-0007/0008).
func nodeCapabilitiesFor(nodeID string) NodeCapabilities {
	if nodeID == "" {
		return CapNone
	}
	caps := CapSendEvents | CapReceiveEvents
	if isPeerRegistrar(nodeID) {
		caps |= CapRegisterPeers
	}
	return caps
}

// peerRegistrars — узлы с правом RegisterPeers. V0: пусто, регистр
// наполняется provisioning-процедурой (см. infra/scripts README).
// Наличие права определяется identity-реестром, а не конфигом соединения.
var peerRegistrars = map[string]struct{}{}

// SetPeerRegistrars задаёт множество нод с правом RegisterPeers.
func SetPeerRegistrars(nodeIDs []string) {
	peerRegistrars = make(map[string]struct{}, len(nodeIDs))
	for _, id := range nodeIDs {
		peerRegistrars[id] = struct{}{}
	}
}

func isPeerRegistrar(nodeID string) bool {
	_, ok := peerRegistrars[nodeID]
	return ok
}
