// Счётчики sync-движка: events_sent/received, last_sync_at и последняя ошибка
// по каждому пиру. Потокобезопасно: пишутся из sync-цикла, читаются из /api/v1/node.
package app

import (
	"sync"
	"time"
)

// PeerStats — счётчики обмена с одним пиром.
type PeerStats struct {
	Addr           string    `json:"addr"`
	Kind           string    `json:"kind"` // static | discovered | central
	NodeID         string    `json:"node_id,omitempty"`
	EventsSent     int       `json:"events_sent"`
	EventsReceived int       `json:"events_received"`
	LastSyncAt     time.Time `json:"last_sync_at,omitempty"`
	LastError      string    `json:"last_error,omitempty"`
}

// Stats — реестр счётчиков по адресам пиров.
type Stats struct {
	mu    sync.Mutex
	peers map[string]*PeerStats
}

func newStats() *Stats { return &Stats{peers: map[string]*PeerStats{}} }

// register создаёт запись пира, если её ещё нет.
func (s *Stats) register(addr, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.peers[addr]; !ok {
		s.peers[addr] = &PeerStats{Addr: addr, Kind: kind}
	}
}

// record обновляет счётчики после цикла обмена с пиром.
func (s *Stats) record(addr, nodeID string, sent, received int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.peers[addr]
	if !ok {
		st = &PeerStats{Addr: addr}
		s.peers[addr] = st
	}
	st.NodeID = nodeID
	st.EventsSent += sent
	st.EventsReceived += received
	st.LastSyncAt = time.Now().UTC()
	if err != nil {
		st.LastError = err.Error()
	} else {
		st.LastError = ""
	}
}

// Snapshot — срез счётчиков, отсортированный по адресу (стабильный JSON).
func (s *Stats) Snapshot() []PeerStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PeerStats, 0, len(s.peers))
	for _, st := range s.peers {
		out = append(out, *st)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Addr < out[j-1].Addr; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
