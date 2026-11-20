// Package migrations: go:embed-точка для SQL-миграций. Нужна gomobile-сборке
// (backend/cmd/mobilebind): встроенная в приложение библиотека не имеет на диске
// каталога migrations/, поэтому SQL подгружается из бинарника.
// Файлы-источники — backend/migrations/<driver>/*.sql, здесь они только встраиваются.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed sqlite/*.sql postgres/*.sql
var embedded embed.FS

// FS возвращает файловую систему с подкаталогами по драйверам (sqlite/, postgres/).
// Формат совместим с тем, что ожидает migrate.Apply/ApplyFS.
func FS() fs.FS { return embedded }
