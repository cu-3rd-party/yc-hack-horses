package sqlite_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/contract"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlite"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// migrationsDir — путь к migrations/ относительно этого пакета.
const migrationsDir = "../../../migrations"

func openStore(t *testing.T, dsn string) *sqlstore.Store {
	t.Helper()
	s, err := sqlite.New(dsn)
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := migrate.Apply(context.Background(), s.DB(), migrationsDir, "sqlite"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return s
}

func TestEventStoreContract(t *testing.T) {
	contract.RunEventStoreSuite(t, func(t *testing.T) (storage.EventStore, func() storage.EventStore) {
		// каждая фабрика — новое изолированное хранилище; reopen возвращает то же
		dsn := filepath.Join(t.TempDir(), "test.db")
		return openStore(t, dsn), func() storage.EventStore { return openStore(t, dsn) }
	})
}

func TestBlobStoreContract(t *testing.T) {
	// один каталог на весь набор: RestartRecovery переоткрывает его же
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
