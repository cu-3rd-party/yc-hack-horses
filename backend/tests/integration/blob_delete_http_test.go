package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"

	"github.com/stretchr/testify/require"
)

// Хендлерный уровень DELETE /api/v1/blobs/{sha256}: коды ответов и отсутствие
// дублирующих событий blob.deleted. Сама проекция и файлы проверяются в
// blob_delete_test.go.

type deleteNodeSummary struct {
	EventCount int64 `json:"event_count"`
}

// journalEventCount читает число записей журнала через /api/v1/node.
//
// Инвариант: вызывать только внутри тестов без t.Parallel(). Счётчик читается
// из общего журнала до и после запроса, и любой параллельно пишущий тест
// сломал бы сравнение before/after.
func journalEventCount(t *testing.T) int64 {
	t.Helper()
	info := DecodeJSON[deleteNodeSummary](t,
		MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/node"), http.StatusOK))
	return info.EventCount
}

// blobDeletedEventCount считает в журнале события blob.deleted именно этого
// блоба: соседние тесты пишут свои события (в том числе blob.deleted других
// sha и любые upload-события), поэтому общий event_count для ассертов не годен.
func blobDeletedEventCount(t *testing.T, sha string) int {
	t.Helper()
	events, err := testStore.GetEvents(context.Background(), storage.Cursor{}, 0)
	require.NoError(t, err)
	var n int
	for _, ev := range events {
		if ev.EventType == domain.EventBlobDeleted && ev.AggregateID == sha {
			n++
		}
	}
	return n
}

// uniqueBlobContent даёт уникальное содержимое, чтобы sha256 не пересекался
// с блобами из других тестов.
func uniqueBlobContent(t *testing.T) []byte {
	t.Helper()
	return []byte(t.Name() + " " + time.Now().UTC().Format(time.RFC3339Nano))
}

// DELETE несуществующего блоба — 404 и ни одного события в журнале.
func TestDeleteBlobNonexistentNoEvent(t *testing.T) {
	sum := sha256.Sum256(append(uniqueBlobContent(t), []byte("-never-uploaded")...))
	sha := hex.EncodeToString(sum[:])

	before := journalEventCount(t)
	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+sha), http.StatusNotFound)
	require.Equal(t, before, journalEventCount(t),
		"DELETE несуществующего блоба не должен писать событие")
	require.Zero(t, blobDeletedEventCount(t, sha))
}

// Повторный DELETE уже удалённого блоба — 404, в журнале ровно одно
// blob.deleted этого блоба.
func TestDeleteBlobRepeatedWritesSingleEvent(t *testing.T) {
	resp, blob := uploadTestBlob(t, uniqueBlobContent(t))
	require.Equal(t, http.StatusCreated, resp.Code)

	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256+"?reason=repeat"),
		http.StatusNoContent)
	// Второй DELETE того же блоба — предсказуемый 404 без нового события.
	MakeRequest(t, NewRequest(t, http.MethodDelete, "/api/v1/blobs/"+blob.SHA256),
		http.StatusNotFound)

	require.Equal(t, 1, blobDeletedEventCount(t, blob.SHA256),
		"повторный DELETE не должен дублировать blob.deleted")
	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/blobs/"+blob.SHA256), http.StatusNotFound)
}

// Конкурентные DELETE одного блоба: ровно один 204, остальные 404 и одно
// событие blob.deleted. Тест фиксирует идемпотентность API при последовательных
// вызовах; ветку гарда внутри транзакции (n == 0) он, как правило, не исполняет —
// на sqlite возможен проход проигравшего до tx, но обычно запросы
// сериализуются одним соединением, и проигравшие получают 404 уже на
// предварительной проверке GetBlobMeta. Гард покрыт отдельно в
// TestMarkBlobDeletedTxGuard (internal/transport/http).
func TestDeleteBlobConcurrentSingleWinner(t *testing.T) {
	resp, blob := uploadTestBlob(t, uniqueBlobContent(t))
	require.Equal(t, http.StatusCreated, resp.Code)

	const callers = 4
	codes := make([]int, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Запрос строим вручную, а не через require-хелперы: хелперы
			// вызывают t.FailNow, что в горутине запрещено (паника вместо
			// корректного фейла). Нужен лишь самостоятельный *http.Request,
			// который отправим в общий роутер.
			req := httptest.NewRequest(http.MethodDelete, "/api/v1/blobs/"+blob.SHA256, nil)
			<-start
			rec := httptest.NewRecorder()
			testRoutes.ServeHTTP(rec, req)
			codes[i] = rec.Code
		}(i)
	}
	close(start)
	wg.Wait()

	sort.Ints(codes)
	require.Equal(t, []int{http.StatusNoContent, http.StatusNotFound, http.StatusNotFound, http.StatusNotFound},
		codes, "при гонке DELETE блоб удаляет ровно один запрос, остальные получают 404")
	require.Equal(t, 1, blobDeletedEventCount(t, blob.SHA256),
		"гонка не должна порождать второе blob.deleted")
}
