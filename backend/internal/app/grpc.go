// Сборка gRPC-сервера синхронизации.
//
// Режимы прослушивания network.grpc_listen_addr:
//   - если сконфигурированы security.cert_file/security.key_file — gRPC
//     слушает с mTLS (internal/security.LoadServerTLSConfig): клиент обязан
//     предъявить доверенный серт с URI SAN przewalski://node/<uuid>.
//   - если сертификатов нет — gRPC слушает В PLAIN-РЕЖИМЕ и ТОЛЬКО на
//     loopback-адресе (127.0.0.0/8, ::1): без TLS нет проверки подлинности
//     пиров, поэтому наружу такой сервер не открывается. Попытка слушать
//     plain-режим на не-loopback адресе завершает запуск ноды ошибкой.
package app

import (
	"fmt"
	"log/slog"
	"net"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/security"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpctransport "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// newGRPCServer собирает grpc.Server с SyncService: mTLS-credentials при
// наличии сертификатов, иначе insecure (только loopback — см. шапку файла).
func newGRPCServer(cfg *config.Config, identity syncengine.NodeIdentity,
	events storage.EventStore, blobs storage.BlobStore, log *slog.Logger,
) (*grpc.Server, error) {
	creds, err := newGRPCServerCreds(cfg, log)
	if err != nil {
		return nil, err
	}
	server := grpc.NewServer(grpc.Creds(creds))
	grpctransport.Register(server, grpctransport.NewServer(grpctransport.ServerDeps{
		Events:   events,
		Blobs:    blobs,
		Identity: identity,
		Log:      log,
	}))
	return server, nil
}

// tlsEnabled — включён ли mTLS по конфигурации security.
func tlsEnabled(cfg *config.Config) bool {
	return cfg.Security.CertFile != "" && cfg.Security.KeyFile != ""
}

func newGRPCServerCreds(cfg *config.Config, log *slog.Logger) (credentials.TransportCredentials, error) {
	if !tlsEnabled(cfg) {
		return insecure.NewCredentials(), nil
	}
	secCfg := security.Config{
		CertFile:        cfg.Security.CertFile,
		KeyFile:         cfg.Security.KeyFile,
		CAFile:          cfg.Security.CAFile,
		CentralHostname: cfg.Security.CentralHostname,
	}
	tlsCfg, err := security.LoadServerTLSConfig(secCfg, "")
	if err != nil {
		return nil, fmt.Errorf("app: grpc server tls config: %w", err)
	}
	return credentials.NewTLS(tlsCfg), nil
}

// validateGRPCListenAddr проверяет правило plain-режима: без сертификатов
// gRPC-адрес обязан быть loopback.
func validateGRPCListenAddr(cfg *config.Config) error {
	if tlsEnabled(cfg) {
		return nil
	}
	host, _, err := net.SplitHostPort(cfg.Network.GRPCListenAddr)
	if err != nil {
		return fmt.Errorf("app: grpc listen addr: %w", err)
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf(
			"app: grpc_listen_addr %q без сертификатов (security.cert_file/key_file) "+
				"допустим только на loopback: plain gRPC без mTLS не должен быть доступен извне",
			cfg.Network.GRPCListenAddr)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
