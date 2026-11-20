// ForegroundSync: исходящая синхронизация Go Core с центральной нодой (ADR-0011).
// Только исходящие gRPC-соединения, только по явному запросу из UI или при
// открытии приложения — ни фонового демона, ни inbound-сервера, ни mDNS.
package mobilebind

import (
	"context"
	"time"

	syncv1 "github.com/cu3rd/yc-hack-horses/backend/api/gen/sync/v1"
	"github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	grpcclient "github.com/cu3rd/yc-hack-horses/backend/internal/transport/grpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// syncResult — JSON-результат одного цикла ForegroundSync.
type syncResult struct {
	Peer        string `json:"peer"`
	Pulled      int    `json:"pulled"`
	Pushed      int    `json:"pushed"`
	DurationMs  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
	SkippedInit bool   `json:"skipped_uninitialized,omitempty"`
}

// ForegroundSync выполняет один цикл синхронизации с пиром по адресу host:port
// (gRPC, sync/v1). Возвращает JSON syncResult; сетевые ошибки не паникуют, а
// попадают в поле error — UI должен показывать результат как есть.
func ForegroundSync(peerAddr string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		res := syncResult{Peer: peerAddr, Error: "core not initialized", SkippedInit: true}
		return string(mustJSON(res)), nil
	}
	if peerAddr == "" {
		res := syncResult{Error: "peer address required"}
		return string(mustJSON(res)), nil
	}
	start := time.Now()
	res := syncResult{Peer: peerAddr}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// V0: plaintext-канал. mTLS-доверие (ADR-0007) в мобильной сборке не настроено —
	// включается отдельно после выпуска клиентских сертификатов устройству.
	conn, err := grpc.NewClient(peerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		res.Error = err.Error()
		res.DurationMs = time.Since(start).Milliseconds()
		return string(mustJSON(res)), nil
	}
	defer conn.Close()

	peer := grpcclient.NewClient(syncv1.NewSyncServiceClient(conn), nil)
	engine := sync.NewEngine(c.store, c.blobs, sync.NodeIdentity{
		NodeID:     c.nodeID,
		Role:       sync.RoleLocal,
		AppVersion: appVersion,
	}, sync.Config{}, nil)
	pulled, pushed, err := engine.Sync(ctx, peer)
	res.Pulled, res.Pushed = pulled, pushed
	res.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		res.Error = err.Error()
	}
	return string(mustJSON(res)), nil
}
