package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
)

// pgMigrationsDir — путь к каталогу миграций из пакета sqlstore.
const pgMigrationsDir = "../../../migrations"

// openPGTestStore открывает TEST_PG_DSN в отдельной схеме и применяет миграции;
// без TEST_PG_DSN тест пропускается (образец — internal/storage/postgres).
//
// Изоляция по образцу backend/tests/sync_e2e/comparisons_test.go:37: каждый
// тест работает в собственной схеме (search_path), поэтому запуск пакета идёт
// параллельно с internal/storage/postgres против той же БД без конфликта
// TRUNCATE общих таблиц (флак). Схема удаляется в t.Cleanup (DROP SCHEMA CASCADE).
func openPGTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN не задан — PostgreSQL-тесты пропущены (подними postgres и экспортируй TEST_PG_DSN=postgres://...)")
	}

	// Административное соединение для CREATE/DROP SCHEMA (не трогает search_path стора).
	admin, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = admin.Close() })

	schema := "sqlstore_pg_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = admin.Exec("DROP SCHEMA " + schema + " CASCADE") })

	// Драйвер pgx принимает search_path как runtime-параметр DSN: все
	// неквалифицированные имена (в т.ч. в миграциях) резолвятся в схему.
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	s, err := Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, migrate.Apply(context.Background(), s.DB(), pgMigrationsDir, "postgres"))
	return s
}

// TestProjectBlobDeletedPG_LIKEReferenceAndGuard — на PostgreSQL проверяет то,
// что расходилось с sqlite: LIKE по blob_ids через LOWER(CAST(...)) находит
// ссылку в верхнем регистре (сырой LIKE по JSONB падает, регистрозависимый LIKE
// не совпал бы), а registered_at-guard не применяет устаревшее удаление.
func TestProjectBlobDeletedPG_LIKEReferenceAndGuard(t *testing.T) {
	s := openPGTestStore(t)
	ctx := context.Background()

	// sha256 в верхнем регистре: ValidSha256Hex допускает A-F.
	sha := strings.ToUpper(strings.Repeat("ab", 32))
	blobID := uuid.Must(uuid.NewV7()).String()
	obsID := uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	_, err := s.UpsertBlobAt(ctx, domain.Blob{ID: blobID, SHA256: sha, MimeType: "image/jpeg", Size: 10}, base.Add(-2*time.Hour))
	require.NoError(t, err)
	require.NoError(t, s.CreateObservation(ctx, domain.Observation{
		ID: obsID, NodeID: "n1", ObservedAt: base.Add(-time.Hour), ObserverID: "w",
		BlobIDs: []string{sha},
	}))

	// LIKE по LOWER(blob_ids) находит ссылку несмотря на верхний регистр.
	referenced, err := s.BlobReferenced(ctx, sha)
	require.NoError(t, err)
	require.True(t, referenced, "LIKE должен находить ссылку в верхнем регистре")

	patterns, err := s.blobRefPatterns(ctx, sha)
	require.NoError(t, err)
	require.Contains(t, patterns, `%"`+strings.ToLower(sha)+`"%`,
		"blobRefPatterns должен отдавать шаблон в нижнем регистре")

	// similar-путь: FindSnapshotByBlobSHA использует тот же LIKE и не падает на
	// JSONB (раньше — сырой LIKE: jsonb ~~ unknown).
	require.NoError(t, s.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: uuid.NewString(), ObservationID: obsID, ModelVersion: "v1",
		Traits: json.RawMessage(`{"coat":"bay"}`), Confidence: 0.9, Fingerprint: "fp-pg", Confirmed: true,
	}))
	ts, err := s.FindSnapshotByBlobSHA(ctx, sha)
	require.NoError(t, err, "FindSnapshotByBlobSHA должен находить снимок на PostgreSQL")
	require.Equal(t, obsID, ts.ObservationID)

	// Устаревшее удаление (до регистрации) — no-op.
	n, err := s.MarkBlobDeleted(ctx, sha, base.Add(-3*time.Hour), "stale")
	require.NoError(t, err)
	require.Zero(t, n, "устаревшее удаление не должно применяться")
	// Более позднее удаление применяется.
	n, err = s.MarkBlobDeleted(ctx, sha, base, "ok")
	require.NoError(t, err)
	require.Equal(t, int64(1), n)
}
