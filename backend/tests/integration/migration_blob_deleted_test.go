package integration

// Smoke-тесты миграции 005 (blobs.deleted_at/delete_reason, observations.
// invalidated_at/invalidation_reason) на ЗАПОЛНЕННЫХ таблицах.
//
// Смысл проверки: 005 приходит на работающий узел, где blobs и observations уже
// не пусты. Миграция только добавляет колонки и индексы, поэтому обязана (а) не
// испортить существующие строки, (б) выставить им «активное» значение по
// умолчанию. Семантику значений сверяем с кодом sqlstore: удалённым считается
// блоб с deleted_at != 0 (ListBlobs/GetBlobMeta фильтруют WHERE deleted_at = 0),
// неактуальным — наблюдение с invalidated_at != 0 (comparisons.go фильтрует
// WHERE o.invalidated_at = 0). Ноль в обеих колонках = строка валидна.
//
// Postgres в тест-окружении может быть недоступен, поэтому sqlite проверяем
// полностью, postgres-файл сверяем с sqlite на эквивалентность операторов, а
// реальный прогон 005 на postgres гейтим по TEST_PG_DSN (см. третий тест).

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/postgres"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	"github.com/google/uuid"

	"github.com/stretchr/testify/require"
)

// copyMigration копирует SQL-файл миграции в тестовый каталог.
func copyMigration(t *testing.T, srcDir, dstDir, name string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(srcDir, name))
	require.NoError(t, err, "чтение исходной миграции %s", name)
	require.NoError(t, os.WriteFile(filepath.Join(dstDir, name), data, 0o600))
}

// columnNames возвращает набор колонок таблицы через PRAGMA table_info.
func columnNames(t *testing.T, db *sql.DB, table string) map[string]bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	require.NoError(t, err)
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		require.NoError(t, rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk))
		out[name] = true
	}
	require.NoError(t, rows.Err())
	return out
}

// TestMigrationBlobDeletedOnFilledSQLite прогоняет 005 на таблицах с данными:
// наполняем схему по 003 включительно, накатываем 005 и проверяем дефолты на
// старых строках, работу новых флагов по семантике sqlstore и идемпотентность.
func TestMigrationBlobDeletedOnFilledSQLite(t *testing.T) {
	ctx := context.Background()
	tmp := t.TempDir()

	// Каталог миграций — копия репозиторных sqlite-файлов. Сначала кладём только
	// 001-003, чтобы 005 применилась именно к уже заполненным таблицам.
	migRoot := filepath.Join(tmp, "migrations")
	migDir := filepath.Join(migRoot, "sqlite")
	require.NoError(t, os.MkdirAll(migDir, 0o700))
	srcDir := filepath.Join("..", "..", "migrations", "sqlite")
	for _, name := range []string{"001_init.sql", "002_trait_snapshots_raw.sql", "003_comparisons.sql"} {
		copyMigration(t, srcDir, migDir, name)
	}

	store, err := sqlstore.Open("sqlite", filepath.Join(tmp, "migration-005.db"))
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()

	require.NoError(t, migrate.Apply(ctx, db, migRoot, "sqlite"))

	// До 005 колонок нет — приложение ещё не умеет мягко удалять.
	before := columnNames(t, db, "blobs")
	require.False(t, before["deleted_at"], "deleted_at не должен существовать до 005")
	require.False(t, before["delete_reason"])
	require.False(t, columnNames(t, db, "observations")["invalidated_at"])

	// Наполняем таблицы «старыми» строками, как на живом узле до миграции.
	now := time.Now().UTC().Truncate(time.Millisecond)
	createdAt := now.Add(-time.Hour).UnixMilli()
	shaActive := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaDeleted := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	_, err = db.Exec(
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES (?, ?, ?, ?, ?)`,
		"blob-active", shaActive, "image/jpeg", 1234, createdAt)
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES (?, ?, ?, ?, ?)`,
		"blob-deleted", shaDeleted, "image/jpeg", 4321, createdAt)
	require.NoError(t, err)

	_, err = db.Exec(
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"obs-active", "horse-1", "01900000-0000-7000-8000-000000000001", createdAt,
		"50.01,30.02", "watcher-1", `["`+shaActive+`"]`, "", "активное")
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"obs-deleted", "horse-1", "01900000-0000-7000-8000-000000000001", createdAt,
		"50.03,30.04", "watcher-2", `["`+shaDeleted+`"]`, "", "с удалённым фото")
	require.NoError(t, err)
	// Подтверждённое фото связывает наблюдение с блобом вторым путём
	// (на него опирается observationIDsByBlob).
	_, err = db.Exec(
		`INSERT INTO observation_photos (id, observation_id, sha256, name, annotation, model_version, embedding)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"photo-1", "obs-deleted", shaDeleted, "фото", "{}", "ref-v1", "[]")
	require.NoError(t, err)

	// Ещё два наблюдения, чтобы развести пути связи: obs-blobonly ссылается на
	// блоб ТОЛЬКО через blob_ids, obs-photoonly — ТОЛЬКО через observation_photos.
	// Без них dedupe в observationIDsByBlob схлопывает оба пути в одну строку и
	// инвариант не отличает их друг от друга.
	_, err = db.Exec(
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"obs-blobonly", "horse-1", "01900000-0000-7000-8000-000000000001", createdAt,
		"50.05,30.06", "watcher-3", `["`+shaDeleted+`"]`, "", "только blob_ids")
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"obs-photoonly", "horse-1", "01900000-0000-7000-8000-000000000001", createdAt,
		"50.07,30.08", "watcher-4", `[]`, "", "только фото")
	require.NoError(t, err)
	_, err = db.Exec(
		`INSERT INTO observation_photos (id, observation_id, sha256, name, annotation, model_version, embedding)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"photo-2", "obs-photoonly", shaDeleted, "фото", "{}", "ref-v1", "[]")
	require.NoError(t, err)

	// Накатываем 005 поверх заполненных таблиц.
	copyMigration(t, srcDir, migDir, "005_blob_deleted.sql")
	require.NoError(t, migrate.Apply(ctx, db, migRoot, "sqlite"))

	// Новые колонки появились.
	afterBlobs := columnNames(t, db, "blobs")
	require.True(t, afterBlobs["deleted_at"])
	require.True(t, afterBlobs["delete_reason"])
	afterObs := columnNames(t, db, "observations")
	require.True(t, afterObs["invalidated_at"])
	require.True(t, afterObs["invalidation_reason"])

	// Индексы по новым колонкам созданы.
	for _, idx := range []string{"idx_blobs_deleted_at", "idx_observations_invalidated_at"} {
		var n int
		require.NoError(t, db.QueryRow(
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, idx).Scan(&n))
		require.Equal(t, 1, n, "индекс %s должен существовать", idx)
	}

	// Старые строки валидны: DEFAULT 0 выставился на существующие записи.
	var total, active int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&total))
	require.Equal(t, 2, total, "миграция не должна удалять строки")
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM blobs WHERE deleted_at = 0 AND delete_reason = ''`).Scan(&active))
	require.Equal(t, 2, active, "все старые блобы должны остаться активными")

	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM observations`).Scan(&total))
	require.Equal(t, 4, total)
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM observations WHERE invalidated_at = 0 AND invalidation_reason = ''`).Scan(&active))
	require.Equal(t, 4, active, "все старые наблюдения должны остаться валидными")

	// NOT NULL DEFAULT 0: явный NULL в новую колонку отвергается. Проверяем
	// симметрично обе колонки каждой таблицы.
	_, err = db.Exec(
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at, deleted_at) VALUES (?, ?, '', 1, 0, NULL)`,
		"blob-bad", strings.Repeat("c", 64))
	require.ErrorContains(t, err, "NOT NULL", "deleted_at NOT NULL должен отвергать NULL")

	_, err = db.Exec(
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at, delete_reason) VALUES (?, ?, '', 1, 0, NULL)`,
		"blob-bad", strings.Repeat("d", 64))
	require.ErrorContains(t, err, "NOT NULL", "delete_reason NOT NULL должен отвергать NULL")

	_, err = db.Exec(
		`INSERT INTO observations (id, node_id, observed_at, observer_id, invalidated_at) VALUES (?, ?, ?, ?, NULL)`,
		"obs-bad", "01900000-0000-7000-8000-000000000001", createdAt, "watcher-1")
	require.ErrorContains(t, err, "NOT NULL", "invalidated_at NOT NULL должен отвергать NULL")

	_, err = db.Exec(
		`INSERT INTO observations (id, node_id, observed_at, observer_id, invalidation_reason) VALUES (?, ?, ?, ?, NULL)`,
		"obs-bad", "01900000-0000-7000-8000-000000000001", createdAt, "watcher-1")
	require.ErrorContains(t, err, "NOT NULL", "invalidation_reason NOT NULL должен отвергать NULL")

	// Новые флаги работают по семантике sqlstore: мягкое удаление через реальные
	// методы хранилища скрывает блоб, но строка остаётся ради ссылок наблюдений.
	markTime := now.Add(time.Minute)
	n, err := store.MarkBlobDeleted(ctx, shaDeleted, markTime, "review cleanup")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	// Строка осталась (append-only), но помечена удалённой.
	var rawDeletedAt int64
	var rawReason string
	require.NoError(t, db.QueryRow(
		`SELECT deleted_at, delete_reason FROM blobs WHERE sha256 = ?`, shaDeleted).Scan(&rawDeletedAt, &rawReason))
	require.EqualValues(t, markTime.UnixMilli(), rawDeletedAt)
	require.Equal(t, "review cleanup", rawReason)

	// GetBlobMeta отдаёт ErrNotFound, ListBlobs исключает удалённый блоб.
	_, err = store.GetBlobMeta(ctx, shaDeleted)
	require.ErrorIs(t, err, storage.ErrNotFound)
	metas, err := store.ListBlobs(ctx)
	require.NoError(t, err)
	require.Len(t, metas, 1)
	require.Equal(t, shaActive, metas[0].SHA256)

	// Повторное удаление — no-op (0 строк), значение не перезаписывается.
	n, err = store.MarkBlobDeleted(ctx, shaDeleted, now.Add(2*time.Minute), "second")
	require.NoError(t, err)
	require.EqualValues(t, 0, n)
	require.NoError(t, db.QueryRow(
		`SELECT deleted_at FROM blobs WHERE sha256 = ?`, shaDeleted).Scan(&rawDeletedAt))
	require.EqualValues(t, markTime.UnixMilli(), rawDeletedAt)

	// Инвалидация наблюдений по блобу: obs-deleted связан обоими путями,
	// obs-blobonly — только через blob_ids, obs-photoonly — только через
	// observation_photos. Все три должны быть помечены (dedupe не теряет пути).
	inv, err := store.InvalidateObservationsByBlob(ctx, shaDeleted, markTime, "reference removed")
	require.NoError(t, err)
	require.EqualValues(t, 3, inv, "инвалидируются наблюдения, связанные любым из путей")

	// Наблюдение с активным фото не тронуто, у удалённого выставлен флаг/причина.
	var obsActiveFlag, obsDelFlag int64
	var obsDelReason string
	require.NoError(t, db.QueryRow(
		`SELECT invalidated_at FROM observations WHERE id = ?`, "obs-active").Scan(&obsActiveFlag))
	require.EqualValues(t, 0, obsActiveFlag)
	require.NoError(t, db.QueryRow(
		`SELECT invalidated_at, invalidation_reason FROM observations WHERE id = ?`, "obs-deleted").
		Scan(&obsDelFlag, &obsDelReason))
	require.EqualValues(t, markTime.UnixMilli(), obsDelFlag)
	require.Equal(t, "reference removed", obsDelReason)

	// Семантика сравнения (comparisons.go: WHERE o.invalidated_at = 0) исключает
	// неактуальное наблюдение, оставляя только активное.
	var comparable int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM observations WHERE invalidated_at = 0`).Scan(&comparable))
	require.Equal(t, 1, comparable)

	// Повторная инвалидация — no-op для уже помеченных строк.
	inv, err = store.InvalidateObservationsByBlob(ctx, shaDeleted, now.Add(3*time.Minute), "again")
	require.NoError(t, err)
	require.EqualValues(t, 0, inv)

	// Повторный Apply — no-op за счёт реестра версий: migrate.Apply пропускает
	// версии, уже записанные в schema_migrations (migrate.go), а сам SQL 005
	// идемпотентным не является (повторный ALTER ... ADD COLUMN упал бы).
	require.NoError(t, migrate.Apply(ctx, db, migRoot, "sqlite"))
	require.NoError(t, migrate.Apply(ctx, db, migRoot, "sqlite"))
	require.NoError(t, db.QueryRow(
		`SELECT deleted_at FROM blobs WHERE sha256 = ?`, shaDeleted).Scan(&rawDeletedAt))
	require.EqualValues(t, markTime.UnixMilli(), rawDeletedAt, "повторный Apply не должен трогать данные")
	var migCount int
	require.NoError(t, db.QueryRow(
		`SELECT COUNT(*) FROM schema_migrations WHERE version = 5`).Scan(&migCount))
	require.Equal(t, 1, migCount, "версия 5 записана ровно один раз")
}

// TestMigration005SQLitePostgresParity сверяет файлы миграции 005 для sqlite и
// postgres без живого Postgres: нормализованный набор операторов (без комментариев
// и пустых строк) должен совпадать. Тексты файлов идентичны, поэтому риск
// расхождения снят на уровне текста; совместимость диалекта здесь не исполняется —
// реальный прогон 005 на postgres вынесен в отдельный гейт по TEST_PG_DSN
// (TestMigration005PostgresOnFilledTables).
func TestMigration005SQLitePostgresParity(t *testing.T) {
	sqliteStmts := readStatements(t, filepath.Join("..", "..", "migrations", "sqlite", "005_blob_deleted.sql"))
	postgresStmts := readStatements(t, filepath.Join("..", "..", "migrations", "postgres", "005_blob_deleted.sql"))

	// Обе версии добавляют одинаковые колонки с NOT NULL DEFAULT и индексы:
	// сверяем содержимое строго (одним require.Equal), затем паритет файлов.
	require.Equal(t, []string{
		"ALTER TABLE blobs ADD COLUMN deleted_at BIGINT NOT NULL DEFAULT 0;",
		"ALTER TABLE blobs ADD COLUMN delete_reason TEXT NOT NULL DEFAULT '';",
		"CREATE INDEX idx_blobs_deleted_at ON blobs (deleted_at);",
		"ALTER TABLE observations ADD COLUMN invalidated_at BIGINT NOT NULL DEFAULT 0;",
		"ALTER TABLE observations ADD COLUMN invalidation_reason TEXT NOT NULL DEFAULT '';",
		"CREATE INDEX idx_observations_invalidated_at ON observations (invalidated_at);",
	}, sqliteStmts, "005 (sqlite) должна содержать ожидаемый набор операторов")
	require.Equal(t, sqliteStmts, postgresStmts,
		"005: набор операторов sqlite и postgres должен совпадать")
}

// TestMigration005PostgresOnFilledTables — реальный прогон 005 на живом
// PostgreSQL поверх заполненных таблиц. Гейт по TEST_PG_DSN: без него тест
// пропускается (как и postgres-контракты в internal/storage/postgres). Изоляция —
// временная схема через search_path, чтобы не трогать данные общей тестовой БД.
func TestMigration005PostgresOnFilledTables(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN не задан — реальный прогон 005 на postgres пропущен")
	}
	ctx := context.Background()

	admin, err := postgres.New(dsn)
	require.NoError(t, err)
	t.Cleanup(func() { admin.Close() })
	schema := "mig005_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.DB().ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := admin.DB().ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		require.NoError(t, err)
	})

	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()

	// Каталог миграций: 001-003, затем 005 — как на живом узле.
	tmp := t.TempDir()
	migRoot := filepath.Join(tmp, "migrations")
	migDir := filepath.Join(migRoot, "postgres")
	require.NoError(t, os.MkdirAll(migDir, 0o700))
	srcDir := filepath.Join("..", "..", "migrations", "postgres")
	for _, name := range []string{"001_init.sql", "002_trait_snapshots_raw.sql", "003_comparisons.sql"} {
		copyMigration(t, srcDir, migDir, name)
	}

	store, err := postgres.New(u.String())
	require.NoError(t, err)
	defer store.Close()
	db := store.DB()

	require.NoError(t, migrate.Apply(ctx, db, migRoot, "postgres"))

	// Заполняем таблицы «старыми» строками до 005.
	now := time.Now().UTC().Truncate(time.Millisecond)
	createdAt := now.Add(-time.Hour).UnixMilli()
	shaActive := strings.Repeat("a", 64)
	shaDeleted := strings.Repeat("b", 64)
	_, err = db.ExecContext(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES ($1, $2, 'image/jpeg', 1234, $3)`,
		"blob-active", shaActive, createdAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES ($1, $2, 'image/jpeg', 4321, $3)`,
		"blob-deleted", shaDeleted, createdAt)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx,
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES ($1, 'horse-1', '01900000-0000-7000-8000-000000000001', $2, '50.01,30.02', 'watcher-1', $3, '', 'активное')`,
		"obs-active", createdAt, `["`+shaActive+`"]`)
	require.NoError(t, err)

	// Накатываем 005 поверх заполненных таблиц — именно этот прогон и проверяем.
	copyMigration(t, srcDir, migDir, "005_blob_deleted.sql")
	require.NoError(t, migrate.Apply(ctx, db, migRoot, "postgres"))

	// Новые колонки появились и объявлены NOT NULL.
	nullable := map[string]string{}
	rows, err := db.QueryContext(ctx,
		`SELECT column_name, is_nullable FROM information_schema.columns WHERE table_schema = $1`, schema)
	require.NoError(t, err)
	for rows.Next() {
		var name, isNullable string
		require.NoError(t, rows.Scan(&name, &isNullable))
		nullable[name] = isNullable
	}
	require.NoError(t, rows.Err())
	rows.Close()
	for _, c := range []string{"deleted_at", "delete_reason", "invalidated_at", "invalidation_reason"} {
		require.Equal(t, "NO", nullable[c], "колонка %s должна быть NOT NULL", c)
	}

	// Старые строки остались активными: DEFAULT 0/'' выставился на существующие записи.
	var active int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM blobs WHERE deleted_at = 0 AND delete_reason = ''`).Scan(&active))
	require.Equal(t, 2, active, "старые блобы должны остаться активными")
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM observations WHERE invalidated_at = 0 AND invalidation_reason = ''`).Scan(&active))
	require.Equal(t, 1, active, "старое наблюдение должно остаться валидным")

	// NOT NULL отвергает явный NULL.
	_, err = db.ExecContext(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at, deleted_at) VALUES ($1, $2, '', 1, 0, NULL)`,
		"blob-bad", strings.Repeat("c", 64))
	require.ErrorContains(t, err, "null value in column", "deleted_at NOT NULL должен отвергать NULL")

	// Мягкое удаление работает на postgres так же, как на sqlite.
	n, err := store.MarkBlobDeleted(ctx, shaDeleted, now.Add(time.Minute), "review cleanup")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	_, err = store.GetBlobMeta(ctx, shaDeleted)
	require.ErrorIs(t, err, storage.ErrNotFound)
}

// readStatements читает SQL-файл и возвращает значимые строки: без пустых строк
// и однострочных комментариев.
func readStatements(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		out = append(out, line)
	}
	return out
}
