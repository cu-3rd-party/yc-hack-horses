package sqlstore

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func timeFromUnixMilli(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// EncodeListCursor — непрозрачный курсор списочной пагинации: base64url от
// "<ключ сортировки>|<id>". Ключ — метка времени (unix-ms) в десятичном виде,
// id — уникальный разрыватель порядка. Пустой курсор — начало списка.
// Экспортируется для HTTP-слоя: там же собирается next-cursor из последней
// отданной записи.
func EncodeListCursor(keyMs int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(keyMs, 10) + "|" + id))
}

// DecodeListCursor разбирает курсор; пустая строка — начало списка (0, "").
// Экспортируется для HTTP-слоя: клиент получает курсор из X-Next-Cursor.
func DecodeListCursor(cursor string) (keyMs int64, id string, err error) {
	if cursor == "" {
		return 0, "", nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, "", fmt.Errorf("sqlstore: cursor: %w", err)
	}
	key, idPart, ok := strings.Cut(string(raw), "|")
	if !ok {
		return 0, "", fmt.Errorf("sqlstore: cursor: bad payload")
	}
	keyMs, err = strconv.ParseInt(key, 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("sqlstore: cursor: bad key: %w", err)
	}
	return keyMs, idPart, nil
}

// appendCursorCond добавляет условие «строго после курсора» для сортировки
// по (колонка ключа, колонка id): (key > ?) OR (key = ? AND id > ?).
// Возвращает пустую строку, если курсор не задан.
func appendCursorCond(keyCol, idCol string, keyMs int64, id string) (string, []any) {
	if keyMs == 0 && id == "" {
		return "", nil
	}
	return " (" + keyCol + " > ? OR (" + keyCol + " = ? AND " + idCol + " > ?))", []any{keyMs, keyMs, id}
}
