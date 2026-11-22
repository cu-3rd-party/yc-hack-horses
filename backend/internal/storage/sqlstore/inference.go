// inference.go: SQL-методы для CV-инференса (ADR-0012). Новый файл — аддитивное
// расширение sqlstore без правок существующих файлов пакета.
package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// GetBlobByID возвращает метаданные блоба по blobs.id ИЛИ по sha256
// (клиенты uploadBlob получают только sha256, поэтому допускаем оба).
// Сравнение id/sha256 — через LOWER с обеих сторон (единый подход пакета):
// sha256 мог быть сохранён в верхнем регистре, а idOrSHA приходит в любом.
func (s *Store) GetBlobByID(ctx context.Context, idOrSHA string) (domain.Blob, error) {
	var b domain.Blob
	var created int64
	err := s.queryRow(ctx,
		`SELECT id, sha256, mime_type, size, created_at FROM blobs WHERE (LOWER(id) = LOWER(?) OR LOWER(sha256) = LOWER(?)) AND deleted_at = 0 ORDER BY id LIMIT 1`,
		idOrSHA, idOrSHA,
	).Scan(&b.ID, &b.SHA256, &b.MimeType, &b.Size, &created)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.Blob{}, storage.ErrNotFound
		}
		return domain.Blob{}, fmt.Errorf("sqlstore: get blob %s: %w", idOrSHA, err)
	}
	return b, nil
}

// GetTraitSnapshot возвращает снимок признаков по id.
func (s *Store) GetTraitSnapshot(ctx context.Context, id string) (domain.TraitSnapshot, error) {
	return scanTraitSnapshot(s.queryRow(ctx,
		`SELECT id, observation_id, model_version, traits, raw_predicted_traits, confidence, fingerprint, confirmed, edited
		 FROM trait_snapshots WHERE id = ?`, id))
}

// ConfirmTraitSnapshot сохраняет подтверждённые человеком значения, НЕЗАТИРАЯ
// исходное предсказание: при первом подтверждении raw_predicted_traits получает
// текущее (модельное) значение traits, при повторных правках — сохраняется.
// traits перезаписываются отредактированными значениями, edited = 1.
func (s *Store) ConfirmTraitSnapshot(ctx context.Context, ts domain.TraitSnapshot) error {
	res, err := s.exec(ctx,
		`UPDATE trait_snapshots
		 SET traits = ?, confidence = ?, fingerprint = ?,
		     raw_predicted_traits = CASE WHEN confirmed = 0 THEN traits ELSE raw_predicted_traits END,
		     confirmed = 1, edited = 1
		 WHERE id = ?`,
		string(ts.Traits), ts.Confidence, ts.Fingerprint, ts.ID,
	)
	if err != nil {
		return fmt.Errorf("sqlstore: confirm trait snapshot %s: %w", ts.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return storage.ErrNotFound
	}
	return nil
}

// ListSimilarTraitSnapshots — поиск похожих снимков по fingerprint (sha256
// канонизированного набора). Это поиск ПОХОЖИХ, НЕ биологическая идентичность.
// excludeID исключает сам снимок из результата; limit <= 0 → 10.
func (s *Store) ListSimilarTraitSnapshots(ctx context.Context, fingerprint, excludeID string, limit int) ([]domain.TraitSnapshot, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	// Снимки без наблюдения остаются в поиске; инвалидированные — выпадают.
	rows, err := s.query(ctx,
		`SELECT ts.id, ts.observation_id, ts.model_version, ts.traits, ts.raw_predicted_traits, ts.confidence, ts.fingerprint, ts.confirmed, ts.edited
		 FROM trait_snapshots ts LEFT JOIN observations o ON o.id = ts.observation_id
		 WHERE ts.fingerprint = ? AND ts.id <> ? AND ts.confirmed = 1 AND COALESCE(o.invalidated_at, 0) = 0
		 ORDER BY ts.id
		 LIMIT ?`,
		fingerprint, excludeID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: similar snapshots %s: %w", fingerprint, err)
	}
	defer rows.Close()
	var out []domain.TraitSnapshot
	for rows.Next() {
		ts, err := scanTraitSnapshotRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}

// FindSnapshotByBlobSHA — снимок, привязанный к наблюдению с данным sha256
// блоба. observations.blob_ids — JSON-массив идентификаторов (blobs.id, UUIDv7)
// или legacy-хэшей; ссылка резолвится тем же blobRefPatterns, что и при удалении,
// поэтому находятся оба варианта. Сравнение — по LOWER(CAST(blob_ids AS TEXT)):
// сырой LIKE по JSONB в postgres падает (jsonb ~~ unknown), а регистрозависимость
// LIKE ломала бы поиск по верхнему регистру. Ссылочное условие обёрнуто в
// скобки: цепочка OR (шаблон sha256 + шаблоны blobs.id) как единое целое, иначе
// фильтр invalidated_at применялся бы только к последнему OR и снимки
// инвалидированных наблюдений протекали бы в поиск похожих.
func (s *Store) FindSnapshotByBlobSHA(ctx context.Context, sha string) (domain.TraitSnapshot, error) {
	patterns, err := s.blobRefPatterns(ctx, sha)
	if err != nil {
		return domain.TraitSnapshot{}, err
	}
	if len(patterns) == 0 {
		return domain.TraitSnapshot{}, storage.ErrNotFound
	}
	cond, args := likeAnyOf(blobIDsText, patterns)
	row := s.queryRow(ctx,
		`SELECT ts.id, ts.observation_id, ts.model_version, ts.traits, ts.raw_predicted_traits,
		        ts.confidence, ts.fingerprint, ts.confirmed, ts.edited
		 FROM trait_snapshots ts
		 JOIN observations o ON ts.observation_id = o.id
		 WHERE (`+cond+`) AND COALESCE(o.invalidated_at, 0) = 0
		 ORDER BY ts.id LIMIT 1`, args...)
	return scanTraitSnapshot(row)
}

func scanTraitSnapshot(row *sql.Row) (domain.TraitSnapshot, error) {
	ts, err := scanTraitSnapshotRow(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.TraitSnapshot{}, storage.ErrNotFound
		}
		return domain.TraitSnapshot{}, err
	}
	return ts, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanTraitSnapshotRow(r rowScanner) (domain.TraitSnapshot, error) {
	var ts domain.TraitSnapshot
	var traits, raw string
	var confirmed, edited bool
	if err := r.Scan(&ts.ID, &ts.ObservationID, &ts.ModelVersion, &traits, &raw,
		&ts.Confidence, &ts.Fingerprint, &confirmed, &edited); err != nil {
		return domain.TraitSnapshot{}, fmt.Errorf("sqlstore: scan trait snapshot: %w", err)
	}
	ts.Traits = json.RawMessage(traits)
	ts.RawPredictedTraits = json.RawMessage(raw)
	ts.Confirmed = confirmed
	ts.Edited = edited
	return ts, nil
}
