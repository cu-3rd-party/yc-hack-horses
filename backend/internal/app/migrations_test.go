package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// registeredEventAt — blob.registered с заданным временем (журнал регистраций).
func registeredEventAt(t *testing.T, seq uint64, sha string, at time.Time) domain.Event {
	t.Helper()
	payload, err := json.Marshal(domain.Blob{ID: uuid.NewString(), SHA256: sha, MimeType: "image/jpeg", Size: 10})
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: testNodeID, Sequence: seq, EventType: domain.EventBlobRegistered,
		SchemaVersion: 1, AggregateID: sha, OccurredAt: at, Payload: payload,
	}
}

// TestMigration006_BackfillRegisteredAt — миграция 006 заполняет registered_at
// из журнала (MAX(occurred_at) события blob.registered), а не из created_at.
// Иначе блоб, перезалитый до миграции, имел бы registered_at = created_at и был
// бы уязвим для старого delete-события (из окна до ре-аплоада).
func TestMigration006_BackfillRegisteredAt(t *testing.T) {
	ctx := context.Background()
	srcDir := filepath.Join("..", "..", "migrations", "sqlite")
	entries, err := os.ReadDir(srcDir)
	require.NoError(t, err)

	// Каталог миграций без 006: её применяем вручную, после вставки legacy-данных.
	tmp := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmp, "sqlite"), 0o755))
	var migration006 string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		require.NoError(t, err)
		if strings.HasPrefix(e.Name(), "006_") {
			migration006 = string(data)
			continue
		}
		require.NoError(t, os.WriteFile(filepath.Join(tmp, "sqlite", e.Name()), data, 0o644))
	}
	require.NotEmpty(t, migration006, "не найден файл миграции 006")

	st, err := sqlstore.Open("sqlite", filepath.Join(tmp, "legacy.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, migrate.Apply(ctx, st.DB(), tmp, "sqlite"))

	sha := strings.Repeat("ab", 32)
	createdAt := time.Now().UTC().Add(-4 * time.Hour).Truncate(time.Millisecond)
	firstUpload := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Millisecond)
	reupload := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)

	// Legacy-строка блоба: колонки registered_at ещё нет, created_at — старое время.
	_, err = st.DB().ExecContext(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES (?, ?, 'image/jpeg', 10, ?)`,
		uuid.NewString(), sha, createdAt.UnixMilli())
	require.NoError(t, err)
	// Журнал: первичная регистрация и ре-аплоад до миграции (сырой Append, без проекций).
	require.NoError(t, st.Append(ctx, []domain.Event{
		registeredEventAt(t, 1, sha, firstUpload),
		registeredEventAt(t, 2, sha, reupload),
	}))

	// Миграция 006: добавляет колонку и бэкфиллит её из журнала.
	_, err = st.DB().ExecContext(ctx, migration006)
	require.NoError(t, err)

	var registeredAt int64
	require.NoError(t, st.DB().QueryRowContext(ctx,
		`SELECT registered_at FROM blobs WHERE sha256 = ?`, sha).Scan(&registeredAt))
	require.Equal(t, reupload.UnixMilli(), registeredAt,
		"registered_at должен быть временем последнего blob.registered")

	// Старое удаление (между первой и повторной регистрацией) — no-op.
	n, err := st.MarkBlobDeleted(ctx, sha, firstUpload.Add(time.Minute), "stale")
	require.NoError(t, err)
	require.Zero(t, n, "старое delete-событие не должно применяться к перезалитому блобу")
	_, err = st.GetBlobMeta(ctx, sha)
	require.NoError(t, err, "блоб должен остаться активным")

	// Более позднее удаление применяется.
	n, err = st.MarkBlobDeleted(ctx, sha, reupload.Add(time.Minute), "now")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}
