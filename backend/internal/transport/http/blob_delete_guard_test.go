// blob_delete_guard_test.go: покрытие гарда внутри транзакции удаления.
// Интеграционный тест на sqlite до ветки n == 0 не доходит (проигравшие
// получают 404 на предварительной проверке), поэтому тело транзакции
// вызывается напрямую при уже удалённой строке.
package http

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	migrations "github.com/cu3rd/yc-hack-horses/backend/migrations"

	"github.com/google/uuid"
)

// TestMarkBlobDeletedTxGuard: строку удалил конкурент между GetBlobMeta и
// транзакцией проигравшего, поэтому UPDATE ... WHERE deleted_at = 0 обязан
// вернуть 0, а markBlobDeletedTx — errBlobAlreadyDeleted и не писать событие.
func TestMarkBlobDeletedTxGuard(t *testing.T) {
	ctx := context.Background()
	store, err := sqlstore.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	require.NoError(t, migrate.ApplyFS(ctx, store.DB(), migrations.FS(), "sqlite"))

	blob := domain.Blob{
		ID:       uuid.Must(uuid.NewV7()).String(),
		SHA256:   strings.Repeat("ab", 32),
		MimeType: "image/jpeg",
		Size:     3,
	}
	created, err := store.UpsertBlob(ctx, blob)
	require.NoError(t, err)
	require.True(t, created)

	now := time.Now().UTC()
	// Победитель гонки: удаляет строку и пишет первое событие.
	var winner domain.Event
	require.NoError(t, store.RunInTx(ctx, func(tx *sqlstore.Store) error {
		var err error
		winner, err = markBlobDeletedTx(ctx, tx, "test-node", blob.SHA256, now, "winner",
			domain.BlobDeleted{SHA256: blob.SHA256, DeletedAt: now, Reason: "winner"})
		return err
	}))

	// Проигравший прошёл предварительную проверку, но строка уже удалена:
	// гард возвращает 0 строк, транзакция откатывается.
	loserPayload := domain.BlobDeleted{SHA256: blob.SHA256, DeletedAt: now.Add(time.Second), Reason: "loser"}
	err = store.RunInTx(ctx, func(tx *sqlstore.Store) error {
		_, err := markBlobDeletedTx(ctx, tx, "test-node", blob.SHA256, now.Add(time.Second), "loser", loserPayload)
		return err
	})
	require.ErrorIs(t, err, errBlobAlreadyDeleted)

	events, err := store.GetEvents(ctx, storage.Cursor{}, 0)
	require.NoError(t, err)
	var deleted int
	for _, ev := range events {
		if ev.EventType == domain.EventBlobDeleted {
			deleted++
		}
	}
	require.Equal(t, 1, deleted, "проигравший не должен писать второе blob.deleted")
	require.Len(t, events, 1, "проигравший не должен сжигать sequence")
	require.Equal(t, uint64(1), winner.Sequence)
}
