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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	"github.com/cu3rd/yc-hack-horses/backend/migrations"

	"github.com/google/uuid"
)

const appVersion = "mobile-0.1.0"

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
		HorseID:    in.HorseID,
		NodeID:     c.nodeID,
		ObservedAt: observedAt,
		Location:   in.Location,
		ObserverID: in.ObserverID,
		BlobIDs:    in.BlobIDs,
	}
	if err := appendWithProjection(c, domain.EventObservationAdded, o.ID, mustJSON(o),
		func(tx *sqlstore.Store) error { return tx.CreateObservation(context.Background(), o) }); err != nil {
		return "", err
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
