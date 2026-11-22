// perf_fixes_test.go: покрытие перформанс-фиксов — пагинация списков, батч-проверка
// ссылок наблюдения, UPDATE...RETURNING подтверждения снимка, страничная
// выдача недостающих событий и инвалидация кэша сводки.
package sqlstore

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

func mustCreateHorse(t *testing.T, s *Store, id string, createdAt time.Time) {
	t.Helper()
	require.NoError(t, s.CreateHorse(context.Background(), domain.Horse{
		ID: id, CreatedAt: createdAt, IdentityStatus: "active", Metadata: map[string]string{},
	}))
}

// testEvent — конверт события с корректными инвариантами (аналог makeEvent
// из контрактного набора, локальный для пакета).
func testEvent(nodeID string, seq uint64, eventType string) domain.Event {
	return domain.Event{
		EventID: "evt-" + nodeID + "-" + strconv.FormatUint(seq, 10), NodeID: nodeID, Sequence: seq,
		EventType: eventType, SchemaVersion: 1, AggregateID: "agg-" + nodeID,
		OccurredAt: time.Now().UTC().Truncate(time.Millisecond),
		Payload:    json.RawMessage(`{}`),
	}
}

func mustUpsertBlob(t *testing.T, s *Store, id, sha string, at time.Time) {
	t.Helper()
	created, err := s.UpsertBlobAt(context.Background(), domain.Blob{ID: id, SHA256: sha}, at)
	require.NoError(t, err)
	_ = created
}

// TestListPagesCursorContinuation — постраничные выборки согласованы с полным
// списком: конкатенация страниц равна списку, hasMore гаснет в конце.
func TestListPagesCursorContinuation(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < 5; i++ {
		mustCreateHorse(t, s, "h-"+string(rune('a'+i)), base.Add(time.Duration(i)*time.Minute))
		mustUpsertBlob(t, s, "b-"+string(rune('a'+i)), "000000000000000000000000000000000000000000000000000000000000000"+string(rune('0'+i)), base.Add(time.Duration(i)*time.Minute))
		require.NoError(t, s.CreateObservation(context.Background(), domain.Observation{
			ID: "o-" + string(rune('a'+i)), HorseID: "h-a", NodeID: "n", ObservedAt: base.Add(time.Duration(i) * time.Minute), BlobIDs: []string{},
		}))
	}

	sweepHorses, err := s.ListHorses(ctx)
	require.NoError(t, err)
	sweepObs, err := s.ListObservations(ctx, "")
	require.NoError(t, err)
	sweepBlobs, err := s.ListBlobs(ctx)
	require.NoError(t, err)
	require.Len(t, sweepHorses, 5)
	require.Len(t, sweepObs, 5)
	require.Len(t, sweepBlobs, 5)

	// лошади
	var got []domain.Horse
	key, id := int64(0), ""
	for {
		page, lastKey, hasMore, err := s.ListHorsesPage(ctx, key, id, 2)
		require.NoError(t, err)
		got = append(got, page...)
		if !hasMore {
			break
		}
		key, id = lastKey, page[len(page)-1].ID
	}
	require.Equal(t, sweepHorses, got)

	// наблюдения
	var gotObs []domain.Observation
	key, id = int64(0), ""
	for {
		page, lastKey, hasMore, err := s.ListObservationsPage(ctx, "", key, id, 2)
		require.NoError(t, err)
		gotObs = append(gotObs, page...)
		if !hasMore {
			break
		}
		key, id = lastKey, page[len(page)-1].ID
	}
	require.Equal(t, sweepObs, gotObs)

	// блобы
	var gotBlobs []domain.Blob
	key, id = int64(0), ""
	for {
		page, lastKey, hasMore, err := s.ListBlobsPage(ctx, key, id, 2)
		require.NoError(t, err)
		gotBlobs = append(gotBlobs, page...)
		if !hasMore {
			break
		}
		key, id = lastKey, page[len(page)-1].SHA256
	}
	require.Equal(t, sweepBlobs, gotBlobs)

	// лимит больше выборки: одна страница, hasMore=false
	one, _, hasMore, err := s.ListHorsesPage(ctx, 0, "", 100)
	require.NoError(t, err)
	require.Len(t, one, 5)
	require.False(t, hasMore)
}

// TestCreateObservationCheckedBatch — наблюдение со ссылкой на мягко удалённый
// блоб создаётся уже инвалидированным, ответ возвращается из транзакции
// (один батч-SELECT на все ссылки).
func TestCreateObservationCheckedBatch(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	mustCreateHorse(t, s, "h-1", now)
	mustUpsertBlob(t, s, "blob-1", "aa", now)
	mustUpsertBlob(t, s, "blob-2", "bb", now)
	require.NoError(t, s.CreateObservation(context.Background(), domain.Observation{ID: "o-1", BlobIDs: []string{}}))

	deletedAt := now.Add(time.Hour)
	// оба блоба из обращений наблюдения: один удалён по sha, другой по id.
	n, err := s.MarkBlobDeleted(ctx, "aa", deletedAt, "test")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, s.RunInTx(ctx, func(tx *Store) error {
		_, err := tx.MarkBlobDeleted(ctx, "bb", deletedAt, "test")
		return err
	}))

	o := domain.Observation{
		ID: "o-2", HorseID: "h-1", NodeID: "n", ObservedAt: now,
		BlobIDs: []string{"BLOB-2", "aa"}, // регистр ссылки отличен от хранения
	}
	stored, err := s.CreateObservationChecked(ctx, o, "test")
	require.NoError(t, err)
	require.True(t, stored.Invalidated, "наблюдение со ссылкой на удалённый блоб должно быть инвалидировано")
	require.NotNil(t, stored.InvalidatedAt)
	require.True(t, stored.InvalidatedAt.Equal(deletedAt), "метка инвалидации — deleted_at блоба")
	require.Contains(t, stored.InvalidationReason, "bb")

	// согласовано с проекцией: то же, что прочитал бы GetObservation
	reread, err := s.GetObservation(ctx, "o-2")
	require.NoError(t, err)
	require.Equal(t, stored.Invalidated, reread.Invalidated)
	require.Equal(t, stored.InvalidationReason, reread.InvalidationReason)

	// живые блобы не инвалидируют наблюдение
	o2 := domain.Observation{ID: "o-3", HorseID: "h-1", NodeID: "n", ObservedAt: now, BlobIDs: []string{"cc"}}
	require.NoError(t, s.RunInTx(ctx, func(tx *Store) error {
		created, err := tx.UpsertBlob(ctx, domain.Blob{ID: "blob-3", SHA256: "cc"})
		require.NoError(t, err)
		_ = created
		return nil
	}))
	fresh, err := s.CreateObservationChecked(ctx, o2, "test")
	require.NoError(t, err)
	require.False(t, fresh.Invalidated)
}

// TestBlobReferencedBatch — пакетная проверка ссылок согласована с по-блобной.
func TestBlobReferencedBatch(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)
	now := time.Now().UTC().Truncate(time.Millisecond)
	// blobs.id по контракту — UUIDv7 (hex+дефис); refs со сторонними символами
	// резолвятся как legacy-sha (см. blobIDPattern).
	const refID = "018f0000-7000-7000-7000-000000000001"
	mustUpsertBlob(t, s, refID, "1111", now)
	mustUpsertBlob(t, s, "018f0000-7000-7000-7000-000000000002", "2222", now)
	// верхний регистр в blob_ids: ссылка должна резолвиться
	require.NoError(t, s.CreateObservation(context.Background(), domain.Observation{
		ID: "o-1", NodeID: "n", ObservedAt: now, BlobIDs: []string{"018F0000-7000-7000-7000-000000000001"},
	}))
	// фото напрямую
	_, err := s.exec(ctx,
		`INSERT INTO observation_photos (id, observation_id, sha256, name, annotation, model_version, embedding)
		 VALUES ('p-1','o-1','3333','x','null','v','null')`)
	require.NoError(t, err)
	// паспорт лошади
	mustCreateHorse(t, s, "h-1", now)
	passportMeta, err := json.Marshal(map[string]string{domain.MetaPassportSHA256: "4444"})
	require.NoError(t, err)
	_, err = s.exec(ctx, `UPDATE horses SET metadata = ? WHERE id = 'h-1'`, string(passportMeta))
	require.NoError(t, err)

	shas := []string{"1111", "2222", "3333", "4444", "5555"}
	batch, err := s.BlobReferencedBatch(ctx, shas)
	require.NoError(t, err)
	require.True(t, batch["1111"], "ссылка через blob_ids (id)")
	require.True(t, batch["3333"], "ссылка через observation_photos.sha256")
	require.True(t, batch["4444"], "ссылка через metadata.passport_sha256")
	require.False(t, batch["2222"], "блоб без ссылок")
	require.False(t, batch["5555"], "несуществующий блоб")

	for _, sha := range shas {
		oneByOne, err := s.BlobReferenced(ctx, sha)
		require.NoError(t, err)
		require.Equal(t, oneByOne, batch[sha], "пакетный и точечный ответ должны совпадать для %s", sha)
	}

	deleted, err := s.BlobsDeleted(ctx, []string{"2222", "1111"})
	require.NoError(t, err)
	require.False(t, deleted["1111"])
	require.False(t, deleted["2222"])
	n, err := s.MarkBlobDeleted(ctx, "2222", now, "test")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	deleted, err = s.BlobsDeleted(ctx, []string{"2222"})
	require.NoError(t, err)
	require.True(t, deleted["2222"])
}

// TestConfirmTraitSnapshotReturning — UPDATE...RETURNING возвращает
// обновлённую строку одним запросом (sqlite).
func TestConfirmTraitSnapshotReturning(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)
	require.NoError(t, s.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: "ts-1", ObservationID: "o-1", ModelVersion: "v1", Traits: json.RawMessage(`{"color":"bay"}`),
		RawPredictedTraits: json.RawMessage(`null`), Confidence: 0.5, Fingerprint: "fp-1",
	}))
	saved, err := s.ConfirmTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: "ts-1", Traits: json.RawMessage(`{"color":"black"}`), Confidence: 1.0, Fingerprint: "fp-2",
	})
	require.NoError(t, err)
	require.Equal(t, json.RawMessage(`{"color":"black"}`), saved.Traits)
	// при первом подтверждении модельное значение сохраняется как raw
	require.Equal(t, json.RawMessage(`{"color":"bay"}`), saved.RawPredictedTraits)
	require.True(t, saved.Confirmed)
	require.Equal(t, "fp-2", saved.Fingerprint)

	// повторное подтверждение raw не затирает
	again, err := s.ConfirmTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: "ts-1", Traits: json.RawMessage(`{"color":"grey"}`), Confidence: 1.0, Fingerprint: "fp-3",
	})
	require.NoError(t, err)
	require.Equal(t, json.RawMessage(`{"color":"bay"}`), again.RawPredictedTraits)

	// несуществующий снимок — ErrNotFound
	_, err = s.ConfirmTraitSnapshot(ctx, domain.TraitSnapshot{ID: "missing", Traits: json.RawMessage(`{}`)})
	require.ErrorIs(t, err, storage.ErrNotFound)
}

// TestGetMissingEventsPaged — постраничная выдача недостающих событий
// согласована с полным GetMissingEvents.
func TestGetMissingEventsPaged(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)
	var events []domain.Event
	for i := 1; i <= 4; i++ {
		events = append(events, testEvent("node-a", uint64(i), domain.EventHorseCreated))
	}
	for i := 1; i <= 3; i++ {
		events = append(events, testEvent("node-b", uint64(i), domain.EventHorseCreated))
	}
	// события должны идти в журнал с валидным payload; makeEvent-подобный
	// конверт выше.
	require.NoError(t, s.Append(ctx, events))

	remote := storage.VectorClock{"node-a": 2}
	full, err := s.GetMissingEvents(ctx, remote)
	require.NoError(t, err)
	require.Len(t, full, 5) // a#3, a#4, b#1..3

	var collected []domain.Event
	cursor := MissingEventsCursor{}
	for {
		page, next, err := s.GetMissingEventsPaged(ctx, remote, cursor, 2)
		require.NoError(t, err)
		collected = append(collected, page...)
		if next == nil {
			break
		}
		cursor = *next
	}
	require.Equal(t, full, collected)

	// пустой хвост: курсор конца — пустая страница
	page, next, err := s.GetMissingEventsPaged(ctx, remote, MissingEventsCursor{NodeID: "node-b", Sequence: 3}, 2)
	require.NoError(t, err)
	require.Empty(t, page)
	require.Nil(t, next)
}

// TestSummaryCacheInvalidation — кэш сводки /node живёт не дольше TTL и
// сбрасывается после Append (свежие данные видны сразу же).
func TestSummaryCacheInvalidation(t *testing.T) {
	ctx := context.Background()
	s := openSQLiteTestStore(t)

	require.NoError(t, s.Append(ctx, []domain.Event{testEvent("node-a", 1, domain.EventHorseCreated)}))
	clock, err := s.GetVectorClock(ctx)
	require.NoError(t, err)
	require.Equal(t, storage.VectorClock{"node-a": 1}, clock)

	// запись тем же хранилищем должна быть видна сразу (инвалидация на Append)
	require.NoError(t, s.Append(ctx, []domain.Event{testEvent("node-a", 2, domain.EventHorseCreated)}))
	clock, err = s.GetVectorClock(ctx)
	require.NoError(t, err)
	require.Equal(t, storage.VectorClock{"node-a": 2}, clock)

	info, err := s.NodeSummary(ctx, "node-a")
	require.NoError(t, err)
	require.EqualValues(t, 2, info.EventCount)
	require.EqualValues(t, 2, info.LastSeq)
}
