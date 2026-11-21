package discovery

import (
	"net"
	"testing"

	"github.com/hashicorp/mdns"
	"github.com/stretchr/testify/require"
)

// TestEntryToPeer — mDNS-запись переводится в адрес кандидата:
// имя инстанса = node_id (без суффикса сервиса), адрес = ip:port.
func TestEntryToPeer(t *testing.T) {
	entry := &mdns.ServiceEntry{
		Name:   "01990000-0000-7000-8000-0000000000aa." + ServiceType + "." + ServiceDomain + ".",
		Host:   "nodehost.local.",
		AddrV4: net.IPv4(192, 168, 1, 10),
		Port:   9443,
	}
	peer, ok := entryToPeer(entry)
	require.True(t, ok, "expected valid peer")
	require.Equal(t, "01990000-0000-7000-8000-0000000000aa", peer.NodeID, "node id")
	require.Equal(t, "192.168.1.10:9443", peer.Addr, "addr")

	// без адреса запись бесполезна
	_, ok = entryToPeer(&mdns.ServiceEntry{Name: "x", Port: 1})
	require.False(t, ok, "entry without address must be rejected")
	// без порта запись бесполезна
	_, ok = entryToPeer(&mdns.ServiceEntry{Name: "x", AddrV4: net.IPv4(1, 2, 3, 4)})
	require.False(t, ok, "entry without port must be rejected")
}

// TestSeenSetDedup — один и тот же адрес отдаётся callback'у один раз.
func TestSeenSetDedup(t *testing.T) {
	seen := newSeenSet()
	require.True(t, seen.add("1.2.3.4:9"), "first add must report new")
	require.False(t, seen.add("1.2.3.4:9"), "duplicate must be filtered")
	require.True(t, seen.add("1.2.3.5:9"), "different addr must report new")
}

// TestServiceName — полное имя сервиса соответствует DNS-SD форме.
func TestServiceName(t *testing.T) {
	require.Equal(t, "_przewalski-node._tcp.local", ServiceName(), "service name")
}
