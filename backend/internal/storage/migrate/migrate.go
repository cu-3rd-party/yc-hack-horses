// Package migrate: версионные миграции из каталога migrations/<driver>/*.sql.
// Идемпотентно: применяются только ещё не записанные в schema_migrations версии.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Apply применяет все неприменённые миграции из dir (dir содержит подкаталоги по драйверам).
func Apply(ctx context.Context, db *sql.DB, dir, driver string) error {
	files, err := list(dir, driver)
	if err != nil {
		return err
	}
	return applyAll(ctx, db, files, driver)
}

// applyAll создаёт schema_migrations, читает применённые версии и накатывает файлы.
// Общий путь для Apply и ApplyFS.
func applyAll(ctx context.Context, db *sql.DB, files []migFile, driver string) error {
	if _, err := db.ExecContext(ctx, createTableSQL(driver)); err != nil {
		return fmt.Errorf("migrate: create schema_migrations: %w", err)
	}
	applied := map[int64]bool{}
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("migrate: read applied versions: %w", err)
	}
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return fmt.Errorf("migrate: scan version: %w", err)
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("migrate: iterate versions: %w", err)
	}
	rows.Close()

	for _, f := range files {
		if applied[f.version] {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("migrate: begin tx: %w", err)
		}
		if _, err := tx.ExecContext(ctx, f.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: apply %s: %w", f.name, err)
		}
		if _, err := tx.ExecContext(ctx, recordVersionSQL(driver),
			f.version, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migrate: record %s: %w", f.name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrate: commit %s: %w", f.name, err)
		}
		slog.Info("migration applied", "file", f.name, "driver", driver)
	}
	return nil
}

type migFile struct {
	version int64
	name    string
	sql     string
}

var versionRe = regexp.MustCompile(`^(\d+)_.+\.sql$`)

func list(dir, driver string) ([]migFile, error) {
	entries, err := os.ReadDir(filepath.Join(dir, driver))
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations dir: %w", err)
	}
	var out []migFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := versionRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: файл %s без числового префикса версии", e.Name())
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		sqlBytes, err := os.ReadFile(filepath.Join(dir, driver, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", e.Name(), err)
		}
		out = append(out, migFile{version: v, name: e.Name(), sql: string(sqlBytes)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
