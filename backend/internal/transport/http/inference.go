// inference.go: REST-хендлеры локального CV-инференса (ADR-0012).
// Аддитивный файл: хуки в роутере — только три маршрута в server.go.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// similarDisclaimer — явный дисклеймер ответа поиска похожих.
const similarDisclaimer = "fingerprint — поиск похожих по набору признаков, НЕ биологическая идентичность"

// traitSnapshotResponse — TraitResult + snapshot metadata из БД.
type traitSnapshotResponse struct {
	domain.TraitSnapshot
	Disclaimer string `json:"disclaimer,omitempty"`
}

type createTraitsRequest struct {
	BlobID        string `json:"blob_id"`        // blobs.id или sha256
	ObservationID string `json:"observation_id"` // опционально: привязка к наблюдению
}

// postInferenceTraits — POST /api/v1/inference/traits: предсказание признаков
// движком по blob_id. Сохраняет снимок (confirmed=false) и возвращает его.
func postInferenceTraits(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var form createTraitsRequest
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if form.BlobID == "" {
			writeError(w, http.StatusBadRequest, `field "blob_id" required`)
			return
		}
		blob, err := d.Store.GetBlobByID(r.Context(), form.BlobID)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		result, err := d.Engine.ExtractTraits(r.Context(), inference.ImageInput{SHA256: blob.SHA256})
		if err != nil {
			handleInferenceError(w, err)
			return
		}
		traitsJSON, err := inference.MarshalCanonical(result.Traits)
		if err != nil {
			serverError(w, err)
			return
		}
		ts := domain.TraitSnapshot{
			ID:            uuid.Must(uuid.NewV7()).String(),
			ObservationID: form.ObservationID,
			ModelVersion:  result.ModelVersion,
			Traits:        traitsJSON,
			Confidence:    result.Confidence,
			Fingerprint:   inference.Fingerprint(result.Traits),
		}
		if err := d.Store.CreateTraitSnapshot(r.Context(), ts); err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Location", "/api/v1/trait_snapshots/"+ts.ID)
		writeJSON(w, http.StatusCreated, ts)
	}
}

type confirmTraitsRequest struct {
	Traits map[string]string `json:"traits"`
}

// patchTraitSnapshot — PATCH /api/v1/trait_snapshots/{id}: подтверждение
// человеком. Оригинальное предсказание сохраняется в raw_predicted_traits,
// правка — в traits, edited = true (ADR-0012, human-in-the-loop).
func patchTraitSnapshot(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var form confirmTraitsRequest
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if len(form.Traits) == 0 {
			writeError(w, http.StatusBadRequest, `field "traits" required`)
			return
		}
		ts, err := d.Store.GetTraitSnapshot(r.Context(), id)
		if err != nil {
			notFoundOrServerError(w, err, "trait snapshot")
			return
		}
		traitsJSON, err := inference.MarshalCanonical(form.Traits)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid traits values")
			return
		}
		if len(traitsJSON) == 0 || string(traitsJSON) == "{}" || string(traitsJSON) == "null" {
			writeError(w, http.StatusBadRequest, "no valid traits values")
			return
		}
		updated := domain.TraitSnapshot{
			ID:          ts.ID,
			Traits:      traitsJSON,
			Confidence:  1.0, // подтверждено человеком
			Fingerprint: inference.Fingerprint(form.Traits),
		}
		if err := d.Store.ConfirmTraitSnapshot(r.Context(), updated); err != nil {
			notFoundOrServerError(w, err, "trait snapshot")
			return
		}
		saved, err := d.Store.GetTraitSnapshot(r.Context(), id)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, saved)
	}
}

type similarResponse struct {
	Query      string                 `json:"query"`
	Disclaimer string                 `json:"disclaimer"`
	Results    []domain.TraitSnapshot `json:"results"`
}

// getSimilarTraits — GET /api/v1/inference/similar?blob_id=...&limit=...:
// поиск по fingerprint среди подтверждённых trait_snapshots.
func getSimilarTraits(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		blobID := strings.TrimSpace(r.URL.Query().Get("blob_id"))
		if blobID == "" {
			writeError(w, http.StatusBadRequest, `query param "blob_id" required`)
			return
		}
		limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
		if err != nil || limit <= 0 {
			limit = 10
		}
		blob, err := d.Store.GetBlobByID(r.Context(), blobID)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		src, err := d.Store.FindSnapshotByBlobSHA(r.Context(), blob.SHA256)
		if err != nil {
			notFoundOrServerError(w, err, "trait snapshot for blob")
			return
		}
		similar, err := d.Store.ListSimilarTraitSnapshots(r.Context(), src.Fingerprint, src.ID, limit)
		if err != nil {
			serverError(w, err)
			return
		}
		if similar == nil {
			similar = []domain.TraitSnapshot{}
		}
		writeJSON(w, http.StatusOK, similarResponse{
			Query:      src.Fingerprint,
			Disclaimer: similarDisclaimer,
			Results:    similar,
		})
	}
}

// handleInferenceError — ошибки движка: модель недоступна → 503, приложение
// не блокируется (ADR-0012: человек вводит признаки сам); прочее → 400.
func handleInferenceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, inference.ErrModelUnavailable):
		writeError(w, http.StatusServiceUnavailable, "model unavailable: введите признаки вручную (ADR-0012)")
	case errors.Is(err, inference.ErrInvalidImage):
		writeError(w, http.StatusBadRequest, "invalid image")
	default:
		slog.Error("inference failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
