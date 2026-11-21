package security

import (
	"crypto/tls"
	"crypto/x509"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCapabilitiesTrustedCert — доверенный серт с node_id даёт базовые capabilities.
func TestCapabilitiesTrustedCert(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	caps := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	require.True(t, caps.Has(CapSendEvents), "want send capabilities, got %v", caps)
	require.True(t, caps.Has(CapReceiveEvents), "want receive capabilities, got %v", caps)
	require.False(t, caps.Has(CapRegisterPeers), "register_peers не выдаётся по умолчанию: %v", caps)
	// Привязка к ожидаемому node_id.
	got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), testNodeA)
	require.NotEqual(t, CapNone, got, "want capabilities for matching node_id")
	// Чужой node_id — ноль capabilities.
	got = CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), testNodeB)
	require.Equal(t, CapNone, got, "want CapNone for foreign node_id")
}

// TestCapabilitiesUntrusted — серт недоверенного CA отклоняется (критерий 6а).
func TestCapabilitiesUntrusted(t *testing.T) {
	ca := newTestCA(t)
	otherCA := newTestCA(t)
	pair := otherCA.issue(t, "node-x", nil, []string{"przewalski://node/" + testNodeA})
	got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	require.Equal(t, CapNone, got, "want CapNone for untrusted CA")
	require.Equal(t, CapNone, CapabilitiesForCert(nil, ca.pool, time.Now(), ""), "want CapNone for nil cert")
}

// TestCapabilitiesExpired — просроченный серт отклоняется.
func TestCapabilitiesExpired(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	future := time.Now().Add(48 * time.Hour)
	got := CapabilitiesForCert(pair.Leaf, ca.pool, future, "")
	require.Equal(t, CapNone, got, "want CapNone for expired cert")
}

// TestCapabilitiesNoURISAN — доверенный серт без przewalski URI SAN — ноль.
func TestCapabilitiesNoURISAN(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "no-uri", nil, nil)
	got := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	require.Equal(t, CapNone, got, "want CapNone for cert without URI SAN")
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
	require.NoError(t, clientErr, "trusted mTLS handshake: client error")
	require.NoError(t, serverErr, "trusted mTLS handshake: server error")
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
	_, serverErr := pairTLS(clientCfg, serverCfg)
	require.Error(t, serverErr, "untrusted client must be rejected")
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
	_, serverErr := pairTLS(clientCfg, serverCfg)
	require.ErrorContains(t, serverErr, "node_id", "want node_id mismatch rejection")
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
	_, serverErr := pairTLS(clientCfg, serverCfg)
	require.Error(t, serverErr, "missing client cert must be rejected")
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
	require.NoError(t, err, "LoadServerTLSConfig")
	require.Equal(t, tls.RequireAndVerifyClientCert, tc.ClientAuth, "server must require and verify client cert")
	require.NotNil(t, tc.ClientCAs, "ClientCAs must be set")
	tcClient, err := LoadClientTLSConfig(cfg, "node-b")
	require.NoError(t, err, "LoadClientTLSConfig")
	require.Equal(t, "node-b", tcClient.ServerName, "client tls.Config must carry server name")
	require.Len(t, tcClient.Certificates, 1, "client tls.Config must carry cert")
	// Пустой CA — ошибка (self-signed auto-trust запрещён).
	_, err = LoadServerTLSConfig(Config{CertFile: cfg.CertFile, KeyFile: cfg.KeyFile}, "")
	require.Error(t, err, "want error without ca_file")
}

// TestVerifyCentralIdentityHappyPath — валидный hostname + серт доверенного
// CA на central-имя → central capabilities (критерий 6г, позитивная часть).
// В тесте CA — наш тестовый; в продакшене caPool = nil и проверка идёт
// против системного пула публичных CA.
func TestVerifyCentralIdentityHappyPath(t *testing.T) {
	ca := newTestCA(t)
	server := ca.issue(t, "central", []string{CentralHostnameDefault}, nil)
	caps, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{server.Leaf}, ca.pool, time.Now())
	require.NoError(t, err, "want confirmed central")
	require.True(t, caps.Confirmed, "want confirmed central, got %+v", caps)
	require.True(t, caps.ServerCertValid, "unexpected caps: %+v", caps)
	require.Equal(t, CentralHostnameDefault, caps.Hostname, "unexpected caps: %+v", caps)
}

// TestVerifyCentralIdentityHostnameSubstitution — подмена hostname отклоняется.
func TestVerifyCentralIdentityHostnameSubstitution(t *testing.T) {
	ca := newTestCA(t)
	server := ca.issue(t, "central", []string{CentralHostnameDefault}, nil)
	_, err := VerifyCentralIdentity("evil.example.com", []*x509.Certificate{server.Leaf}, ca.pool, time.Now())
	require.Error(t, err, "want rejection for foreign hostname")
}

// TestVerifyCentralIdentityCertSubstitution — серт не на central-имя отклоняется.
func TestVerifyCentralIdentityCertSubstitution(t *testing.T) {
	ca := newTestCA(t)
	// Валидный серт, но на чужое имя.
	server := ca.issue(t, "other", []string{"other.example.com"}, nil)
	_, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{server.Leaf}, ca.pool, time.Now())
	require.Error(t, err, "want rejection for cert without central DNS SAN")
	// Серт недоверенного CA.
	rogue := newTestCA(t)
	fake := rogue.issue(t, "central", []string{CentralHostnameDefault}, nil)
	_, err = VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{fake.Leaf}, ca.pool, time.Now())
	require.Error(t, err, "want rejection for untrusted CA")
	_, err = VerifyCentralIdentity(CentralHostnameDefault, nil, ca.pool, time.Now())
	require.Error(t, err, "want rejection for empty chain")
}

// TestRejectLocalCentral — локальная нода не может объявить себя central (критерий 6д/3).
func TestRejectLocalCentral(t *testing.T) {
	ca := newTestCA(t)
	// Локальный серт на central-имя: hostname в конфиге совпадает, но серт
	// не выпущен публичным CA (nil → системный пул) → проверка отвергает его.
	local := ca.issue(t, "local-pretending-central", []string{CentralHostnameDefault}, nil)
	require.NoError(t, RejectLocalCentral(local.Leaf, nil, time.Now()), "RejectLocalCentral must tolerate rejected certs")
	// Прямая проверка: локальный серт не проходит VerifyCentralIdentity
	// против публичных CA, даже если локальный CA добавлен в пул.
	_, err := VerifyCentralIdentity(CentralHostnameDefault, []*x509.Certificate{local.Leaf}, nil, time.Now())
	require.Error(t, err, "local self-signed cert must not yield central capabilities")
	// capabilities от роли: role_hint — не источник полномочий.
	got := CapabilitiesForCert(local.Leaf, ca.pool, time.Now(), "")
	require.False(t, got.Has(CapRegisterPeers), "config role must not grant register_peers")
}

// TestSetPeerRegistrars — RegisterPeers выдаётся только нодам из реестра.
func TestSetPeerRegistrars(t *testing.T) {
	ca := newTestCA(t)
	pair := ca.issue(t, "node-a", nil, []string{"przewalski://node/" + testNodeA})
	SetPeerRegistrars([]string{testNodeA})
	defer SetPeerRegistrars(nil)
	caps := CapabilitiesForCert(pair.Leaf, ca.pool, time.Now(), "")
	require.True(t, caps.Has(CapRegisterPeers), "registrar node must have register_peers")
	nodeB := ca.issue(t, "node-b", nil, []string{"przewalski://node/" + testNodeB})
	got := CapabilitiesForCert(nodeB.Leaf, ca.pool, time.Now(), "")
	require.False(t, got.Has(CapRegisterPeers), "non-registrar must not have register_peers")
	require.False(t, CapNone.Has(CapSendEvents), "CapNone must grant nothing")
	require.NotEqual(t, "", CapSendEvents.String(), "String() must render capabilities")
	require.Equal(t, "none", CapNone.String(), "String() must render capabilities")
}
