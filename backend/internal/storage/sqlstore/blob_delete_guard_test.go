// blob_delete_guard_test.go: границы guard'а registered_at в MarkBlobDeleted.
// Сценарий «устаревшее удаление после ре-аплоада» на e2e не воспроизводим:
// допуск BlobDeleteClockSkew (5 минут) поглощает разницу в доли секунды, поэтому
// проверка перенесена сюда, где метки времени задаются напрямую.
package sqlstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	migrations "github.com/cu3rd/yc-hack-horses/backend/migrations"
)

// openSQLiteTestStore поднимает sqlite в памяти со всеми миграциями.
func openSQLiteTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, migrate.ApplyFS(context.Background(), s.DB(), migrations.FS(), "sqlite"))
	return s
}

// TestMarkBlobDeletedSkewBoundaries — guard сравнивает registered_at с
// deletedAt + BlobDeleteClockSkew. Удаление, отстающее от регистрации больше
// чем на окно, обязано быть no-op (ре-аплоад не убивается ретраем sync);
// удаление в пределах окна применяется (легитимное удаление с отстающих часов).
func TestMarkBlobDeletedSkewBoundaries(t *testing.T) {
	ctx := context.Background()
	registeredAt := time.Now().UTC().Truncate(time.Millisecond)

	// Чуть больше окна skew: устаревшее удаление не применяется.
	t.Run("за окном skew — no-op", func(t *testing.T) {
		s := openSQLiteTestStore(t)
		sha := strings.Repeat("ab", 32)
		_, err := s.UpsertBlobAt(ctx, domain.Blob{
			ID: uuid.Must(uuid.NewV7()).String(), SHA256: sha, MimeType: "image/jpeg", Size: 3,
		}, registeredAt)
		require.NoError(t, err)

		n, err := s.MarkBlobDeleted(ctx, sha, registeredAt.Add(-BlobDeleteClockSkew-time.Millisecond), "stale")
		require.NoError(t, err)
		require.Zero(t, n, "удаление старше окна skew не должно применяться")

		_, deleted, err := s.BlobDeleted(ctx, sha)
		require.NoError(t, err)
		require.False(t, deleted, "блоб обязан остаться активным после устаревшего удаления")
	})

	// В пределах окна skew: удаление применяется, даже если метка чуть раньше
	// registered_at (расхождение часов узлов).
	t.Run("в пределах skew — применяется", func(t *testing.T) {
		s := openSQLiteTestStore(t)
		sha := strings.Repeat("cd", 32)
		_, err := s.UpsertBlobAt(ctx, domain.Blob{
			ID: uuid.Must(uuid.NewV7()).String(), SHA256: sha, MimeType: "image/jpeg", Size: 3,
		}, registeredAt)
		require.NoError(t, err)

		n, err := s.MarkBlobDeleted(ctx, sha, registeredAt.Add(-BlobDeleteClockSkew+time.Millisecond), "fresh")
		require.NoError(t, err)
		require.Equal(t, int64(1), n, "удаление в пределах окна skew должно применяться")

		_, deleted, err := s.BlobDeleted(ctx, sha)
		require.NoError(t, err)
		require.True(t, deleted, "блоб обязан быть удалён в пределах окна skew")
	})

	// Точная граница окна включительна (registered_at <= deletedAt + skew).
	t.Run("ровно на границе — применяется", func(t *testing.T) {
		s := openSQLiteTestStore(t)
		sha := strings.Repeat("ef", 32)
		_, err := s.UpsertBlobAt(ctx, domain.Blob{
			ID: uuid.Must(uuid.NewV7()).String(), SHA256: sha, MimeType: "image/jpeg", Size: 3,
		}, registeredAt)
		require.NoError(t, err)

		n, err := s.MarkBlobDeleted(ctx, sha, registeredAt.Add(-BlobDeleteClockSkew), "boundary")
		require.NoError(t, err)
		require.Equal(t, int64(1), n, "граница skew включительна")
	})
}
