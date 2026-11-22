package integration

// Аудит путей GetBlobByID / inference / similar / observations для блобов,
// удалённых событием blob.deleted (PR #22). Тесты фиксируют фактическое
// поведение API; расхождения с ожидаемым описаны в комментариях.
//
// Пакет не поддерживает -shuffle: все тесты делят общий TestMain-стор без
// очистки между тестами (см. TestHorseLifecycle, который ломается при
// перестановке). Изоляция здесь обеспечивается не порядком, а уникальными
// для каждого теста seed изображения (deletedPathPNG), horse_id/observation_id
// и уникальным набором трейтов (uniqueTraits), поэтому сами тесты друг на
// друга не влияют независимо от порядка запуска.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"

	"github.com/stretchr/testify/require"
)

// uniqueTraitSeq — счётчик обращений к uniqueTraits в пределах прогона пакета.
var uniqueTraitSeq atomic.Uint64

// uniqueTraits возвращает канонический набор признаков, уникальный в пределах
// прогона пакета: комбинация значений собирается в смешанной системе счисления
// по счётчику, поэтому два вызова не дают одинаковый fingerprint. Это удерживает
// выдачи similar (проверки Len==1) от склейки со снимками других тестов,
// подтверждёнными с тем же набором признаков.
func uniqueTraits(t *testing.T) map[string]string {
	t.Helper()
	seq := uniqueTraitSeq.Add(1)
	traits := make(map[string]string, len(inference.CanonicalKeys))
	for _, key := range inference.CanonicalKeys {
		vals := inference.AllowedValues(key)
		require.NotEmpty(t, vals, "нет допустимых значений для признака %q", key)
		traits[key] = vals[seq%uint64(len(vals))]
		seq /= uint64(len(vals))
	}
	return traits
}

// deletedPathPNG — детерминированное, но уникальное для каждого seed
// изображение: sha256 не пересекается с inferenceImagePNG и другими тестами
// пакета, чтобы удаление блоба не влияло на соседние сценарии.
func deletedPathPNG(t *testing.T, seed int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 64+seed%7, 96+seed%5))
	bounds := img.Bounds()
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((x*7 + seed*31) % 256),
				G: uint8((y*11 + seed*17) % 256),
				B: uint8((x + y + seed*53) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

// uploadedPNG грузит изображение и возвращает блоб из ответа 201.
func uploadedPNG(t *testing.T, seed int) domain.Blob {
	t.Helper()
	resp, blob := uploadTestBlob(t, deletedPathPNG(t, seed))
	require.Equal(t, http.StatusCreated, resp.Code, "upload: %s", resp.Body.String())
	return blob
}

// predictedSnapshot запускает POST /api/v1/inference/traits и возвращает снимок.
func predictedSnapshot(t *testing.T, blobID, observationID string) domain.TraitSnapshot {
	t.Helper()
	body := map[string]any{"blob_id": blobID}
	if observationID != "" {
		body["observation_id"] = observationID
	}
	resp := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/inference/traits", body), http.StatusCreated)
	return DecodeJSON[domain.TraitSnapshot](t, resp)
}

// confirmedSnapshot подтверждает признаки человеком, чтобы снимок попал в индекс
// похожих, и возвращает сохранённый снимок (fingerprint пересчитан по правке).
func confirmedSnapshot(t *testing.T, id string, traits map[string]string) domain.TraitSnapshot {
	t.Helper()
	resp := MakeRequest(t, NewRequestWithJSON(t, http.MethodPatch, "/api/v1/trait_snapshots/"+id,
		map[string]any{"traits": traits}), http.StatusOK)
	return DecodeJSON[domain.TraitSnapshot](t, resp)
}

// blobIDBySHA — внутренний id блоба из GET /api/v1/blobs: POST /api/v1/blobs
// возвращает только sha256 (поле id в ответе не заполняется — расхождение,
// описанное в отчёте).
func blobIDBySHA(t *testing.T, sha string) string {
	t.Helper()
	list := DecodeJSON[[]domain.Blob](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs"), http.StatusOK))
	for _, b := range list {
		if b.SHA256 == sha {
			require.NotEmpty(t, b.ID, "id блоба должен отдаваться в списке")
			return b.ID
		}
	}
	require.Failf(t, "blob not found in list", "sha=%s", sha)
	return ""
}

func blobSHAInList(list []domain.Blob, sha string) bool {
	for _, b := range list {
		if b.SHA256 == sha {
			return true
		}
	}
	return false
}

// TestBlobDeletedHiddenFromBlobAPI: после blob.deleted блоб не отдаётся
// по GET /api/v1/blobs/{sha256} и выпадает из списка GET /api/v1/blobs,
// при этом соседний (не удалённый) блоб остаётся доступным.
func TestBlobDeletedHiddenFromBlobAPI(t *testing.T) {
	kept := uploadedPNG(t, 101)
	removed := uploadedPNG(t, 102)
	removedID := blobIDBySHA(t, removed.SHA256)

	before := DecodeJSON[[]domain.Blob](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs"), http.StatusOK))
	require.True(t, blobSHAInList(before, kept.SHA256))
	require.True(t, blobSHAInList(before, removed.SHA256))

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+removed.SHA256+"?reason=audit"), http.StatusNoContent)

	// одиночный GET по sha256 — 404 c явным телом
	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+removed.SHA256), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "blob not found")

	// внутренний id блоба маршрут тоже не открывает (GetBlobMeta работает по sha256)
	byID := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+removedID), http.StatusNotFound)
	require.Contains(t, byID.Body.String(), "blob not found")

	after := DecodeJSON[[]domain.Blob](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs"), http.StatusOK))
	require.False(t, blobSHAInList(after, removed.SHA256), "удалённый блоб не должен попадать в список")
	require.True(t, blobSHAInList(after, kept.SHA256), "соседний блоб не должен исчезать из списка")

	// файл не тронут, но API его всё равно не отдаёт (deleted_at фильтруется раньше)
	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+kept.SHA256), http.StatusOK)
}

// TestBlobDeletedInferenceTraits: postInferenceTraits идёт через GetBlobByID,
// поэтому удалённый блоб даёт 404 и по sha256, и по id; после ре-аплоада
// (blob.registered) предсказание снова работает.
func TestBlobDeletedInferenceTraits(t *testing.T) {
	blob := uploadedPNG(t, 201)
	blobID := blobIDBySHA(t, blob.SHA256)
	first := predictedSnapshot(t, blob.SHA256, "")
	require.NotEmpty(t, first.ID)

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=audit"), http.StatusNoContent)

	// по sha256
	resp := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/inference/traits",
		map[string]any{"blob_id": blob.SHA256}), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "blob not found")

	// по внутреннему id — тоже 404: GetBlobByID фильтрует deleted_at = 0
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/inference/traits",
		map[string]any{"blob_id": blobID}), http.StatusNotFound)

	// отсутствующий блоб ведёт себя так же — 404, без 500 и без снимка
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/inference/traits",
		map[string]any{"blob_id": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"}),
		http.StatusNotFound)

	// ре-аплоад того же файла регистрирует блоб заново — инференс снова доступен
	reupload, reuploaded := uploadTestBlob(t, deletedPathPNG(t, 201))
	require.Equal(t, http.StatusCreated, reupload.Code)
	require.Equal(t, blob.SHA256, reuploaded.SHA256)
	second := predictedSnapshot(t, blob.SHA256, "")
	require.NotEmpty(t, second.ID)
	require.Equal(t, first.Fingerprint, second.Fingerprint)
}

// TestBlobDeletedSimilarPaths: GET /api/v1/inference/similar по удалённому
// блобу. Фактическое поведение: 404 на уровне блоба (GetBlobByID), а не
// пустой список. РАСХОЖДЕНИЕ: после ре-аплоада similar снова отвечает 200 и
// берёт за источник снимок инвалидированного наблюдения — FindSnapshotByBlobSHA
// не фильтрует invalidated_at, хотя ListSimilarTraitSnapshots такие снимки
// исключает. По смыслу blob.deleted ре-аплоад — это новое фото без снимка,
// т.е. ожидался бы 404 "trait snapshot for blob".
func TestBlobDeletedSimilarPaths(t *testing.T) {
	blob := uploadedPNG(t, 301)
	blobID := blobIDBySHA(t, blob.SHA256)

	// блоб есть, снимка ещё нет — 404 по снимку, а не 500
	resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/inference/similar?blob_id="+blob.SHA256), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "trait snapshot for blob not found")

	horse := DecodeJSON[domain.Horse](t, MakeRequest(t,
		NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses", map[string]any{}), http.StatusCreated))
	obs := DecodeJSON[domain.Observation](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations",
		map[string]any{"horse_id": horse.ID, "observer_id": "similar-audit", "blob_ids": []string{blob.SHA256}}),
		http.StatusCreated))

	traits := uniqueTraits(t)
	src := predictedSnapshot(t, blob.SHA256, obs.ID)
	srcConfirmed := confirmedSnapshot(t, src.ID, traits)
	sibling := predictedSnapshot(t, blob.SHA256, "")
	confirmedSnapshot(t, sibling.ID, traits)

	type similarResponse struct {
		Query      string                 `json:"query"`
		Disclaimer string                 `json:"disclaimer"`
		Results    []domain.TraitSnapshot `json:"results"`
	}

	// до удаления — источник найден, в выдаче ровно соседний снимок
	beforeDelete := DecodeJSON[similarResponse](t, MakeRequest(t,
		NewRequest(t, http.MethodGet, "/api/v1/inference/similar?blob_id="+blob.SHA256+"&limit=5"), http.StatusOK))
	require.Equal(t, srcConfirmed.Fingerprint, beforeDelete.Query)
	require.Len(t, beforeDelete.Results, 1)
	require.Equal(t, sibling.ID, beforeDelete.Results[0].ID)

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=audit"), http.StatusNoContent)

	// удалённый блоб: 404 и по sha256, и по id, и никакого частичного результата
	resp = MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/inference/similar?blob_id="+blob.SHA256), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "blob not found")
	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/inference/similar?blob_id="+blobID), http.StatusNotFound)

	// ре-аплоад: фактическое поведение — 200, источник = снимок инвалидированного наблюдения
	reupload, reuploaded := uploadTestBlob(t, deletedPathPNG(t, 301))
	require.Equal(t, http.StatusCreated, reupload.Code)
	require.Equal(t, blob.SHA256, reuploaded.SHA256)

	afterReupload := DecodeJSON[similarResponse](t, MakeRequest(t,
		NewRequest(t, http.MethodGet, "/api/v1/inference/similar?blob_id="+blob.SHA256+"&limit=5"), http.StatusOK))
	require.Equal(t, srcConfirmed.Fingerprint, afterReupload.Query, "источник — снимок инвалидированного наблюдения")
	require.Len(t, afterReupload.Results, 1)
	require.Equal(t, sibling.ID, afterReupload.Results[0].ID)
}

// TestBlobDeletedComparisonPaths: путь similarity POST /api/v1/comparisons
// проверяет блоб через GetBlobMeta раньше модели, поэтому удалённый блоб даёт
// 404 даже когда сама модель сравнения в ноде не сконфигурирована (иначе был
// бы 503). Так же ведёт себя неизвестный, но валидный по формату sha256.
func TestBlobDeletedComparisonPaths(t *testing.T) {
	blob := uploadedPNG(t, 501)

	// без модели сравнения живой блоб упирается в 503 — контрольная точка
	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/comparisons",
		map[string]any{"blob_sha256": blob.SHA256, "top_k": 5}), http.StatusServiceUnavailable)

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=audit"), http.StatusNoContent)

	resp := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/comparisons",
		map[string]any{"blob_sha256": blob.SHA256, "top_k": 5}), http.StatusNotFound)
	require.Contains(t, resp.Body.String(), "blob not found")

	MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/comparisons",
		map[string]any{"blob_sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "top_k": 5}),
		http.StatusNotFound)
}

// historyItem — плоское представление comparison.History в ответе
// GET /api/v1/observations?include=photos,annotations.
type historyItem struct {
	ID                 string     `json:"id"`
	HorseID            string     `json:"horse_id"`
	BlobIDs            []string   `json:"blob_ids"`
	Invalidated        bool       `json:"invalidated"`
	InvalidatedAt      *time.Time `json:"invalidated_at"`
	InvalidationReason string     `json:"invalidation_reason"`
	Photos             []struct {
		ID   string `json:"photo_id"`
		SHA  string `json:"blob_sha256"`
		Name string `json:"name"`
	} `json:"photos"`
}

// TestBlobDeletedObservationInclude: наблюдение с удалённым блобом отдаётся
// в GET /api/v1/observations?include=photos,annotations с invalidated=true,
// invalidated_at и причиной. РАСХОЖДЕНИЕ: photo-ссылка на удалённый блоб
// остаётся в photos[].blob_sha256 — historyPhotos не фильтрует deleted_at,
// хотя GET /api/v1/blobs/{sha} для этого хэша уже 404.
func TestBlobDeletedObservationInclude(t *testing.T) {
	blob := uploadedPNG(t, 401)
	horse := DecodeJSON[domain.Horse](t, MakeRequest(t,
		NewRequestWithJSON(t, http.MethodPost, "/api/v1/horses", map[string]any{}), http.StatusCreated))
	obs := DecodeJSON[domain.Observation](t, MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/api/v1/observations",
		map[string]any{"horse_id": horse.ID, "observer_id": "include-audit", "blob_ids": []string{blob.SHA256}}),
		http.StatusCreated))
	require.False(t, obs.Invalidated)

	url := "/api/v1/observations?horse_id=" + horse.ID + "&include=photos,annotations"

	before := DecodeJSON[[]historyItem](t, MakeRequest(t, NewRequest(t, http.MethodGet, url), http.StatusOK))
	require.Len(t, before, 1)
	require.False(t, before[0].Invalidated)
	require.Len(t, before[0].Photos, 1)
	require.Equal(t, blob.SHA256, before[0].Photos[0].SHA)

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=review+cleanup"), http.StatusNoContent)

	after := DecodeJSON[[]historyItem](t, MakeRequest(t, NewRequest(t, http.MethodGet, url), http.StatusOK))
	require.Len(t, after, 1, "наблюдение остаётся в журнале и в выдаче")
	require.Equal(t, obs.ID, after[0].ID)
	require.True(t, after[0].Invalidated)
	require.NotNil(t, after[0].InvalidatedAt)
	require.Equal(t, "review cleanup", after[0].InvalidationReason)
	require.Contains(t, after[0].BlobIDs, blob.SHA256, "ссылка на удалённый блоб сохраняется для аудита")
	// фактическое поведение: photo-ссылка не вычищается из include=photos
	require.Len(t, after[0].Photos, 1)
	require.Equal(t, blob.SHA256, after[0].Photos[0].SHA)

	// только annotations — та же инвалидация
	onlyAnnotations := DecodeJSON[[]historyItem](t, MakeRequest(t,
		NewRequest(t, http.MethodGet, "/api/v1/observations?horse_id="+horse.ID+"&include=annotations"), http.StatusOK))
	require.Len(t, onlyAnnotations, 1)
	require.True(t, onlyAnnotations[0].Invalidated)

	// без include — тоже invalidated=true
	plain := DecodeJSON[[]domain.Observation](t, MakeRequest(t,
		NewRequest(t, http.MethodGet, "/api/v1/observations?horse_id="+horse.ID), http.StatusOK))
	require.Len(t, plain, 1)
	require.True(t, plain[0].Invalidated)
	require.Equal(t, "review cleanup", plain[0].InvalidationReason)
}
