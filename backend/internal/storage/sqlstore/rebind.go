package sqlstore

import (
	"strconv"
	"strings"
)

// bindSQLite оставляет "?" как есть.
func bindSQLite(query string) string { return query }

// bindPostgres заменяет "?" на "$n" по порядку. Строковых литералов с "?" в наших
// запросах нет — конвертация безопасна.
func bindPostgres(query string) string {
	var b strings.Builder
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}
