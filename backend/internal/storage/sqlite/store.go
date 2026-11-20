// Package sqlite: sqlite-реализация хранилища (общий движок в sqlstore).
package sqlite

import (
	"fmt"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// New открывает sqlite-хранилище по dsn (путь к файлу).
func New(dsn string) (*sqlstore.Store, error) {
	s, err := sqlstore.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite: %w", err)
	}
	return s, nil
}
