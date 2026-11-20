// Package contract: общий набор контракт-тестов EventStore и BlobStore.
// Драйверы (sqlite, postgres) вызывают RunEventStoreSuite / RunBlobStoreSuite
// из своих _test.go, поэтому набор гарантированно одинаковый.
package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"

	"github.com/stretchr/testify/require"
)

// EventStoreFactory открывает хранилище с применёнными миграциями. Каждый вызов
// даёт ИЗОЛИРОВАННОЕ хранилище; reopen переоткрывает то же (тот же DSN) — для RestartRecovery.
type EventStoreFactory func(t *testing.T) (store storage.EventStore, reopen func() storage.EventStore)

// EventStoreWithTx — необязательное расширение: транзакционная запись журнала.
type EventStoreWithTx interface {
	storage.EventStore
	RunInTxEvents(ctx context.Context, fn func(tx storage.EventStore) error) error
}

func makeEvent(nodeID string, seq uint64, eventType string) domain.Event {
	payload, err := json.Marshal(map[string]any{"k": fmt.Sprintf("%s-%d", nodeID, seq)})
	if err != nil {
		panic(err) // в тестовом хелпере маршал map не падает
	}
	return domain.Event{
		EventID:       "evt-" + nodeID + "-" + fmt.Sprint(seq),
		NodeID:        nodeID,
		Sequence:      seq,
		EventType:     eventType,
		SchemaVersion: 1,
		AggregateID:   "agg-" + nodeID,
		OccurredAt:    time.Now().UTC().Truncate(time.Millisecond),
		Payload:       payload,
	}
}

// RunEventStoreSuite прогоняет весь контрактный набор EventStore.
func RunEventStoreSuite(t *testing.T, newStore EventStoreFactory) {
	t.Helper()

	t.Run("AppendIdempotent", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		batch := []domain.Event{makeEvent("node-a", 1, domain.EventHorseCreated), makeEvent("node-a", 2, domain.EventHorseCreated)}
		require.NoError(t, s.Append(ctx, batch))
		require.NoError(t, s.Append(ctx, batch)) // повтор — no-op
		clock, err := s.GetVectorClock(ctx)
		require.NoError(t, err)
		require.Equal(t, storage.VectorClock{"node-a": 2}, clock)
	})

	t.Run("GetEventsByCursor", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		require.NoError(t, s.Append(ctx, []domain.Event{
			makeEvent("node-a", 1, domain.EventHorseCreated),
			makeEvent("node-a", 2, domain.EventHorseCreated),
			makeEvent("node-b", 1, domain.EventObservationAdded),
		}))

		all, err := s.GetEvents(ctx, storage.Cursor{}, 0)
		require.NoError(t, err)
		require.Len(t, all, 3)
		// порядок: (node_id, sequence)
		require.Equal(t, "node-a", all[0].NodeID)
		require.Equal(t, "node-b", all[2].NodeID)

		after, err := s.GetEvents(ctx, storage.Cursor{NodeID: "node-a", Sequence: 1}, 0)
		require.NoError(t, err)
		require.Len(t, after, 2) // node-a#2 и всё node-b
		require.Equal(t, uint64(2), after[0].Sequence)
		require.Equal(t, "node-b", after[1].NodeID)

		limited, err := s.GetEvents(ctx, storage.Cursor{}, 1)
		require.NoError(t, err)
		require.Len(t, limited, 1)
	})

	t.Run("GetVectorClock", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		require.NoError(t, s.Append(ctx, []domain.Event{
			makeEvent("node-a", 3, domain.EventHorseCreated),
			makeEvent("node-b", 7, domain.EventHorseCreated),
		}))
		clock, err := s.GetVectorClock(ctx)
		require.NoError(t, err)
		require.Equal(t, storage.VectorClock{"node-a": 3, "node-b": 7}, clock)
	})

	t.Run("GetMissingEvents", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		require.NoError(t, s.Append(ctx, []domain.Event{
			makeEvent("node-a", 1, domain.EventHorseCreated),
			makeEvent("node-a", 2, domain.EventHorseCreated),
			makeEvent("node-a", 3, domain.EventHorseCreated),
			makeEvent("node-c", 1, domain.EventHorseCreated),
		}))
		missing, err := s.GetMissingEvents(ctx, storage.VectorClock{"node-a": 2})
		require.NoError(t, err)
		require.Len(t, missing, 2)
		require.Equal(t, uint64(3), missing[0].Sequence) // node-a#3
		require.Equal(t, "node-c", missing[1].NodeID)   // ноды нет в remote clock — всё отдаём

		empty, err := s.GetMissingEvents(ctx, storage.VectorClock{"node-a": 3, "node-c": 1})
		require.NoError(t, err)
		require.Empty(t, empty)
	})

	t.Run("LastSequence", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		seq, err := s.LastSequence(ctx, "node-a")
		require.NoError(t, err)
		require.Zero(t, seq) // нет событий — 0
		require.NoError(t, s.Append(ctx, []domain.Event{makeEvent("node-a", 5, domain.EventHorseCreated)}))
		seq, err = s.LastSequence(ctx, "node-a")
		require.NoError(t, err)
		require.Equal(t, uint64(5), seq)
	})

	t.Run("UniquenessNodeSequence", func(t *testing.T) {
		s, _ := newStore(t)
		ctx := context.Background()
		first := makeEvent("node-a", 1, domain.EventHorseCreated)
		require.NoError(t, s.Append(ctx, []domain.Event{first}))
		// конфликт (node_id, sequence) с другим event_id: первый побеждает, ошибки нет
		conflict := makeEvent("node-a", 1, domain.EventObservationAdded)
		require.NoError(t, s.Append(ctx, []domain.Event{conflict}))
		events, err := s.GetEvents(ctx, storage.Cursor{}, 0)
		require.NoError(t, err)
		require.Len(t, events, 1)
		require.Equal(t, first.EventID, events[0].EventID)
	})

	t.Run("Transactionality", func(t *testing.T) {
		s, _ := newStore(t)
		txs, ok := s.(EventStoreWithTx)
		if !ok {
			t.Skip("хранилище не поддерживает транзакционную запись")
		}
		ctx := context.Background()
		err := txs.RunInTxEvents(ctx, func(tx storage.EventStore) error {
			if err := tx.Append(ctx, []domain.Event{makeEvent("node-a", 1, domain.EventHorseCreated)}); err != nil {
				return err
			}
			return errors.New("rollback me")
		})
		require.Error(t, err)
		clock, err2 := s.GetVectorClock(ctx)
		require.NoError(t, err2)
		require.Empty(t, clock) // откат: ничего не записано

		require.NoError(t, txs.RunInTxEvents(ctx, func(tx storage.EventStore) error {
			return tx.Append(ctx, []domain.Event{makeEvent("node-a", 1, domain.EventHorseCreated)})
		}))
		clock, err = s.GetVectorClock(ctx)
		require.NoError(t, err)
		require.Equal(t, storage.VectorClock{"node-a": 1}, clock)
	})

	t.Run("RestartRecovery", func(t *testing.T) {
		s, reopen := newStore(t)
		ctx := context.Background()
		require.NoError(t, s.Append(ctx, []domain.Event{
			makeEvent("node-a", 1, domain.EventHorseCreated),
			makeEvent("node-a", 2, domain.EventObservationAdded),
		}))
		// закрываем и переоткрываем то же хранилище
		if closer, ok := s.(interface{ Close() error }); ok {
			require.NoError(t, closer.Close())
		}
		s2 := reopen()
		events, err := s2.GetEvents(ctx, storage.Cursor{}, 0)
		require.NoError(t, err)
		require.Len(t, events, 2)
		clock, err := s2.GetVectorClock(ctx)
		require.NoError(t, err)
		require.Equal(t, storage.VectorClock{"node-a": 2}, clock)
	})
}

// BlobStoreFactory открывает файловый BlobStore; reopen переоткрывает тот же каталог.
type BlobStoreFactory func(t *testing.T) (store storage.BlobStore, reopen func() storage.BlobStore)

// RunBlobStoreSuite прогоняет контрактный набор BlobStore.
func RunBlobStoreSuite(t *testing.T, newStore BlobStoreFactory) {
	t.Helper()

	sha := func(data []byte) string {
		sum := sha256.Sum256(data)
		return hex.EncodeToString(sum[:])
	}

	t.Run("PutGetRoundtrip", func(t *testing.T) {
		bs, _ := newStore(t)
		ctx := context.Background()
		data := []byte("приваз, лошадка")
		h := sha(data)
		require.NoError(t, bs.Put(ctx, h, "text/plain", data))
		got, err := bs.Get(ctx, h)
		require.NoError(t, err)
		require.True(t, bytes.Equal(data, got))
		ok, err := bs.Has(ctx, h)
		require.NoError(t, err)
		require.True(t, ok)
		size, err := bs.Size(ctx, h)
		require.NoError(t, err)
		require.EqualValues(t, len(data), size)
	})

	t.Run("Deduplication", func(t *testing.T) {
		bs, _ := newStore(t)
		ctx := context.Background()
		data := []byte("same content")
		h := sha(data)
		require.NoError(t, bs.Put(ctx, h, "text/plain", data))
		require.NoError(t, bs.Put(ctx, h, "application/octet-stream", data)) // повтор — no-op
		size, err := bs.Size(ctx, h)
		require.NoError(t, err)
		require.EqualValues(t, len(data), size)
	})

	t.Run("Delete", func(t *testing.T) {
		bs, _ := newStore(t)
		ctx := context.Background()
		data := []byte("doomed")
		h := sha(data)
		require.NoError(t, bs.Put(ctx, h, "", data))
		require.NoError(t, bs.Delete(ctx, h))
		_, err := bs.Get(ctx, h)
		require.True(t, errors.Is(err, storage.ErrNotFound))
		require.ErrorIs(t, bs.Delete(ctx, h), storage.ErrNotFound) // повторное удаление — not found
	})

	t.Run("ListMissing", func(t *testing.T) {
		bs, _ := newStore(t)
		ctx := context.Background()
		h1 := sha([]byte("one"))
		h2 := sha([]byte("two"))
		require.NoError(t, bs.Put(ctx, h1, "", []byte("one")))
		missing, err := bs.ListMissing(ctx, []string{h1, h2})
		require.NoError(t, err)
		require.Equal(t, []string{h2}, missing)
	})

	t.Run("InvalidHashRejected", func(t *testing.T) {
		bs, _ := newStore(t)
		ctx := context.Background()
		require.Error(t, bs.Put(ctx, "не хэш", "", []byte("x")))
		_, err := bs.Get(ctx, "zz")
		require.Error(t, err)
	})

	t.Run("RestartRecovery", func(t *testing.T) {
		bs, reopen := newStore(t)
		ctx := context.Background()
		data := []byte("survives restart")
		h := sha(data)
		require.NoError(t, bs.Put(ctx, h, "", data))
		bs2 := reopen()
		got, err := bs2.Get(ctx, h)
		require.NoError(t, err)
		require.True(t, bytes.Equal(data, got))
	})
}
