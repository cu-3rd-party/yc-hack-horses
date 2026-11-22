package http

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/enroll"
	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
)

// enrollTestCA — тестовый CA для endpoint-тестов (PEM-серт + PKCS#1 ключ).
func enrollTestCA(t *testing.T, dir string) (caFile, keyFile string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "endpoint test CA"},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	caFile = dir + "/ca.crt"
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	keyFile = dir + "/ca.key"
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile, keyFile
}

// enrollTestCSR — ключ+CSR для endpoint-тестов.
func enrollTestCSR(nodeID string) ([]byte, error) {
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return nil, err
	}
	return enroll.NewCSR(key, nodeID)
}

func TestEnrollEndpointDisabled(t *testing.T) {
	r := NewRouter(Deps{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enroll", strings.NewReader("{}"))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("без enrollment-сервиса: %d, ожидается 404", rec.Code)
	}
}

func TestEnrollEndpointHappyPath(t *testing.T) {
	dir := t.TempDir()
	caFile, keyFile := enrollTestCA(t, dir)
	svc, err := enroll.NewService(enroll.ServerConfig{
		Token:     "tok",
		CAFile:    caFile,
		CAKeyFile: keyFile,
		DataDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(Deps{Enrollment: svc})

	nodeID := "0198a7b4-aaaa-7bbb-8ccc-ddddeee00009"
	csrPEM, err := enrollTestCSR(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{
		"node_id": nodeID,
		"csr":     string(csrPEM),
		"token":   "tok",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enroll", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("happy path: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		NodeCert string `json:"node_cert"`
		CACert   string `json:"ca_cert"`
		NodeID   string `json:"node_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode([]byte(resp.NodeCert))
	if blk == nil {
		t.Fatal("node_cert не PEM")
	}
	cert, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	id, err := identity.NodeIDFromCert(cert)
	if err != nil || id != nodeID {
		t.Fatalf("URI SAN выданного серта: %q (%v)", id, err)
	}
	if cert.NotAfter.Sub(time.Now()) < 300*24*time.Hour {
		t.Fatalf("срок серта меньше года: %s", cert.NotAfter)
	}
	if len(svc.Issued()) != 1 {
		t.Fatalf("issued-список пуст")
	}
}

func TestEnrollEndpointBadToken(t *testing.T) {
	dir := t.TempDir()
	caFile, keyFile := enrollTestCA(t, dir)
	svc, err := enroll.NewService(enroll.ServerConfig{
		Token:     "tok",
		CAFile:    caFile,
		CAKeyFile: keyFile,
		DataDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	r := NewRouter(Deps{Enrollment: svc})
	nodeID := "0198a7b4-aaaa-7bbb-8ccc-ddddeee00010"
	csrPEM, err := enrollTestCSR(nodeID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{
		"node_id": nodeID,
		"csr":     string(csrPEM),
		"token":   "wrong",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/enroll", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("неверный токен: %d, ожидается 403", rec.Code)
	}
}
