package sqlstore

import (
	// регистрация драйверов в database/sql: sqlite — pure Go (modernc), postgres — pgx stdlib.
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)
