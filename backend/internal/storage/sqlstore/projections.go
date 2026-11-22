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

// InvalidateObservationIfBlobsDeleted помечает наблюдение неактуальным, если
// хоть одна ссылка (blob id или sha256) уже мягко удалена. Нужна, когда
// наблюдение создаётся после удаления фото: события в журнале уже разошлись.
func (s *Store) InvalidateObservationIfBlobsDeleted(ctx context.Context, observationID string, refs []string, reason string) (bool, error) {
	for _, ref := range refs {
		sha, deletedAt, deleted, err := s.blobDeletion(ctx, ref)
		if err != nil {
			return false, err
		}
		if !deleted {
			continue
		}
		// В причине — канонический sha256: по ссылке blobs.id не видно, какое фото выпало.
		if sha == "" {
			sha = ref
		}
		if _, err := s.InvalidateObservation(ctx, observationID, deletedAt, InvalidationReason(sha, reason)); err != nil {
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
// Проверка паспорта добавляет проход по horses (лошадей сотни, это дёшево).
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
	// Файл прикреплённого PDF-паспорта не удаляем с диска.
	horses, err := s.ListHorses(ctx)
	if err != nil {
		return false, fmt.Errorf("sqlstore: blob %s passport references: %w", sha, err)
	}
	for _, h := range horses {
		if strings.EqualFold(h.Metadata[domain.MetaPassportSHA256], sha) {
			return true, nil
		}
	}
	return false, nil
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
	row := s.queryRow(ctx, `SELECT COUNT(*) FROM events`)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("sqlstore: event count: %w", err)
	}
	return n, nil
}
