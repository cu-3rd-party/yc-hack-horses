package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

const testNodeID = "01900000-0000-7000-8000-0000000000aa"

// newProjectionFixture поднимает sqlite + fsblob во временном каталоге и
// возвращает хранилище с проекциями (тем же путём, что sync-движок).
func newProjectionFixture(t *testing.T) (*sqlstore.Store, *fsblob.Store, storage.EventStore) {
	t.Helper()
	dir := t.TempDir()
	st, err := sqlstore.Open("sqlite", filepath.Join(dir, "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, migrate.Apply(context.Background(), st.DB(), "../../migrations", "sqlite"))
	blobs, err := fsblob.New(filepath.Join(dir, "blobs"))
	require.NoError(t, err)
	return st, blobs, newProjectedStore(st, blobs)
}

func putBlob(t *testing.T, blobs *fsblob.Store, content []byte) string {
	t.Helper()
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	require.NoError(t, blobs.Put(context.Background(), sha, "image/jpeg", content))
	return sha
}

func registeredEvent(t *testing.T, seq uint64, sha string) domain.Event {
	t.Helper()
	payload, err := json.Marshal(domain.Blob{ID: uuid.NewString(), SHA256: sha, MimeType: "image/jpeg", Size: 10})
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: testNodeID, Sequence: seq, EventType: domain.EventBlobRegistered,
		SchemaVersion: 1, AggregateID: sha, OccurredAt: time.Now().UTC(), Payload: payload,
	}
}

func deletedEvent(t *testing.T, nodeID string, seq uint64, sha string, at time.Time, reason string) domain.Event {
	t.Helper()
	payload, err := json.Marshal(domain.BlobDeleted{SHA256: sha, DeletedAt: at, Reason: reason})
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: nodeID, Sequence: seq, EventType: domain.EventBlobDeleted,
		SchemaVersion: 1, AggregateID: sha, OccurredAt: at, Payload: payload,
	}
}

func observationEvent(t *testing.T, seq uint64, obsID string, refs []string) domain.Event {
	t.Helper()
	record := comparison.ObservationEvent{
		Observation: domain.Observation{
			ID:         obsID,
			NodeID:     testNodeID,
			ObservedAt: time.Now().UTC(),
			Location:   "50.01,30.02",
			ObserverID: "watcher",
			BlobIDs:    refs,
		},
	}
	payload, err := json.Marshal(record)
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: testNodeID, Sequence: seq, EventType: domain.EventObservationAdded,
		SchemaVersion: 1, AggregateID: obsID, OccurredAt: time.Now().UTC(), Payload: payload,
	}
}

// Нет ссылок: строка скрывается из API, файл удаляется.
func TestProjectBlobDeleted_UnreferencedFileRemoved(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("deletable photo"))

	require.NoError(t, store.Append(ctx, []domain.Event{
		registeredEvent(t, 1, sha),
		deletedEvent(t, testNodeID, 2, sha, time.Now().UTC(), "cleanup"),
	}))

	_, err := st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound)

	list, err := st.ListBlobs(ctx)
	require.NoError(t, err)
	require.Empty(t, list)

	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.False(t, present, "файл без ссылок должен быть удалён")
}

// Повторная доставка blob.deleted — no-op, в том числе с другого узла.
func TestProjectBlobDeleted_Idempotent(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("photo deleted twice"))
	at := time.Now().UTC()

	require.NoError(t, store.Append(ctx, []domain.Event{registeredEvent(t, 1, sha)}))

	ev := deletedEvent(t, testNodeID, 2, sha, at, "duplicate original")
	require.NoError(t, store.Append(ctx, []domain.Event{ev}))
	// та же пара (node_id, sequence) — Append идемпотентен, проекция тоже
	require.NoError(t, store.Append(ctx, []domain.Event{ev}))
	// независимая доставка того же удаления с пира
	require.NoError(t, store.Append(ctx, []domain.Event{deletedEvent(t, "peer-node", 1, sha, at, "duplicate original")}))

	_, err := st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound)

	events, err := st.GetEvents(ctx, storage.Cursor{}, 100)
	require.NoError(t, err)
	require.Len(t, events, 3) // registered + deleted + deleted с пира
}

// Ре-аплоад после удаления снова регистрирует блоб.
func TestProjectBlobDeleted_ReuploadRevives(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("photo reuploaded"))

	require.NoError(t, store.Append(ctx, []domain.Event{
		registeredEvent(t, 1, sha),
		deletedEvent(t, testNodeID, 2, sha, time.Now().UTC(), "cleanup"),
	}))
	_, err := st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound)

	require.NoError(t, store.Append(ctx, []domain.Event{registeredEvent(t, 3, sha)}))

	meta, err := st.GetBlobMeta(ctx, sha)
	require.NoError(t, err)
	require.Equal(t, sha, meta.SHA256)
	list, err := st.ListBlobs(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
}

// Наблюдение со ссылкой на блоб инвалидируется, но файл остаётся на диске.
func TestProjectBlobDeleted_KeepsReferencedFile(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("referenced photo"))
	obsID := uuid.NewString()

	require.NoError(t, store.Append(ctx, []domain.Event{
		registeredEvent(t, 1, sha),
		observationEvent(t, 2, obsID, []string{sha}),
	}))
	require.NoError(t, store.Append(ctx, []domain.Event{
		deletedEvent(t, testNodeID, 3, sha, time.Now().UTC(), "old photo"),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated)
	require.NotNil(t, obs.InvalidatedAt)
	require.Equal(t, "blob deleted: "+sha+" (old photo)", obs.InvalidationReason)

	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл под ссылкой наблюдения удалять нельзя")
}

// Наблюдение, приехавшее после удаления блоба, создаётся сразу неактуальным.
func TestProjectObservationAdded_InvalidatedForDeletedBlob(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("late observation photo"))
	obsID := uuid.NewString()

	require.NoError(t, store.Append(ctx, []domain.Event{
		registeredEvent(t, 1, sha),
		deletedEvent(t, testNodeID, 2, sha, time.Now().UTC(), "deleted before observation"),
		observationEvent(t, 3, obsID, []string{sha}),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated)

	// Повторная доставка observation.added не сбрасывает метку.
	require.NoError(t, store.Append(ctx, []domain.Event{
		observationEvent(t, 4, obsID, []string{sha}),
	}))
	obs, err = st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated)
}

// Снимки признаков инвалидированных наблюдений не попадают в поиск похожих.
func TestProjectBlobDeleted_ExcludesSimilarTraits(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("trait photo"))
	horseID, obsID := uuid.NewString(), uuid.NewString()

	require.NoError(t, store.Append(ctx, []domain.Event{registeredEvent(t, 1, sha)}))
	require.NoError(t, st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		if err := tx.CreateHorse(ctx, domain.Horse{ID: horseID, CreatedAt: time.Now().UTC(), IdentityStatus: "active"}); err != nil {
			return err
		}
		if err := tx.CreateObservation(ctx, domain.Observation{
			ID: obsID, HorseID: horseID, NodeID: testNodeID, ObservedAt: time.Now().UTC(),
			Location: "50.01,30.02", ObserverID: "watcher", BlobIDs: []string{sha},
		}); err != nil {
			return err
		}
		return tx.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
			ID: uuid.NewString(), ObservationID: obsID, ModelVersion: "v1",
			Traits: json.RawMessage(`{"coat":"bay"}`), Confidence: 0.9, Fingerprint: "fp-1", Confirmed: true,
		})
	}))

	similar, err := st.ListSimilarTraitSnapshots(ctx, "fp-1", "", 10)
	require.NoError(t, err)
	require.Len(t, similar, 1)

	require.NoError(t, store.Append(ctx, []domain.Event{
		deletedEvent(t, testNodeID, 2, sha, time.Now().UTC(), "old traits photo"),
	}))

	similar, err = st.ListSimilarTraitSnapshots(ctx, "fp-1", "", 10)
	require.NoError(t, err)
	require.Empty(t, similar, "снимки инвалидированного наблюдения не должны находиться поиском")
}

// Вектора инвалидированных наблюдений не участвуют в сравнении и не попадают
// в подтверждённые reference photos.
func TestProjectBlobDeleted_ExcludesVectorsFromReferences(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("reference photo"))
	otherSHA := putBlob(t, blobs, []byte("kept reference photo"))
	horseID := uuid.NewString()
	require.NoError(t, store.Append(ctx, []domain.Event{registeredEvent(t, 1, sha), registeredEvent(t, 2, otherSHA)}))

	require.NoError(t, st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		if err := tx.CreateHorse(ctx, domain.Horse{ID: horseID, CreatedAt: time.Now().UTC(), IdentityStatus: "active"}); err != nil {
			return err
		}
		// по одному фото на наблюдение: инвалидация должна убрать ровно одно
		for _, p := range []struct {
			sha    string
			vector []float64
		}{{sha, []float64{1, 0}}, {otherSHA, []float64{0, 1}}} {
			if err := tx.CreateReviewedObservation(ctx, comparison.ObservationEvent{
				Observation: domain.Observation{
					ID: uuid.NewString(), HorseID: horseID, NodeID: testNodeID, ObservedAt: time.Now().UTC(),
					Location: "50.01,30.02", ObserverID: "watcher", BlobIDs: []string{p.sha},
				},
				Photos: []comparison.ReferencePhoto{
					{Photo: comparison.Photo{ID: uuid.NewString(), SHA: p.sha}, Descriptor: comparison.Descriptor{Version: "v1", Vector: p.vector}},
				},
			}); err != nil {
				return err
			}
		}
		return nil
	}))

	refs, err := st.References(ctx, "v1")
	require.NoError(t, err)
	require.Len(t, refs, 2)

	require.NoError(t, store.Append(ctx, []domain.Event{
		deletedEvent(t, testNodeID, 3, sha, time.Now().UTC(), "old reference"),
	}))

	refs, err = st.References(ctx, "v1")
	require.NoError(t, err)
	require.Len(t, refs, 1, "вектор удалённого фото должен выпасть из сравнения")
	require.Equal(t, []float64{0, 1}, refs[0].Vector)
}
