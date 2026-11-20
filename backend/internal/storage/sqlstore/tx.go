package sqlstore

import (
	"context"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
)

// RunInTxEvents — транзакционная запись журнала через контрактный интерфейс
// (используется contract-тестами и приложением).
func (s *Store) RunInTxEvents(ctx context.Context, fn func(tx storage.EventStore) error) error {
	return s.RunInTx(ctx, func(tx *Store) error { return fn(tx) })
}
