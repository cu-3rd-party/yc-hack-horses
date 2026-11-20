package migrate

// SQL реестра миграций отличается плейсхолдерами: sqlite — "?", postgres — "$n".
func createTableSQL(driver string) string {
	if driver == "postgres" {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL)`
}

func recordVersionSQL(driver string) string {
	if driver == "postgres" {
		return `INSERT INTO schema_migrations (version, applied_at) VALUES ($1, $2)`
	}
	return `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`
}
