package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// handlers.go: REST-хендлеры horses/observations/blobs/node.
// Запись = событие в журнал + проекция, обе в одной транзакции (RunInTx).

// eventHorseUpdated — событие правки лошади; в domain перечислены базовые типы,
// этот тип согласован по схеме payload (identity_status + metadata).
const eventHorseUpdated = "horse.updated"

// appendWithProjection назначает sequence в транзакции, пишет событие и проекцию.
// Публикация в WS — только после успешного коммита.
func appendWithProjection(
	d Deps,
	ctx context.Context,
	eventType, aggregateID string,
	payload json.RawMessage,
	projection func(tx *sqlstore.Store) error,
) error {
	var published domain.Event
	err := d.Store.RunInTx(ctx, func(tx *sqlstore.Store) error {
		var err error
		published, err = appendLocalEvent(ctx, tx, d.NodeID, eventType, aggregateID, payload)
		if err != nil {
			return err
		}
		return projection(tx)
	})
	if err == nil {
		publishEvent(d, published)
	}
	return err
}

// appendLocalEvent runs in the caller's transaction. Publish only after commit.
func appendLocalEvent(ctx context.Context, tx *sqlstore.Store, nodeID, kind, id string, payload json.RawMessage) (domain.Event, error) {
	last, err := tx.LastSequence(ctx, nodeID)
	if err != nil {
		return domain.Event{}, err
	}
	ev := domain.Event{EventID: uuid.Must(uuid.NewV7()).String(), NodeID: nodeID, Sequence: last + 1, EventType: kind, SchemaVersion: 1, AggregateID: id, OccurredAt: time.Now().UTC(), Payload: payload}
	return ev, tx.Append(ctx, []domain.Event{ev})
}

// --- horses ---

type createHorseOption struct {
	Metadata map[string]string `json:"metadata"`
}

type editHorseOption struct {
	IdentityStatus *string            `json:"identity_status"`
	Metadata       *map[string]string `json:"metadata"`
}

func createHorse(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var form createHorseOption
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if form.Metadata == nil {
			form.Metadata = map[string]string{}
		}
		h := domain.Horse{
			ID:             uuid.Must(uuid.NewV7()).String(),
			CreatedAt:      time.Now().UTC(),
			IdentityStatus: "active",
			Metadata:       form.Metadata,
		}
		err := appendWithProjection(d, r.Context(), domain.EventHorseCreated, h.ID, mustJSON(h),
			func(tx *sqlstore.Store) error { return tx.CreateHorse(r.Context(), h) })
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Location", "/api/v1/horses/"+h.ID)
		writeJSON(w, http.StatusCreated, h)
	}
}

func listHorses(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		horses, err := d.Store.ListHorses(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		if horses == nil {
			horses = []domain.Horse{}
		}
		writeJSON(w, http.StatusOK, horses)
	}
}

func getHorse(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h, err := d.Store.GetHorse(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			notFoundOrServerError(w, err, "horse")
			return
		}
		writeJSON(w, http.StatusOK, h)
	}
}

func patchHorse(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var form editHorseOption
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		current, err := d.Store.GetHorse(r.Context(), id)
		if err != nil {
			notFoundOrServerError(w, err, "horse")
			return
		}
		status := current.IdentityStatus
		if form.IdentityStatus != nil {
			status = *form.IdentityStatus
		}
		err = appendWithProjection(d, r.Context(), eventHorseUpdated, id,
			mustJSON(map[string]any{"identity_status": status}),
			func(tx *sqlstore.Store) error {
				h, err := tx.UpdateHorse(r.Context(), id, &status, *form.Metadata)
				if err != nil {
					return err
				}
				current = h
				return nil
			})
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				writeError(w, http.StatusNotFound, "horse not found")
				return
			}
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, current)
	}
}

// --- observations ---

type createObservationOption struct {
	HorseID    string    `json:"horse_id"`
	ObservedAt time.Time `json:"observed_at"`
	Location   string    `json:"location"`
	ObserverID string    `json:"observer_id"`
	BlobIDs    []string  `json:"blob_ids"`
}

func createObservation(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var form createObservationOption
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if form.ObserverID == "" {
			writeError(w, http.StatusUnprocessableEntity, "observer_id required")
			return
		}
		if form.ObservedAt.IsZero() {
			form.ObservedAt = time.Now().UTC()
		}
		// указанная лошадь должна существовать
		if form.HorseID != "" {
			if _, err := d.Store.GetHorse(r.Context(), form.HorseID); err != nil {
				notFoundOrServerError(w, err, "horse")
				return
			}
		}
		if form.BlobIDs == nil {
			form.BlobIDs = []string{}
		}
		o := domain.Observation{
			ID:         uuid.Must(uuid.NewV7()).String(),
			HorseID:    form.HorseID,
			NodeID:     d.NodeID,
			ObservedAt: form.ObservedAt.UTC(),
			Location:   form.Location,
			ObserverID: form.ObserverID,
			BlobIDs:    form.BlobIDs,
		}
		err := appendWithProjection(d, r.Context(), domain.EventObservationAdded, o.ID, mustJSON(o),
			func(tx *sqlstore.Store) error { return tx.CreateObservation(r.Context(), o) })
		if err != nil {
			serverError(w, err)
			return
		}
		w.Header().Set("Location", "/api/v1/observations/"+o.ID)
		writeJSON(w, http.StatusCreated, o)
	}
}

func listObservations(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		horseID := r.URL.Query().Get("horse_id")
		include := strings.Split(r.URL.Query().Get("include"), ",")
		if slices.Contains(include, "photos") || slices.Contains(include, "annotations") {
			history, err := d.Store.ObservationHistory(r.Context(), horseID, slices.Contains(include, "annotations"))
			if err != nil {
				serverError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, history)
			return
		}
		observations, err := d.Store.ListObservations(r.Context(), horseID)
		if err != nil {
			serverError(w, err)
			return
		}
		if observations == nil {
			observations = []domain.Observation{}
		}
		writeJSON(w, http.StatusOK, observations)
	}
}

// --- blobs ---

const maxBlobSize = 50 << 20 // 50 MiB

func uploadBlob(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(maxBlobSize); err != nil {
			writeError(w, http.StatusBadRequest, "multipart form required")
			return
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, `field "file" required`)
			return
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, maxBlobSize+1))
		if err != nil {
			serverError(w, err)
			return
		}
		if len(data) > maxBlobSize {
			writeError(w, http.StatusRequestEntityTooLarge, "blob too large")
			return
		}
		sum := sha256Hex(data)
		mime := header.Header.Get("Content-Type")
		if err := d.Blobs.Put(r.Context(), sum, mime, data); err != nil {
			serverError(w, err)
			return
		}
		// метаданные + событие blob.registered только при первой регистрации
		blob := domain.Blob{ID: uuid.Must(uuid.NewV7()).String(), SHA256: sum, MimeType: mime, Size: int64(len(data))}
		var created bool
		if err := d.Store.RunInTx(r.Context(), func(tx *sqlstore.Store) error {
			var err error
			created, err = tx.UpsertBlob(r.Context(), blob)
			if err != nil || !created {
				return err
			}
			_, err = appendLocalEvent(r.Context(), tx, d.NodeID, domain.EventBlobRegistered, sum, mustJSON(blob))
			return err
		}); err != nil {
			serverError(w, err)
			return
		}
		status := http.StatusCreated
		if !created {
			status = http.StatusOK // дедупликация: блоб уже был
		}
		writeJSON(w, status, domain.Blob{SHA256: sum, MimeType: mime, Size: int64(len(data))})
	}
}

func listBlobs(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		blobs, err := d.Store.ListBlobs(r.Context())
		if err != nil {
			serverError(w, err)
			return
		}
		if blobs == nil {
			blobs = []domain.Blob{}
		}
		writeJSON(w, http.StatusOK, blobs)
	}
}

func getBlob(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sha := chi.URLParam(r, "sha256")
		meta, err := d.Store.GetBlobMeta(r.Context(), sha)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		data, err := d.Blobs.Get(r.Context(), sha)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		if meta.MimeType != "" {
			w.Header().Set("Content-Type", meta.MimeType)
		}
		w.Header().Set("ETag", `"`+meta.SHA256+`"`)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}
}

// --- node ---

func nodeInfo(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		info, err := d.Store.NodeSummary(r.Context(), d.NodeID)
		if err != nil {
			serverError(w, err)
			return
		}
		out := map[string]any{
			"node_id":      d.NodeID,
			"app_version":  d.AppVersion,
			"role_hint":    d.RoleHint,
			"event_count":  info.EventCount,
			"last_seq":     info.LastSeq,
			"vector_clock": info.VectorClock,
			"driver":       d.Store.Driver(),
		}
		if d.SyncStats != nil {
			out["sync"] = d.SyncStats()
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// --- helpers ---

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// структуры приложения маршалятся всегда; ошибка — программная,
		// для ожидаемых ошибок panic запрещён, здесь инвариант нарушен
		panic(fmt.Sprintf("marshal payload: %v", err))
	}
	return b
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// serverError логирует детали и отдаёт 500 без раскрытия внутренностей.
func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
