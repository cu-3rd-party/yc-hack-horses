package sqlstore

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// ListActiveReferencedBlobHashes — sha256 блобов, на которые ссылаются АКТИВНЫЕ
// наблюдения (invalidated_at = 0), плюс sha256 PDF-паспортов лошадей из
// metadata.passport_sha256. Это список потребностей неполного blob-синка:
// инвалидированные наблюдения остаются в базе как история (их фото удалено
// событием blob.deleted), но качать их файлы пирам незачем — они выпали из
// сравнения, поиска и инференса. Хэш, на который ссылается хоть одно живое
// наблюдение, остаётся в списке.
//
// observations.blob_ids — это blobs.id (UUIDv7, так объявлено в OpenAPI), а не
// sha256; старые наблюдения хранили сам sha256. Поэтому ссылки приводятся к
// каноническому sha256 так же, как в ядре: обе стороны сравнения — в нижнем
// регистре (LOWER), как blobIDsText/blobIDPattern приводят к нему колонку и
// шаблон. Верхний регистр sha256/UUID (ValidSha256Hex допускает A-F) не ломает
// резолв ни в sqlite (LIKE там регистронезависим), ни в postgres (LIKE
// регистрозависим). Отдавать сырые blob_ids нельзя: движок отфильтровывает
// не-sha256 (ValidSha256Hex), и живые блобы, на которые наблюдения ссылаются по
// UUID, выпадали бы из потребностей синка.
//
// Выборка идёт одним проходом по observations: отдельный скан разворачивает
// blob_ids активных наблюдений во множество ссылок (refs, приведённых к нижнему
// регистру), которое затем резолвится в sha256 одним соединением с blobs по
// условию refs.ref IN (LOWER(id), LOWER(sha256)) — ссылка может быть как UUID
// блоба, так и legacy-хэшем. Так уходит O(|blobs|×|observations|) с ведущим
// % LIKE (индекс не используется): наблюдения сканируются один раз, а резолв
// идёт по равенству, дружественному индексам. DISTINCT LOWER(b.sha256) схлопывает
// дубли: если в blobs лежат строки одного sha256 в разном регистре (UNIQUE(sha256)
// регистрозависим), потребность остаётся одна. Tombstone-блобы (deleted_at <> 0) в
// потребности не попадают: их файлы удалены и пирам не нужны.
//
// Разворот JSON-массива отличается у драйверов (sqlite — json_each, postgres —
// jsonb_array_elements_text), поэтому запрос собирается по s.driver. Фильтр по
// типу массива отсекает скаляры/объекты (например, legacy-запись blob_ids='null')
// до вызова разворачивающей функции, а в sqlite json_valid-обёртка гасит битый
// JSON: он даёт 0 ссылок, а не ошибку запроса.
func (s *Store) ListActiveReferencedBlobHashes(ctx context.Context) ([]string, error) {
	var q string
	if s.driver == "postgres" {
		q = `
			WITH refs AS (
				SELECT DISTINCT LOWER(r.ref) AS ref
				FROM (
					SELECT blob_ids FROM observations
					WHERE invalidated_at = 0 AND jsonb_typeof(blob_ids) = 'array'
				) o, jsonb_array_elements_text(o.blob_ids) AS r(ref)
			)
			SELECT DISTINCT LOWER(b.sha256)
			FROM blobs b
			JOIN refs ON refs.ref IN (LOWER(b.id), LOWER(b.sha256))
			WHERE b.deleted_at = 0
			ORDER BY 1`
	} else {
		q = `
			WITH refs AS (
				SELECT DISTINCT LOWER(j.value) AS ref
				FROM (
					SELECT CASE WHEN json_valid(blob_ids) AND json_type(blob_ids) = 'array'
					            THEN CAST(blob_ids AS TEXT) END AS blob_ids
					FROM observations
					WHERE invalidated_at = 0
				) o, json_each(o.blob_ids) j
				WHERE j.type = 'text'
			)
			SELECT DISTINCT LOWER(b.sha256)
			FROM blobs b
			JOIN refs ON refs.ref IN (LOWER(b.id), LOWER(b.sha256))
			WHERE b.deleted_at = 0
			ORDER BY 1`
	}
	rows, err := s.query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list active referenced blob hashes: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("sqlstore: scan active referenced blob hash: %w", err)
		}
		out[sha] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: list active referenced blob hashes: %w", err)
	}
	// Закрываем выборку до следующих запросов: на sqlite открытый rows держит
	// соединение, и ListHorses/скан deleted-блобов за ним не пройдут.
	rows.Close()

	// PDF-паспорта лошадей живут не в наблюдениях, а в metadata.passport_sha256:
	// без них паспорт не доедет до пиров при неполном синке. Блоб уже sha256,
	// поэтому ссылку не резолвим, а только отбрасываем мягко удалённые.
	deleted, err := s.deletedBlobHashes(ctx)
	if err != nil {
		return nil, err
	}
	horses, err := s.ListHorses(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list horses for passport blobs: %w", err)
	}
	for _, h := range horses {
		sha := strings.ToLower(strings.TrimSpace(h.Metadata[domain.MetaPassportSHA256]))
		if !storage.ValidSha256Hex(sha) {
			continue
		}
		if _, dead := deleted[sha]; dead {
			continue
		}
		out[sha] = struct{}{}
	}

	res := make([]string, 0, len(out))
	for sha := range out {
		res = append(res, sha)
	}
	sort.Strings(res)
	return res, nil
}

// deletedBlobHashes — множество LOWER(sha256) мягко удалённых блобов: их файлы
// уже удалены, и в потребности неполного синка они не входят.
func (s *Store) deletedBlobHashes(ctx context.Context) (map[string]struct{}, error) {
	rows, err := s.query(ctx, `SELECT DISTINCT LOWER(sha256) FROM blobs WHERE deleted_at <> 0`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list deleted blob hashes: %w", err)
	}
	defer rows.Close()
	out := map[string]struct{}{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, fmt.Errorf("sqlstore: scan deleted blob hash: %w", err)
		}
		out[sha] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: list deleted blob hashes: %w", err)
	}
	return out, nil
}
