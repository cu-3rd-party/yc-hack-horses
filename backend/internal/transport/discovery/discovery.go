// Package discovery — обнаружение нод по mDNS/DNS-SD.
//
// Библиотека: github.com/hashicorp/mdns. Обоснование выбора: минимальный
// набор зависимостей (только miekg/dns), прямая поддержка и регистрации
// сервиса (Server + MDNSService), и поиска пиров (QueryContext); в отличие
// от github.com/grandcat/zeroconf не тянет лишние обёртки и не заброшен
// без правок публичного API. Сервис _przewalski-node._tcp.local,
// имя инстанса — node_id, порт — gRPC-порт ноды.
//
// Доверие: обнаружение даёт только адрес-кандидат. Никакие полномочия
// из mDNS не выводятся — пир становится рабочим только после успешного
// mTLS-handshake (см. internal/security), который движок выполняет
// при первой попытке синхронизации.
package discovery

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/mdns"
)

// ServiceType — DNS-SD тип сервиса нод Пржевальского.
const ServiceType = "_przewalski-node._tcp"

// ServiceDomain — домен mDNS-объявления.
const ServiceDomain = "local"

// Announcer — регистрация своей ноды в mDNS.
type Announcer struct {
	mu      sync.Mutex
	server  *mdns.Server
	nodeID  string
	port    int
	stopped bool
}

// ServiceName возвращает полное имя сервиса (network.service_name в конфиге).
func ServiceName() string { return ServiceType + "." + ServiceDomain }

// Announce регистрирует ноду в mDNS: instance = nodeID, порт = grpcPort.
// Адреса берутся с локальных интерфейсов.
func Announce(nodeID string, grpcPort int) (*Announcer, error) {
	if nodeID == "" {
		return nil, fmt.Errorf("discovery: пустой node_id")
	}
	if grpcPort <= 0 {
		return nil, fmt.Errorf("discovery: некорректный gRPC порт %d", grpcPort)
	}
	ips, err := localIPs()
	if err != nil {
		return nil, err
	}
	service, err := mdns.NewMDNSService(nodeID, ServiceType, ServiceDomain, "",
		grpcPort, ips, nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: mdns service: %w", err)
	}
	server, err := mdns.NewServer(&mdns.Config{Zone: service})
	if err != nil {
		return nil, fmt.Errorf("discovery: mdns server: %w", err)
	}
	slog.Info("mdns announced", "node_id", nodeID, "port", grpcPort, "ips", ips)
	return &Announcer{server: server, nodeID: nodeID, port: grpcPort}, nil
}

// Shutdown прекращает отвечать на mDNS-запросы о своей ноде.
func (a *Announcer) Shutdown() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.server != nil && !a.stopped {
		a.stopped = true
		if err := a.server.Shutdown(); err != nil {
			slog.Warn("mdns shutdown failed", "err", err)
		}
	}
}

// localIPs — адреса локальных интерфейсов (без loopback и link-local).
func localIPs() ([]net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("discovery: interface addrs: %w", err)
	}
	var ips []net.IP
	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipnet.IP
		if ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.To4() != nil {
			ips = append(ips, ip)
		}
	}
	return ips, nil
}

// Browser — периодический поиск пиров; найденные адреса отдаются callback'у.
type Browser struct {
	service string
}

// NewBrowser создаёт поисковик по service-имени (обычно ServiceName()).
func NewBrowser(service string) *Browser {
	if service == "" {
		service = ServiceName()
	}
	return &Browser{service: service}
}

// PeerFound — кандидат пира, обнаруженный в сети.
type PeerFound struct {
	NodeID string // имя mDNS-инстанса = node_id
	Addr   string // ip:port gRPC-сервера пира
}

// Run ищет пиров каждые interval и вызывает onPeer для каждого нового
// (по сравнению с уже отданным набором) адреса. Блокируется до отмены ctx.
// Фильтрация собственного node_id — на вызывающем (see SelfFilter).
func (b *Browser) Run(ctx context.Context, interval time.Duration, onPeer func(PeerFound)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	seen := newSeenSet()
	browseOnce(ctx, b.service, seen, onPeer)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			browseOnce(ctx, b.service, seen, onPeer)
		}
	}
}

// browseOnce выполняет один mDNS-запрос.
func browseOnce(ctx context.Context, service string, seen *seenSet, onPeer func(PeerFound)) {
	entries := make(chan *mdns.ServiceEntry, 16)
	params := mdns.DefaultParams(service)
	params.Entries = entries
	params.Timeout = 2 * time.Second
	go func() {
		defer close(entries)
		qctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := mdns.QueryContext(qctx, params); err != nil {
			return // mDNS в сети может отсутствовать — не ошибка уровня ноды
		}
	}()
	for entry := range entries {
		peer, ok := entryToPeer(entry)
		if !ok {
			continue
		}
		if seen.add(peer.Addr) {
			slog.Debug("mdns peer found", "node_id", peer.NodeID, "addr", peer.Addr)
			onPeer(peer)
		}
	}
}

// entryToPeer переводит mDNS-запись в адрес кандидата. Имя инстанса —
// node_id, Host/AddrV4+Port — адрес gRPC-сервера.
func entryToPeer(entry *mdns.ServiceEntry) (PeerFound, bool) {
	if entry == nil || entry.Port <= 0 {
		return PeerFound{}, false
	}
	name := strings.TrimSuffix(entry.Name, ".")
	name = strings.TrimSuffix(name, "."+ServiceType+"."+ServiceDomain)
	if name == "" {
		return PeerFound{}, false
	}
	addr := ""
	if entry.AddrV4 != nil && !entry.AddrV4.IsUnspecified() {
		addr = entry.AddrV4.String()
	} else if entry.AddrV6IPAddr != nil {
		addr = entry.AddrV6IPAddr.String()
	}
	if addr == "" && entry.Host != "" {
		addr = strings.TrimSuffix(entry.Host, ".")
	}
	if addr == "" {
		return PeerFound{}, false
	}
	return PeerFound{NodeID: name, Addr: net.JoinHostPort(addr, fmt.Sprint(entry.Port))}, true
}

// seenSet — дедупликация отданных адресов между проходами.
type seenSet struct {
	mu   sync.Mutex
	seen map[string]struct{}
}

func newSeenSet() *seenSet { return &seenSet{seen: map[string]struct{}{}} }

func (s *seenSet) add(addr string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.seen[addr]; ok {
		return false
	}
	s.seen[addr] = struct{}{}
	return true
}
