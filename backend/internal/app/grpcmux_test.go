package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"
	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
	"github.com/cu3rd/yc-hack-horses/backend/internal/security"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpctransport "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// muxCA — тестовый CA для сертов из заголовка (копия подхода internal/security).
type muxCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pool *x509.CertPool
}

func newMuxCA(t *testing.T) *muxCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Przewalski Mux Test CA"},
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
	return &muxCA{cert: cert, key: key, pool: pool}
}

// issueNode выпускает клиентский серт ноды с URI SAN przewalski://node/<id>
// (uriSAN пуст — серт без identity, должен отвергаться).
func (ca *muxCA) issueNode(t *testing.T, nodeID string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "node"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(12 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	if nodeID != "" {
		u, err := url.Parse("przewalski://node/" + nodeID)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	// Форма, в которой Traefik кладёт PEM в заголовок: одна строка,
	// переводы строк экранированы как literal \n.
	pemBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return strings.ReplaceAll(string(pemBlock), "\n", `\n`)
}

// muxTestEnv — мультиплекс-сервер с тестовым CA и REST-роутером.
type muxTestEnv struct {
	ts *httptest.Server
	ca *muxCA
}

func newMuxTestEnv(t *testing.T) *muxTestEnv {
	t.Helper()
	ca := newMuxCA(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	unary, stream := certAuthInterceptors(ca.pool, log)
	grpcSrv := grpc.NewServer(
		grpc.Creds(insecure.NewCredentials()),
		grpc.ChainUnaryInterceptor(unary),
		grpc.ChainStreamInterceptor(stream),
	)
	grpctransport.Register(grpcSrv, grpctransport.NewServer(grpctransport.ServerDeps{
		Identity: syncengine.NodeIdentity{NodeID: "0198a7b4-aaaa-7bbb-8ccc-ddddeee00fff"},
	}))
	rest := http.NewServeMux()
	rest.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler, err := newMuxHandler(grpcSrv, rest)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &muxTestEnv{ts: ts, ca: ca}
}

// handshake через мультиплекс-порт с заданным значением заголовка серта.
func (e *muxTestEnv) handshake(t *testing.T, certHeader string) (syncengine.HandshakeInfo, error) {
	t.Helper()
	// grpc-go поверх h2c: insecure-credentials + свой dialer на адрес
	// httptest-сервера (реальный HTTP/2 prior knowledge, не bufconn-gRPC).
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", e.ts.Listener.Addr().String())
	}
	cc, err := grpc.NewClient("127.0.0.1:1",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(dialer))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cc.Close() })
	ctx := context.Background()
	if certHeader != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-forwarded-tls-client-cert", certHeader)
	}
	client := grpctransport.NewClient(syncv1.NewSyncServiceClient(cc), &grpctransport.ClientConfig{Timeout: 10 * time.Second})
	return syncengine.Handshake(ctx, client, syncengine.NodeIdentity{NodeID: "0198a7b4-aaaa-7bbb-8ccc-ddddeee00abc"})
}

// caCert — значение заголовка X-Forwarded-Tls-Client-Cert для серта ноды.
func (e *muxTestEnv) caCert(t *testing.T, nodeID string) string {
	t.Helper()
	return e.ca.issueNode(t, nodeID)
}

func TestMuxRESTOverHTTP11(t *testing.T) {
	env := newMuxTestEnv(t)
	resp, err := env.ts.Client().Get(env.ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("REST через mux: status %d, want 200", resp.StatusCode)
	}
}

func TestMuxGRPCHandshakeWithValidCert(t *testing.T) {
	env := newMuxTestEnv(t)
	info, err := env.handshake(t, env.caCert(t, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00001"))
	if err != nil {
		t.Fatalf("handshake с валидным сертом: %v", err)
	}
	if info.NodeID != "0198a7b4-aaaa-7bbb-8ccc-ddddeee00fff" {
		t.Fatalf("handshake node_id %q, want server identity", info.NodeID)
	}
}

func TestMuxGRPCHandshakeOtherNodeCert(t *testing.T) {
	// Валидный серт другой ноды — тоже проходит (любая нода dev-CA допущена).
	env := newMuxTestEnv(t)
	_, err := env.handshake(t, env.caCert(t, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00002"))
	if err != nil {
		t.Fatalf("handshake с сертом другой ноды: %v", err)
	}
}

func TestMuxGRPCAnonymousRejected(t *testing.T) {
	env := newMuxTestEnv(t)
	_, err := env.handshake(t, "")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("handshake без заголовка: %v, want Unauthenticated", err)
	}
}

func TestMuxGRPCGarbageCertRejected(t *testing.T) {
	env := newMuxTestEnv(t)
	_, err := env.handshake(t, "-----BEGIN CERTIFICATE-----\\nZm9v\\n-----END CERTIFICATE-----\\n")
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("handshake с битым сертом: %v, want Unauthenticated", err)
	}
}

func TestMuxGRPCCertWithoutURISANRejected(t *testing.T) {
	env := newMuxTestEnv(t)
	_, err := env.handshake(t, env.caCert(t, ""))
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("handshake с сертом без URI SAN: %v, want Unauthenticated", err)
	}
}

// ParseClientCertHeader: экранированная форма Traefik разбирается.
func TestParseClientCertHeaderEscaped(t *testing.T) {
	ca := newMuxCA(t)
	header := ca.issueNode(t, "0198a7b4-aaaa-7bbb-8ccc-ddddeee00009")
	cert, err := security.ParseClientCertHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := identity.NodeIDFromCert(cert); err != nil {
		t.Fatalf("URI SAN потерян при разборе заголовка: %v", err)
	}
}
