package identity

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"testing"
	"time"
)

func mustCert(t *testing.T, uriSAN string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	if uriSAN != "" {
		u, err := url.Parse(uriSAN)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

const nodeID = "0198a7b4-1111-7222-8333-444455556666"

func TestNodeIDFromCert(t *testing.T) {
	cert := mustCert(t, "przewalski://node/"+nodeID)
	got, err := NodeIDFromCert(cert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != nodeID {
		t.Fatalf("got %q, want %q", got, nodeID)
	}
}

func TestNodeIDFromCertNoSAN(t *testing.T) {
	if _, err := NodeIDFromCert(mustCert(t, "")); err == nil {
		t.Fatal("want error for cert without URI SAN")
	}
	if _, err := NodeIDFromCert(nil); err == nil {
		t.Fatal("want error for nil cert")
	}
}

func TestNodeIDFromCertForeignScheme(t *testing.T) {
	if _, err := NodeIDFromCert(mustCert(t, "https://example.com/node/"+nodeID)); err == nil {
		t.Fatal("want error for foreign URI scheme")
	}
}

func TestVerifyPeerNodeID(t *testing.T) {
	cert := mustCert(t, "przewalski://node/"+nodeID)
	if err := VerifyPeerNodeID(cert, nodeID); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := VerifyPeerNodeID(cert, "another-node-id"); err == nil {
		t.Fatal("want error for mismatched node_id")
	}
	if err := VerifyPeerNodeID(cert, ""); err == nil {
		t.Fatal("want error for empty expected node_id")
	}
}
