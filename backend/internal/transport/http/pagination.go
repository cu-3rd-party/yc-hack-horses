package http

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

// pagination.go: курсорная пагинация списочных эндпоинтов (/horses,
// /observations, /blobs, /observations?include=photos).
//
// Контракт (обратно совместимый):
//   - Без параметров — старое поведение: полный список (для /observations —
//     исторический LIMIT 1000). Тело ответа — JSON-массив, как раньше.
//   - ?limit=N или ?cursor=<opaque> — включается курсорная пагинация:
//     массив отдается усечённым, продолжение помечается заголовком
//     X-Next-Cursor (непрозрачный курсор от (ключ сортировки, id) последней
//     записи; пустой заголовок/отсутствие — страниц больше нет). Тело остаётся
//     JSON-массивом, чтобы существующие клиенты не ломались.
//   - limit: по умолчанию 1000, максимум 5000.
//   - Ошибка разбора параметров — 400, а не молчаливый сброс.
//
// Сортировка: /horses — (created_at, id), /observations — (observed_at, id),
// /blobs — (created_at, sha256).

const (
	listDefaultLimit = 1000
	listMaxLimit     = 5000
)

type listQuery struct {
	limit int
	keyMs int64
	id    string
	paged bool
}

// parseListQuery разбирает limit/cursor; paged=false — старое поведение.
func parseListQuery(r *http.Request) (listQuery, error) {
	q := listQuery{paged: false}
	rawLimit := strings.TrimSpace(r.URL.Query().Get("limit"))
	rawCursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	if rawLimit == "" && rawCursor == "" {
		return q, nil
	}
	q.paged = true
	q.limit = listDefaultLimit
	if rawLimit != "" {
		n, err := strconv.Atoi(rawLimit)
		if err != nil || n <= 0 {
			return q, errBadListQuery
		}
		q.limit = n
	}
	if q.limit > listMaxLimit {
		q.limit = listMaxLimit
	}
	if rawCursor != "" {
		keyMs, id, err := sqlstore.DecodeListCursor(rawCursor)
		if err != nil {
			return q, errBadListQuery
		}
		q.keyMs, q.id = keyMs, id
	}
	return q, nil
}

var errBadListQuery = errInvalidListParams{}

type errInvalidListParams struct{}

func (errInvalidListParams) Error() string { return "invalid list query parameters" }
