// Package comparison defines the data exchanged between the model adapter,
// the journal and the desktop review flow.
package comparison

import "github.com/cu3rd/yc-hack-horses/backend/internal/domain"

type (
	Point [2]float64
	Trait struct {
		Key     string  `json:"key"`
		Value   string  `json:"value"`
		Polygon []Point `json:"polygon"`
	}
)

// PartMask is the lossless indexed anatomical PNG inside its normalized frame box.
type PartMask struct {
	PNG []byte     `json:"png"`
	Box [4]float64 `json:"box"`
}
type Annotation struct {
	PartMask     *PartMask `json:"part_mask,omitempty"`
	HorsePolygon []Point   `json:"horse_polygon"`
	Traits       []Trait   `json:"traits"`
}
type Candidate struct {
	HorseID        string  `json:"horse_id"`
	Name           string  `json:"name"`
	Similarity     float64 `json:"cosine_similarity"`
	BestPhotoID    string  `json:"best_photo_id"`
	ReferenceCount int     `json:"reference_count"`
	SeasonFallback bool    `json:"season_fallback"`
}
type Detection struct {
	ID         string      `json:"detection_id"`
	Confidence float64     `json:"detection_confidence"`
	Annotation Annotation  `json:"annotation"`
	Candidates []Candidate `json:"candidates"`
}
type Result struct {
	ID         string      `json:"comparison_id"`
	SHA        string      `json:"blob_sha256"`
	Detections []Detection `json:"detections"`
}
type Decision struct {
	Comment   string `json:"comment,omitempty"`
	Action    string `json:"action"`
	HorseID   string `json:"horse_id,omitempty"`
	HorseName string `json:"horse_name,omitempty"`
	Name      string `json:"observation_name"`
}
type Saved struct {
	ObservationID string   `json:"observation_id"`
	HorseID       string   `json:"horse_id"`
	Remaining     []string `json:"remaining_detection_ids"`
}
type Cancelled struct {
	Status  string   `json:"status"`
	Saved   []string `json:"saved_observation_ids"`
	Skipped []string `json:"skipped_detection_ids"`
}
type Photo struct {
	ID         string      `json:"photo_id"`
	SHA        string      `json:"blob_sha256"`
	Name       string      `json:"name,omitempty"`
	Annotation *Annotation `json:"annotation"`
}
type History struct {
	domain.Observation
	Photos []Photo `json:"photos"`
}

// Descriptor is private to Go and replicated for confirmed reference photos.
type Descriptor struct {
	CaptureTime string    `json:"capture_time,omitempty"`
	Version     string    `json:"model_version"`
	Vector      []float64 `json:"embedding"`
}
type ReferencePhoto struct {
	Photo
	Descriptor
}
type ObservationEvent struct {
	domain.Observation
	Photos []ReferencePhoto `json:"photos,omitempty"`
}

// Review is a local aggregate. Locking the comparison row serializes decisions
// and cancellation even when different detections are being reviewed.
type Review struct {
	ID        string `json:"id"`
	SHA       string `json:"sha"`
	Cancelled bool   `json:"cancelled"`
	Items     []Item `json:"items"`
}
type Item struct {
	Detection
	Descriptor
	Decision *Decision `json:"decision,omitempty"`
	Saved    *Saved    `json:"saved,omitempty"`
}

func (r Review) Remaining() []string {
	ids := []string{}
	if !r.Cancelled {
		for _, item := range r.Items {
			if item.Saved == nil {
				ids = append(ids, item.ID)
			}
		}
	}
	return ids
}
