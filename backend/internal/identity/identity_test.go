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

	"github.com/stretchr/testify/require"
)

func mustCert(t *testing.T, uriSAN string) *x509.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	if uriSAN != "" {
		u, err := url.Parse(uriSAN)
		require.NoError(t, err)
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

const nodeID = "0198a7b4-1111-7222-8333-444455556666"

func TestNodeIDFromCert(t *testing.T) {
	cert := mustCert(t, "przewalski://node/"+nodeID)
	got, err := NodeIDFromCert(cert)
	require.NoError(t, err)
	require.Equal(t, nodeID, got)
}

func TestNodeIDFromCertNoSAN(t *testing.T) {
	_, err := NodeIDFromCert(mustCert(t, ""))
	require.Error(t, err, "want error for cert without URI SAN")
	_, err = NodeIDFromCert(nil)
	require.Error(t, err, "want error for nil cert")
}

func TestNodeIDFromCertForeignScheme(t *testing.T) {
	_, err := NodeIDFromCert(mustCert(t, "https://example.com/node/"+nodeID))
	require.Error(t, err, "want error for foreign URI scheme")
}

func TestVerifyPeerNodeID(t *testing.T) {
	cert := mustCert(t, "przewalski://node/"+nodeID)
	require.NoError(t, VerifyPeerNodeID(cert, nodeID))
	err := VerifyPeerNodeID(cert, "another-node-id")
	require.Error(t, err, "want error for mismatched node_id")
	err = VerifyPeerNodeID(cert, "")
	require.Error(t, err, "want error for empty expected node_id")
}
