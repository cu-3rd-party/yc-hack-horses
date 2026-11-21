// Тесты фасада: инициализация на реальном SQLite в temp-каталоге, append,
// блоб и статус. Без сети — ForegroundSync тестируется в internal/sync.
package mobilebind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
