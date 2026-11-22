package sqlite_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
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

// TestListReferencedBlobHashes — реестр blob-ссылок наблюдений: уникальные
// хэши из всех наблюдений, включая те, чьих файлов ещё нет на диске.
func TestListReferencedBlobHashes(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"))
	obs := domain.Observation{
		ID:         "obs-1",
		NodeID:     "node-a",
		ObserverID: "observer-1",
		Location:   "loc",
		BlobIDs:    []string{"hash-1", "hash-2", "hash-1"},
	}
	if err := s.CreateObservation(ctx, obs); err != nil {
		t.Fatalf("create observation: %v", err)
	}
	obs2 := domain.Observation{
		ID:         "obs-2",
		NodeID:     "node-a",
		ObserverID: "observer-1",
		Location:   "loc",
		BlobIDs:    []string{"hash-2", "hash-3"},
	}
	if err := s.CreateObservation(ctx, obs2); err != nil {
		t.Fatalf("create observation 2: %v", err)
	}
	got, err := s.ListReferencedBlobHashes(ctx)
	if err != nil {
		t.Fatalf("list referenced blob hashes: %v", err)
	}
	want := []string{"hash-1", "hash-2", "hash-3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, h := range want {
		if got[i] != h {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], h)
		}
	}
}

// TestListActiveReferencedBlobHashes — потребности blob-синка строятся по
// активным наблюдениям: ссылка blobs.id (UUIDv7) резолвится в sha256, legacy
// ссылка sha256 берётся как есть, дедуп по sha256, инвалидированные наблюдения
// выпадают, а хэш, общий с активным наблюдением, остаётся. Ссылка в ВЕРХНЕМ
// регистре резолвится так же: сравнение идёт по LOWER обеих сторон.
func TestListActiveReferencedBlobHashes(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"))

	sha1 := strings.Repeat("a", 64)
	sha2 := strings.Repeat("b", 64)
	sha3 := strings.Repeat("c", 64)
	// sha4/sha5 регистрируются в нижнем регистре, а наблюдения ссылаются на них
	// в верхнем: резолв обязан быть регистронезависимым (LOWER обеих сторон).
	sha4 := strings.Repeat("d", 64)
	sha5 := strings.Repeat("e", 64)
	// sha6 зарегистрирован дважды — в нижнем и верхнем регистре (UNIQUE(sha256)
	// регистрозависим): обе строки резолвятся в один sha256, а DISTINCT
	// LOWER(b.sha256) схлопывает их в одну потребность.
	sha6 := strings.Repeat("f", 64)
	const (
		id1 = "01990000-0000-7000-8000-0000000000a1"
		id2 = "01990000-0000-7000-8000-0000000000a2"
		id3 = "01990000-0000-7000-8000-0000000000a3"
		id4 = "01990000-0000-7000-8000-0000000000a4"
		id5 = "01990000-0000-7000-8000-0000000000a5"
		id6 = "01990000-0000-7000-8000-0000000000a6"
		id7 = "01990000-0000-7000-8000-0000000000a7"
	)
	for _, b := range []domain.Blob{
		{ID: id1, SHA256: sha1, MimeType: "image/jpeg", Size: 1},
		{ID: id2, SHA256: sha2, MimeType: "image/jpeg", Size: 1},
		{ID: id3, SHA256: sha3, MimeType: "image/jpeg", Size: 1},
		{ID: id4, SHA256: sha4, MimeType: "image/jpeg", Size: 1},
		{ID: id5, SHA256: sha5, MimeType: "image/jpeg", Size: 1},
		{ID: id6, SHA256: sha6, MimeType: "image/jpeg", Size: 1},
		{ID: id7, SHA256: strings.ToUpper(sha6), MimeType: "image/jpeg", Size: 1},
	} {
		if _, err := s.UpsertBlob(ctx, b); err != nil {
			t.Fatalf("upsert blob %s: %v", b.SHA256, err)
		}
	}

	create := func(id string, refs []string) {
		t.Helper()
		if err := s.CreateObservation(ctx, domain.Observation{
			ID: id, NodeID: "node-a", ObserverID: "observer-1", Location: "loc", BlobIDs: refs,
		}); err != nil {
			t.Fatalf("create observation %s: %v", id, err)
		}
	}
	// obs-1 ссылается по blobs.id (UUID) -> должен резолвиться в sha1.
	create("obs-1", []string{id1})
	// obs-2: legacy sha1 (дубль sha1) и UUID id2 — дедуп по sha256.
	create("obs-2", []string{sha1, id2})
	// obs-3 держит sha2 активным наблюдением.
	create("obs-3", []string{sha2})
	// obs-4 инвалидировано: sha3 выпадает, sha2 остаётся (держится obs-3).
	create("obs-4", []string{id3, sha2})
	if _, err := s.InvalidateObservation(ctx, "obs-4", time.Now().UTC(), "blob deleted"); err != nil {
		t.Fatalf("invalidate observation: %v", err)
	}
	// obs-5: ссылка на sha4 в верхнем регистре -> резолвится в sha4.
	create("obs-5", []string{strings.ToUpper(sha4)})
	// obs-6: ссылка на blobs.id (UUID) в верхнем регистре -> резолвится в sha5.
	create("obs-6", []string{strings.ToUpper(id5)})
	// obs-7/obs-8 ссылаются на две строки blobs одного sha256 в разном регистре:
	// обе резолвятся в sha6, но потребность остаётся одна.
	create("obs-7", []string{id6})
	create("obs-8", []string{id7})

	got, err := s.ListActiveReferencedBlobHashes(ctx)
	if err != nil {
		t.Fatalf("list active referenced blob hashes: %v", err)
	}
	want := []string{sha1, sha2, sha4, sha5, sha6}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i, h := range want {
		if got[i] != h {
			t.Fatalf("got[%d] = %q, want %q", i, got[i], h)
		}
	}
}

// TestListActiveReferencedBlobHashesInvalidJSON — битый JSON в blob_ids не
// роняет выборку потребностей (sqlite json_type на нём падает): json_valid-обёртка
// отсекает такую строку, и она даёт 0 ссылок и не мешает остальным наблюдениям.
func TestListActiveReferencedBlobHashesInvalidJSON(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, filepath.Join(t.TempDir(), "test.db"))

	sha := strings.Repeat("a", 64)
	const id1 = "01990000-0000-7000-8000-0000000000b1"
	if _, err := s.UpsertBlob(ctx, domain.Blob{ID: id1, SHA256: sha, MimeType: "image/jpeg", Size: 1}); err != nil {
		t.Fatalf("upsert blob: %v", err)
	}
	if err := s.CreateObservation(ctx, domain.Observation{
		ID: "obs-ok", NodeID: "node-a", ObserverID: "observer-1", Location: "loc", BlobIDs: []string{id1},
	}); err != nil {
		t.Fatalf("create observation: %v", err)
	}
	// Битый blob_ids вставляем сырым INSERT: CreateObservation всегда пишет
	// сериализованный JSON-массив.
	if _, err := s.DB().ExecContext(ctx,
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids)
		 VALUES ('obs-broken', '', 'node-a', 0, 'loc', 'observer-1', 'not-json')`); err != nil {
		t.Fatalf("insert broken observation: %v", err)
	}

	got, err := s.ListActiveReferencedBlobHashes(ctx)
	if err != nil {
		t.Fatalf("list active referenced blob hashes: %v", err)
	}
	if len(got) != 1 || got[0] != sha {
		t.Fatalf("got %v, want [%q]", got, sha)
	}
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
