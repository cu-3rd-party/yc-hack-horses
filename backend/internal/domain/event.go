// Package domain: append-only event envelope и сущности. Файл-контракт —
// менять только через согласование с оркестратором.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Event — неизменяемый конверт события. Append-only: после записи не модифицируется.
type Event struct {
	EventID      string          `json:"event_id"        db:"event_id"`      // UUIDv7
	NodeID       string          `json:"node_id"         db:"node_id"`
	Sequence     uint64          `json:"sequence"        db:"sequence"`      // монотонный в пределах node_id
	EventType    string          `json:"event_type"      db:"event_type"`    // см. ниже
	SchemaVersion int            `json:"schema_version"  db:"schema_version"`
	AggregateID  string          `json:"aggregate_id"    db:"aggregate_id"`
	OccurredAt   time.Time       `json:"occurred_at"     db:"occurred_at"`
	Payload      json.RawMessage `json:"payload"         db:"payload"` // только доменные данные, без конфига
	ModelVersion string          `json:"model_version,omitempty" db:"model_version"`
}

// PayloadHash — sha256 от канонического JSON payload.
func (e Event) PayloadHash() string {
	h := sha256.Sum256(canonicalJSON(e.Payload))
	return hex.EncodeToString(h[:])
}

func canonicalJSON(b json.RawMessage) []byte {
	if b == nil {
		return []byte("null")
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return []byte(b)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return []byte(b)
	}
	return out
}

// Типы событий (event_type).
const (
	EventHorseCreated     = "horse.created"
	EventObservationAdded = "observation.added"
	EventTraitSnapshotSet = "trait.snapshot_set"
	EventIdentityProposed = "identity.proposed"
	EventIdentityResolved = "identity.resolved"
	EventBlobRegistered   = "blob.registered"
)

// Horse — животное. Стабильный UUID, не производный от признаков.
type Horse struct {
	ID             string            `json:"id"`
	CreatedAt      time.Time         `json:"created_at"`
	IdentityStatus string            `json:"identity_status"` // active | merged | unresolved
	Metadata       map[string]string `json:"metadata"`
}

// Observation — наблюдение. HorseID пуст, если животное ещё не идентифицировано.
type Observation struct {
	ID          string    `json:"id"`
	HorseID     string    `json:"horse_id,omitempty"`
	NodeID      string    `json:"node_id"`
	ObservedAt  time.Time `json:"observed_at"`
	Location    string    `json:"location"` // текст/latlon — точный формат уточнит Agent Domain
	ObserverID  string    `json:"observer_id"`
	BlobIDs     []string  `json:"blob_ids"`
	TraitSnapID string    `json:"trait_snapshot_id,omitempty"`
}

// TraitSnapshot — признаки из одного фото. Fingerprint — для поиска похожих, НЕ биологическое ID.
type TraitSnapshot struct {
	ID           string          `json:"id"`
	ObservationID string         `json:"observation_id"`
	ModelVersion string          `json:"model_version"`
	Traits       json.RawMessage `json:"traits"`
	Confidence   float64         `json:"confidence"`
	Fingerprint  string          `json:"fingerprint"` // sha256 канонизированных признаков
	Confirmed    bool            `json:"confirmed"`   // human-in-the-loop: человек подтвердил
	Edited        bool           `json:"edited"`      // человек редактировал исходное предсказание
}

// Blob — ссылка на файл. Фотографии не едут в payload событий.
type Blob struct {
	ID       string `json:"id"`
	SHA256   string `json:"sha256"`
	MimeType string `json:"mime_type"`
	Size     int64  `json:"size"`
}

var ErrInvalidEvent = errors.New("invalid event")

// Validate — инварианты конверта.
func (e Event) Validate() error {
	if e.EventID == "" || e.NodeID == "" || e.EventType == "" || e.AggregateID == "" {
		return fmt.Errorf("%w: empty required field", ErrInvalidEvent)
	}
	if e.Sequence == 0 {
		return fmt.Errorf("%w: sequence must be >= 1", ErrInvalidEvent)
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("%w: occurred_at required", ErrInvalidEvent)
	}
	if len(e.Payload) == 0 {
		return fmt.Errorf("%w: empty payload", ErrInvalidEvent)
	}
	return nil
}
