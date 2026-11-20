package sync_test

import (
	"context"
	"errors"
	"testing"

	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
)

type fakeHandshakePeer struct {
	info syncengine.HandshakeInfo
	err  error
}

func (f *fakeHandshakePeer) Handshake(context.Context, syncengine.HandshakeRequest) (syncengine.HandshakeInfo, error) {
	return f.info, f.err
}

// TestHandshakeVersionCheck — несовместимая версия протокола отклоняется.
func TestHandshakeVersionCheck(t *testing.T) {
	local := syncengine.NodeIdentity{NodeID: "a", Role: syncengine.RoleLocal, AppVersion: "1.0"}
	ok := &fakeHandshakePeer{info: syncengine.HandshakeInfo{NodeID: "b", ProtocolVersion: syncengine.ProtocolVersion, Role: syncengine.RoleCentral}}
	if _, err := syncengine.Handshake(context.Background(), ok, local); err != nil {
		t.Fatalf("compatible peer rejected: %v", err)
	}
	bad := &fakeHandshakePeer{info: syncengine.HandshakeInfo{NodeID: "b", ProtocolVersion: "0.9"}}
	if _, err := syncengine.Handshake(context.Background(), bad, local); !errors.Is(err, syncengine.ErrProtocolVersion) {
		t.Fatalf("want ErrProtocolVersion, got %v", err)
	}
	self := &fakeHandshakePeer{info: syncengine.HandshakeInfo{NodeID: "a", ProtocolVersion: syncengine.ProtocolVersion}}
	if _, err := syncengine.Handshake(context.Background(), self, local); err == nil {
		t.Fatal("peer with our own node id should be rejected")
	}
}
