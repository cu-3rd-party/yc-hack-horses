package enroll

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testCA создаёт тестовый CA (self-signed) и возвращает серт/ключ.
func testCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		NotAfter:              time.Date(2036, 1, 1, 0, 0, 0, 0, time.UTC),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key, der
}

// writeTestCA кладёт CA (PEM-серт + PKCS#1 ключ) во временные файлы.
func writeTestCA(t *testing.T, dir string) (caFile, keyFile string) {
	t.Helper()
	cert, key, der := testCA(t)
	caFile = filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caFile, pemEncode("CERTIFICATE", der), 0o600); err != nil {
		t.Fatal(err)
	}
	keyFile = filepath.Join(dir, "ca.key")
	if err := os.WriteFile(keyFile, pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = cert
	return caFile, keyFile
}

// pemEncode оборачивает DER в PEM-блок заданного типа.
func pemEncode(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

// csrFor генерирует ключ+CSR заданного node_id.
func csrFor(t *testing.T, nodeID string) (keyPEM, csrPEM []byte) {
	t.Helper()
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csrPEM, err = NewCSR(key, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = pemEncode("RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(key))
	return keyPEM, csrPEM
}

func TestNewCSRAndParseCSR(t *testing.T) {
	_, csrPEM := csrFor(t, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001")
	if _, err := ParseCSR(csrPEM, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001"); err != nil {
		t.Fatalf("валидный CSR отклонён: %v", err)
	}
}

func TestParseCSRMismatch(t *testing.T) {
	_, csrPEM := csrFor(t, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001")
	// Чужой node_id в поле запроса (CN/SAN не совпадают) — отказ.
	if _, err := ParseCSR(csrPEM, "other-id"); err == nil {
		t.Fatal("CSR с чужим node_id принят")
	}
	// Мусор вместо CSR.
	if _, err := ParseCSR([]byte("garbage"), "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001"); err == nil {
		t.Fatal("мусор принят как CSR")
	}
}

func TestSignCSRLifetime365(t *testing.T) {
	caCert, caKey, _ := testCA(t)
	nodeID := "0198a7b4-aaaa-7bbb-8ccc-ddddeee00002"
	_, csrPEM := csrFor(t, nodeID)
	csr, err := ParseCSR(csrPEM, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	pemBytes, err := SignCSR(caCert, caKey, csr, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := parseCert(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.AddDate(0, 0, CertDaysDefault); !cert.NotAfter.Equal(want) {
		t.Fatalf("срок серта %s, ожидается %s (365 дней)", cert.NotAfter, want)
	}
	if cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		t.Fatal("нет KeyUsageDigitalSignature")
	}
	for _, eku := range cert.ExtKeyUsage {
		if eku != x509.ExtKeyUsageClientAuth {
			t.Fatalf("чужой ExtKeyUsage: %v", cert.ExtKeyUsage)
		}
	}
	// Цепочка проверяется против CA и URI SAN разбирается в node_id.
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		t.Fatalf("серт не проходит проверку против CA: %v", err)
	}
}

func TestServiceEnrollTokenAndLedger(t *testing.T) {
	dir := t.TempDir()
	caFile, keyFile := writeTestCA(t, dir)
	svc, err := NewService(ServerConfig{
		Token:     "secret-token",
		CAFile:    caFile,
		CAKeyFile: keyFile,
		DataDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	nodeID := "0198a7b4-aaaa-7bbb-8ccc-ddddeee00003"
	_, csrPEM := csrFor(t, nodeID)

	// Неверный токен — отказ.
	if _, _, _, err := svc.Enroll(nodeID, string(csrPEM), "wrong"); err == nil {
		t.Fatal("неверный токен принят")
	} else if want := ErrBadToken; err != want {
		t.Fatalf("ошибка %v, ожидается ErrBadToken", err)
	}
	// Валидный — выдача + запись в ledger.
	nodePEM, caPEM, expires, err := svc.Enroll(nodeID, string(csrPEM), "secret-token")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(nodePEM), "BEGIN CERTIFICATE") || !strings.Contains(string(caPEM), "BEGIN CERTIFICATE") {
		t.Fatal("ответ не PEM")
	}
	if time.Until(expires) < 300*24*time.Hour {
		t.Fatalf("срок серта слишком мал: %s", expires)
	}
	recs := svc.Issued()
	if len(recs) != 1 || recs[0].NodeID != nodeID {
		t.Fatalf("ledger: %+v", recs)
	}
	// Повторная выдача для того же node_id — новая запись.
	if _, _, _, err := svc.Enroll(nodeID, string(csrPEM), "secret-token"); err != nil {
		t.Fatal(err)
	}
	if len(svc.Issued()) != 2 {
		t.Fatalf("ожидалось 2 записи, получено %d", len(svc.Issued()))
	}
	// Файл ledger лежит в data_dir и перечитывается новым сервисом.
	data, err := os.ReadFile(filepath.Join(dir, issuedFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "\n") != 2 {
		t.Fatalf("в файле не 2 строки: %q", string(data))
	}
	svc2, err := NewService(ServerConfig{
		Token:     "secret-token",
		CAFile:    caFile,
		CAKeyFile: keyFile,
		DataDir:   dir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(svc2.Issued()) != 2 {
		t.Fatalf("issued-список не восстановился: %d", len(svc2.Issued()))
	}
}

func TestStatusOf(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	// Нет серта — нужен enrollment.
	st := StatusOf(certPath, time.Now())
	if st.Enrolled || !st.NeedsReenroll {
		t.Fatalf("статус без серта: %+v", st)
	}
	// Живой серт на 365 дней — enrollment не нужен.
	caCert, caKey, _ := testCA(t)
	nodeID := "0198a7b4-aaaa-7bbb-8ccc-ddddeee00004"
	_, csrPEM := csrFor(t, nodeID)
	csr, err := ParseCSR(csrPEM, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	pemBytes, err := SignCSR(caCert, caKey, csr, CertDaysDefault, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	st = StatusOf(certPath, now)
	if !st.Enrolled || st.NeedsReenroll || st.NodeID != nodeID {
		t.Fatalf("статус живого серта: %+v", st)
	}
	// Слот за 7 дней до конца срока — пере-enroll.
	st = StatusOf(certPath, now.AddDate(0, 0, CertDaysDefault-6))
	if !st.NeedsReenroll || st.Reason == "" {
		t.Fatalf("ожидается NeedsReenroll за 7 дней: %+v", st)
	}
	// Истёкший серт — пере-enroll.
	st = StatusOf(certPath, now.AddDate(1, 0, 1))
	if !st.NeedsReenroll {
		t.Fatalf("истёкший серт не требует пере-enroll: %+v", st)
	}
}
