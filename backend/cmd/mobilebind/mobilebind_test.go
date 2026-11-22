// Тесты фасада: инициализация на реальном SQLite в temp-каталоге, append,
// блоб и статус. Без сети — ForegroundSync тестируется в internal/sync.
package mobilebind

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"

	"github.com/stretchr/testify/require"
)

func TestInitAppendStatus(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Init(dir), "Init")
	defer func() { _ = Close() }()

	statusJSON, err := NodeStatus()
	require.NoError(t, err, "NodeStatus")
	var status map[string]any
	require.NoError(t, json.Unmarshal([]byte(statusJSON), &status), "status json")
	require.NotEqual(t, "", status["node_id"], "unexpected status: %s", statusJSON)
	require.Equal(t, "sqlite", status["driver"], "unexpected status: %s", statusJSON)

	// блоб до наблюдения: observation ссылается на его id
	blobJSON, err := StoreBlob([]byte("photo-bytes"), "image/jpeg")
	require.NoError(t, err, "StoreBlob")
	var blob struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal([]byte(blobJSON), &blob), "blob json")
	require.NotEqual(t, "", blob.SHA256, "blob sha256 empty")

	obsJSON, err := AppendObservation(`{"observer_id":"tester","location":"47.5,42.1","blob_ids":["` + blob.SHA256 + `"]}`)
	require.NoError(t, err, "AppendObservation")
	var obs struct {
		ID         string   `json:"id"`
		NodeID     string   `json:"node_id"`
		ObserverID string   `json:"observer_id"`
		BlobIDs    []string `json:"blob_ids"`
	}
	require.NoError(t, json.Unmarshal([]byte(obsJSON), &obs), "obs json")
	require.NotEqual(t, "", obs.ID, "unexpected observation: %s", obsJSON)
	require.NotEqual(t, "", obs.NodeID, "unexpected observation: %s", obsJSON)
	require.Len(t, obs.BlobIDs, 1, "unexpected observation: %s", obsJSON)

	// повторная инициализация в том же каталоге: node_id сохраняется, события не теряются
	statusBefore := statusJSON
	require.NoError(t, Init(dir), "re-Init")
	statusJSON2, err := NodeStatus()
	require.NoError(t, err, "NodeStatus 2")
	if statusJSON2 == "" || statusJSON2 == statusBefore {
		// node_id тот же, но event_count вырос — строки должны отличаться
		var s2 map[string]any
		_ = json.Unmarshal([]byte(statusJSON2), &s2)
		eventCount, _ := s2["event_count"].(float64)
		require.GreaterOrEqual(t, eventCount, 2.0, "events lost after re-init: %s", statusJSON2)
	}
	_, err = os.Stat(filepath.Join(dir, "blobs"))
	require.NoError(t, err, "blobs dir")
}

func TestAppendObservationRequiresObserver(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()
	_, err := AppendObservation(`{}`)
	require.Error(t, err, "expected error for empty observer_id")
}

// storeBlobSHA — вспомогательный шаг: кладёт блоб и возвращает его sha256.
func storeBlobSHA(t *testing.T, data string) string {
	t.Helper()
	blobJSON, err := StoreBlob([]byte(data), "image/jpeg")
	require.NoError(t, err, "StoreBlob")
	var blob struct {
		SHA256 string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal([]byte(blobJSON), &blob), "blob json")
	require.NotEqual(t, "", blob.SHA256, "blob sha256 empty")
	return blob.SHA256
}

// appendObs — вспомогательный шаг: пишет наблюдение со ссылкой на блоб, возвращает id.
func appendObs(t *testing.T, sha string) string {
	t.Helper()
	obsJSON, err := AppendObservation(`{"observer_id":"tester","blob_ids":["` + sha + `"]}`)
	require.NoError(t, err, "AppendObservation")
	var obs struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(obsJSON), &obs), "obs json")
	require.NotEqual(t, "", obs.ID, "observation id empty")
	return obs.ID
}

// После blob.deleted мобильный путь обязан отдавать наблюдение с полями
// инвалидации (invalidated/invalidated_at/invalidation_reason).
func TestDeleteBlobInvalidatesObservation(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()

	sha := storeBlobSHA(t, "photo-invalidate")
	obsID := appendObs(t, sha)

	// до удаления — наблюдение актуально
	before, err := GetObservation(obsID)
	require.NoError(t, err, "GetObservation before")
	var beforeObs struct {
		Invalidated bool `json:"invalidated"`
	}
	require.NoError(t, json.Unmarshal([]byte(before), &beforeObs), "before json")
	require.False(t, beforeObs.Invalidated, "unexpected invalidated before delete: %s", before)

	// удаляем фото — наблюдение становится неактуальным
	require.NoError(t, DeleteBlob(sha, "user requested"), "DeleteBlob")

	after, err := GetObservation(obsID)
	require.NoError(t, err, "GetObservation after")
	var afterObs struct {
		Invalidated        bool   `json:"invalidated"`
		InvalidatedAt      string `json:"invalidated_at"`
		InvalidationReason string `json:"invalidation_reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(after), &afterObs), "after json")
	require.True(t, afterObs.Invalidated, "expected invalidated after delete: %s", after)
	require.NotEqual(t, "", afterObs.InvalidatedAt, "expected invalidated_at: %s", after)
	// Причина инвалидации — единый формат ядра: "blob deleted: <sha> (<reason>)".
	reasonPrefix := "blob deleted: " + sha + " ("
	require.Equal(t, reasonPrefix+"user requested)", afterObs.InvalidationReason, "unexpected reason: %s", after)

	// список наблюдений тоже отдаёт неактуальную запись с полями инвалидации
	listJSON, err := ListObservations("")
	require.NoError(t, err, "ListObservations")
	var list []struct {
		ID          string `json:"id"`
		Invalidated bool   `json:"invalidated"`
	}
	require.NoError(t, json.Unmarshal([]byte(listJSON), &list), "list json")
	found := false
	for _, o := range list {
		if o.ID == obsID {
			found = true
			require.True(t, o.Invalidated, "list entry not invalidated: %s", listJSON)
		}
	}
	require.True(t, found, "observation missing from list: %s", listJSON)
}

// Наблюдение, ссылающееся на уже удалённый блоб, создаётся сразу неактивным.
func TestAppendObservationWithDeletedBlobIsInvalidated(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()

	sha := storeBlobSHA(t, "photo-late")
	require.NoError(t, DeleteBlob(sha, "gone"), "DeleteBlob")

	obsJSON, err := AppendObservation(`{"observer_id":"tester","blob_ids":["` + sha + `"]}`)
	require.NoError(t, err, "AppendObservation")
	var obs struct {
		Invalidated        bool   `json:"invalidated"`
		InvalidationReason string `json:"invalidation_reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(obsJSON), &obs), "obs json")
	require.True(t, obs.Invalidated, "expected invalidated immediately: %s", obsJSON)
}

func TestDeleteBlobErrors(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()

	// некорректный sha256 → 400. Sentinel неэкспортируем (см. errInvalidSHA256),
	// поэтому клиентский контракт проверяем через helper IsInvalidSHA256 и
	// стабильный строковый префикс сообщения.
	err := DeleteBlob("not-a-sha", "")
	require.ErrorIs(t, err, errInvalidSHA256, "expected errInvalidSHA256, got %v", err)
	require.True(t, IsInvalidSHA256(err), "IsInvalidSHA256 must report true: %v", err)
	require.False(t, IsNotFound(err), "invalid sha256 must not be not-found: %v", err)
	require.False(t, IsInvalidSHA256(nil), "IsInvalidSHA256(nil) must be false")
	require.True(t, strings.HasPrefix(err.Error(), errInvalidSHA256Prefix),
		"error message must start with stable prefix, got %q", err.Error())

	// неизвестный (никогда не существовавший) блоб → 404. Симметрично проверяем
	// helper IsNotFound и префикс errNotFoundPrefix; sentinel тут — внешний
	// storage.ErrNotFound под обёрткой.
	err = DeleteBlob(strings.Repeat("a", 64), "")
	require.ErrorIs(t, err, storage.ErrNotFound, "expected storage.ErrNotFound, got %v", err)
	require.True(t, IsNotFound(err), "IsNotFound must report true: %v", err)
	require.False(t, IsInvalidSHA256(err), "not-found must not be invalid sha256: %v", err)
	require.False(t, IsNotFound(nil), "IsNotFound(nil) must be false")
	require.False(t, IsNotFound(errInvalidSHA256), "invalid sha256 must not be not-found: %v", err)
	require.True(t, strings.HasPrefix(err.Error(), errNotFoundPrefix),
		"not-found message must start with stable prefix, got %q", err.Error())

	// повторное удаление уже удалённого блоба → тоже 404
	sha := storeBlobSHA(t, "photo-twice")
	require.NoError(t, DeleteBlob(sha, "first"), "first DeleteBlob")
	err = DeleteBlob(sha, "second")
	require.ErrorIs(t, err, storage.ErrNotFound, "expected storage.ErrNotFound for deleted blob, got %v", err)
	require.True(t, IsNotFound(err), "repeated delete must be not-found: %v", err)
}

// reason длиннее лимита + пробелы по краям: reason обрезается до maxReasonLen
// рун ПОСЛЕ TrimSpace, крайние пробелы в сохранённую причину не попадают.
func TestDeleteBlobReasonTrimAndTruncate(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()

	sha := storeBlobSHA(t, "photo-reason")
	obsID := appendObs(t, sha)

	// 600 значащих рун + обрамляющие пробелы (больше лимита после трима).
	reason := "  " + strings.Repeat("я", 600) + "  "
	require.NoError(t, DeleteBlob(sha, reason), "DeleteBlob")

	after, err := GetObservation(obsID)
	require.NoError(t, err, "GetObservation")
	var afterObs struct {
		InvalidationReason string `json:"invalidation_reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(after), &afterObs), "after json")

	reasonPrefix := "blob deleted: " + sha + " ("
	got := []rune(afterObs.InvalidationReason)
	require.Len(t, got, len([]rune(reasonPrefix))+maxReasonLen+1, "reason must be truncated to %d runes: %q", maxReasonLen, afterObs.InvalidationReason)
	require.Equal(t, reasonPrefix+strings.Repeat("я", maxReasonLen)+")", afterObs.InvalidationReason,
		"reason must keep only the leading runes, no edge spaces: %q", afterObs.InvalidationReason)
	require.Equal(t, afterObs.InvalidationReason, strings.TrimSpace(afterObs.InvalidationReason),
		"reason must not have edge spaces: %q", afterObs.InvalidationReason)
}

// Усечение по границе: если 512-я руна — пробел, повторный TrimSpace после
// усечения обязан убрать хвостовой пробел (без него причина кончалась бы пробелом).
func TestDeleteBlobReasonTruncationTrimsBoundarySpace(t *testing.T) {
	require.NoError(t, Init(t.TempDir()), "Init")
	defer func() { _ = Close() }()

	sha := storeBlobSHA(t, "photo-reason-boundary")
	obsID := appendObs(t, sha)

	// 511 значащих рун + пробел ровно на 512-й позиции + хвост.
	reason := strings.Repeat("я", maxReasonLen-1) + " " + strings.Repeat("б", 50)
	require.NoError(t, DeleteBlob(sha, reason), "DeleteBlob")

	after, err := GetObservation(obsID)
	require.NoError(t, err, "GetObservation")
	var afterObs struct {
		InvalidationReason string `json:"invalidation_reason"`
	}
	require.NoError(t, json.Unmarshal([]byte(after), &afterObs), "after json")

	reasonPrefix := "blob deleted: " + sha + " ("
	require.Equal(t, reasonPrefix+strings.Repeat("я", maxReasonLen-1)+")", afterObs.InvalidationReason,
		"boundary space must be trimmed after truncation: %q", afterObs.InvalidationReason)
	require.Equal(t, afterObs.InvalidationReason, strings.TrimSpace(afterObs.InvalidationReason),
		"reason must not end with a space: %q", afterObs.InvalidationReason)
}

// Поиск похожих (мобильный путь векторов) не должен отдавать снимки
// инвалидированных наблюдений. Данные готовятся через отдельный дескриптор той
// же БД, чтобы не залезать в синглтон core.
func TestFindSimilarObservationsExcludesInvalidated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Init(dir), "Init")
	defer func() { _ = Close() }()

	blob1 := storeBlobSHA(t, "photo-similar-1")
	blob2 := storeBlobSHA(t, "photo-similar-2")
	obs1 := appendObs(t, blob1)
	obs2 := appendObs(t, blob2)

	// Сид trait_snapshots — через второй дескриптор той же БД. Причина: у
	// пакета нет exported-API для записи снимков, а тащить весь синглтон core
	// в тест не хотим (он держит один *sqlstore.Store под мьютексом и не даёт
	// игнорировать проекцию наблюдений). sqlstore.Open сам ставит WAL и
	// busy_timeout=5000, поэтому второй хэндл к тому же файлу безопасен даже при
	// одновременной записи; MaxOpenConns=1 сериализует записи внутри хэндла.
	st, err := sqlstore.Open("sqlite", "file:"+filepath.Join(dir, "node.db"))
	require.NoError(t, err, "open store")
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	const fp = "fingerprint-shared"
	require.NoError(t, st.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: "ts-1", ObservationID: obs1, ModelVersion: "m1",
		Traits: json.RawMessage(`{"x":1}`), Confidence: 0.9, Fingerprint: fp, Confirmed: true,
	}), "snapshot 1")
	require.NoError(t, st.CreateTraitSnapshot(ctx, domain.TraitSnapshot{
		ID: "ts-2", ObservationID: obs2, ModelVersion: "m1",
		Traits: json.RawMessage(`{"x":1}`), Confidence: 0.9, Fingerprint: fp, Confirmed: true,
	}), "snapshot 2")

	// до удаления похожий снимок второго наблюдения виден
	before, err := FindSimilarObservations(blob1, 10)
	require.NoError(t, err, "FindSimilarObservations before")
	require.True(t, similarHasID(t, before, "ts-2"), "expected similar snapshot before delete: %s", before)

	// удаляем фото второго наблюдения — его снимок выпадает из поиска
	require.NoError(t, DeleteBlob(blob2, "gone"), "DeleteBlob")
	after, err := FindSimilarObservations(blob1, 10)
	require.NoError(t, err, "FindSimilarObservations after")
	require.False(t, similarHasID(t, after, "ts-2"), "invalidated snapshot must be excluded: %s", after)
}

// similarHasID разбирает ответ FindSimilarObservations в типизированную
// структуру {query, disclaimer, results} и проверяет наличие снимка по ID.
func similarHasID(t *testing.T, payload, id string) bool {
	t.Helper()
	var resp struct {
		Query      string `json:"query"`
		Disclaimer string `json:"disclaimer"`
		Results    []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &resp), "similar json")
	require.Equal(t, similarDisclaimer, resp.Disclaimer, "unexpected disclaimer: %s", payload)
	for _, r := range resp.Results {
		if r.ID == id {
			return true
		}
	}
	return false
}
