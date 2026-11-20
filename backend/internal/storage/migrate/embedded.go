// Дополнение к migrate: применение миграций из fs.FS (go:embed).
// Нужно gomobile-библиотеке (backend/cmd/mobilebind), у которой нет каталога
// migrations/ на диске. Логика записи версий общая с migrate.Apply.
package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var versionFSRe = regexp.MustCompile(`^(\d+)_.+\.sql$`)

// ApplyFS применяет миграции из fsys (ожидается структура <driver>/<N>_*.sql).
// Идемпотентность и порядок версий — как в Apply.
func ApplyFS(ctx context.Context, db *sql.DB, fsys fs.FS, driver string) error {
	files, err := listFS(fsys, driver)
	if err != nil {
		return err
	}
	return applyAll(ctx, db, files, driver)
}

func listFS(fsys fs.FS, driver string) ([]migFile, error) {
	entries, err := fs.ReadDir(fsys, driver)
	if err != nil {
		return nil, fmt.Errorf("migrate: read embedded migrations dir: %w", err)
	}
	var out []migFile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := versionFSRe.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migrate: файл %s без числового префикса версии", e.Name())
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		sqlBytes, err := fs.ReadFile(fsys, driver+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", e.Name(), err)
		}
		out = append(out, migFile{version: v, name: e.Name(), sql: string(sqlBytes)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}
