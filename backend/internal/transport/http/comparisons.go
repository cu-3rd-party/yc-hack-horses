package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var errDecisionConflict = errors.New("the detection already has a different decision or was skipped")
var errCandidate = errors.New("horse was not a candidate for this detection")

func createComparison(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		form := struct {
			SHA  string `json:"blob_sha256"`
			TopK int    `json:"top_k"`
		}{TopK: 5}
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			writeError(w, 400, "invalid JSON body")
			return
		}
		if _, err := hex.DecodeString(form.SHA); len(form.SHA) != 64 || err != nil || form.TopK < 1 {
			writeError(w, 422, "valid blob_sha256 and positive top_k required")
			return
		}
		ctx := r.Context()
		b, err := d.Store.GetBlobMeta(ctx, form.SHA)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		if d.ComparisonModel.URL == "" {
			writeError(w, 503, comparison.ErrUnavailable.Error())
			return
		}
		image, err := d.Blobs.Get(ctx, b.SHA256)
		if err != nil {
			notFoundOrServerError(w, err, "blob")
			return
		}
		found, err := d.ComparisonModel.Analyze(ctx, image, b.MimeType)
		if err != nil {
			if errors.Is(err, comparison.ErrUnavailable) {
				writeError(w, 503, comparison.ErrUnavailable.Error())
			} else {
				writeError(w, 502, "invalid segmentation model response")
			}
			return
		}
		review := comparison.Review{ID: uuid.Must(uuid.NewV7()).String(), SHA: b.SHA256}
		result := comparison.Result{ID: review.ID, SHA: review.SHA, Detections: make([]comparison.Detection, 0, len(found))}
		if len(found) > 0 {
			refs, err := d.Store.References(ctx, found[0].Version)
			if err != nil {
				serverError(w, err)
				return
			}
			for _, p := range found {
				det := comparison.Detection{ID: uuid.Must(uuid.NewV7()).String(), Confidence: p.Confidence, Annotation: p.Annotation, Candidates: comparison.Rank(p.Descriptor, refs, form.TopK)}
				result.Detections = append(result.Detections, det)
				review.Items = append(review.Items, comparison.Item{Detection: det, Descriptor: p.Descriptor})
			}
		}
		if err := d.Store.CreateComparison(ctx, review); err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, result)
	}
}

var reservedFilename = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])(\.|$)`)

func validDecision(v comparison.Decision) bool {
	if v.Name == "" || len(v.Name) > 255 || strings.ContainsAny(v.Name, `<>:"/\|?*`) || strings.IndexFunc(v.Name, unicode.IsControl) >= 0 || strings.HasSuffix(v.Name, ".") || reservedFilename.MatchString(v.Name) {
		return false
	}
	switch v.Action {
	case "match":
		return v.HorseID != "" && v.HorseName == ""
	case "create_horse":
		return v.HorseID == "" && v.HorseName != ""
	}
	return false
}

func decideComparison(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var input comparison.Decision
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, 400, "invalid JSON body")
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		input.HorseName = strings.TrimSpace(input.HorseName)
		if !validDecision(input) {
			writeError(w, 422, "invalid comparison decision or name")
			return
		}
		ctx := r.Context()
		var reply comparison.Saved
		var events []domain.Event
		err := d.Store.UpdateComparison(ctx, chi.URLParam(r, "id"), func(tx *sqlstore.Store, review *comparison.Review) error {
			for i := range review.Items {
				item := &review.Items[i]
				if item.ID != chi.URLParam(r, "detection") {
					continue
				}
				if item.Saved != nil {
					if *item.Decision != input {
						return errDecisionConflict
					}
					reply = *item.Saved
					// The observation is the same even if other detections were decided meanwhile.
					reply.Remaining = review.Remaining()
					return nil
				}
				if review.Cancelled {
					return errDecisionConflict
				}
				saved, published, err := saveDetection(ctx, d, tx, review.SHA, item, input)
				if err != nil {
					return err
				}
				item.Decision = &input
				item.Saved = &saved
				reply = saved
				reply.Remaining = review.Remaining()
				events = published
				return nil
			}
			return storage.ErrNotFound
		})
		if err != nil {
			switch {
			case errors.Is(err, errDecisionConflict):
				writeError(w, 409, err.Error())
			case errors.Is(err, errCandidate):
				writeError(w, 422, err.Error())
			default:
				notFoundOrServerError(w, err, "comparison, detection or horse")
			}
			return
		}
		for _, ev := range events {
			publishEvent(d, ev)
		}
		writeJSON(w, 200, reply)
	}
}

func saveDetection(ctx context.Context, d Deps, tx *sqlstore.Store, sha string, item *comparison.Item, v comparison.Decision) (comparison.Saved, []domain.Event, error) {
	var saved comparison.Saved
	var events []domain.Event
	now := d.Clock().UTC()
	horseID := v.HorseID
	if v.Action == "create_horse" {
		horseID = uuid.Must(uuid.NewV7()).String()
		h := domain.Horse{ID: horseID, CreatedAt: now, IdentityStatus: "active", Metadata: map[string]string{"name": v.HorseName}}
		if err := tx.CreateHorse(ctx, h); err != nil {
			return saved, nil, err
		}
		ev, err := appendLocalEvent(ctx, tx, d.NodeID, domain.EventHorseCreated, h.ID, mustJSON(h))
		if err != nil {
			return saved, nil, err
		}
		events = append(events, ev)
	} else {
		found := false
		for _, c := range item.Candidates {
			if c.HorseID == horseID {
				found = true
				break
			}
		}
		if !found {
			return saved, nil, errCandidate
		}
		if _, err := tx.GetHorse(ctx, horseID); err != nil {
			return saved, nil, err
		}
	}
	o := domain.Observation{ID: uuid.Must(uuid.NewV7()).String(), HorseID: horseID, NodeID: d.NodeID, ObserverID: d.NodeID, Name: v.Name, ObservedAt: now, BlobIDs: []string{sha}}
	p := comparison.ReferencePhoto{Photo: comparison.Photo{ID: item.ID, SHA: sha, Name: v.Name, Annotation: &item.Annotation}, Descriptor: item.Descriptor}
	record := comparison.ObservationEvent{Observation: o, Photos: []comparison.ReferencePhoto{p}}
	if err := tx.CreateReviewedObservation(ctx, record); err != nil {
		return saved, nil, err
	}
	ev, err := appendLocalEvent(ctx, tx, d.NodeID, domain.EventObservationAdded, o.ID, mustJSON(record))
	if err != nil {
		return saved, nil, err
	}
	return comparison.Saved{ObservationID: o.ID, HorseID: horseID}, append(events, ev), nil
}

func cancelComparison(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reply := comparison.Cancelled{Status: "cancelled", Saved: []string{}, Skipped: []string{}}
		err := d.Store.UpdateComparison(r.Context(), chi.URLParam(r, "id"), func(_ *sqlstore.Store, review *comparison.Review) error {
			review.Cancelled = true
			for _, item := range review.Items {
				if item.Saved != nil {
					reply.Saved = append(reply.Saved, item.Saved.ObservationID)
				} else {
					reply.Skipped = append(reply.Skipped, item.ID)
				}
			}
			return nil
		})
		if err != nil {
			notFoundOrServerError(w, err, "comparison")
			return
		}
		writeJSON(w, 200, reply)
	}
}
