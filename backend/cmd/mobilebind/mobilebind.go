// Package mobilebind — API-фасад Go Core для мобильного приложения (ADR-0011).
// gomobile bind собирает этот пакет в нативную библиотеку; React Native вызывает
// функции через NativeModules. Здесь только сборка вызовов над существующими
// внутренностями backend (sqlstore, fsblob, migrate, sync engine) — логика
// синхронизации и записи НЕ дублируется.
//
// Ограничения gomobile: экспортируемые функции принимают/возвращают только
// простые типы, поэтому сложные данные ходят как JSON-строки, ошибки — как error.
package mobilebind

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	"github.com/cu3rd/yc-hack-horses/backend/migrations"

	"github.com/google/uuid"
)

const appVersion = "mobile-0.1.0"

// similarDisclaimer — явный дисклеймер ответа поиска похожих (паритет с
// http-путём, inference.go): fingerprint — это поиск похожих по набору
// признаков, НЕ биологическая идентичность.
const similarDisclaimer = "fingerprint — поиск похожих по набору признаков, НЕ биологическая идентичность"

// maxReasonLen ограничивает длину reason, чтобы произвольный текст клиента не
// раздувал payload события blob.deleted. Лимит держим только на мобильном
// фасаде: http-путь (DELETE /api/v1/blobs/{sha256}) длину reason НЕ ограничивает.
// Свести лимиты в один — задача интеграции, вне этого слоя.
const maxReasonLen = 512

// errInvalidSHA256Prefix — стабильный строковый контракт сообщения об ошибке
// некорректного sha256. Держим его синхронным с errInvalidSHA256: префикс =
// весь текст errInvalidSHA256 (обёртка добавляет ": <sha>"). Менять только
// вместе с мобильным клиентом.
const errInvalidSHA256Prefix = "mobilebind: invalid sha256"

// errNotFoundPrefix — стабильный строковый контракт сообщения об ошибке
// «блоб не найден/уже удалён» (аналог 404 в http-пути). Сообщение начинается с
// этого префикса, поэтому старый мобильный клиент может различать 400/404 по
// началу строки (err.Error()), не дожидаясь нового биндинга. Держим синхронным
// с IsNotFound; менять только вместе с мобильным клиентом.
const errNotFoundPrefix = "mobilebind: blob not found"

// errInvalidSHA256 — некорректный sha256 (аналог 400 в http-пути). Намеренно НЕ
// экспортируем: gomobile bind превращает package-level переменные в get/set-пары,
// и тогда мобильный клиент мог бы подменить sentinel и сломать errors.Is.
// Контракт для клиента — exported helper IsInvalidSHA256 и стабильный префикс
// сообщения errInvalidSHA256Prefix.
var errInvalidSHA256 = errors.New(errInvalidSHA256Prefix)

// IsInvalidSHA256 сообщает, является ли ошибка ошибкой некорректного sha256
// (аналог 400 в http-пути). Экспортируемая функция, а не переменная: sentinel
// errInvalidSHA256 намеренно неэкспортируем, чтобы клиент не мог его подменить.
// Для случая «блоб не найден/уже удалён» (аналог 404) используйте IsNotFound.
func IsInvalidSHA256(err error) bool {
	return errors.Is(err, errInvalidSHA256)
}

// IsNotFound сообщает, является ли ошибка ошибкой «не найдено» (аналог 404 в
// http-пути): неизвестный или уже удалённый блоб, отсутствующее наблюдение и
// т.п. Симметричен IsInvalidSHA256 — мобильный клиент различает 400/404 по этим
// двум helper'ам, не прибегая к errors.Is напрямую (storage в биндинг не
// экспортирован). Плюс сообщение имеет стабильный префикс errNotFoundPrefix.
func IsNotFound(err error) bool {
	return errors.Is(err, storage.ErrNotFound)
}

// coreState — единственный экземпляр ядра на процесс. gomobile не передаёт
// указатели между языками, поэтому состояние живёт в синглтоне под мьютексом.
var (
	mu   sync.Mutex
	core *mobileCore
)

type mobileCore struct {
	store  *sqlstore.Store
	blobs  *fsblob.Store
	nodeID string
}

// Init открывает SQLite в каталоге данных приложения и применяет миграции.
// Вызывать один раз при старте приложения; повторный вызов закрывает и
// переоткрывает хранилище.
func Init(dataDir string) error {
	mu.Lock()
	defer mu.Unlock()
	return initLocked(dataDir)
}

func initLocked(dataDir string) error {
	if dataDir == "" {
		return fmt.Errorf("mobilebind: dataDir required")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("mobilebind: mkdir data dir: %w", err)
	}
	if core != nil {
		_ = core.store.Close()
		core = nil
	}
	dsn := "file:" + filepath.Join(dataDir, "node.db")
	store, err := sqlstore.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	// Миграции встроены в бинарник: библиотека не имеет каталога migrations/ на диске.
	if err := migrate.ApplyFS(context.Background(), store.DB(), migrations.FS(), "sqlite"); err != nil {
		store.Close()
		return fmt.Errorf("mobilebind: migrations: %w", err)
	}
	blobs, err := fsblob.New(filepath.Join(dataDir, "blobs"))
	if err != nil {
		store.Close()
		return err
	}
	nodeID, err := loadOrCreateNodeID(dataDir)
	if err != nil {
		store.Close()
		return err
	}
	core = &mobileCore{store: store, blobs: blobs, nodeID: nodeID}
	return nil
}

func current() (*mobileCore, error) {
	if core == nil {
		return nil, fmt.Errorf("mobilebind: core not initialized, call Init first")
	}
	return core, nil
}

// loadOrCreateNodeID — копия логики app.loadOrCreateNodeID: <data_dir>/node.json.
// Оригинал не экспортирован, а тащить весь app (HTTP-сервер) на мобильное
// устройство не нужно.
func loadOrCreateNodeID(dataDir string) (string, error) {
	path := filepath.Join(dataDir, "node.json")
	if data, err := os.ReadFile(path); err == nil {
		var stored struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.ID != "" {
			return stored.ID, nil
		}
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("mobilebind: generate node id: %w", err)
	}
	data, _ := json.MarshalIndent(map[string]string{"id": id.String()}, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("mobilebind: persist node id: %w", err)
	}
	return id.String(), nil
}

// observationInput — форма наблюдения (соответствует POST /api/v1/observations).
type observationInput struct {
	Comment    string   `json:"comment"`
	HorseID    string   `json:"horse_id"`
	ObservedAt string   `json:"observed_at"` // RFC3339; пусто = текущее время
	Location   string   `json:"location"`
	ObserverID string   `json:"observer_id"`
	BlobIDs    []string `json:"blob_ids"`
}

// AppendObservation записывает наблюдение офлайн: событие observation.added в
// локальный журнал + проекция, в одной транзакции. Возвращает JSON наблюдения.
func AppendObservation(payloadJSON string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	var in observationInput
	if err := json.Unmarshal([]byte(payloadJSON), &in); err != nil {
		return "", fmt.Errorf("mobilebind: invalid payload: %w", err)
	}
	if in.ObserverID == "" {
		return "", fmt.Errorf("mobilebind: observer_id required")
	}
	observedAt := time.Now().UTC()
	if in.ObservedAt != "" {
		observedAt, err = time.Parse(time.RFC3339, in.ObservedAt)
		if err != nil {
			return "", fmt.Errorf("mobilebind: observed_at: %w", err)
		}
	}
	if in.HorseID != "" {
		if _, err := c.store.GetHorse(context.Background(), in.HorseID); err != nil {
			return "", fmt.Errorf("mobilebind: horse %s: %w", in.HorseID, err)
		}
	}
	if in.BlobIDs == nil {
		in.BlobIDs = []string{}
	}
	o := domain.Observation{
		ID:         uuid.Must(uuid.NewV7()).String(),
		Comment:    strings.TrimSpace(in.Comment),
		HorseID:    in.HorseID,
		NodeID:     c.nodeID,
		ObservedAt: observedAt,
		Location:   in.Location,
		ObserverID: in.ObserverID,
		BlobIDs:    in.BlobIDs,
	}
	if err := appendWithProjection(c, domain.EventObservationAdded, o.ID, mustJSON(o),
		func(tx *sqlstore.Store) error {
			if err := tx.CreateObservation(context.Background(), o); err != nil {
				return err
			}
			// Фото могло быть удалено раньше: наблюдение создаётся, но сразу
			// помечается неактуальным (как в http-пути).
			_, err := tx.InvalidateObservationIfBlobsDeleted(context.Background(), o.ID, o.BlobIDs, "blob deleted")
			return err
		}); err != nil {
		return "", err
	}
	// Ответ — из проекции: наблюдение могло быть помечено неактуальным сразу.
	// Ошибку чтения не глотаем молча: логируем расхождение, но отдаём исходное
	// значение, чтобы запись не считалась проваленной.
	if stored, err := c.store.GetObservation(context.Background(), o.ID); err != nil {
		slog.Warn("mobilebind: read observation after append", "id", o.ID, "err", err)
	} else {
		o = stored
	}
	return string(mustJSON(o)), nil
}

// StoreBlob сохраняет фото/файл в content-addressed хранилище и регистрирует
// событие blob.registered. Возвращает JSON вида {"id","sha256","mime_type","size"}.
func StoreBlob(data []byte, mimeType string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("mobilebind: empty blob")
	}
	sum := sha256Hex(data)
	if err := c.blobs.Put(context.Background(), sum, mimeType, data); err != nil {
		return "", err
	}
	b := domain.Blob{ID: uuid.Must(uuid.NewV7()).String(), SHA256: sum, MimeType: mimeType, Size: int64(len(data))}
	var created bool
	if err := c.store.RunInTx(context.Background(), func(tx *sqlstore.Store) error {
		var err error
		created, err = tx.UpsertBlob(context.Background(), b)
		if err != nil || !created {
			return err
		}
		return appendEventLocked(tx, c, domain.EventBlobRegistered, sum,
			mustJSON(map[string]any{"sha256": sum, "mime_type": mimeType, "size": len(data)}))
	}); err != nil {
		return "", err
	}
	return string(mustJSON(b)), nil
}

// DeleteBlob мягко удаляет блоб событием blob.deleted (аналог
// DELETE /api/v1/blobs/{sha256}): строка помечается в проекции, наблюдения с
// этим фото становятся неактуальными (invalidated), а физический файл удаляется
// только если на блоб не ссылается ни одно наблюдение. Повторный вызов для уже
// удалённого/неизвестного блоба — ошибка (в http-пути это 404).
func DeleteBlob(sha256Hex, reason string) error {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return err
	}
	// Паритет с http-путём: пробелы по краям sha ошибочны только если после
	// трима значение не является корректным sha256.
	sha256Hex = strings.TrimSpace(sha256Hex)
	if !storage.ValidSha256Hex(sha256Hex) {
		return fmt.Errorf("%w: %s", errInvalidSHA256, sha256Hex)
	}
	// Блоб должен существовать и быть не удалённым — иначе удалять нечего
	// (аналог 404: ошибка несёт storage.ErrNotFound). Сообщению даём стабильный
	// префикс errNotFoundPrefix, чтобы клиент различал 400/404 по началу строки.
	if _, err := c.store.GetBlobMeta(context.Background(), sha256Hex); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("%s: %s: %w", errNotFoundPrefix, sha256Hex, err)
		}
		return fmt.Errorf("mobilebind: blob %s: %w", sha256Hex, err)
	}
	reason = strings.TrimSpace(reason)
	if r := []rune(reason); len(r) > maxReasonLen {
		// Усечение может оставить хвостовой пробел, если граница попала на
		// 512-ю руну-разделитель — повторный TrimSpace его убирает.
		reason = strings.TrimSpace(string(r[:maxReasonLen]))
	}
	now := time.Now().UTC()
	payload := domain.BlobDeleted{SHA256: sha256Hex, DeletedAt: now, Reason: reason}
	if err := appendWithProjection(c, domain.EventBlobDeleted, sha256Hex, mustJSON(payload),
		func(tx *sqlstore.Store) error {
			_, err := tx.ProjectBlobDeleted(context.Background(), sha256Hex, now, reason)
			return err
		}); err != nil {
		return err
	}
	// Файл — после коммита события и проекции (см. storage.PruneBlobs).
	storage.PruneBlobs(context.Background(), c.store, c.blobs, []string{sha256Hex}, slog.Default())
	return nil
}

// GetObservation возвращает JSON одного наблюдения, включая поля инвалидации
// (invalidated/invalidated_at/invalidation_reason): мобильный UI показывает
// устаревшие наблюдения отдельно, не удаляя их из журнала.
func GetObservation(id string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("mobilebind: observation id required")
	}
	o, err := c.store.GetObservation(context.Background(), id)
	if err != nil {
		return "", fmt.Errorf("mobilebind: observation %s: %w", id, err)
	}
	return string(mustJSON(o)), nil
}

// ListObservations возвращает JSON-массив наблюдений (при horseID != "" — только
// по указанной лошади). Неактуальные наблюдения остаются в выдаче вместе с
// полями инвалидации, чтобы UI мог пометить их как устаревшие.
func ListObservations(horseID string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	list, err := c.store.ListObservations(context.Background(), horseID)
	if err != nil {
		return "", err
	}
	if list == nil {
		list = []domain.Observation{}
	}
	return string(mustJSON(list)), nil
}

// similarResponse — форма ответа поиска похожих: паритет с http-путём
// (inference.go similarResponse) — query + disclaimer + results.
type similarResponse struct {
	Query      string                 `json:"query"`
	Disclaimer string                 `json:"disclaimer"`
	Results    []domain.TraitSnapshot `json:"results"`
}

// FindSimilarObservations — мобильный путь поиска похожих по признакам (аналог
// GET /api/v1/inference/similar). Идёт через ListSimilarTraitSnapshots, который
// исключает снимки неактуальных (invalidated) наблюдений: после blob.deleted их
// вектора в выдачу не попадают. Ответ — та же обёртка, что и в http-пути:
// {query, disclaimer, results}.
func FindSimilarObservations(blobID string, limit int) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	// Паритет с http getSimilarTraits: пробелы по краям blob_id отбрасываем
	// до проверки на пустоту.
	blobID = strings.TrimSpace(blobID)
	if blobID == "" {
		return "", fmt.Errorf("mobilebind: blob_id required")
	}
	blob, err := c.store.GetBlobByID(context.Background(), blobID)
	if err != nil {
		return "", fmt.Errorf("mobilebind: blob %s: %w", blobID, err)
	}
	src, err := c.store.FindSnapshotByBlobSHA(context.Background(), blob.SHA256)
	if err != nil {
		return "", fmt.Errorf("mobilebind: snapshot for blob %s: %w", blob.SHA256, err)
	}
	similar, err := c.store.ListSimilarTraitSnapshots(context.Background(), src.Fingerprint, src.ID, limit)
	if err != nil {
		return "", err
	}
	if similar == nil {
		similar = []domain.TraitSnapshot{}
	}
	return string(mustJSON(similarResponse{
		Query:      src.Fingerprint,
		Disclaimer: similarDisclaimer,
		Results:    similar,
	})), nil
}

// NodeStatus возвращает JSON-статус локальной ноды (id, роль, число событий, часы).
func NodeStatus() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	c, err := current()
	if err != nil {
		return "", err
	}
	info, err := c.store.NodeSummary(context.Background(), c.nodeID)
	if err != nil {
		return "", err
	}
	return string(mustJSON(map[string]any{
		"node_id":      c.nodeID,
		"app_version":  appVersion,
		"role":         "local",
		"driver":       c.store.Driver(),
		"event_count":  info.EventCount,
		"last_seq":     info.LastSeq,
		"vector_clock": info.VectorClock,
	})), nil
}

// Close закрывает хранилище (по событию lifecycle приложения).
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	if core == nil {
		return nil
	}
	err := core.store.Close()
	core = nil
	return err
}

// appendWithProjection — локальный аналог http.appendWithProjection: sequence
// назначается в транзакции, событие + проекция пишутся атомарно. WS-публикации
// на мобильной ноде нет.
func appendWithProjection(c *mobileCore, eventType, aggregateID string, payload json.RawMessage, projection func(tx *sqlstore.Store) error) error {
	return c.store.RunInTx(context.Background(), func(tx *sqlstore.Store) error {
		if err := appendEventLocked(tx, c, eventType, aggregateID, payload); err != nil {
			return err
		}
		return projection(tx)
	})
}

func appendEventLocked(tx *sqlstore.Store, c *mobileCore, eventType, aggregateID string, payload json.RawMessage) error {
	last, err := tx.LastSequence(context.Background(), c.nodeID)
	if err != nil {
		return err
	}
	return tx.Append(context.Background(), []domain.Event{{
		EventID:       uuid.Must(uuid.NewV7()).String(),
		NodeID:        c.nodeID,
		Sequence:      last + 1,
		EventType:     eventType,
		SchemaVersion: 1,
		AggregateID:   aggregateID,
		OccurredAt:    time.Now().UTC(),
		Payload:       payload,
	}})
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		// структуры домена маршалируются всегда; паника — программная ошибка
		panic(fmt.Sprintf("mobilebind: marshal: %v", err))
	}
	return b
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
