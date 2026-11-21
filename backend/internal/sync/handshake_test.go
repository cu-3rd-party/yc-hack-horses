package sync_test

import (
	"context"
	"testing"

	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"

	"github.com/stretchr/testify/require"
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
	_, err := syncengine.Handshake(context.Background(), ok, local)
	require.NoError(t, err, "compatible peer rejected")
	bad := &fakeHandshakePeer{info: syncengine.HandshakeInfo{NodeID: "b", ProtocolVersion: "0.9"}}
	_, err = syncengine.Handshake(context.Background(), bad, local)
	require.ErrorIs(t, err, syncengine.ErrProtocolVersion, "want ErrProtocolVersion")
	self := &fakeHandshakePeer{info: syncengine.HandshakeInfo{NodeID: "a", ProtocolVersion: syncengine.ProtocolVersion}}
	_, err = syncengine.Handshake(context.Background(), self, local)
	require.Error(t, err, "peer with our own node id should be rejected")
}
