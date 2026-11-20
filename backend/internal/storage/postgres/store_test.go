package postgres_test

import (
	"context"
	"os"
	"strings"
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

func openStore(t *testing.T, dsn string) *sqlstore.Store {
	t.Helper()
	s, err := postgres.New(dsn)
	if err != nil {
		t.Fatalf("postgres.New: %v", err)
	}
	if err := migrate.Apply(context.Background(), s.DB(), migrationsDir, "postgres"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestEventStoreContract(t *testing.T) {
	dsn := testDSN(t)
	contract.RunEventStoreSuite(t, func(t *testing.T) (storage.EventStore, func() storage.EventStore) {
		// изолированная схема на каждый субтест; reopen — та же база
		dsn := dsn + "?options=-csearch_path%3Dcontract_" + strings.ReplaceAll(t.Name(), "/", "_")
		return openStore(t, dsn), func() storage.EventStore { return openStore(t, dsn) }
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
