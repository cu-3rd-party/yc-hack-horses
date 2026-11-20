package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/contract"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/postgres"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

const migrationsDir = "../../../migrations"

// testDSN — DSN тестовой базы, задаётся через TEST_PG_DSN.
func testDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN не задан — PostgreSQL contract-тесты пропущены (подними postgres и экспортируй TEST_PG_DSN=postgres://...)")
	}
	return dsn
}

// openFresh открывает postgres, применяет миграции и очищает таблицы.
// Субтесты изолируются очисткой (одна БД, zero-downtime схема идентична sqlite).
func openFresh(t *testing.T, dsn string, truncate bool) *sqlstore.Store {
	t.Helper()
	s, err := postgres.New(dsn)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	if err := migrate.Apply(context.Background(), s.DB(), migrationsDir, "postgres"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if truncate {
		if _, err := s.DB().ExecContext(context.Background(),
			`TRUNCATE events, horses, observations, trait_snapshots, blobs`); err != nil {
			t.Fatalf("truncate: %v", err)
		}
	}
	return s
}

func TestEventStoreContract(t *testing.T) {
	dsn := testDSN(t)
	contract.RunEventStoreSuite(t, func(t *testing.T) (storage.EventStore, func() storage.EventStore) {
		// первый open субтеста очищает таблицы; reopen — та же база без очистки
		first := true
		s := openFresh(t, dsn, first)
		first = false
		return s, func() storage.EventStore { return openFresh(t, dsn, false) }
	})
}

func TestBlobStoreContract(t *testing.T) {
	testDSN(t)
	dir := t.TempDir()
	contract.RunBlobStoreSuite(t, func(t *testing.T) (storage.BlobStore, func() storage.BlobStore) {
		s, err := fsblob.New(dir)
		if err != nil {
			t.Fatalf("fsblob: %v", err)
		}
		return s, func() storage.BlobStore {
			s, err := fsblob.New(dir)
			if err != nil {
				t.Fatalf("fsblob reopen: %v", err)
			}
			return s
		}
	})
}
