// gRPC-мультиплекс на общем с REST порту (network.grpc_multiplex).
//
// Центральная нода в self-service живёт за Traefik ingress: TLS терминируется
// на ingress, наружу открыт только порт 8080. При grpc_multiplex=true на
// network.listen_addr работает обёртка h2c (golang.org/x/net/http2/h2c):
//   - HTTP/2 + content-type application/grpc → gRPC-сервер;
//   - остальное (HTTP/1.1 и h2c-REST) → REST-роутер.
//
// Аутентификация: прямое mTLS-рукопожатие до бэкенда не доходит, клиентский
// серт прокидывается Traefik-middleware passTLSClientCert в заголовке
// X-Forwarded-Tls-Client-Cert. Интерцепторы ниже проверяют серт против
// security.ca_file (chain, срок действия, URI SAN przewalski://node/<id>)
// и требуют полномочия SendEvents+ReceiveEvents. Нет заголовка или невалидный
// серт → Unauthenticated: анонимный gRPC через ingress запрещён.
package app

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
	"github.com/cu3rd/yc-hack-horses/backend/internal/security"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpctransport "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// minPeerCaps — минимальные полномочия для gRPC через мультиплекс.
const minPeerCaps = security.CapSendEvents | security.CapReceiveEvents

// newMuxHandler оборачивает REST-роутер в h2c-мультиплекс с gRPC-сервером.
func newMuxHandler(grpcSrv *grpc.Server, rest http.Handler) (http.Handler, error) {
	h2s := &http2.Server{}
	mux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("content-type"), "application/grpc") {
			grpcSrv.ServeHTTP(w, r)
			return
		}
		rest.ServeHTTP(w, r)
	})
	return h2c.NewHandler(mux, h2s), nil
}

// newMuxGRPCServer собирает gRPC-сервер для мультиплекс-порта: TLS терминирует
// ingress, поэтому credentials — insecure, а аутентификация выполняется
// интерцепторами по серту из заголовка (fail closed).
func newMuxGRPCServer(cfg *config.Config, identity syncengine.NodeIdentity,
	events storage.EventStore, blobs storage.BlobStore, log *slog.Logger,
) (*grpc.Server, error) {
	caPool, err := security.LoadCAPool(cfg.Security.CAFile)
	if err != nil {
		return nil, fmt.Errorf("app: grpc mux ca pool: %w", err)
	}
	if caPool == nil {
		// Конфиг-валидация уже требует ca_file для multiplex; страховка.
		return nil, fmt.Errorf("app: grpc mux: security.ca_file не задан")
	}
	unary, stream := certAuthInterceptors(caPool, log)
	server := grpc.NewServer(
		grpc.Creds(insecure.NewCredentials()),
		grpc.ChainUnaryInterceptor(unary),
		grpc.ChainStreamInterceptor(stream),
	)
	grpctransport.Register(server, grpctransport.NewServer(grpctransport.ServerDeps{
		Events:   events,
		Blobs:    blobs,
		Identity: identity,
		Log:      log,
	}))
	return server, nil
}

// certAuthInterceptors — unary/stream-интерцепторы аутентификации по
// клиентскому серту из заголовка ingress. Идентичность пира берётся только
// из URI SAN серта (identity.NodeIDFromCert), никогда из протокола.
func certAuthInterceptors(caPool *x509.CertPool, log *slog.Logger) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	auth := func(ctx context.Context) (context.Context, error) {
		cert, err := clientCertFromMD(ctx)
		if err != nil {
			// Диагностика: какие заголовки вообще дошли (значения не логируем).
			if md, ok := metadata.FromIncomingContext(ctx); ok {
				keys := make([]string, 0, len(md))
				for k, v := range md {
					keys = append(keys, fmt.Sprintf("%s(%d)", k, len(v)))
				}
				sort.Strings(keys)
				log.Warn("grpc mux: unauthenticated request", "err", err, "md", strings.Join(keys, " "))
			}
			return ctx, status.Error(codes.Unauthenticated, "node certificate required")
		}
		caps := security.CapabilitiesForCert(cert, caPool, time.Now(), "")
		if !caps.Has(minPeerCaps) {
			return ctx, status.Error(codes.Unauthenticated, "invalid node certificate")
		}
		nodeID, err := identity.NodeIDFromCert(cert)
		if err != nil {
			// CapabilitiesForCert уже проверил URI SAN — страховка fail closed.
			return ctx, status.Error(codes.Unauthenticated, "invalid node certificate")
		}
		log.Debug("grpc mux: peer authenticated", "node_id", nodeID)
		return ctx, nil
	}
	unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		ctx, err := auth(ctx)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
	stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if _, err := auth(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
	return unary, stream
}

// clientCertFromMD извлекает клиентский серт из входящих метаданных gRPC
// (Traefik-заголовок в нижнем регистре).
func clientCertFromMD(ctx context.Context) (*x509.Certificate, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, security.ErrNoClientCert
	}
	vals := md.Get(strings.ToLower(security.ClientCertHeaderName))
	if len(vals) == 0 || vals[0] == "" {
		return nil, security.ErrNoClientCert
	}
	return security.ParseClientCertHeader(vals[0])
}
