// Package storage: контракт хранилища. SQL-специфика НЕ должна попадать в sync engine.
package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
)

// Cursor — позиция чтения журнала.
type Cursor struct {
	NodeID   string `json:"node_id,omitempty"` // пусто = все ноды
	Sequence uint64 `json:"sequence"`
}

// VectorClock — прогресс синхронизации по нодам.
type VectorClock map[string]uint64 // node_id -> последний применённый sequence

// EventStore — общий контракт SQLite и PostgreSQL реализаций. Обе проходят contract tests.
type EventStore interface {
	Append(ctx context.Context, events []domain.Event) error // идемпотентно: повтор (node_id, sequence) — no-op
	GetEvents(ctx context.Context, cursor Cursor, limit int) ([]domain.Event, error)
	GetVectorClock(ctx context.Context) (VectorClock, error)
	GetMissingEvents(ctx context.Context, remote VectorClock) ([]domain.Event, error)
	LastSequence(ctx context.Context, nodeID string) (uint64, error)
}

var ErrNotFound = errors.New("not found")

// BlobStore — content-addressed файловое хранилище (V0 — локальная ФС).
type BlobStore interface {
	Put(ctx context.Context, sha256hex string, mime string, r []byte) error
	Get(ctx context.Context, sha256hex string) ([]byte, error)
	Has(ctx context.Context, sha256hex string) (bool, error)
	Delete(ctx context.Context, sha256hex string) error
	ListMissing(ctx context.Context, hashes []string) ([]string, error) // какие запрошенные хэши отсутствуют
	Size(ctx context.Context, sha256hex string) (int64, error)
}

// ValidSha256Hex — строка является корректным sha256-хэшем (64 hex-символа).
// BlobStore адресуется хэшем, поэтому ссылка на legacy blob UUID в наблюдениях
// через content-addressed канал не едет: такие ссылки надо фильтровать до
// обращения к BlobStore (ListMissing валидирует и на невалидном хэше падает).
func ValidSha256Hex(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

// Clock — точка для тестов и замены.
type Clock interface{ Now() time.Time }
