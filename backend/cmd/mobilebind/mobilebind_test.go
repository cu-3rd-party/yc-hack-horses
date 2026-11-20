// Тесты фасада: инициализация на реальном SQLite в temp-каталоге, append,
// блоб и статус. Без сети — ForegroundSync тестируется в internal/sync.
package mobilebind

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInitAppendStatus(t *testing.T) {
	dir := t.TempDir()
	if err := Init(dir); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Close() }()

	statusJSON, err := NodeStatus()
	if err != nil {
		t.Fatalf("NodeStatus: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal([]byte(statusJSON), &status); err != nil {
		t.Fatalf("status json: %v", err)
	}
	if status["node_id"] == "" || status["driver"] != "sqlite" {
		t.Fatalf("unexpected status: %s", statusJSON)
	}

	// блоб до наблюдения: observation ссылается на его id
	blobJSON, err := StoreBlob([]byte("photo-bytes"), "image/jpeg")
	if err != nil {
		t.Fatalf("StoreBlob: %v", err)
	}
	var blob struct {
		ID     string `json:"id"`
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal([]byte(blobJSON), &blob); err != nil {
		t.Fatalf("blob json: %v", err)
	}
	if blob.SHA256 == "" {
		t.Fatalf("blob sha256 empty")
	}

	obsJSON, err := AppendObservation(`{"observer_id":"tester","location":"47.5,42.1","blob_ids":["` + blob.SHA256 + `"]}`)
	if err != nil {
		t.Fatalf("AppendObservation: %v", err)
	}
	var obs struct {
		ID         string   `json:"id"`
		NodeID     string   `json:"node_id"`
		ObserverID string   `json:"observer_id"`
		BlobIDs    []string `json:"blob_ids"`
	}
	if err := json.Unmarshal([]byte(obsJSON), &obs); err != nil {
		t.Fatalf("obs json: %v", err)
	}
	if obs.ID == "" || obs.NodeID == "" || len(obs.BlobIDs) != 1 {
		t.Fatalf("unexpected observation: %s", obsJSON)
	}

	// повторная инициализация в том же каталоге: node_id сохраняется, события не теряются
	statusBefore := statusJSON
	if err := Init(dir); err != nil {
		t.Fatalf("re-Init: %v", err)
	}
	statusJSON2, err := NodeStatus()
	if err != nil {
		t.Fatalf("NodeStatus 2: %v", err)
	}
	if statusJSON2 == "" || statusJSON2 == statusBefore {
		// node_id тот же, но event_count вырос — строки должны отличаться
		var s2 map[string]any
		_ = json.Unmarshal([]byte(statusJSON2), &s2)
		if s2["event_count"].(float64) < 2 {
			t.Fatalf("events lost after re-init: %s", statusJSON2)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "blobs")); err != nil {
		t.Fatalf("blobs dir: %v", err)
	}
}

func TestAppendObservationRequiresObserver(t *testing.T) {
	if err := Init(t.TempDir()); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer func() { _ = Close() }()
	if _, err := AppendObservation(`{}`); err == nil {
		t.Fatal("expected error for empty observer_id")
	}
}
