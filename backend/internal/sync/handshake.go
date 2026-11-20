package sync

import (
	"context"
	"fmt"
	"time"
)

// HandshakeInfo — ответ пира на рукопожатие.
type HandshakeInfo struct {
	NodeID          string
	ProtocolVersion string
	AppVersion      string
	Role            string
}

// HandshakePeer — минимальный интерфейс для рукопожатия; полноценный Peer
// включает его через транспортный адаптер.
type HandshakePeer interface {
	Handshake(ctx context.Context, req HandshakeRequest) (HandshakeInfo, error)
}

// HandshakeRequest — параметры исходящего рукопожатия.
type HandshakeRequest struct {
	NodeID     string
	AppVersion string
}

// Handshake выполняет рукопожатие с пиром и проверяет совместимость версии
// протокола. Расхождение версий — ошибка ErrProtocolVersion.
func Handshake(ctx context.Context, peer HandshakePeer, local NodeIdentity) (HandshakeInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	info, err := peer.Handshake(ctx, HandshakeRequest{NodeID: local.NodeID, AppVersion: local.AppVersion})
	if err != nil {
		return HandshakeInfo{}, fmt.Errorf("handshake: %w", err)
	}
	if info.ProtocolVersion != ProtocolVersion {
		return info, fmt.Errorf("%w: peer %q speaks %q, we speak %q",
			ErrProtocolVersion, info.NodeID, info.ProtocolVersion, ProtocolVersion)
	}
	if info.NodeID == local.NodeID {
		return info, fmt.Errorf("handshake: peer %q reports our own node id", info.NodeID)
	}
	return info, nil
}
