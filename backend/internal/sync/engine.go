// Package sync — единый движок синхронизации для всех типов нод (ADR-0002).
// Различия нод — через storage.EventStore, capabilities и транспорт; сама логика одна.
//
// Прогресс синхронизации — векторные часы (ADR-0005): map node_id -> последний
// применённый sequence. Обрыв соединения не требует восстановления сессии —
// достаточно повторного цикла Sync с актуальным clock.
package sync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// Роли нод (ADR-0008).
const (
	RoleLocal   = "local"
	RoleCentral = "central"
)

// ProtocolVersion — версия межнодового протокола; сверяется при Handshake.
const ProtocolVersion = "1"

// ErrProtocolVersion — пир ответил несовместимой версией протокола.
var ErrProtocolVersion = errors.New("protocol version mismatch")

// ErrBlobHash — полученные данные не совпали с запрошенным sha256.
var ErrBlobHash = errors.New("blob sha256 mismatch")

// NodeIdentity — локальные данные ноды, нужные движку и протоколу.
// Проверка доступности пиров вынесена в PeerChecker, чтобы не тянуть сеть в движок.
type NodeIdentity struct {
	NodeID     string
	Role       string // RoleLocal | RoleCentral
	AppVersion string
}

// PeerChecker решает, с какими пирами синхронизоваться (доступность, расписание).
type PeerChecker interface {
	// Reachable сообщает, стоит ли пытаться синхронизоваться с пиром по адресу.
	Reachable(ctx context.Context, addr string) bool
}

// Config — параметры движка.
type Config struct {
	// BatchLimit — максимум событий в одном батче/стриме. 0 = 256.
	BatchLimit int
	// ChunkSize — размер чанка blob-передачи в байтах. 0 = 64KiB.
	ChunkSize int
	// RPCTimeout — таймаут одного RPC к пиру. 0 = 30s.
	RPCTimeout time.Duration
	// MaxRounds — число циклов обмена событиями за один Sync. 0 = 8.
	MaxRounds int
	// MaxBlobsPerSync — максимум blob'ов, скачиваемых за один цикл Sync.
	// Ограничивает трафик: датасет может быть на много порядков больше, чем
	// наблюдений, поэтому доливается постепенно, за несколько циклов. 0 = 64.
	MaxBlobsPerSync int
	// NeededBlobHashes — хэши blob'ов, на которые ссылается эта нода
	// (наблюдения локального лога). Синк — неполный и по потребности: движок
	// качает только недостающие из списка, а не весь реестр. nil = blob-синхронизация off.
	NeededBlobHashes func(ctx context.Context) ([]string, error)
}

func (c Config) batchLimit() int {
	if c.BatchLimit <= 0 {
		return 256
	}
	return c.BatchLimit
}

func (c Config) chunkSize() int {
	if c.ChunkSize <= 0 {
		return 64 << 10
	}
	return c.ChunkSize
}

func (c Config) rpcTimeout() time.Duration {
	if c.RPCTimeout <= 0 {
		return 30 * time.Second
	}
	return c.RPCTimeout
}

func (c Config) maxRounds() int {
	if c.MaxRounds <= 0 {
		return 8
	}
	return c.MaxRounds
}

func (c Config) maxBlobsPerSync() int {
	if c.MaxBlobsPerSync <= 0 {
		return 64
	}
	return c.MaxBlobsPerSync
}

// Peer — транспортный клиент пира. Реализуется gRPC-адаптером
// (internal/transport/grpc) и фейками в тестах. Таймауты применяет реализация.
type Peer interface {
	// ExchangeVectorClock возвращает часы пира после обмена ours.
	ExchangeVectorClock(ctx context.Context, ours storage.VectorClock) (storage.VectorClock, error)
	// PullEvents отдаёт стрим событий, которых нет у вызывающего. yield получает
	// батчи до limit; возвращённая из yield ошибка останавливает стрим.
	PullEvents(ctx context.Context, have storage.VectorClock, limit int, yield func([]domain.Event) error) error
	// PushEvents открывает клиентский стрим: sink.Send батчами, Close отдаёт ACK.
	PushEvents(ctx context.Context) (PeerEventSink, error)
	// DownloadBlob возвращает reader с содержимым blob пира.
	DownloadBlob(ctx context.Context, sha256hex string) (io.ReadCloser, error)
}

// PeerEventSink — клиентская сторона стрима PushEvents.
type PeerEventSink interface {
	Send(events []domain.Event) error
	// Close завершает стрим и возвращает подтверждение записанного.
	Close() (Ack, error)
}

// Ack — подтверждение PushEvents.
type Ack struct {
	AckedEventIDs []string
	AckedThrough  uint64
	NodeID        string
}

// Engine — ядро синхронизации. Работает только через контракты storage.
type Engine struct {
	events   storage.EventStore
	blobs    storage.BlobStore
	identity NodeIdentity
	cfg      Config
	log      *slog.Logger
}

// NewEngine собирает движок. blobs может быть nil — тогда синхронизуются только события.
func NewEngine(events storage.EventStore, blobs storage.BlobStore, id NodeIdentity, cfg Config, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{events: events, blobs: blobs, identity: id, cfg: cfg, log: log}
}

// NodeID — идентификатор этой ноды.
func (e *Engine) NodeID() string { return e.identity.NodeID }

// Role — роль этой ноды.
func (e *Engine) Role() string { return e.identity.Role }

// AppVersion — версия приложения этой ноды.
func (e *Engine) AppVersion() string { return e.identity.AppVersion }

// LocalVectorClock — текущий прогресс применения событий.
func (e *Engine) LocalVectorClock(ctx context.Context) (storage.VectorClock, error) {
	return e.events.GetVectorClock(ctx)
}

// Sync выполняет полный цикл обмена с пирами: PullEvents (недостающее у нас) и
// PushEvents (недостающее у пира), затем blob-синхронизацию, пока обе стороны
// не сойдутся. Возвращает число принятых и отправленных событий.
func (e *Engine) Sync(ctx context.Context, peer Peer) (pulled int, pushed int, err error) {
	for round := 0; round < e.cfg.maxRounds(); round++ {
		if ctx.Err() != nil {
			return pulled, pushed, ctx.Err()
		}
		// Обмен часами: пир говорит, что у него есть; мы говорим, что есть у нас.
		local, err := e.events.GetVectorClock(ctx)
		if err != nil {
			return pulled, pushed, fmt.Errorf("sync: local vector clock: %w", err)
		}
		remote, err := peer.ExchangeVectorClock(ctx, local)
		if err != nil {
			return pulled, pushed, fmt.Errorf("sync: exchange vector clock: %w", err)
		}

		p, err := e.pullMissing(ctx, peer, local)
		pulled += p
		if err != nil {
			return pulled, pushed, err
		}
		q, err := e.pushMissing(ctx, peer, remote)
		pushed += q
		if err != nil {
			return pulled, pushed, err
		}
		if p == 0 && q == 0 {
			break // сходимость: обе стороны сообщили, что недостающего нет
		}
	}
	if err := e.syncBlobs(ctx, peer); err != nil {
		return pulled, pushed, fmt.Errorf("sync: blobs: %w", err)
	}
	return pulled, pushed, nil
}

// pullMissing принимает от пира события, отсутствующие в локальном логе.
// Append идемпотентен по (node_id, sequence), так что повторная передача не
// создаёт дубликатов.
func (e *Engine) pullMissing(ctx context.Context, peer Peer, have storage.VectorClock) (int, error) {
	got := 0
	err := peer.PullEvents(ctx, have, e.cfg.batchLimit(), func(batch []domain.Event) error {
		if err := e.events.Append(ctx, batch); err != nil {
			return fmt.Errorf("sync: append pulled batch: %w", err)
		}
		got += len(batch)
		return nil
	})
	if err != nil {
		return got, fmt.Errorf("sync: pull events: %w", err)
	}
	return got, nil
}

// pushMissing отправляет пиру события, которых у него нет (по его часам remote).
func (e *Engine) pushMissing(ctx context.Context, peer Peer, remote storage.VectorClock) (int, error) {
	missing, err := e.events.GetMissingEvents(ctx, remote)
	if err != nil {
		return 0, fmt.Errorf("sync: select missing events: %w", err)
	}
	if len(missing) == 0 {
		return 0, nil
	}
	sink, err := peer.PushEvents(ctx)
	if err != nil {
		return 0, fmt.Errorf("sync: open push stream: %w", err)
	}
	for start := 0; start < len(missing); start += e.cfg.batchLimit() {
		end := min(start+e.cfg.batchLimit(), len(missing))
		if err := sink.Send(missing[start:end]); err != nil {
			return 0, fmt.Errorf("sync: push batch: %w", err)
		}
	}
	ack, err := sink.Close()
	if err != nil {
		return 0, fmt.Errorf("sync: push ack: %w", err)
	}
	e.log.Debug("push acknowledged", "peer", ack.NodeID, "acked", len(ack.AckedEventIDs))
	return len(missing), nil
}
