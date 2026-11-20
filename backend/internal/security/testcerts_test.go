package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA — самодостаточный тестовый CA (только для тестов, не для dev-скрипта).
type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newTestCA(t *testing.T) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Przewalski Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &testCA{cert: cert, key: key, pool: pool}
}

// issue выпускает лист под тестовым CA.
func (ca *testCA) issue(t *testing.T, cn string, dnsSANs []string, uriSANs []string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(12 * time.Hour),
		DNSNames:     []string{cn}, // hostname-проверка серверного серта в тестах
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	for _, d := range dnsSANs {
		tmpl.DNSNames = append(tmpl.DNSNames, d)
	}
	for _, u := range uriSANs {
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, parsed)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{
		Certificate: [][]byte{der, ca.cert.Raw},
		PrivateKey:  key,
		Leaf:        parsed,
	}
}

const (
	testNodeA = "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001"
	testNodeB = "0198a7b4-aaaa-7bbb-8ccc-ddddeee00002"
)

// nodeCertOpts — опции выпуска нодового серта.
type nodeCertOpts struct {
	nodeID string // przewalski://node/<id>; "" — без URI SAN
	cn     string
}

// writePEM пишет ca.pem, cert.pem и key.pem во временный каталог.
func writePEM(t *testing.T, dir string, ca *testCA, pair tls.Certificate) {
	t.Helper()
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]})
	keyDER, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	for name, data := range map[string][]byte{
		"ca.pem":   caPEM,
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// pairTLS через net.Pipe выполняет TLS-handshake между clientCfg и serverCfg.
// Возвращает ошибку любой из сторон.
func pairTLS(clientCfg, serverCfg *tls.Config) (clientErr, serverErr error) {
	c1, c2 := net.Pipe()
	// Дедлайны гарантируют завершение, если goroutine блокируется на записи.
	_ = c1.SetDeadline(time.Now().Add(30 * time.Second))
	_ = c2.SetDeadline(time.Now().Add(30 * time.Second))
	done := make(chan struct{}, 2)
	go func() {
		serverErr = tls.Server(c2, serverCfg).Handshake()
		// Закрываем raw-конец: tls.Conn.Close шлёт close_notify и может
		// заблокироваться на синхронном pipe (партнёр уже не читает).
		_ = c2.Close()
		done <- struct{}{}
	}()
	go func() {
		clientErr = tls.Client(c1, clientCfg).Handshake()
		_ = c1.Close()
		done <- struct{}{}
	}()
	<-done
	<-done
	return clientErr, serverErr
}
