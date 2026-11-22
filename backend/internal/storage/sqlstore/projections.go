package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
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
	return s.UpdateHorseFrom(ctx, h, status, metadata)
}

// UpdateHorseFrom — как UpdateHorse, но запись уже прочитана вызывающим:
// повторный SELECT внутри транзакции не нужен (см. patchHorse / putHorsePassport).
func (s *Store) UpdateHorseFrom(ctx context.Context, current domain.Horse, status *string, metadata map[string]string) (domain.Horse, error) {
	id := current.ID
	h := current
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
		`INSERT INTO observations (id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name, comment)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.HorseID, o.NodeID, o.ObservedAt.UnixMilli(), o.Location, o.ObserverID, string(blobIDs), o.TraitSnapID, o.Name, o.Comment,
	); err != nil {
		return fmt.Errorf("sqlstore: create observation %s: %w", o.ID, err)
	}
	return nil
}

// GetObservation возвращает наблюдение или storage.ErrNotFound.
func (s *Store) GetObservation(ctx context.Context, id string) (domain.Observation, error) {
	var (
		o             domain.Observation
		observedAt    int64
		blobIDs       string
		invalidatedAt int64
	)
	err := s.queryRow(ctx,
		`SELECT id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name, comment,
		        invalidated_at, invalidation_reason
		 FROM observations WHERE id = ?`, id,
	).Scan(&o.ID, &o.HorseID, &o.NodeID, &observedAt, &o.Location, &o.ObserverID, &blobIDs, &o.TraitSnapID, &o.Name, &o.Comment,
		&invalidatedAt, &o.InvalidationReason)
	if err != nil {
		return domain.Observation{}, notFound("observation", err)
	}
	o.ObservedAt = timeFromUnixMilli(observedAt)
	if err := json.Unmarshal([]byte(blobIDs), &o.BlobIDs); err != nil {
		return domain.Observation{}, fmt.Errorf("sqlstore: observation %s blob_ids: %w", id, err)
	}
	setInvalidation(&o, invalidatedAt)
	return o, nil
}

// setInvalidation переносит unix-ms метку инвалидации в доменную модель.
func setInvalidation(o *domain.Observation, invalidatedAt int64) {
	if invalidatedAt == 0 {
		o.Invalidated, o.InvalidatedAt = false, nil
		return
	}
	at := timeFromUnixMilli(invalidatedAt)
	o.Invalidated, o.InvalidatedAt = true, &at
}

// ListObservations возвращает наблюдения, при horseID != "" — отфильтрованные по лошади.
func (s *Store) ListObservations(ctx context.Context, horseID string) ([]domain.Observation, error) {
	return s.listObservations(ctx, horseID, true)
}

func (s *Store) listObservations(ctx context.Context, horseID string, limited bool) ([]domain.Observation, error) {
	q := `SELECT id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name, comment,
	             invalidated_at, invalidation_reason FROM observations`
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
			o             domain.Observation
			observedAt    int64
			blobIDs       string
			invalidatedAt int64
		)
		if err := rows.Scan(&o.ID, &o.HorseID, &o.NodeID, &observedAt, &o.Location, &o.ObserverID, &blobIDs, &o.TraitSnapID, &o.Name, &o.Comment,
			&invalidatedAt, &o.InvalidationReason); err != nil {
			return nil, fmt.Errorf("sqlstore: scan observation: %w", err)
		}
		o.ObservedAt = timeFromUnixMilli(observedAt)
		o.BlobIDs = []string{}
		_ = json.Unmarshal([]byte(blobIDs), &o.BlobIDs)
		setInvalidation(&o, invalidatedAt)
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- пагинация списков ---
// Курсорная пагинация по ключу сортировки (unix-ms, id): /horses — created_at,
// /observations — observed_at, /blobs — created_at. Все методы забирают limit+1
// строку, чтобы вызывающий узнал о продолжении без лишнего запроса.
// Курсор непрозрачный для клиента (см. encodeListCursor в helpers.go).

// ListHorsesPage — страница лошадей после (keyMs, id) в порядке (created_at, id).
// limit <= 0 — без ограничения (полный список, совместимо с ListHorses).
// lastKeyMs — ключ сортировки последней отданной записи (для next-cursor).
func (s *Store) ListHorsesPage(ctx context.Context, keyMs int64, id string, limit int) ([]domain.Horse, int64, bool, error) {
	q := `SELECT id, created_at, identity_status, metadata FROM horses`
	cond, args := appendCursorCond("created_at", "id", keyMs, id)
	if cond != "" {
		q += " WHERE"
	}
	q += cond + ` ORDER BY created_at, id`
	if limit > 0 {
		// limit+1 строка — как в ListObservationsPage/ListBlobsPage: по ней
		// узнаём о продолжении, лишний запрос не нужен.
		q += fmt.Sprintf(" LIMIT %d", limit+1)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list horses: %w", err)
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
			return nil, 0, false, fmt.Errorf("sqlstore: scan horse: %w", err)
		}
		h.CreatedAt = timeFromUnixMilli(createdAt)
		h.Metadata = map[string]string{}
		_ = json.Unmarshal([]byte(metadata), &h.Metadata)
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list horses: %w", err)
	}
	if limit > 0 && len(out) > limit {
		lastKey := out[limit-1].CreatedAt.UnixMilli()
		return out[:limit], lastKey, true, nil
	}
	if limit > 0 && len(out) > 0 {
		return out, out[len(out)-1].CreatedAt.UnixMilli(), false, nil
	}
	return out, 0, false, nil
}

// ListObservationsPage — страница наблюдений после (keyMs, id) в порядке
// (observed_at, id); horseID != "" фильтрует по лошади. limit <= 0 — без лимита.
func (s *Store) ListObservationsPage(ctx context.Context, horseID string, keyMs int64, id string, limit int) ([]domain.Observation, int64, bool, error) {
	q := `SELECT id, horse_id, node_id, observed_at, location, observer_id, blob_ids, trait_snapshot_id, name, comment,
	             invalidated_at, invalidation_reason
	     FROM observations`
	var args []any
	conds := []string{}
	if horseID != "" {
		conds = append(conds, "horse_id = ?")
		args = append(args, horseID)
	}
	cond, cursorArgs := appendCursorCond("observed_at", "id", keyMs, id)
	if cond != "" {
		conds = append(conds, strings.TrimPrefix(cond, " "))
		args = append(args, cursorArgs...)
	}
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += ` ORDER BY observed_at, id`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit+1)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list observations: %w", err)
	}
	defer rows.Close()
	var out []domain.Observation
	for rows.Next() {
		var (
			o             domain.Observation
			observedAt    int64
			blobIDs       string
			invalidatedAt int64
		)
		if err := rows.Scan(&o.ID, &o.HorseID, &o.NodeID, &observedAt, &o.Location, &o.ObserverID, &blobIDs, &o.TraitSnapID, &o.Name, &o.Comment,
			&invalidatedAt, &o.InvalidationReason); err != nil {
			return nil, 0, false, fmt.Errorf("sqlstore: scan observation: %w", err)
		}
		o.ObservedAt = timeFromUnixMilli(observedAt)
		o.BlobIDs = []string{}
		_ = json.Unmarshal([]byte(blobIDs), &o.BlobIDs)
		setInvalidation(&o, invalidatedAt)
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list observations: %w", err)
	}
	if limit > 0 && len(out) > limit {
		lastKey := out[limit-1].ObservedAt.UnixMilli()
		return out[:limit], lastKey, true, nil
	}
	if limit > 0 && len(out) > 0 {
		return out, out[len(out)-1].ObservedAt.UnixMilli(), false, nil
	}
	return out, 0, false, nil
}

// ListBlobsPage — страница блобов после (keyMs, id) в порядке (created_at, sha256).
// limit <= 0 — без лимита.
func (s *Store) ListBlobsPage(ctx context.Context, keyMs int64, id string, limit int) ([]domain.Blob, int64, bool, error) {
	q := `SELECT id, sha256, mime_type, size, created_at FROM blobs WHERE deleted_at = 0`
	cond, args := appendCursorCond("created_at", "sha256", keyMs, id)
	if cond != "" {
		q += " AND"
	}
	q += cond + ` ORDER BY created_at, sha256`
	if limit > 0 {
		q += fmt.Sprintf(" LIMIT %d", limit+1)
	}
	rows, err := s.query(ctx, q, args...)
	if err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list blobs: %w", err)
	}
	defer rows.Close()
	var out []domain.Blob
	var keys []int64
	for rows.Next() {
		var b domain.Blob
		var createdAt int64
		if err := rows.Scan(&b.ID, &b.SHA256, &b.MimeType, &b.Size, &createdAt); err != nil {
			return nil, 0, false, fmt.Errorf("sqlstore: scan blob: %w", err)
		}
		out = append(out, b)
		keys = append(keys, createdAt)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, fmt.Errorf("sqlstore: list blobs: %w", err)
	}
	// Ключ курсора — created_at последней отданной строки.
	if limit > 0 && len(out) > limit {
		return out[:limit], keys[limit-1], true, nil
	}
	if limit > 0 && len(out) > 0 {
		return out, keys[len(out)-1], false, nil
	}
	return out, 0, false, nil
}

// ListReferencedBlobHashes возвращает уникальные blob_ids из всех наблюдений и
// sha256 PDF-паспортов лошадей — датасет, на который ссылается эта нода. Список может быть больше, чем блобов
// на диске: часть файлов ещё не доехала (неполный синк), поэтому его нельзя
// использовать как источник для отправки, только как список потребностей.
func (s *Store) ListReferencedBlobHashes(ctx context.Context) ([]string, error) {
	rows, err := s.query(ctx, `SELECT blob_ids FROM observations WHERE blob_ids != '[]'`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list referenced blob hashes: %w", err)
	}
	defer rows.Close()
	seen := map[string]struct{}{}
	var out []string
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("sqlstore: scan observation blob_ids: %w", err)
		}
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("sqlstore: parse observation blob_ids: %w", err)
		}
		for _, id := range ids {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: list referenced blob hashes: %w", err)
	}
	// PDF-паспорта лошадей тоже должны доехать до пиров вместе с фото.
	horses, err := s.ListHorses(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: list referenced blob hashes: %w", err)
	}
	for _, h := range horses {
		if id := h.Metadata[domain.MetaPassportSHA256]; id != "" {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	sort.Strings(out)
	return out, nil
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

// UpsertBlob регистрирует метаданные блоба, считая временем регистрации «сейчас».
// Локальный аплоад идёт через этот метод; события пиров — через UpsertBlobAt.
// Возвращает true, если блоб зарегистрирован сейчас: первая регистрация или
// ре-аплоад после мягкого удаления (deleted_at сбрасывается).
func (s *Store) UpsertBlob(ctx context.Context, b domain.Blob) (bool, error) {
	return s.UpsertBlobAt(ctx, b, timeNow())
}

// UpsertBlobAt — как UpsertBlob, но время регистрации берётся из события:
// на пире регистрация могла случиться раньше, чем событие до нас доехало.
// registered_at обновляется только при фактической вставке или оживлении строки:
// повторная доставка blob.registered не должна «омолаживать» блоб и блокировать
// более позднее удаление.
func (s *Store) UpsertBlobAt(ctx context.Context, b domain.Blob, registeredAt time.Time) (bool, error) {
	if registeredAt.IsZero() {
		registeredAt = timeNow()
	}
	res, err := s.exec(ctx,
		`INSERT INTO blobs (id, sha256, mime_type, size, created_at, registered_at) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (sha256) DO UPDATE SET deleted_at = 0, delete_reason = '',
		     mime_type = excluded.mime_type, size = excluded.size,
		     registered_at = excluded.registered_at, pruned_at = 0
		 WHERE blobs.deleted_at != 0`,
		b.ID, b.SHA256, b.MimeType, b.Size, timeNow().UnixMilli(), registeredAt.UnixMilli(),
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

// BlobDeleteClockSkew — допуск на расхождение часов между узлами при проверке
// guard'а registered_at <= deletedAt. Часы отправителя удаления могут отставать
// от часов регистратора: легитимное удаление свежего блоба штампуется временем
// чуть меньше registered_at, и без допуска оно молча становилось бы no-op.
// Сравниваем с запасом: registered_at <= deletedAt + skew.
const BlobDeleteClockSkew = 5 * time.Minute

// MarkBlobDeleted мягко удаляет блоб: строка остаётся ради ссылок наблюдений,
// API перестаёт его отдавать. Повторная доставка события — no-op (0 строк).
// Удаление, которое старше последней регистрации блоба (с допуском
// BlobDeleteClockSkew на расхождение часов узлов), тоже no-op: так ретрай
// sync не убивает блоб, перезалитый после удаления (re-upload -> revive).
// Сравнение sha256 — через LOWER с обеих сторон (единый подход пакета):
// sha256 мог быть сохранён в верхнем регистре, а удаление приходит в любом.
func (s *Store) MarkBlobDeleted(ctx context.Context, sha string, deletedAt time.Time, reason string) (int64, error) {
	res, err := s.exec(ctx,
		`UPDATE blobs SET deleted_at = ?, delete_reason = ?
		 WHERE LOWER(sha256) = LOWER(?) AND deleted_at = 0 AND registered_at <= ?`,
		deletedAt.UnixMilli(), reason, sha, deletedAt.Add(BlobDeleteClockSkew).UnixMilli(),
	)
	if err != nil {
		return 0, fmt.Errorf("sqlstore: mark blob %s deleted: %w", sha, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlstore: mark blob %s deleted: rows affected: %w", sha, err)
	}
	return n, nil
}

// InvalidateObservationsByBlob помечает наблюдения, ссылающиеся на блоб, как
// неактуальные: observations.blob_ids (JSON-массив) и observation_photos.sha256.
// Запись и её вектора остаются в базе (append-only), но выпадают из сравнения.
// Повторный вызов — no-op для уже помеченных строк.
func (s *Store) InvalidateObservationsByBlob(ctx context.Context, sha string, invalidatedAt time.Time, reason string) (int64, error) {
	ids, err := s.observationIDsByBlob(ctx, sha)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := make([]any, 0, len(ids)+3)
	args = append(args, invalidatedAt.UnixMilli(), reason)
	for _, id := range ids {
		args = append(args, id)
	}
	res, err := s.exec(ctx,
		`UPDATE observations SET invalidated_at = ?, invalidation_reason = ?
		 WHERE invalidated_at = 0 AND id IN (`+placeholders+`)`, args...)
	if err != nil {
		return 0, fmt.Errorf("sqlstore: invalidate observations for blob %s: %w", sha, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlstore: invalidate observations for blob %s: rows affected: %w", sha, err)
	}
	return n, nil
}

// observationIDsByBlob собирает id наблюдений, ссылающихся на блоб. Записи
// observations.blob_ids — это blobs.id (UUIDv7, так объявлено в OpenAPI), но
// старые наблюдения хранили сам sha256, поэтому каждая запись резолвится через
// таблицу blobs (id -> sha256), плюс остаётся прямая ссылка из observation_photos.
// Скан ограничен одной колонкой и точным элементом JSON-массива (LIKE), а не
// разбором всех наблюдений в Go.
func (s *Store) observationIDsByBlob(ctx context.Context, sha string) ([]string, error) {
	patterns, err := s.blobRefPatterns(ctx, sha)
	if err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	var ids []string
	if len(patterns) > 0 {
		cond, args := likeAnyOf(blobIDsText, patterns)
		rows, err := s.query(ctx,
			`SELECT id FROM observations WHERE invalidated_at = 0 AND (`+cond+`)`, args...)
		if err != nil {
			return nil, fmt.Errorf("sqlstore: observations by blob %s: %w", sha, err)
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sqlstore: scan observation id: %w", err)
			}
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				ids = append(ids, id)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlstore: observations by blob %s: %w", sha, err)
		}
		// sqlite работает на одном соединении: следующий запрос только после Close.
		rows.Close()
	}

	photoRows, err := s.query(ctx, `SELECT DISTINCT observation_id FROM observation_photos WHERE LOWER(sha256) = LOWER(?)`, sha)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: photo observations by blob %s: %w", sha, err)
	}
	defer photoRows.Close()
	for photoRows.Next() {
		var id string
		if err := photoRows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlstore: scan photo observation: %w", err)
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	if err := photoRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: photo observations by blob %s: %w", sha, err)
	}
	return ids, nil
}

// blobRefPatterns — LIKE-шаблоны ссылки на блоб в observations.blob_ids:
// сам sha256 (legacy-записи) и все blobs.id с этим sha256 (OpenAPI: blob_ids —
// это blobs.id, то есть UUIDv7).
func (s *Store) blobRefPatterns(ctx context.Context, sha string) ([]string, error) {
	var patterns []string
	seen := map[string]struct{}{}
	add := func(id string) {
		p, ok := blobIDPattern(id)
		if !ok {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		patterns = append(patterns, p)
	}
	add(sha)
	// LOWER с обеих сторон: sha256 мог быть сохранён в верхнем регистре (валиден
	// по ValidSha256Hex), а удаление/проверка ссылок приходят в любом регистре.
	rows, err := s.query(ctx, `SELECT id FROM blobs WHERE LOWER(sha256) = LOWER(?)`, sha)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: blob ids for %s: %w", sha, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("sqlstore: scan blob id: %w", err)
		}
		add(id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: blob ids for %s: %w", sha, err)
	}
	return patterns, nil
}

// blobIDPattern — LIKE-шаблон точного элемента JSON-массива: %"<id>"%.
// Значение с посторонними символами отбрасывается: подставлять в LIKE шаблонные
// % и _ нельзя, а реальные идентификаторы — только hex и дефис. Идентификатор
// приводится к нижнему регистру: сравнение идёт по LOWER(blob_ids) (см.
// blobIDsText), поэтому верхний регистр sha256/UUID не ломает поиск ни в
// sqlite, ни в postgres.
func blobIDPattern(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F', r == '-':
		default:
			return "", false
		}
	}
	return `%"` + strings.ToLower(id) + `"%`, true
}

// blobIDsText — колонка blob_ids как текст, приведённая к нижнему регистру:
// в sqlite это TEXT, в postgres JSONB (LIKE по JSONB не работает без CAST).
// LIKE в postgres регистрозависим, а sha256/UUID в blob_ids могут быть в верхнем
// регистре, поэтому LOWER применяется и к колонке, и к шаблонам (blobIDPattern) —
// единый подход для обоих драйверов. Оговорка: LIKE с ведущим % индекса не
// использует, это full scan по observations; вызовы редки (удаление/проверка
// ссылок), поэтому цена приемлема.
const blobIDsText = "LOWER(CAST(blob_ids AS TEXT))"

// likeAnyOf собирает условие "col LIKE ? OR col LIKE ?" из шаблонов.
func likeAnyOf(col string, patterns []string) (string, []any) {
	conds := make([]string, 0, len(patterns))
	args := make([]any, 0, len(patterns))
	for _, p := range patterns {
		conds = append(conds, col+" LIKE ?")
		args = append(args, p)
	}
	return strings.Join(conds, " OR "), args
}

// ProjectBlobDeleted — проекция blob.deleted: мягко удаляет блоб и помечает
// наблюдения с этим фото неактуальными. Обе записи в одной транзакции вызывающего.
// Возвращает число реально изменённых строк блоба: 0 — событие повторное или
// устаревшее (блоб перерегистрирован позже), тогда наблюдения не трогаем, а
// вызывающий не должен пытаться удалять файл.
func (s *Store) ProjectBlobDeleted(ctx context.Context, sha string, deletedAt time.Time, reason string) (int64, error) {
	n, err := s.MarkBlobDeleted(ctx, sha, deletedAt, reason)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		slog.Debug("blob.deleted skipped: blob re-registered later or already deleted", "sha256", sha)
		return 0, nil
	}
	if _, err := s.InvalidateObservationsByBlob(ctx, sha, deletedAt, InvalidationReason(sha, reason)); err != nil {
		return 0, err
	}
	return n, nil
}

// InvalidationReason собирает причину инвалидации: метка, sha256 удалённого
// блоба и уточнение вызывающего, если оно не совпадает с меткой по умолчанию.
// Экспортируется, чтобы все пути (HTTP deleteBlob/markBlobDeletedTx и проекция
// blob.deleted) писали в observations.invalidation_reason одинаковый текст.
func InvalidationReason(sha, reason string) string {
	msg := "blob deleted: " + sha
	if r := strings.TrimSpace(reason); r != "" && r != "blob deleted" {
		msg += " (" + r + ")"
	}
	return msg
}

// BlobDeleted сообщает, помечен ли блоб (по sha256 или id) как удалённый, и когда.
func (s *Store) BlobDeleted(ctx context.Context, idOrSHA string) (time.Time, bool, error) {
	_, deletedAt, deleted, err := s.blobDeletion(ctx, idOrSHA)
	return deletedAt, deleted, err
}

// blobDeletion резолвит ссылку (blobs.id или sha256) в канонический sha256 и
// возвращает метку удаления: по id из blob_ids нужно знать, какой это файл.
// Сравнение id/sha256 — через LOWER с обеих сторон (единый подход пакета):
// и id, и sha256 могли быть записаны в верхнем регистре (sha256 — валидно).
func (s *Store) blobDeletion(ctx context.Context, idOrSHA string) (string, time.Time, bool, error) {
	var (
		sha       string
		deletedAt int64
	)
	err := s.queryRow(ctx, `SELECT sha256, deleted_at FROM blobs WHERE LOWER(id) = LOWER(?) OR LOWER(sha256) = LOWER(?) ORDER BY deleted_at DESC LIMIT 1`,
		idOrSHA, idOrSHA).Scan(&sha, &deletedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", time.Time{}, false, nil
		}
		return "", time.Time{}, false, fmt.Errorf("sqlstore: blob %s deleted_at: %w", idOrSHA, err)
	}
	if deletedAt == 0 {
		return sha, time.Time{}, false, nil
	}
	return sha, timeFromUnixMilli(deletedAt), true, nil
}

// InvalidateObservation помечает одно наблюдение неактуальным; уже помеченное — no-op.
func (s *Store) InvalidateObservation(ctx context.Context, id string, invalidatedAt time.Time, reason string) (int64, error) {
	res, err := s.exec(ctx,
		`UPDATE observations SET invalidated_at = ?, invalidation_reason = ? WHERE id = ? AND invalidated_at = 0`,
		invalidatedAt.UnixMilli(), reason, id,
	)
	if err != nil {
		return 0, fmt.Errorf("sqlstore: invalidate observation %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sqlstore: invalidate observation %s: rows affected: %w", id, err)
	}
	return n, nil
}

// blobDeletionsFor — батч-проверка ссылок наблюдения: один SELECT возвращает
// все мягко удалённые блобы, на которые ссылаются refs (по blobs.id или legacy
// sha256). Результат — lower(ссылка) -> (канонический sha256, метка удаления).
func (s *Store) blobDeletionsFor(ctx context.Context, refs []string) (map[string]blobDeletionInfo, error) {
	out := map[string]blobDeletionInfo{}
	for start := 0; start < len(refs); start += 500 {
		end := min(start+500, len(refs))
		chunk := refs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("LOWER(?),", len(chunk)), ",")
		rows, err := s.query(ctx,
			`SELECT LOWER(id), LOWER(sha256), deleted_at FROM blobs
			 WHERE deleted_at != 0 AND (LOWER(id) IN (`+placeholders+`) OR LOWER(sha256) IN (`+placeholders+`))`,
			append(toLowerArgs(chunk), toLowerArgs(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("sqlstore: blob deletions: %w", err)
		}
		for rows.Next() {
			var (
				id, sha   string
				deletedAt int64
			)
			if err := rows.Scan(&id, &sha, &deletedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sqlstore: blob deletions: %w", err)
			}
			// Одна ссылка может совпасть и по id, и по sha256: берём позднейшую метку.
			for _, key := range []string{id, sha} {
				if prev, ok := out[key]; ok && prev.deletedAtMs >= deletedAt {
					continue
				}
				out[key] = blobDeletionInfo{sha256: sha, deletedAtMs: deletedAt}
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlstore: blob deletions: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

type blobDeletionInfo struct {
	sha256      string
	deletedAtMs int64
}

// CreateObservationChecked — вставка наблюдения и проверка его ссылок на блобы
// одной транзакцией вызывающего: один батч-SELECT вместо запроса на каждую
// ссылку. Возвращает финальную проекцию наблюдения (с флагами инвалидации),
// чтобы HTTP-хендлеру не нужно было перечитывать строку после коммита.
func (s *Store) CreateObservationChecked(ctx context.Context, o domain.Observation, reason string) (domain.Observation, error) {
	if err := s.CreateObservation(ctx, o); err != nil {
		return o, err
	}
	if len(o.BlobIDs) == 0 {
		return o, nil
	}
	deletions, err := s.blobDeletionsFor(ctx, o.BlobIDs)
	if err != nil {
		return o, err
	}
	for _, ref := range o.BlobIDs {
		d, ok := deletions[strings.ToLower(ref)]
		if !ok {
			continue
		}
		// В причине — канонический sha256: по ссылке blobs.id не видно, какое фото выпало.
		sha := d.sha256
		if sha == "" {
			sha = ref
		}
		deletedAt := timeFromUnixMilli(d.deletedAtMs)
		if _, err := s.InvalidateObservation(ctx, o.ID, deletedAt, InvalidationReason(sha, reason)); err != nil {
			return o, err
		}
		o.Invalidated, o.InvalidatedAt, o.InvalidationReason = true, &deletedAt, InvalidationReason(sha, reason)
		return o, nil
	}
	return o, nil
}

// InvalidateObservationIfBlobsDeleted помечает наблюдение неактуальным, если
// хоть одна ссылка (blob id или sha256) уже мягко удалена. Нужна, когда
// наблюдение создаётся после удаления фото: события в журнале уже разошлись.
func (s *Store) InvalidateObservationIfBlobsDeleted(ctx context.Context, observationID string, refs []string, reason string) (bool, error) {
	if len(refs) == 0 {
		return false, nil
	}
	deletions, err := s.blobDeletionsFor(ctx, refs)
	if err != nil {
		return false, err
	}
	for _, ref := range refs {
		d, ok := deletions[strings.ToLower(ref)]
		if !ok {
			continue
		}
		// В причине — канонический sha256: по ссылке blobs.id не видно, какое фото выпало.
		sha := d.sha256
		if sha == "" {
			sha = ref
		}
		if _, err := s.InvalidateObservation(ctx, observationID, timeFromUnixMilli(d.deletedAtMs), InvalidationReason(sha, reason)); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

// BlobReferenced сообщает, ссылается ли на блоб хоть одно наблюдение:
// observations.blob_ids (запись — blobs.id или legacy sha256) или
// observation_photos.sha256, либо это PDF-паспорт лошади
// (metadata.passport_sha256). По этим ссылкам файл нельзя удалять физически.
//
// Стоимость: проверка выполняется отдельным COUNT-запросом с LIKE-условием по
// CAST(blob_ids AS TEXT) плюс COUNT по observation_photos — на больших таблицах
// это полный скан, индекса на JSON-массив нет. Это осознанный компромисс:
// правка ужесточена ядром (loop-1), а более дешёвая LIKE/EXISTS-версия (или
// отдельная таблица ссылок blob->observation с индексом) придёт с ядром и
// интеграционным слоем — здесь метод не переписываем, только фиксируем цену
// и что каждый вызов из PruneBlobs стоит этих сканов (их два на блоб).
// Проверка паспорта — точечный COUNT по metadata LIKE, без чтения всех лошадей.
// Для групповой проверки рипером есть BlobReferencedBatch ниже.
func (s *Store) BlobReferenced(ctx context.Context, sha string) (bool, error) {
	patterns, err := s.blobRefPatterns(ctx, sha)
	if err != nil {
		return false, err
	}
	if len(patterns) > 0 {
		cond, args := likeAnyOf(blobIDsText, patterns)
		var n int64
		if err := s.queryRow(ctx,
			`SELECT COUNT(*) FROM (SELECT 1 FROM observations WHERE `+cond+` LIMIT 1) refs`, args...).Scan(&n); err != nil {
			return false, fmt.Errorf("sqlstore: blob %s references: %w", sha, err)
		}
		if n > 0 {
			return true, nil
		}
	}
	var n int64
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM observation_photos WHERE LOWER(sha256) = LOWER(?)`, sha).Scan(&n); err != nil {
		return false, fmt.Errorf("sqlstore: blob %s photo references: %w", sha, err)
	}
	if n > 0 {
		return true, nil
	}
	// Файл прикреплённого PDF-паспорта не удаляем с диска. Точечный EXISTS по
	// metadata LIKE вместо полного ListHorses: паспортов ищем по точному
	// значению в JSON, полный список лошадей читать не нужно. Два шаблона —
	// sqlite-текст без пробела и postgres-jsonb с пробелом после двоеточия.
	passportConds, passportArgs := passportMetadataConds([]string{sha})
	var passportCount int64
	if err := s.queryRow(ctx, `SELECT COUNT(*) FROM horses WHERE `+passportConds, passportArgs...).Scan(&passportCount); err != nil {
		return false, fmt.Errorf("sqlstore: blob %s passport references: %w", sha, err)
	}
	return passportCount > 0, nil
}

// passportMetadataConds — условия «в metadata есть ключ passport_sha256 с данным
// значением» для одного или нескольких sha256. Первый вариант — json.Marshal
// (sqlite, TEXT, без пробела после двоеточия), второй — вывод jsonb::text
// (postgres, пробел после двоеточия). Регистронезависимо: sha256 в metadata мог
// быть записан в верхнем регистре.
func passportMetadataConds(shas []string) (string, []any) {
	var conds []string
	var args []any
	for _, sha := range shas {
		for _, sp := range []string{"", " "} {
			conds = append(conds, "LOWER(CAST(metadata AS TEXT)) LIKE ?")
			args = append(args, `%"passport_sha256":`+sp+`"`+strings.ToLower(sha)+`"%`)
		}
	}
	return strings.Join(conds, " OR "), args
}

// BlobReferencedBatch — пакетная версия BlobReferenced для рипера: на группу
// блобов три запроса вместо трёх сканов на каждый блоб.
//  1. observations: один проход по blob_ids, разбор JSON и поиск в множестве Go
//     (LIKE не умеет атрибутировать совпадение конкретному sha, поэтому
//     сравнение идёт по точному элементу массива после разбора).
//  2. observation_photos: DISTINCT по LOWER(sha256) IN (...).
//  3. horses: один LIKE-запрос по metadata для всех sha сразу.
func (s *Store) BlobReferencedBatch(ctx context.Context, shas []string) (map[string]bool, error) {
	out := make(map[string]bool, len(shas))
	if len(shas) == 0 {
		return out, nil
	}
	for _, sha := range shas {
		out[strings.ToLower(sha)] = false
	}
	// id блобов для каждого sha (blob_ids ссылается на blobs.id или legacy
	// sha256). Без фильтра deleted_at: блоб с наблюдениями мог быть мягко
	// удалён, ссылка из blob_ids при этом остаётся (см. historyPhotos).
	idRows, err := s.query(ctx,
		`SELECT id, sha256 FROM blobs WHERE LOWER(sha256) IN (`+strings.TrimSuffix(strings.Repeat("LOWER(?),", len(shas)), ",")+`)`,
		toLowerArgs(shas)...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}
	// lower(id) -> sha256, в который группируем ссылки.
	byID := map[string]string{}
	shaSet := map[string]struct{}{}
	for _, sha := range shas {
		shaSet[strings.ToLower(sha)] = struct{}{}
	}
	defer idRows.Close()
	for idRows.Next() {
		var id, sha string
		if err := idRows.Scan(&id, &sha); err != nil {
			return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
		}
		if _, ok := shaSet[strings.ToLower(sha)]; ok {
			byID[strings.ToLower(id)] = strings.ToLower(sha)
		}
	}
	if err := idRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}

	// 1) observations: полный проход по blob_ids один раз на группу.
	obsRows, err := s.query(ctx, `SELECT blob_ids FROM observations WHERE blob_ids != '[]'`)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}
	defer obsRows.Close()
	for obsRows.Next() {
		var raw string
		if err := obsRows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
		}
		var ids []string
		if err := json.Unmarshal([]byte(raw), &ids); err != nil {
			return nil, fmt.Errorf("sqlstore: blob references batch: parse blob_ids: %w", err)
		}
		for _, id := range ids {
			if sha, ok := byID[strings.ToLower(id)]; ok {
				out[sha] = true
			}
			// legacy: в массиве записан сам sha256
			if _, ok := shaSet[strings.ToLower(id)]; ok {
				out[strings.ToLower(id)] = true
			}
		}
	}
	if err := obsRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}

	// 2) observation_photos — одним запросом на группу.
	photos, err := s.query(ctx,
		`SELECT DISTINCT LOWER(sha256) FROM observation_photos WHERE LOWER(sha256) IN (`+strings.TrimSuffix(strings.Repeat("LOWER(?),", len(shas)), ",")+`)`,
		toLowerArgs(shas)...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}
	defer photos.Close()
	for photos.Next() {
		var sha string
		if err := photos.Scan(&sha); err != nil {
			return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
		}
		out[sha] = true
	}
	if err := photos.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}

	// 3) паспорта лошадей — один LIKE-запрос на группу.
	conds, args := passportMetadataConds(shas)
	horseRows, err := s.query(ctx, `SELECT metadata FROM horses WHERE `+conds, args...)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}
	defer horseRows.Close()
	for horseRows.Next() {
		var metadata string
		if err := horseRows.Scan(&metadata); err != nil {
			return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
		}
		var m map[string]string
		if err := json.Unmarshal([]byte(metadata), &m); err != nil {
			continue
		}
		if _, ok := shaSet[strings.ToLower(m[domain.MetaPassportSHA256])]; ok {
			out[strings.ToLower(m[domain.MetaPassportSHA256])] = true
		}
	}
	if err := horseRows.Err(); err != nil {
		return nil, fmt.Errorf("sqlstore: blob references batch: %w", err)
	}
	return out, nil
}

// toLowerArgs приводит аргументы к нижнему регистру для IN-сравнений с LOWER.
func toLowerArgs(shas []string) []any {
	args := make([]any, len(shas))
	for i, sha := range shas {
		args[i] = strings.ToLower(sha)
	}
	return args
}

// BlobsDeleted — пакетная проверка «ссылка помечена мягко удалённой» для рипера:
// один SELECT на группу вместо полного скана blobs на каждую ссылку.
// Регистр сравнения — LOWER с обеих сторон (единый подход пакета).
func (s *Store) BlobsDeleted(ctx context.Context, shas []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, sha := range shas {
		out[strings.ToLower(sha)] = false
	}
	for start := 0; start < len(shas); start += 500 {
		end := min(start+500, len(shas))
		chunk := shas[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("LOWER(?),", len(chunk)), ",")
		rows, err := s.query(ctx,
			`SELECT LOWER(id), LOWER(sha256), deleted_at FROM blobs
			 WHERE LOWER(id) IN (`+placeholders+`) OR LOWER(sha256) IN (`+placeholders+`)`,
			append(toLowerArgs(chunk), toLowerArgs(chunk)...)...)
		if err != nil {
			return nil, fmt.Errorf("sqlstore: blobs deleted: %w", err)
		}
		for rows.Next() {
			var (
				id, sha   string
				deletedAt int64
			)
			if err := rows.Scan(&id, &sha, &deletedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("sqlstore: blobs deleted: %w", err)
			}
			deleted := deletedAt != 0
			for _, key := range []string{id, sha} {
				if prev, ok := out[key]; ok && prev && !deleted {
					continue
				}
				out[key] = deleted
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sqlstore: blobs deleted: %w", err)
		}
		rows.Close()
	}
	return out, nil
}

// ListBlobs возвращает зарегистрированные блобы, кроме мягко удалённых.
func (s *Store) ListBlobs(ctx context.Context) ([]domain.Blob, error) {
	rows, err := s.query(ctx, `SELECT id, sha256, mime_type, size FROM blobs WHERE deleted_at = 0 ORDER BY created_at, sha256`)
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

// GetBlobMeta возвращает метаданные блоба по sha256; мягко удалённый — storage.ErrNotFound.
// Сравнение sha256 — через LOWER с обеих сторон (единый подход пакета):
// sha256 мог быть сохранён в верхнем регистре, а запрос приходит в любом.
func (s *Store) GetBlobMeta(ctx context.Context, sha string) (domain.Blob, error) {
	var b domain.Blob
	err := s.queryRow(ctx, `SELECT id, sha256, mime_type, size FROM blobs WHERE LOWER(sha256) = LOWER(?) AND deleted_at = 0`, sha).
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
	var before uint64
	if s.summary != nil {
		if n, ok := s.summary.freshCount(); ok {
			return n, nil
		}
		before = s.summary.invalidationSeq()
	}
	row := s.queryRow(ctx, `SELECT COUNT(*) FROM events`)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlstore: event count: %w", err)
	}
	if s.summary != nil {
		s.summary.fillCount(before, n)
	}
	return n, nil
}
