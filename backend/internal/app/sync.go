// Sync-контур ноды: реестр пиров (статические из конфига + discovery + central),
// gRPC-клиенты, цикл обмена событиями по sync.interval и счётчики.
//
// Доверие: адрес-кандидат (в т.ч. из mDNS) — это только адрес. Пир становится
// рабочим исключительно после mTLS-handshake: серверный серт проверяется
// против security.ca_file (LoadClientTLSConfig), клиентский серт предъявляет
// наш (LoadServerTLSConfig на стороне пира). В plain-режиме (без сертификатов,
// разрешён только на loopback) discovered-пиры не используются вовсе.
package app

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/security"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpctransport "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// peerKind — происхождение адреса пира.
const (
	kindStatic     = "static"
	kindDiscovered = "discovered"
	kindCentral    = "central"
)

// peerEntry — кандидат пира в реестре.
type peerEntry struct {
	Addr string // host:port gRPC-сервера пира
	Kind string // static | discovered | central
	// NodeID — ожидаемый node_id пира (сверяется с URI SAN серта). Пусто —
	// проверяется только валидность серта и наличие przewalski://node/.
	NodeID string
	// Host — TLS ServerName для проверки серверного серта (DNS SAN).
	// Пусто — host из Addr.
	Host string
}

// peerRegistry — потокобезопасный набор кандидатов (дедуп по адресу).
// Static-пир из конфига сильнее discovered: перезапись kind не происходит.
type peerRegistry struct {
	mu      sync.Mutex
	entries map[string]peerEntry
}

func newPeerRegistry() *peerRegistry {
	return &peerRegistry{entries: map[string]peerEntry{}}
}

func (r *peerRegistry) addStatic(cfg config.PeerConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[cfg.Addr]; !ok {
		r.entries[cfg.Addr] = peerEntry{Addr: cfg.Addr, Kind: kindStatic, NodeID: cfg.NodeID, Host: cfg.Host}
	}
}

func (r *peerRegistry) addDiscovered(nodeID, addr string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cur, ok := r.entries[addr]; ok && cur.Kind != kindDiscovered {
		return // статический/central не перетираем
	}
	r.entries[addr] = peerEntry{Addr: addr, Kind: kindDiscovered, NodeID: nodeID}
}

func (r *peerRegistry) snapshot() []peerEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]peerEntry, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e)
	}
	return out
}

// syncRuntime — соединения с пирами и цикл синхронизации.
type syncRuntime struct {
	cfg      *config.Config
	identity syncengine.NodeIdentity
	engine   *syncengine.Engine
	registry *peerRegistry
	stats    *Stats
	log      *slog.Logger
	// onStats — публикация снапшота счётчиков (WS), ставится App'ом.
	onStats func([]PeerStats)

	mu    sync.Mutex
	conns map[string]*peerConn
}

// peerConn — установленное gRPC-соединение с пиром.
type peerConn struct {
	cc     *grpc.ClientConn
	client *grpctransport.Client
	nodeID string
	// verified — mTLS/центральная идентичность проверены; после обрыва
	// соединение удаляется и проверка выполняется заново.
	verified bool
}

// newSyncRuntime собирает контур синхронизации.
func newSyncRuntime(cfg *config.Config, engine *syncengine.Engine, identity syncengine.NodeIdentity,
	log *slog.Logger,
) *syncRuntime {
	registry := newPeerRegistry()
	for _, p := range cfg.Sync.Peers {
		registry.addStatic(p)
	}
	return &syncRuntime{
		cfg:      cfg,
		identity: identity,
		engine:   engine,
		registry: registry,
		stats:    newStats(),
		log:      log,
		conns:    map[string]*peerConn{},
	}
}

// StatsSnapshot — счётчики для /api/v1/node.
func (s *syncRuntime) StatsSnapshot() []PeerStats { return s.stats.Snapshot() }

// setStatsPublisher — callback публикации кадра счётчиков (WS), ставится App'ом.
func (s *syncRuntime) setStatsPublisher(fn func([]PeerStats)) { s.onStats = fn }

// addCentral добавляет в реестр центральный адрес (исходящий sync к central).
func (s *syncRuntime) addCentral(addr string) {
	s.registry.addStatic(config.PeerConfig{Addr: addr})
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.registry.entries[addr]; ok {
		e.Kind = kindCentral
		s.registry.entries[addr] = e
	}
}

// addDiscoveredPeer — кандидат от discovery (или другого источника).
func (s *syncRuntime) addDiscoveredPeer(nodeID, addr string) { s.registry.addDiscovered(nodeID, addr) }

// runLoop — периодический цикл sync. Блокируется до отмены ctx.
func (s *syncRuntime) runLoop(ctx context.Context) {
	s.runCycle(ctx)
	ticker := time.NewTicker(s.cfg.Sync.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runCycle(ctx)
		}
	}
}

// runCycle — один проход по всем пирам реестра. Используется циклом и тестами.
func (s *syncRuntime) runCycle(ctx context.Context) {
	entries := s.registry.snapshot()
	// plain-режим: доверия discovered-адресам нет (см. шапку файла).
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		if entry.Kind == kindDiscovered && !tlsEnabled(s.cfg) {
			s.log.Debug("plain mode: skipping discovered peer", "addr", entry.Addr)
			continue
		}
		s.syncPeer(ctx, entry)
	}
	s.publishStats()
}

// publishStats рассылает кадр sync-счётчиков в WS.
func (s *syncRuntime) publishStats() {
	if s.onStats != nil {
		s.onStats(s.stats.Snapshot())
	}
}

func (s *syncRuntime) syncPeer(ctx context.Context, entry peerEntry) {
	s.stats.register(entry.Addr, entry.Kind)
	pc, err := s.connFor(ctx, entry)
	if err != nil {
		s.stats.record(entry.Addr, "", 0, 0, err)
		s.log.Warn("sync: peer unavailable", "addr", entry.Addr, "err", err)
		return
	}
	pulled, pushed, err := s.engine.Sync(ctx, pc.client)
	if err != nil {
		s.stats.record(entry.Addr, pc.nodeID, pushed, pulled, err)
		s.log.Warn("sync: peer cycle failed", "addr", entry.Addr, "err", err)
		// Обрыв — соединение не переиспользуем: следующий цикл наберёт заново.
		s.dropConn(entry.Addr)
		return
	}
	s.stats.record(entry.Addr, pc.nodeID, pushed, pulled, nil)
	if pushed > 0 || pulled > 0 {
		s.log.Info("sync cycle done", "addr", entry.Addr, "sent", pushed, "received", pulled)
	}
}

// dropConn закрывает и удаляет соединение с пиром (после ошибки).
func (s *syncRuntime) dropConn(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if pc, ok := s.conns[addr]; ok {
		_ = pc.cc.Close()
		delete(s.conns, addr)
	}
}

// connFor возвращает живое соединение или устанавливает новое. При central —
// дополнительно выполняется VerifyCentralIdentity (hostname + серт).
func (s *syncRuntime) connFor(ctx context.Context, entry peerEntry) (*peerConn, error) {
	s.mu.Lock()
	if pc, ok := s.conns[entry.Addr]; ok {
		s.mu.Unlock()
		return pc, nil
	}
	s.mu.Unlock()

	pc, err := s.dial(ctx, entry)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	// Кто-то другой мог подключиться параллельно — берём его.
	if existing, ok := s.conns[entry.Addr]; ok {
		s.mu.Unlock()
		_ = pc.cc.Close()
		return existing, nil
	}
	s.conns[entry.Addr] = pc
	s.mu.Unlock()
	return pc, nil
}

func (s *syncRuntime) dial(ctx context.Context, entry peerEntry) (*peerConn, error) {
	var creds credentials.TransportCredentials
	switch {
	case entry.Kind == kindCentral:
		// central: SNI и проверка имени зафиксированы на central-домен
		// (security.ClientTLSConfigForCentral), caPool может быть nil —
		// тогда используется системный пул публичных CA.
		secCfg := s.securityConfig()
		caPool, err := security.LoadCAPool(secCfg.CAFile)
		if err != nil {
			return nil, err
		}
		tlsCfg, err := security.ClientTLSConfigForCentral(secCfg, caPool)
		if err != nil {
			return nil, err
		}
		if err := s.verifyCentralIdentity(tlsCfg, caPool); err != nil {
			return nil, err
		}
		creds = credentials.NewTLS(tlsCfg)
	case tlsEnabled(s.cfg):
		secCfg := s.securityConfig()
		serverName := entry.Host
		if serverName == "" {
			serverName = hostOf(entry.Addr)
		}
		tlsCfg, err := security.LoadClientTLSConfig(secCfg, serverName)
		if err != nil {
			return nil, err
		}
		creds = credentials.NewTLS(tlsCfg)
	default:
		// plain: разрешён только loopback (сервер уже это проверил); discovered
		// отсечены в runCycle — здесь остаются только статические loopback-пир'ы.
		creds = insecure.NewCredentials()
	}
	addr := entry.Addr
	if entry.Kind == kindCentral {
		addr = centralDialAddr(s.cfg.Sync.CentralURL)
	}
	cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(creds))
	if err != nil {
		return nil, fmt.Errorf("sync: grpc client %s: %w", addr, err)
	}
	client := grpctransport.NewClient(syncv1.NewSyncServiceClient(cc), &grpctransport.ClientConfig{
		Timeout: 30 * time.Second,
	})
	pc := &peerConn{cc: cc, client: client}

	// Рукопожатие: сверка версии протокола и привязка серта к node_id
	// (если entry.NodeID задан — identity.VerifyPeerNodeID на стороне TLS
	// уже проверил chain; здесь сверяем версию и сам node_id пира).
	hctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	info, err := syncengine.Handshake(hctx, pc.client, s.identity)
	if err != nil {
		_ = cc.Close()
		return nil, err
	}
	if entry.NodeID != "" && info.NodeID != entry.NodeID {
		_ = cc.Close()
		return nil, fmt.Errorf("sync: peer %s reports node id %q, want %q", entry.Addr, info.NodeID, entry.NodeID)
	}
	pc.nodeID = info.NodeID
	return pc, nil
}

// verifyCentralIdentity — hostname-проверка central до gRPC: с сервера
// снимается серверный серт (tls.Dial с той же tls.Config) и проверяется через
// security.VerifyCentralIdentity (hostname == central-домен, серт валиден,
// DNS SAN совпадает). Отказ — пир не central, sync с ним не начинается.
func (s *syncRuntime) verifyCentralIdentity(tlsCfg *tls.Config, caPool *x509.CertPool) error {
	addr := net.JoinHostPort(tlsCfg.ServerName, "443")
	if _, port, err := net.SplitHostPort(centralDialAddr(s.cfg.Sync.CentralURL)); err == nil && port != "" {
		addr = net.JoinHostPort(tlsCfg.ServerName, port)
	}
	dialer := &tls.Dialer{Config: tlsCfg, NetDialer: &net.Dialer{Timeout: 10 * time.Second}}
	tconn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("sync: central tls dial: %w", err)
	}
	defer tconn.Close()
	state := tconn.(*tls.Conn).ConnectionState()
	caps, err := security.VerifyCentralIdentity(state.ServerName, state.PeerCertificates, caPool, time.Now())
	if err != nil {
		return fmt.Errorf("sync: central identity: %w", err)
	}
	if !caps.Confirmed {
		return security.ErrNotCentral
	}
	s.log.Info("central identity verified", "hostname", caps.Hostname)
	return nil
}

func (s *syncRuntime) securityConfig() security.Config {
	return security.Config{
		CertFile:        s.cfg.Security.CertFile,
		KeyFile:         s.cfg.Security.KeyFile,
		CAFile:          s.cfg.Security.CAFile,
		CentralHostname: s.cfg.Security.CentralHostname,
	}
}

func (s *syncRuntime) shutdown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for addr, pc := range s.conns {
		_ = pc.cc.Close()
		delete(s.conns, addr)
	}
}

// onStats хранится отдельно, чтобы не тащить httpapi-зависимость.

// centralDialAddr — host:port для gRPC-подключения к central из URL.
func centralDialAddr(centralURL string) string {
	u, err := url.Parse(centralURL)
	if err != nil || u.Host == "" {
		return centralURL
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if strings.EqualFold(u.Scheme, "https") {
			port = "443"
		} else {
			port = "80"
		}
	}
	return net.JoinHostPort(host, port)
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
