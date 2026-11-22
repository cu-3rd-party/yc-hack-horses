package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// Тесты проекции blob.deleted: маппинг blob_ids -> sha256, повторная доставка
// после ре-аплоада и out-of-order удаление до регистрации.

// blobRegisteredEvent — blob.registered с явным id блоба (blobs.id = UUIDv7) и временем.
func blobRegisteredEvent(t *testing.T, seq uint64, blobID, sha string, at time.Time) domain.Event {
	t.Helper()
	payload, err := json.Marshal(domain.Blob{ID: blobID, SHA256: sha, MimeType: "image/jpeg", Size: 10})
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: testNodeID, Sequence: seq, EventType: domain.EventBlobRegistered,
		SchemaVersion: 1, AggregateID: sha, OccurredAt: at, Payload: payload,
	}
}

// blobDeletedEvent — blob.deleted с явным отправителем и временем удаления.
func blobDeletedEvent(t *testing.T, nodeID string, seq uint64, sha string, at time.Time, reason string) domain.Event {
	t.Helper()
	payload, err := json.Marshal(domain.BlobDeleted{SHA256: sha, DeletedAt: at, Reason: reason})
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: nodeID, Sequence: seq, EventType: domain.EventBlobDeleted,
		SchemaVersion: 1, AggregateID: sha, OccurredAt: at, Payload: payload,
	}
}

// blobObservationEvent — observation.added со ссылками blob_ids.
func blobObservationEvent(t *testing.T, seq uint64, obsID string, refs []string, at time.Time) domain.Event {
	t.Helper()
	record := comparison.ObservationEvent{
		Observation: domain.Observation{
			ID:         obsID,
			NodeID:     testNodeID,
			ObservedAt: at,
			Location:   "50.01,30.02",
			ObserverID: "watcher",
			BlobIDs:    refs,
		},
	}
	payload, err := json.Marshal(record)
	require.NoError(t, err)
	return domain.Event{
		EventID: uuid.NewString(), NodeID: testNodeID, Sequence: seq, EventType: domain.EventObservationAdded,
		SchemaVersion: 1, AggregateID: obsID, OccurredAt: at, Payload: payload,
	}
}

// blob_ids хранит blobs.id (UUIDv7, как объявлено в OpenAPI): удаление блоба по
// sha256 должно найти наблюдение через таблицу blobs, а файл остаётся на диске.
func TestBlobDeleteProjection_ObservationByBlobID(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("observation references blob id"))
	blobID := uuid.Must(uuid.NewV7()).String()
	obsID := uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-2*time.Hour)),
		blobObservationEvent(t, 2, obsID, []string{blobID}, base.Add(-time.Hour)),
	}))
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, testNodeID, 3, sha, base, "uuid reference"),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated, "наблюдение, ссылающееся на blobs.id, должно инвалидироваться")
	require.Equal(t, "blob deleted: "+sha+" (uuid reference)", obs.InvalidationReason)

	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл под живой ссылкой blobs.id удалять нельзя")

	referenced, err := st.BlobReferenced(ctx, sha)
	require.NoError(t, err)
	require.True(t, referenced, "prune должен видеть ссылку через blobs.id")
}

// Старые наблюдения хранят в blob_ids сам sha256: этот вариант тоже обязан
// находиться и запрещать prune.
func TestBlobDeleteProjection_ObservationByLegacySHA(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("observation references sha256"))
	blobID := uuid.Must(uuid.NewV7()).String()
	obsID := uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-2*time.Hour)),
		blobObservationEvent(t, 2, obsID, []string{sha}, base.Add(-time.Hour)),
	}))
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, testNodeID, 3, sha, base, "sha reference"),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated, "наблюдение с legacy-ссылкой sha256 должно инвалидироваться")
	require.Equal(t, "blob deleted: "+sha+" (sha reference)", obs.InvalidationReason)

	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл под legacy-ссылкой sha256 удалять нельзя")
}

// Ссылка только через observation_photos: наблюдение инвалидируется, файл жив.
func TestBlobDeleteProjection_ObservationByPhoto(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("observation references photo"))
	blobID := uuid.Must(uuid.NewV7()).String()
	horseID, obsID := uuid.NewString(), uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-2*time.Hour)),
	}))
	require.NoError(t, st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		if err := tx.CreateHorse(ctx, domain.Horse{ID: horseID, CreatedAt: base.Add(-3 * time.Hour), IdentityStatus: "active"}); err != nil {
			return err
		}
		return tx.CreateReviewedObservation(ctx, comparison.ObservationEvent{
			Observation: domain.Observation{
				ID: obsID, HorseID: horseID, NodeID: testNodeID, ObservedAt: base.Add(-time.Hour),
				Location: "50.01,30.02", ObserverID: "watcher",
			},
			Photos: []comparison.ReferencePhoto{
				{Photo: comparison.Photo{ID: uuid.NewString(), SHA: sha}, Descriptor: comparison.Descriptor{Version: "v1", Vector: []float64{1, 0}}},
			},
		})
	}))

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, testNodeID, 2, sha, base, "photo reference"),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.True(t, obs.Invalidated, "наблюдение со ссылкой из observation_photos должно инвалидироваться")

	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл подтверждённого фото удалять нельзя")
}

// delete -> ре-аплоад (revive) -> повторная доставка того же удаления с пира:
// блоб остаётся активным, файл на диске.
func TestBlobDeleteProjection_RedeliveryAfterReuploadKeepsBlob(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	content := []byte("photo revived after delete")
	sha := putBlob(t, blobs, content)
	blobID := uuid.Must(uuid.NewV7()).String()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-3*time.Hour)),
		blobDeletedEvent(t, testNodeID, 2, sha, base.Add(-2*time.Hour), "cleanup"),
	}))
	_, err := st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound)
	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.False(t, present, "файл без ссылок после удаления должен исчезнуть")

	// Ре-аплоад: файл вернулся на диск, событие blob.registered оживляет строку.
	require.NoError(t, blobs.Put(ctx, sha, "image/jpeg", content))
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 3, blobID, sha, base.Add(-time.Hour)),
	}))
	meta, err := st.GetBlobMeta(ctx, sha)
	require.NoError(t, err)
	require.Equal(t, sha, meta.SHA256)

	// Ретрай sync / второй пир доставляет то же старое удаление.
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, "peer-node", 7, sha, base.Add(-2*time.Hour), "cleanup"),
	}))

	meta, err = st.GetBlobMeta(ctx, sha)
	require.NoError(t, err, "устаревшее удаление не должно убивать перезалитый блоб")
	require.Equal(t, sha, meta.SHA256)
	present, err = blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл восстановленного блоба должен остаться на диске")
}

// Удаление приехало раньше регистрации: строки блоба ещё нет, файл не трогаем;
// после регистрации блоб активен и файл на месте (раньше prune удалял его).
func TestBlobDeleteProjection_OutOfOrderDeleteBeforeRegister(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	content := []byte("delete arrived before register")
	sha := putBlob(t, blobs, content)
	blobID := uuid.Must(uuid.NewV7()).String()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, testNodeID, 1, sha, base.Add(-2*time.Hour), "out of order"),
	}))
	present, err := blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "удаление неизвестного блоба не должно удалять файл")

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 2, blobID, sha, base.Add(-time.Hour)),
	}))
	meta, err := st.GetBlobMeta(ctx, sha)
	require.NoError(t, err, "после регистрации блоб должен быть активен")
	require.Equal(t, sha, meta.SHA256)
	present, err = blobs.Has(ctx, sha)
	require.NoError(t, err)
	require.True(t, present, "файл зарегистрированного блоба должен остаться")
}

// Устаревшее удаление не инвалидирует наблюдения живого (перезалитого) блоба.
func TestBlobDeleteProjection_StaleDeleteKeepsObservations(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	content := []byte("observation on revived blob")
	sha := putBlob(t, blobs, content)
	blobID := uuid.Must(uuid.NewV7()).String()
	obsID := uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-3*time.Hour)),
		blobDeletedEvent(t, testNodeID, 2, sha, base.Add(-2*time.Hour), "cleanup"),
	}))
	require.NoError(t, blobs.Put(ctx, sha, "image/jpeg", content))
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 3, blobID, sha, base.Add(-time.Hour)),
		// наблюдение уже после ре-аплоада
		blobObservationEvent(t, 4, obsID, []string{blobID}, base.Add(-time.Minute)),
	}))
	// повторная доставка старого удаления
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, "peer-node", 9, sha, base.Add(-2*time.Hour), "cleanup"),
	}))

	obs, err := st.GetObservation(ctx, obsID)
	require.NoError(t, err)
	require.False(t, obs.Invalidated, "устаревшее удаление не должно инвалидировать живое наблюдение")
}

// similar-путь: снимок находится по UUID-ссылке в blob_ids (OpenAPI: blob_ids —
// blobs.id), а не только по legacy-хэшу. Ссылка резолвится через blobRefPatterns.
func TestFindSnapshotByBlobSHA_UUIDReference(t *testing.T) {
	st, blobs, store := newProjectionFixture(t)
	ctx := context.Background()
	sha := putBlob(t, blobs, []byte("similar snapshot by uuid reference"))
	blobID := uuid.Must(uuid.NewV7()).String()
	obsID := uuid.NewString()
	base := time.Now().UTC().Truncate(time.Millisecond)

	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base.Add(-2*time.Hour)),
		blobObservationEvent(t, 2, obsID, []string{blobID}, base.Add(-time.Hour)),
	}))
	require.NoError(t, st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		return tx.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
			ID: uuid.NewString(), ObservationID: obsID, ModelVersion: "v1",
			Traits: json.RawMessage(`{"coat":"bay"}`), Confidence: 0.9, Fingerprint: "fp-uuid", Confirmed: true,
		})
	}))

	ts, err := st.FindSnapshotByBlobSHA(ctx, sha)
	require.NoError(t, err, "снимок должен находиться по UUID-ссылке blobs.id в blob_ids")
	require.Equal(t, obsID, ts.ObservationID)
	require.Equal(t, "fp-uuid", ts.Fingerprint)
}

// Часы удаляющего узла могут отставать от часов регистратора: легитимное
// удаление свежего блоба штампуется временем чуть меньше registered_at.
// Guard должен пропускать такие удаления в пределах допуска BlobDeleteClockSkew
// и по-прежнему отбрасывать по-настоящему устаревшие.
func TestBlobDeleteProjection_ClockSkewTolerance(t *testing.T) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Millisecond)

	// deletedAt на 4 минуты раньше registered_at — в пределах допуска (5 минут):
	// удаление применяется.
	st, blobs, store := newProjectionFixture(t)
	sha := putBlob(t, blobs, []byte("delete slightly before register"))
	blobID := uuid.Must(uuid.NewV7()).String()
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, base),
		blobDeletedEvent(t, testNodeID, 2, sha, base.Add(-4*time.Minute), "skew within tolerance"),
	}))
	_, err := st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"удаление в пределах BlobDeleteClockSkew должно применяться")

	// deletedAt на 10 минут раньше registered_at — вне допуска: no-op, блоб жив.
	st2, blobs2, store2 := newProjectionFixture(t)
	sha2 := putBlob(t, blobs2, []byte("delete far before register"))
	blobID2 := uuid.Must(uuid.NewV7()).String()
	require.NoError(t, store2.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID2, sha2, base),
		blobDeletedEvent(t, testNodeID, 2, sha2, base.Add(-10*time.Minute), "stale beyond tolerance"),
	}))
	meta, err := st2.GetBlobMeta(ctx, sha2)
	require.NoError(t, err, "удаление за пределами допуска не должно применяться")
	require.Equal(t, sha2, meta.SHA256)
}

// Граница окна допуска BlobDeleteClockSkew на сценарии ре-аплоада: удаление
// блоба, затем ре-аплоад (revive), затем повторная доставка того же старого
// удаления с пира. Зафиксированное поведение: ре-аплоад в пределах 5 минут
// после удаления НЕ защищает блоб — старое удаление всё ещё применяется
// (registered_at <= deletedAt + skew). Ре-аплоад позже окна защищает —
// удаление становится no-op. Окно расширения 5 минут — известный компромисс:
// допуск на расхождение часов не отличает «часы отстают» от «ре-аплоад сразу
// после удаления», поэтому короткий ре-аплоад теряется вместе со старым
// удалением. Это цена, за которую легитимные удаления свежих блобов не глохнут.
func TestBlobDeleteProjection_ReuploadSkewBoundary(t *testing.T) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Millisecond)
	deleteAt := base.Add(-time.Hour)

	// Ре-аплоад через 4 минуты после удаления: внутри допуска — повторная
	// доставка старого удаления снова удаляет блоб.
	st, blobs, store := newProjectionFixture(t)
	sha := putBlob(t, blobs, []byte("reupload within skew window"))
	blobID := uuid.Must(uuid.NewV7()).String()
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID, sha, deleteAt.Add(-time.Hour)),
		blobDeletedEvent(t, testNodeID, 2, sha, deleteAt, "cleanup"),
		blobRegisteredEvent(t, 3, blobID, sha, deleteAt.Add(4*time.Minute)),
	}))
	_, err := st.GetBlobMeta(ctx, sha)
	require.NoError(t, err, "ре-аплоад должен оживить блоб")
	require.NoError(t, store.Append(ctx, []domain.Event{
		blobDeletedEvent(t, "peer-node", 7, sha, deleteAt, "cleanup"),
	}))
	_, err = st.GetBlobMeta(ctx, sha)
	require.ErrorIs(t, err, storage.ErrNotFound,
		"ре-аплоад в пределах BlobDeleteClockSkew не защищает от повторного удаления")

	// Ре-аплоад через 6 минут после удаления: вне допуска — старое удаление
	// становится no-op, блоб остаётся живым.
	st2, blobs2, store2 := newProjectionFixture(t)
	sha2 := putBlob(t, blobs2, []byte("reupload beyond skew window"))
	blobID2 := uuid.Must(uuid.NewV7()).String()
	require.NoError(t, store2.Append(ctx, []domain.Event{
		blobRegisteredEvent(t, 1, blobID2, sha2, deleteAt.Add(-time.Hour)),
		blobDeletedEvent(t, testNodeID, 2, sha2, deleteAt, "cleanup"),
		blobRegisteredEvent(t, 3, blobID2, sha2, deleteAt.Add(6*time.Minute)),
	}))
	require.NoError(t, store2.Append(ctx, []domain.Event{
		blobDeletedEvent(t, "peer-node", 7, sha2, deleteAt, "cleanup"),
	}))
	meta, err := st2.GetBlobMeta(ctx, sha2)
	require.NoError(t, err, "ре-аплоад позже окна допуска должен защищать блоб от старого удаления")
	require.Equal(t, sha2, meta.SHA256)
}
