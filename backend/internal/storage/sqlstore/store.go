// Package sqlstore: единая SQL-реализация EventStore и проекций для sqlite и postgres.
// Разница драйверов — плейсхолдеры и типы в миграциях; запросы общие ("?" ребиндится в $n).
package sqlstore

import (
	"context"
	"database/sql"
	"fmt"
)

// Store — SQL-хранилище. Реализует storage.EventStore; операции проекций в projections.go.
// RunInTx даёт транзакционность между журналом событий и проекциями.
type Store struct {
	db     *sql.DB
	tx     *sql.Tx // не nil внутри RunInTx
	rebind func(query string) string
	driver string
}

// Open открывает пул соединений. driver: "sqlite" | "postgres". Миграции отдельно (package migrate).
func Open(driver, dsn string) (*Store, error) {
	switch driver {
	case "sqlite", "postgres":
	default:
		return nil, fmt.Errorf("sqlstore: неизвестный драйвер %q", driver)
	}
	db, err := sql.Open(driverName(driver), dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlstore: open %s: %w", driver, err)
	}
	if driver == "sqlite" {
		// WAL и busy_timeout: чтение не блокирует запись, краткая блокировка не роняет запрос.
		for _, pragma := range []string{
			`PRAGMA journal_mode=WAL`,
			`PRAGMA busy_timeout=5000`,
			`PRAGMA foreign_keys=ON`,
			`PRAGMA synchronous=NORMAL`,
		} {
			if _, err := db.Exec(pragma); err != nil {
				db.Close()
				return nil, fmt.Errorf("sqlstore: pragma: %w", err)
			}
		}
		// sqlite допускает один писатель: одно соединение исключает SQLITE_BUSY между горутинами.
		db.SetMaxOpenConns(1)
	} else {
		db.SetMaxOpenConns(10)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlstore: ping %s: %w", driver, err)
	}
	s := &Store{db: db, driver: driver, rebind: bindSQLite}
	if driver == "postgres" {
		s.rebind = bindPostgres
	}
	return s, nil
}

func driverName(driver string) string {
	if driver == "postgres" {
		return "pgx"
	}
	return "sqlite"
}

// Close закрывает пул.
func (s *Store) Close() error { return s.db.Close() }

// DB возвращает нижележащий пул (для миграций и /readyz).
func (s *Store) DB() *sql.DB { return s.db }

// Ping проверяет доступность БД (используется /readyz).
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Driver возвращает имя драйвера.
func (s *Store) Driver() string { return s.driver }

// RunInTx выполняет fn в транзакции: переданный в fn Store ходит тем же tx.
// Ошибка fn откатывает всё; частичная запись невозможна. Вложенные RunInTx не поддерживаются.
func (s *Store) RunInTx(ctx context.Context, fn func(tx *Store) error) error {
	if s.tx != nil {
		// Вложенный вызов выполняется в уже открытой транзакции вызывающего.
		return fn(s)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlstore: begin tx: %w", err)
	}
	inner := &Store{db: s.db, tx: tx, driver: s.driver, rebind: s.rebind}
	if err := fn(inner); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlstore: commit: %w", err)
	}
	return nil
}

// exec выполняет запрос через tx (если есть) или пул.
func (s *Store) exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	query = s.rebind(query)
	if s.tx != nil {
		return s.tx.ExecContext(ctx, query, args...)
	}
	return s.db.ExecContext(ctx, query, args...)
}

// query выполняет выборку через tx (если есть) или пул.
func (s *Store) query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	query = s.rebind(query)
	if s.tx != nil {
		return s.tx.QueryContext(ctx, query, args...)
	}
	return s.db.QueryContext(ctx, query, args...)
}

// queryRow выполняет однострочную выборку через tx (если есть) или пул.
func (s *Store) queryRow(ctx context.Context, query string, args ...any) *sql.Row {
	query = s.rebind(query)
	if s.tx != nil {
		return s.tx.QueryRowContext(ctx, query, args...)
	}
	return s.db.QueryRowContext(ctx, query, args...)
}
