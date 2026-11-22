// Аудит путей поиска при удалённом фото (blob.deleted): инвалидированное
// наблюдение не участвует ни в ранжировании сравнения, ни в подтверждённых
// reference photos, ни в поиске похожих trait-снимков, а живое соседнее
// наблюдение продолжает участвовать.
package comparison_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

const (
	testNodeID    = "01900000-0000-7000-8000-0000000000b5"
	testVersion   = "v1"
	testFingerprt = "fp-dead-blob"
)

// newComparisonStore поднимает sqlite с миграциями: те же таблицы, что у ноды.
func newComparisonStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	st, err := sqlstore.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	require.NoError(t, migrate.Apply(context.Background(), st.DB(), "../../migrations", "sqlite"))
	return st
}

// seedObservation заводит лошадь, блоб, наблюдение с подтверждённым фото
// (observation_photos — источник и для References, и для поиска похожих).
func seedObservation(t *testing.T, st *sqlstore.Store, horseID, horseName, sha, photoID string, vector []float64) string {
	t.Helper()
	ctx := context.Background()
	if _, err := st.GetHorse(ctx, horseID); err != nil {
		require.NoError(t, st.CreateHorse(ctx, domain.Horse{
			ID: horseID, CreatedAt: time.Now().UTC(), IdentityStatus: "active",
			Metadata: map[string]string{"name": horseName},
		}))
	}
	_, err := st.UpsertBlob(ctx, domain.Blob{ID: uuid.NewString(), SHA256: sha, MimeType: "image/jpeg", Size: 16})
	require.NoError(t, err)

	observationID := uuid.NewString()
	require.NoError(t, st.CreateReviewedObservation(ctx, comparison.ObservationEvent{
		Observation: domain.Observation{
			ID: observationID, HorseID: horseID, NodeID: testNodeID, ObservedAt: time.Now().UTC(),
			Location: "50.01,30.02", ObserverID: "watcher", BlobIDs: []string{sha},
		},
		Photos: []comparison.ReferencePhoto{{
			Photo:      comparison.Photo{ID: photoID, SHA: sha},
			Descriptor: comparison.Descriptor{Version: testVersion, Vector: vector},
		}},
	}))

	// Подтверждённый снимок признаков с общим fingerprint — вход поиска похожих.
	require.NoError(t, st.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: uuid.NewString(), ObservationID: observationID, ModelVersion: testVersion,
		Traits: json.RawMessage(`{"coat":"bay"}`), Confidence: 0.9,
		Fingerprint: testFingerprt, Confirmed: true,
	}))
	return observationID
}

func photoIDs(candidates []comparison.Candidate) []string {
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.BestPhotoID)
	}
	return out
}

// TestRankSkipsInvalidatedReferences: даже если источник передал удалённый
// эталон в ранжирование, он не даёт ни очков, ни BestPhotoID — побеждает
// живое соседнее фото той же лошади.
func TestRankSkipsInvalidatedReferences(t *testing.T) {
	deletedAt := time.Now().UTC()
	ref := func(horse, photo string, invalidated *time.Time, v ...float64) comparison.Reference {
		return comparison.Reference{HorseID: horse, PhotoID: photo, InvalidatedAt: invalidated,
			Descriptor: comparison.Descriptor{Version: testVersion, Vector: v}}
	}
	query := comparison.Descriptor{Version: testVersion, Vector: []float64{1, 0}}
	refs := []comparison.Reference{
		ref("h-live", "live-photo", nil, 0.6, 0.8),
		// Косинус выше, но фото удалено: кандидатом оно быть не может.
		ref("h-live", "deleted-photo", &deletedAt, 1, 0),
		// У этой лошади удалённое фото единственное — кандидата нет вовсе.
		ref("h-gone", "only-deleted-photo", &deletedAt, 1, 0),
	}

	got := comparison.Rank(query, refs, 5)
	require.Len(t, got, 1)
	require.Equal(t, "h-live", got[0].HorseID)
	require.Equal(t, "live-photo", got[0].BestPhotoID)
	require.InDelta(t, 0.6, got[0].Similarity, 1e-9)
	require.NotContains(t, photoIDs(got), "deleted-photo")
}

// TestRankKeepsLiveReference — anti-case к TestRankSkipsInvalidatedReferences:
// живой эталон с InvalidatedAt == nil не отбрасывается, проходит скоринг и
// становится BestPhotoID. Иначе фильтр «на всякий случай» мог бы отсечь всё.
func TestRankKeepsLiveReference(t *testing.T) {
	query := comparison.Descriptor{Version: testVersion, Vector: []float64{1, 0}}
	refs := []comparison.Reference{{
		HorseID: "h-live", PhotoID: "live-photo", InvalidatedAt: nil,
		Descriptor: comparison.Descriptor{Version: testVersion, Vector: []float64{0.6, 0.8}},
	}}

	got := comparison.Rank(query, refs, 5)
	require.Len(t, got, 1)
	require.Equal(t, "h-live", got[0].HorseID)
	require.Equal(t, "live-photo", got[0].BestPhotoID)
	require.InDelta(t, 0.6, got[0].Similarity, 1e-9)
}

// TestInvalidatedObservationExcludedFromAllSearchPaths проверяет три пути
// поиска на реальных проекциях: инвалидированное наблюдение выпадает из
// References (и значит из ранжирования/BestPhotoID) и из похожих снимков,
// а живое соседнее наблюдение остаётся видимым.
func TestInvalidatedObservationExcludedFromAllSearchPaths(t *testing.T) {
	ctx := context.Background()
	st := newComparisonStore(t)

	const (
		horseID   = "h-1"
		deadSHA   = "1111111111111111111111111111111111111111111111111111111111111111"
		liveSHA   = "2222222222222222222222222222222222222222222222222222222222222222"
		deadPhoto = "photo-deleted"
		livePhoto = "photo-live"
	)
	deadObs := seedObservation(t, st, horseID, "Ласточка", deadSHA, deadPhoto, []float64{1, 0})
	liveObs := seedObservation(t, st, horseID, "Ласточка", liveSHA, livePhoto, []float64{0.8, 0.6})
	require.NotEqual(t, deadObs, liveObs)

	query := comparison.Descriptor{Version: testVersion, Vector: []float64{1, 0}}

	// До удаления оба фото — кандидаты, удалённое выигрывает по косинусу.
	refs, err := st.References(ctx, testVersion)
	require.NoError(t, err)
	require.Len(t, refs, 2)
	refsBefore := append([]comparison.Reference{}, refs...)
	before := comparison.Rank(query, refs, 5)
	require.Len(t, before, 1)
	require.Equal(t, deadPhoto, before[0].BestPhotoID)

	similar, err := st.ListSimilarTraitSnapshots(ctx, testFingerprt, "", 10)
	require.NoError(t, err)
	require.Len(t, similar, 2)

	// blob.deleted: мягкое удаление блоба и инвалидация наблюдений (как в проекции).
	at := time.Now().UTC()
	n, err := st.MarkBlobDeleted(ctx, deadSHA, at, "test")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	invalidated, err := st.InvalidateObservationsByBlob(ctx, deadSHA, at, "test")
	require.NoError(t, err)
	require.EqualValues(t, 1, invalidated)
	obs, err := st.GetObservation(ctx, deadObs)
	require.NoError(t, err)
	require.True(t, obs.Invalidated)

	// 1. Подтверждённые reference photos: остался только живой вектор.
	refs, err = st.References(ctx, testVersion)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	require.Equal(t, livePhoto, refs[0].PhotoID)

	// 2. Ранжирование сравнения: удалённое фото не всплывает даже как BestPhotoID
	//    той же лошади — кандидат строится по живому соседнему фото.
	got := comparison.Rank(query, refs, 5)
	require.Len(t, got, 1)
	require.Equal(t, horseID, got[0].HorseID)
	require.Equal(t, livePhoto, got[0].BestPhotoID)
	require.InDelta(t, 0.8, got[0].Similarity, 1e-9)
	require.NotContains(t, photoIDs(got), deadPhoto)

	// 2б. Если вызывающий держит эталоны, собранные до удаления, Rank обязан
	//     отбросить помеченный эталон сам.
	for i := range refsBefore {
		if refsBefore[i].PhotoID == deadPhoto {
			refsBefore[i].InvalidatedAt = &at
		}
	}
	guarded := comparison.Rank(query, refsBefore, 5)
	require.Len(t, guarded, 1)
	require.Equal(t, livePhoto, guarded[0].BestPhotoID)
	require.NotContains(t, photoIDs(guarded), deadPhoto)

	// 3. Похожие trait-снимки: снимок инвалидированного наблюдения не находится.
	similar, err = st.ListSimilarTraitSnapshots(ctx, testFingerprt, "", 10)
	require.NoError(t, err)
	require.Len(t, similar, 1)
	require.Equal(t, liveObs, similar[0].ObservationID)

	// Живое наблюдение по-прежнему доступно и не помечено.
	live, err := st.GetObservation(ctx, liveObs)
	require.NoError(t, err)
	require.False(t, live.Invalidated)
}
