package discovery

import (
	"net"
	"testing"

	"github.com/hashicorp/mdns"
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
	if !ok {
		t.Fatal("expected valid peer")
	}
	if peer.NodeID != "01990000-0000-7000-8000-0000000000aa" {
		t.Fatalf("node id = %q", peer.NodeID)
	}
	if peer.Addr != "192.168.1.10:9443" {
		t.Fatalf("addr = %q", peer.Addr)
	}

	// без адреса запись бесполезна
	if _, ok := entryToPeer(&mdns.ServiceEntry{Name: "x", Port: 1}); ok {
		t.Fatal("entry without address must be rejected")
	}
	// без порта запись бесполезна
	if _, ok := entryToPeer(&mdns.ServiceEntry{Name: "x", AddrV4: net.IPv4(1, 2, 3, 4)}); ok {
		t.Fatal("entry without port must be rejected")
	}
}

// TestSeenSetDedup — один и тот же адрес отдаётся callback'у один раз.
func TestSeenSetDedup(t *testing.T) {
	seen := newSeenSet()
	if !seen.add("1.2.3.4:9") {
		t.Fatal("first add must report new")
	}
	if seen.add("1.2.3.4:9") {
		t.Fatal("duplicate must be filtered")
	}
	if !seen.add("1.2.3.5:9") {
		t.Fatal("different addr must report new")
	}
}

// TestServiceName — полное имя сервиса соответствует DNS-SD форме.
func TestServiceName(t *testing.T) {
	if got, want := ServiceName(), "_przewalski-node._tcp.local"; got != want {
		t.Fatalf("service name = %q, want %q", got, want)
	}
}
