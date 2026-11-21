// Применение проекций к событиям, приехавшим по sync. Локальные события
// проектируются в HTTP-хендлерах (events + projection в одной транзакции),
// удалённые приходят через EventStore.Append движка — здесь тот же принцип:
// Append и проекция в одной транзакции, всё идемпотентно.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// projectedStore — адаптер storage.EventStore поверх sqlstore.Store,
// который при Append (события от пиров) строит проекции.
type projectedStore struct {
	st *sqlstore.Store
}

var _ storage.EventStore = (*projectedStore)(nil)

func newProjectedStore(st *sqlstore.Store) *projectedStore { return &projectedStore{st: st} }

func (p *projectedStore) Append(ctx context.Context, events []domain.Event) error {
	return p.st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		if err := tx.Append(ctx, events); err != nil {
			return fmt.Errorf("projected append: %w", err)
		}
		return applyProjections(ctx, tx, events)
	})
}

func (p *projectedStore) GetEvents(ctx context.Context, cursor storage.Cursor, limit int) ([]domain.Event, error) {
	return p.st.GetEvents(ctx, cursor, limit)
}

func (p *projectedStore) GetVectorClock(ctx context.Context) (storage.VectorClock, error) {
	return p.st.GetVectorClock(ctx)
}

func (p *projectedStore) GetMissingEvents(ctx context.Context, remote storage.VectorClock) ([]domain.Event, error) {
	return p.st.GetMissingEvents(ctx, remote)
}

func (p *projectedStore) LastSequence(ctx context.Context, nodeID string) (uint64, error) {
	return p.st.LastSequence(ctx, nodeID)
}

// applyProjections идемпотентно строит проекции для типов событий, у которых
// они есть. Неизвестные типы пропускаются (forward compatibility).
func applyProjections(ctx context.Context, tx *sqlstore.Store, events []domain.Event) error {
	for _, ev := range events {
		var err error
		switch ev.EventType {
		case domain.EventHorseCreated:
			err = projectHorseCreated(ctx, tx, ev)
		case domain.EventObservationAdded:
			err = projectObservationAdded(ctx, tx, ev)
		case domain.EventBlobRegistered:
			err = projectBlobRegistered(ctx, tx, ev)
		default:
			slog.Debug("no projection for event type", "event_type", ev.EventType, "event_id", ev.EventID)
		}
		if err != nil {
			return fmt.Errorf("projection %s %s: %w", ev.EventType, ev.EventID, err)
		}
	}
	return nil
}

// projectHorseCreated: лошадь уже есть (повторная доставка) — no-op.
func projectHorseCreated(ctx context.Context, tx *sqlstore.Store, ev domain.Event) error {
	var h domain.Horse
	if err := json.Unmarshal(ev.Payload, &h); err != nil {
		return fmt.Errorf("decode horse payload: %w", err)
	}
	if h.ID == "" {
		h.ID = ev.AggregateID
	}
	if _, err := tx.GetHorse(ctx, h.ID); err == nil {
		return nil
	}
	if err := tx.CreateHorse(ctx, h); err != nil {
		return fmt.Errorf("create horse: %w", err)
	}
	return nil
}

// projectObservationAdded: наблюдение уже есть — no-op.
func projectObservationAdded(ctx context.Context, tx *sqlstore.Store, ev domain.Event) error {
	var o comparison.ObservationEvent
	if err := json.Unmarshal(ev.Payload, &o); err != nil {
		return fmt.Errorf("decode observation payload: %w", err)
	}
	if o.ID == "" {
		o.ID = ev.AggregateID
	}
	if _, err := tx.GetObservation(ctx, o.ID); err == nil {
		return nil
	}
	if err := tx.CreateReviewedObservation(ctx, o); err != nil {
		return fmt.Errorf("create observation: %w", err)
	}
	return nil
}

// projectBlobRegistered: UpsertBlob сам дедуплицирует по sha256.
func projectBlobRegistered(ctx context.Context, tx *sqlstore.Store, ev domain.Event) error {
	var b domain.Blob
	if err := json.Unmarshal(ev.Payload, &b); err != nil {
		return fmt.Errorf("decode blob payload: %w", err)
	}
	if b.SHA256 == "" {
		b.SHA256 = ev.AggregateID
	}
	// Older senders included only SHA, so derive a stable, distinct ID for each original.
	if b.ID == "" {
		b.ID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("blob:"+b.SHA256)).String()
	}
	if _, err := tx.UpsertBlob(ctx, b); err != nil {
		return fmt.Errorf("upsert blob: %w", err)
	}
	return nil
}
