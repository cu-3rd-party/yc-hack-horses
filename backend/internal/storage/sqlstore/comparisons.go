package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
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
		_, err = s.exec(ctx, `INSERT INTO observation_photos (id,observation_id,sha256,name,annotation,model_version,embedding,capture_time)
			VALUES (?,?,?,?,?,?,?,NULLIF(?,''))`, p.ID, record.ID, p.SHA, p.Name, string(annotation), p.Version, string(embedding), p.CaptureTime)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) References(ctx context.Context, version string) ([]comparison.Reference, error) {
	rows, err := s.query(ctx, `SELECT h.id,h.metadata,p.id,p.embedding,p.sha256,p.capture_time FROM observation_photos p
		JOIN observations o ON o.id=p.observation_id JOIN horses h ON h.id=o.horse_id
		WHERE p.model_version=? AND h.identity_status <> 'merged' AND o.invalidated_at = 0`, version)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []comparison.Reference
	for rows.Next() {
		var r comparison.Reference
		var vector, metadata string
		var capture sql.NullString
		if err := rows.Scan(&r.HorseID, &metadata, &r.PhotoID, &vector, &r.SHA, &capture); err != nil {
			return nil, err
		}
		r.Version = version
		r.CaptureTime = capture.String
		r.NeedsCapture = !capture.Valid
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

func (s *Store) SetLegacyCapture(ctx context.Context, sha, capture string) error {
	_, err := s.exec(ctx, "UPDATE observation_photos SET capture_time=? WHERE sha256=? AND capture_time IS NULL", capture, sha)
	return err
}

func (s *Store) ObservationHistory(ctx context.Context, horseID string, annotations bool) ([]comparison.History, error) {
	out, _, _, err := s.ObservationHistoryPage(ctx, horseID, 0, "", 0, annotations)
	return out, err
}

// ObservationHistoryPage — история с пагинацией по (observed_at, id):
// keyMs/id — курсор последней отданной записи (0 — начало), limit <= 0 — без
// ограничения. Возвращает ключ курсора последней записи и hasMore.
func (s *Store) ObservationHistoryPage(ctx context.Context, horseID string, keyMs int64, id string, limit int, annotations bool) ([]comparison.History, int64, bool, error) {
	observations, lastKey, hasMore, err := s.ListObservationsPage(ctx, horseID, keyMs, id, limit)
	if err != nil {
		return nil, 0, false, err
	}
	photoGroups, err := s.historyPhotos(ctx, horseID, annotations)
	if err != nil {
		return nil, 0, false, err
	}
	// Резолв blob_id -> sha256 одним батч-запросом вместо GetBlobByID на
	// каждую не-sha ссылку (N+1 на наблюдение с фото).
	shaByID, err := s.blobSHAByIDs(ctx, observations)
	if err != nil {
		return nil, 0, false, err
	}
	out := make([]comparison.History, len(observations))
	for i, o := range observations {
		photos := photoGroups[o.ID]
		if len(photos) == 0 {
			photos = make([]comparison.Photo, 0, len(o.BlobIDs))
			for index, blob := range o.BlobIDs {
				sha := blob
				if len(blob) != 64 {
					resolved, ok := shaByID[strings.ToLower(blob)]
					if !ok {
						continue // блоба нет — фото из истории выпадает (как раньше при ErrNotFound)
					}
					sha = resolved
				}
				id := uuid.NewSHA1(uuid.NameSpaceOID, []byte(o.ID+"/"+strconv.Itoa(index))).String()
				photos = append(photos, comparison.Photo{ID: id, SHA: sha})
			}
		}
		out[i] = comparison.History{Observation: o, Photos: photos}
	}
	return out, lastKey, hasMore, nil
}

// blobSHAByIDs — один батч-SELECT по всем не-sha ссылкам (blobs.id) из
// наблюдений страницы. Чанки по 500 ссылок, чтобы не упираться в лимит
// плейсхолдеров; сравнение по LOWER — идентификаторы могли быть в любом регистре.
func (s *Store) blobSHAByIDs(ctx context.Context, observations []domain.Observation) (map[string]string, error) {
	var ids []string
	seen := map[string]struct{}{}
	for _, o := range observations {
		for _, blob := range o.BlobIDs {
			if len(blob) == 64 {
				continue
			}
			key := strings.ToLower(blob)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			ids = append(ids, blob)
		}
	}
	out := map[string]string{}
	for start := 0; start < len(ids); start += 500 {
		end := min(start+500, len(ids))
		chunk := ids[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("LOWER(?),", len(chunk)), ",")
		rows, err := s.query(ctx,
			`SELECT LOWER(id), LOWER(sha256) FROM blobs WHERE deleted_at = 0 AND LOWER(id) IN (`+placeholders+`)`,
			toLowerArgs(chunk)...)
		if err != nil {
			return nil, fmt.Errorf("sqlstore: blob sha by ids: %w", err)
		}
		for rows.Next() {
			var id, sha string
			if err := rows.Scan(&id, &sha); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sqlstore: blob sha by ids: %w", err)
			}
			out[id] = sha
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlstore: blob sha by ids: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// historyPhotos отдаёт фото наблюдений без фильтра по invalidated_at, поэтому
// в историю лошади попадают и снимки инвалидированных наблюдений (blob.deleted).
// Запись остаётся аудит-следом — что, когда и почему удалили, — поэтому строку
// истории не скрываем. Но само фото из истории уже не отдаётся: метаданные
// блоба погашены, GET /api/v1/blobs/<sha256> отвечает 404, и содержимое не
// достать. Файл на нодах, где он уже лежал, не прунится: BlobReferenced видит
// ссылку из blob_ids (или legacy sha256) и не даёт prune снести его с диска;
// в потребности blob-синка инвалидированные наблюдения не входят — мёртвые файлы
// пиры не качают.
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
