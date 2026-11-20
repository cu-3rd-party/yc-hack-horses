// Package postgres: postgres-реализация хранилища (общий движок в sqlstore, драйвер pgx).
package postgres

import (
	"fmt"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// New открывает postgres-хранилище по dsn.
func New(dsn string) (*sqlstore.Store, error) {
	s, err := sqlstore.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: %w", err)
	}
	return s, nil
}
