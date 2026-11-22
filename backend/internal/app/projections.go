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
	st    *sqlstore.Store
	blobs storage.BlobStore // может быть nil: тогда физические файлы не трогаем
}

var _ storage.EventStore = (*projectedStore)(nil)

func newProjectedStore(st *sqlstore.Store, blobs storage.BlobStore) *projectedStore {
	return &projectedStore{st: st, blobs: blobs}
}

func (p *projectedStore) Append(ctx context.Context, events []domain.Event) error {
	var prune []string
	err := p.st.RunInTx(ctx, func(tx *sqlstore.Store) error {
		if err := tx.Append(ctx, events); err != nil {
			return fmt.Errorf("projected append: %w", err)
		}
		var err error
		prune, err = applyProjections(ctx, tx, events)
		return err
	})
	if err != nil {
		return err
	}
	// Файлы удаляем после коммита: откат транзакции не вернёт данные на диск.
	storage.PruneBlobs(ctx, p.st, p.blobs, prune, slog.Default())
	return nil
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
// Возвращает sha256 блобов, чьи файлы можно попробовать удалить после коммита.
func applyProjections(ctx context.Context, tx *sqlstore.Store, events []domain.Event) ([]string, error) {
	var prune []string
	for _, ev := range events {
		var err error
		switch ev.EventType {
		case domain.EventHorseCreated:
			err = projectHorseCreated(ctx, tx, ev)
		case domain.EventObservationAdded:
			err = projectObservationAdded(ctx, tx, ev)
		case domain.EventBlobRegistered:
			err = projectBlobRegistered(ctx, tx, ev)
		case domain.EventBlobDeleted:
			var (
				sha string
				n   int64
			)
			sha, n, err = projectBlobDeleted(ctx, tx, ev)
			// Файл трогаем только если удаление реально применилось: устаревшее
			// событие (блоб перерегистрирован позже) не повод удалять с диска.
			if err == nil && n > 0 {
				prune = append(prune, sha)
			}
		default:
			slog.Debug("no projection for event type", "event_type", ev.EventType, "event_id", ev.EventID)
		}
		if err != nil {
			return nil, fmt.Errorf("projection %s %s: %w", ev.EventType, ev.EventID, err)
		}
	}
	return prune, nil
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

// projectObservationAdded: наблюдение уже есть — no-op (метку invalidated
// повторная доставка события не сбрасывает: удаление фото необратимо).
// Новое наблюдение, ссылающееся на уже удалённый блоб, сразу помечается неактуальным.
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
	refs := append([]string{}, o.BlobIDs...)
	for _, p := range o.Photos {
		refs = append(refs, p.SHA)
	}
	if _, err := tx.InvalidateObservationIfBlobsDeleted(ctx, o.ID, refs, "blob deleted"); err != nil {
		return fmt.Errorf("invalidate observation: %w", err)
	}
	return nil
}

// projectBlobRegistered: UpsertBlob сам дедуплицирует по sha256; после мягкого
// удаления ре-аплоад снова регистрирует блоб (deleted_at сбрасывается).
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
	if _, err := tx.UpsertBlobAt(ctx, b, ev.OccurredAt); err != nil {
		return fmt.Errorf("upsert blob: %w", err)
	}
	return nil
}

// projectBlobDeleted: мягко удаляет блоб и помечает наблюдения с этим фото
// неактуальными (их вектора выпадают из сравнения). Повторная доставка или
// удаление старше последней регистрации — no-op (n == 0), файл не трогаем.
// Возвращает sha256 и число изменённых строк блоба.
func projectBlobDeleted(ctx context.Context, tx *sqlstore.Store, ev domain.Event) (string, int64, error) {
	var p domain.BlobDeleted
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		return "", 0, fmt.Errorf("decode blob.deleted payload: %w", err)
	}
	if p.SHA256 == "" {
		p.SHA256 = ev.AggregateID
	}
	if !storage.ValidSha256Hex(p.SHA256) {
		return "", 0, fmt.Errorf("invalid sha256 %q", p.SHA256)
	}
	deletedAt := p.DeletedAt
	if deletedAt.IsZero() {
		deletedAt = ev.OccurredAt
	}
	n, err := tx.ProjectBlobDeleted(ctx, p.SHA256, deletedAt, p.Reason)
	if err != nil {
		return "", 0, fmt.Errorf("project blob.deleted: %w", err)
	}
	return p.SHA256, n, nil
}
