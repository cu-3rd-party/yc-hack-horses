package sqlstore

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/google/uuid"
)

func (s *Store) CreateComparison(ctx context.Context, r comparison.Review) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.exec(ctx, "INSERT INTO comparisons (id,state) VALUES (?,?)", r.ID, string(data))
	return err
}

// UpdateComparison serializes the read/modify/write and all callback writes.
func (s *Store) UpdateComparison(ctx context.Context, id string, update func(*Store, *comparison.Review) error) error {
	return s.RunInTx(ctx, func(tx *Store) error {
		query := "SELECT state FROM comparisons WHERE id = ?"
		if tx.driver == "postgres" {
			query += " FOR UPDATE"
		}
		var data string
		if err := tx.queryRow(ctx, query, id).Scan(&data); err != nil {
			return notFound("comparison", err)
		}
		var review comparison.Review
		if err := json.Unmarshal([]byte(data), &review); err != nil {
			return err
		}
		if err := update(tx, &review); err != nil {
			return err
		}
		changed, err := json.Marshal(review)
		if err != nil {
			return err
		}
		_, err = tx.exec(ctx, "UPDATE comparisons SET state = ? WHERE id = ?", string(changed), id)
		return err
	})
}

// CreateReviewedObservation is used for both a local decision and a replicated event.
func (s *Store) CreateReviewedObservation(ctx context.Context, record comparison.ObservationEvent) error {
	if err := s.CreateObservation(ctx, record.Observation); err != nil {
		return err
	}
	for _, p := range record.Photos {
		annotation, err := json.Marshal(p.Annotation)
		if err != nil {
			return err
		}
		embedding, err := json.Marshal(p.Vector)
		if err != nil {
			return err
		}
		_, err = s.exec(ctx, `INSERT INTO observation_photos (id,observation_id,sha256,name,annotation,model_version,embedding)
			VALUES (?,?,?,?,?,?,?)`, p.ID, record.ID, p.SHA, p.Name, string(annotation), p.Version, string(embedding))
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) References(ctx context.Context, version string) ([]comparison.Reference, error) {
	rows, err := s.query(ctx, `SELECT h.id,h.metadata,p.id,p.embedding FROM observation_photos p
		JOIN observations o ON o.id=p.observation_id JOIN horses h ON h.id=o.horse_id
		WHERE p.model_version=? AND h.identity_status <> 'merged'`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []comparison.Reference
	for rows.Next() {
		var r comparison.Reference
		var vector, metadata string
		if err := rows.Scan(&r.HorseID, &metadata, &r.PhotoID, &vector); err != nil {
			return nil, err
		}
		r.Version = version
		if err := json.Unmarshal([]byte(vector), &r.Vector); err != nil {
			return nil, err
		}
		var horse map[string]string
		if err := json.Unmarshal([]byte(metadata), &horse); err != nil {
			return nil, err
		}
		r.Name = horse["name"]
		if r.Name == "" {
			r.Name = horse["nickname"]
		}
		if r.Name == "" {
			r.Name = r.HorseID
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) ObservationHistory(ctx context.Context, horseID string, annotations bool) ([]comparison.History, error) {
	observations, err := s.listObservations(ctx, horseID, false)
	if err != nil {
		return nil, err
	}
	photoGroups, err := s.historyPhotos(ctx, horseID, annotations)
	if err != nil {
		return nil, err
	}
	out := make([]comparison.History, len(observations))
	for i, o := range observations {
		photos := photoGroups[o.ID]
		if len(photos) == 0 {
			photos = make([]comparison.Photo, 0, len(o.BlobIDs))
			for index, blob := range o.BlobIDs {
				sha := blob
				if len(blob) != 64 {
					meta, err := s.GetBlobByID(ctx, blob)
					if errors.Is(err, storage.ErrNotFound) {
						continue
					}
					if err != nil {
						return nil, err
					}
					sha = meta.SHA256
				}
				id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(o.ID+"/"+strconv.Itoa(index))).String()
				photos = append(photos, comparison.Photo{ID: id, SHA: sha})
			}
		}
		out[i] = comparison.History{Observation: o, Photos: photos}
	}
	return out, nil
}

func (s *Store) historyPhotos(ctx context.Context, horseID string, annotations bool) (map[string][]comparison.Photo, error) {
	geometry := "'null'"
	if annotations {
		geometry = "p.annotation"
	}
	query := "SELECT p.observation_id,p.id,p.sha256,p.name," + geometry + " FROM observation_photos p"
	var args []any
	if horseID != "" {
		query += " JOIN observations o ON o.id=p.observation_id WHERE o.horse_id=?"
		args = append(args, horseID)
	}
	rows, err := s.query(ctx, query+" ORDER BY p.observation_id,p.id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string][]comparison.Photo)
	for rows.Next() {
		var p comparison.Photo
		var observationID, geom string
		if err := rows.Scan(&observationID, &p.ID, &p.SHA, &p.Name, &geom); err != nil {
			return nil, err
		}
		if annotations {
			if err := json.Unmarshal([]byte(geom), &p.Annotation); err != nil {
				return nil, err
			}
		}
		out[observationID] = append(out[observationID], p)
	}
	return out, rows.Err()
}
