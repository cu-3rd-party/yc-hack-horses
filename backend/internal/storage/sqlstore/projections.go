package sqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// Проекции horses/observations/blobs. Пишутся в той же транзакции, что и события,
// через RunInTx; чтения идут напрямую с таблиц.

// CreateHorse добавляет проекцию лошади.
func (s *Store) CreateHorse(ctx context.Context, h domain.Horse) error {
	metadata, err := json.Marshal(h.Metadata)
	if err != nil {
		return fmt.Errorf("sqlstore: create horse: metadata: %w", err)
	}
	if _, err := s.exec(ctx,
		`INSERT INTO horses (id, created_at, identity_status, metadata) VALUES (?, ?, ?, ?)`,
		h.ID, h.CreatedAt.UnixMilli(), h.IdentityStatus, string(metadata),
	); err != nil {
		return fmt.Errorf("sqlstore: create horse %s: %w", h.ID, err)
	}
	return nil
}

// GetHorse возвращает лошадь или storage.ErrNotFound.
func (s *Store) GetHorse(ctx context.Context, id string) (domain.Horse, error) {
	var (
		h         domain.Horse
		createdAt int64
		metadata  string
	)
	err := s.queryRow(ctx,
		`SELECT id, created_at, identity_status, metadata FROM horses WHERE id = ?`, id,
	).Scan(&h.ID, &createdAt, &h.IdentityStatus, &metadata)
	if err != nil {
		return domain.Horse{}, notFound("horse", err)
	}
	h.CreatedAt = timeFromUnixMilli(createdAt)
	if err := json.Unmarshal([]byte(metadata), &h.Metadata); err != nil {
		return domain.Horse{}, fmt.Errorf("sqlstore: horse %s metadata: %w", id, err)
	}
	if h.Metadata == nil {
		h.Metadata = map[string]string{}
	}
	return h, nil
}

// ListHorses возвращает всех лошадей по возрастанию времени создания.
func (s *Store) ListHorses(ctx context.Context) ([]domain.Horse, error) {
	rows, err := s.query(ctx, `SELECT id, created_at, identity_status, metadata FROM horses ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list horses: %w", err)
	}
	defer rows.Close()
	var out []domain.Horse
	for rows.Next() {
		var (
			h         domain.Horse
			createdAt int64
			metadata  string
		)
		if err := rows.Scan(&h.ID, &createdAt, &h.IdentityStatus, &metadata); err != nil {
			return nil, fmt.Errorf("sqlstore: scan horse: %w", err)
		}
		h.CreatedAt = timeFromUnixMilli(createdAt)
		h.Metadata = map[string]string{}
		_ = json.Unmarshal([]byte(metadata), &h.Metadata)
		out = append(out, h)
	}
	return out, rows.Err()
}

// UpdateHorse меняет статус идентификации и метаданные (nil = не менять) и возвращает новую версию.
func (s *Store) UpdateHorse(ctx context.Context, id string, status *string, metadata map[string]string) (domain.Horse, error) {
	h, err := s.GetHorse(ctx, id)
	if err != nil {
		return domain.Horse{}, err
	}
	if status != nil {
		h.IdentityStatus = *status
	}
	if metadata != nil {
		merged := map[string]string{}
		for k, v := range h.Metadata {
			merged[k] = v
		}
		for k, v := range metadata {
			merged[k] = v
		}
		h.Metadata = merged
	}
	metadataJSON, err := json.Marshal(h.Metadata)
	if err != nil {
		return domain.Horse{}, fmt.Errorf("sqlstore: update horse %s: metadata: %w", id, err)
	}
	if _, err := s.exec(ctx,
		`UPDATE horses SET identity_status = ?, metadata = ? WHERE id = ?`,
		h.IdentityStatus, string(metadataJSON), id,
	); err != nil {
		return domain.Horse{}, fmt.Errorf("sqlstore: update horse %s: %w", id, err)
	}
	return h, nil
}

// CreateObservation добавляет проекцию наблюдения.
func (s *Store) CreateObservation(ctx context.Context, o domain.Observation) error {
	blobIDs, err := json.Marshal(o.BlobIDs)
	if err != nil {
		return fmt.Errorf("sqlstore: create observation: blob_ids: %w", err)
	}
	if _, err := s.exec(ctx,
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.HorseID, o.NodeID, o.ObservedAt.UnixMilli(), o.Location, o.ObserverID, string(blobIDs), o.TraitSnapID, o.Name,
	); err != nil {
		return fmt.Errorf("sqlstore: create observation %s: %w", o.ID, err)
	}
	return nil
}

// GetObservation возвращает наблюдение или storage.ErrNotFound.
func (s *Store) GetObservation(ctx context.Context, id string) (domain.Observation, error) {
	var (
		o          domain.Observation
		observedAt int64
		blobIDs    string
	)
	err := s.queryRow(ctx,
		`SELECT id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name
		 FROM observations WHERE id = ?`, id,
	).Scan(&o.ID, &o.HorseID, &o.NodeID, &observedAt, &o.Location, &o.ObserverID, &blobIDs, &o.TraitSnapID, &o.Name)
	if err != nil {
		return domain.Observation{}, notFound("observation", err)
	}
	o.ObservedAt = timeFromUnixMilli(observedAt)
	if err := json.Unmarshal([]byte(blobIDs), &o.BlobIDs); err != nil {
		return domain.Observation{}, fmt.Errorf("sqlstore: observation %s blob_ids: %w", id, err)
	}
	return o, nil
}

// ListObservations возвращает наблюдения, при horseID != "" — отфильтрованные по лошади.
func (s *Store) ListObservations(ctx context.Context, horseID string) ([]domain.Observation, error) {
	return s.listObservations(ctx, horseID, true)
}
func (s *Store) listObservations(ctx context.Context, horseID string, limited bool) ([]domain.Observation, error) {
	q := `SELECT id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name FROM observations`
	var args []any
	if horseID != "" {
		q += ` WHERE horse_id = ?`
		args = append(args, horseID)
	}
	q += ` ORDER BY observed_at, id`
	if limited {
		q += " LIMIT 1000"
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list observations: %w", err)
	}
	defer rows.Close()
	var out []domain.Observation
	for rows.Next() {
		var (
			o          domain.Observation
			observedAt int64
			blobIDs    string
		)
		if err := rows.Scan(&o.ID, &o.HorseID, &o.NodeID, &observedAt, &o.Location, &o.ObserverID, &blobIDs, &o.TraitSnapID, &o.Name); err != nil {
			return nil, fmt.Errorf("sqlstore: scan observation: %w", err)
		}
		o.ObservedAt = timeFromUnixMilli(observedAt)
		o.BlobIDs = []string{}
		_ = json.Unmarshal([]byte(blobIDs), &o.BlobIDs)
		out = append(out, o)
	}
	return out, rows.Err()
}

// CreateTraitSnapshot добавляет снимок признаков.
func (s *Store) CreateTraitSnapshot(ctx context.Context, ts domain.TraitSnapshot) error {
	if _, err := s.exec(ctx,
		`INSERT INTO trait_snapshots (id, observation_id, model_version, traits, confidence, fingerprint, confirmed, edited)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		ts.ID, ts.ObservationID, ts.ModelVersion, string(ts.Traits), ts.Confidence, ts.Fingerprint, ts.Confirmed, ts.Edited,
	); err != nil {
		return fmt.Errorf("sqlstore: create trait snapshot %s: %w", ts.ID, err)
	}
	return nil
}

// UpsertBlob регистрирует метаданные блоба; возвращает true, если запись создана сейчас.
func (s *Store) UpsertBlob(ctx context.Context, b domain.Blob) (bool, error) {
	res, err := s.exec(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (sha256) DO NOTHING`,
		b.ID, b.SHA256, b.MimeType, b.Size, timeNow().UnixMilli(),
	)
	if err != nil {
		return false, fmt.Errorf("sqlstore: upsert blob %s: %w", b.SHA256, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("sqlstore: upsert blob %s: rows affected: %w", b.SHA256, err)
	}
	return n > 0, nil
}

// ListBlobs возвращает зарегистрированные блобы.
func (s *Store) ListBlobs(ctx context.Context) ([]domain.Blob, error) {
	rows, err := s.query(ctx, `SELECT id, sha256, mime_type, size FROM blobs ORDER BY created_at, sha256`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list blobs: %w", err)
	}
	defer rows.Close()
	var out []domain.Blob
	for rows.Next() {
		var b domain.Blob
		if err := rows.Scan(&b.ID, &b.SHA256, &b.MimeType, &b.Size); err != nil {
			return nil, fmt.Errorf("sqlstore: scan blob: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBlobMeta возвращает метаданные блоба по sha256.
func (s *Store) GetBlobMeta(ctx context.Context, sha string) (domain.Blob, error) {
	var b domain.Blob
	err := s.queryRow(ctx, `SELECT id, sha256, mime_type, size FROM blobs WHERE sha256 = ?`, sha).
		Scan(&b.ID, &b.SHA256, &b.MimeType, &b.Size)
	if err != nil {
		return domain.Blob{}, notFound("blob", err)
	}
	return b, nil
}

// NodeInfo — сводка для /api/v1/node.
type NodeInfo struct {
	EventCount  int64
	LastSeq     uint64
	VectorClock storage.VectorClock
}

// NodeSummary собирает сводку одним проходом.
func (s *Store) NodeSummary(ctx context.Context, nodeID string) (NodeInfo, error) {
	var info NodeInfo
	var err error
	if info.EventCount, err = s.eventCount(ctx); err != nil {
		return info, err
	}
	if info.LastSeq, err = s.LastSequence(ctx, nodeID); err != nil {
		return info, err
	}
	if info.VectorClock, err = s.GetVectorClock(ctx); err != nil {
		return info, err
	}
	return info, nil
}

func timeNow() time.Time { return time.Now().UTC() }

// eventCount возвращает число событий в журнале.
func (s *Store) eventCount(ctx context.Context) (int64, error) {
	row := s.queryRow(ctx, `SELECT COUNT(*) FROM events`)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlstore: event count: %w", err)
	}
	return n, nil
}
