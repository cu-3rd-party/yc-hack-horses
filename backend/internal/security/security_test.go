package security

import (
	"crypto/tls"
	"crypto/x509"
	"strings"
	"testing"
	"time"
)

// TestCapabilitiesTrustedCert — доверенный серт с node_id даёт базовые capabilities.
func TestCapabilitiesTrustedCert(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	caps := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	if !caps.Has(CapSendEvents) || !caps.Has(CapReceiveEvents) {
		t.Fatalf("want send+receive capabilities, got %v", caps)
	}
	if caps.Has(CapRegisterPeers) {
		t.Fatalf("register_peers не выдаётся по умолчанию: %v", caps)
	}
	// Привязка к ожидаемому node_id.
	if got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), testNodeA); got == CapNone {
		t.Fatal("want capabilities for matching node_id")
	}
	// Чужой node_id — ноль capabilities.
	if got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), testNodeB); got != CapNone {
		t.Fatalf("want CapNone for foreign node_id, got %v", got)
	}
}

// TestCapabilitiesUntrusted — серт недоверенного CA отклоняется (критерий 6а).
func TestCapabilitiesUntrusted(t *testing.T) {
	ca := newTestCA(t)
	otherCA := newTestCA(t)
	pair := otherCA.issue(t, "node-x", nil, []string{"przewalski://node/" + testNodeA})
	if got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), ""); got != CapNone {
		t.Fatalf("want CapNone for untrusted CA, got %v", got)
	}
	if got := CapabilitiesForCert(nil, ca.pool, time.Now(), ""); got != CapNone {
		t.Fatal("want CapNone for nil cert")
	}
}

// TestCapabilitiesExpired — просроченный серт отклоняется.
func TestCapabilitiesExpired(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	future := time.Now().Add(48 * time.Hour)
	if got := CapabilitiesForCert(pair.Leaf, ca.pool, future, ""); got != CapNone {
		t.Fatalf("want CapNone for expired cert, got %v", got)
	}
}

// TestCapabilitiesNoURISAN — доверенный серт без przewalski URI SAN — ноль.
func TestCapabilitiesNoURISAN(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "no-uri", nil, nil)
	if got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), ""); got != CapNone {
		t.Fatalf("want CapNone for cert without URI SAN, got %v", got)
	}
}

// TestMTLSHandshakeTrusted — peer-to-peer mTLS handshake для доверенных нод (критерий 6в).
func TestMTLSHandshakeTrusted(t *testing.T) {
	ca := newTestCA(t)
	nodeA := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})

	serverCfg := &tls.Config{
		MinVersion:            MinTLSVersion,
		Certificates:          []tls.Certificate{nodeB},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             ca.pool,
		VerifyPeerCertificate: peerCertVerify(""),
	}
	clientCfg := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{nodeA},
		RootCAs:      ca.pool,
		ServerName:   "node-b",
	}
	clientErr, serverErr := pairTLS(clientCfg, serverCfg)
	if clientErr != nil || serverErr != nil {
		t.Fatalf("trusted mTLS handshake failed: client=%v server=%v", clientErr, serverErr)
	}
}

// TestMTLSHandshakeUntrustedClient — сервер отклоняет клиента недоверенного CA.
func TestMTLSHandshakeUntrustedClient(t *testing.T) {
	ca := newTestCA(t)
	rogue := newTestCA(t)
	nodeA := rogue.issue(t, "rogue", nil, []string{"przewalski://node/" + testNodeA})
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})

	serverCfg := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{nodeB},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool,
	}
	clientCfg := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{nodeA},
		RootCAs:      ca.pool,
		ServerName:   "node-b",
	}
	clientErr, serverErr := pairTLS(clientCfg, serverCfg)
	if serverErr == nil {
		t.Fatalf("untrusted client must be rejected: client=%v server=%v", clientErr, serverErr)
	}
}

// TestMTLSHandshakeWrongNodeID — серт с идентичностью другой ноды отклоняется (критерий 6б).
func TestMTLSHandshakeWrongNodeID(t *testing.T) {
	ca := newTestCA(t)
	nodeA := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})

	// Сервер ждёт node_id ноды B, а подключается нода A.
	serverCfg := &tls.Config{
		MinVersion:            MinTLSVersion,
		Certificates:          []tls.Certificate{nodeB},
		ClientAuth:            tls.RequireAndVerifyClientCert,
		ClientCAs:             ca.pool,
		VerifyPeerCertificate: peerCertVerify(testNodeB),
	}
	clientCfg := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{nodeA},
		RootCAs:      ca.pool,
		ServerName:   "node-b",
	}
	clientErr, serverErr := pairTLS(clientCfg, serverCfg)
	if serverErr == nil || !strings.Contains(serverErr.Error(), "node_id") {
		t.Fatalf("want node_id mismatch rejection, got server=%v client=%v", serverErr, clientErr)
	}
}

// TestMTLSNoClientCert — сервер требует клиентский серт.
func TestMTLSNoClientCert(t *testing.T) {
	ca := newTestCA(t)
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})
	serverCfg := &tls.Config{
		MinVersion:   MinTLSVersion,
		Certificates: []tls.Certificate{nodeB},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.pool,
	}
	clientCfg := &tls.Config{MinVersion: MinTLSVersion, RootCAs: ca.pool, ServerName: "node-b"}
	clientErr, serverErr := pairTLS(clientCfg, serverCfg)
	if serverErr == nil {
		t.Fatalf("missing client cert must be rejected: client=%v server=%v", clientErr, serverErr)
	}
}

// TestLoadServerTLSConfigFromFileWrites — tls.Config из Config строится
// через временные PEM-файлы (LoadCAPool / LoadX509KeyPair).
func TestLoadServerTLSConfigFromFileWrites(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	dir := t.TempDir()
	writePEM(t, dir, ca, pair)

	cfg := Config{CertFile: dir + "/cert.pem", KeyFile: dir + "/key.pem", CAFile: dir + "/ca.pem"}
	tc, err := LoadServerTLSConfig(cfg, "")
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	if tc.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Fatal("server must require and verify client cert")
	}
	if tc.ClientCAs == nil {
		t.Fatal("ClientCAs must be set")
	}
	tcClient, err := LoadClientTLSConfig(cfg, "node-b")
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}
	if tcClient.ServerName != "node-b" || len(tcClient.Certificates) != 1 {
		t.Fatal("client tls.Config must carry cert and server name")
	}
	// Пустой CA — ошибка (self-signed auto-trust запрещён).
	if _, err := LoadServerTLSConfig(Config{CertFile: cfg.CertFile, KeyFile: cfg.KeyFile}, ""); err == nil {
		t.Fatal("want error without ca_file")
	}
}

// TestVerifyCentralIdentityHappyPath — валидный hostname + серт доверенного
// CA на central-имя → central capabilities (критерий 6г, позитивная часть).
// В тесте CA — наш тестовый; в продакшене caPool = nil и проверка идёт
// против системного пула публичных CA.
func TestVerifyCentralIdentityHappyPath(t *testing.T) {
	ca := newTestCA(t)
	server := ca.issue(t, "central", []string{CentralHostnameDefault}, nil)
	caps, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{server.Leaf}, ca.pool, time.Now())
	if err != nil || !caps.Confirmed {
		t.Fatalf("want confirmed central, err=%v caps=%v", err, caps)
	}
	if !caps.ServerCertValid || caps.Hostname != CentralHostnameDefault {
		t.Fatalf("unexpected caps: %+v", caps)
	}
}

// TestVerifyCentralIdentityHostnameSubstitution — подмена hostname отклоняется.
func TestVerifyCentralIdentityHostnameSubstitution(t *testing.T) {
	ca := newTestCA(t)
	server := ca.issue(t, "central", []string{CentralHostnameDefault}, nil)
	if _, err := VerifyCentralIdentity("evil.example.com", []*x509.Certificate{server.Leaf}, ca.pool, time.Now()); err == nil {
		t.Fatal("want rejection for foreign hostname")
	}
}

// TestVerifyCentralIdentityCertSubstitution — серт не на central-имя отклоняется.
func TestVerifyCentralIdentityCertSubstitution(t *testing.T) {
	ca := newTestCA(t)
	// Валидный серт, но на чужое имя.
	server := ca.issue(t, "other", []string{"other.example.com"}, nil)
	if _, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{server.Leaf}, ca.pool, time.Now()); err == nil {
		t.Fatal("want rejection for cert without central DNS SAN")
	}
	// Серт недоверенного CA.
	rogue := newTestCA(t)
	fake := rogue.issue(t, "central", []string{CentralHostnameDefault}, nil)
	if _, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{fake.Leaf}, ca.pool, time.Now()); err == nil {
		t.Fatal("want rejection for untrusted CA")
	}
	if _, err := VerifyCentralIdentity(CentralHostnameDefault, nil, ca.pool, time.Now()); err == nil {
		t.Fatal("want rejection for empty chain")
	}
}

// TestRejectLocalCentral — локальная нода не может объявить себя central (критерий 6д/3).
func TestRejectLocalCentral(t *testing.T) {
	ca := newTestCA(t)
	// Локальный серт на central-имя: hostname в конфиге совпадает, но серт
	// не выпущен публичным CA (nil → системный пул) → проверка отвергает его.
	local := ca.issue(t, "local-pretending-central", []string{CentralHostnameDefault}, nil)
	if err := RejectLocalCentral(local.Leaf, nil, time.Now()); err != nil {
		t.Fatalf("RejectLocalCentral must tolerate rejected certs, got %v", err)
	}
	// Прямая проверка: локальный серт не проходит VerifyCentralIdentity
	// против публичных CA, даже если локальный CA добавлен в пул.
	if _, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{local.Leaf}, nil, time.Now()); err == nil {
		t.Fatal("local self-signed cert must not yield central capabilities")
	}
	// capabilities от роли: role_hint — не источник полномочий.
	if got := CapabilitiesForCert(local.Leaf, ca.pool, time.Now(), ""); got.Has(CapRegisterPeers) {
		t.Fatal("config role must not grant register_peers")
	}
}

// TestSetPeerRegistrars — RegisterPeers выдаётся только нодам из реестра.
func TestSetPeerRegistrars(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	SetPeerRegistrars([]string{testNodeA})
	defer SetPeerRegistrars(nil)
	caps := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	if !caps.Has(CapRegisterPeers) {
		t.Fatal("registrar node must have register_peers")
	}
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})
	if got := CapabilitiesForCert(nodeB.Leaf, ca.pool, time.Now(), ""); got.Has(CapRegisterPeers) {
		t.Fatal("non-registrar must not have register_peers")
	}
	if CapNone.Has(CapSendEvents) {
		t.Fatal("CapNone must grant nothing")
	}
	if CapSendEvents.String() == "" || CapNone.String() != "none" {
		t.Fatal("String() must render capabilities")
	}
}
