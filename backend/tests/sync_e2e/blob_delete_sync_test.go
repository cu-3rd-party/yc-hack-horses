// E2E: удаление фото (blob.deleted) расходится по sync между нодами.
// Инвалидированное наблюдение остаётся в истории как аудит-запись, но его блоб
// больше не входит в потребности неполного синка: нода, впервые увидевшая
// данные уже после удаления, мёртвый файл не качает.
package sync_e2e_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/stretchr/testify/require"
)

// nodeBlobStats — часть ответа /api/v1/node про состояние блобов.
type nodeBlobStats struct {
	Blobs struct {
		Referenced int `json:"referenced"`
		Present    int `json:"present"`
		Missing    int `json:"missing"`
	} `json:"blobs"`
}

// blobOnDisk — лежит ли файл блоба в каталоге данных ноды (fsblob: blobs/<2>/<sha>).
func blobOnDisk(t *testing.T, dataDir, sha string) bool {
	t.Helper()
	if len(sha) < 2 {
		t.Fatalf("bad sha %q", sha)
	}
	_, err := os.Stat(filepath.Join(dataDir, "blobs", sha[:2], sha))
	return err == nil
}

// observationByID ищет наблюдение в выдаче ноды.
func observationByID(t *testing.T, n *e2eNode, id string) *domain.Observation {
	t.Helper()
	for _, o := range n.listObservations(t) {
		if o.ID == id {
			got := o
			return &got
		}
	}
	return nil
}

// blobStats читает срез состояния блобов из /api/v1/node.
func blobStats(t *testing.T, n *e2eNode) nodeBlobStats {
	t.Helper()
	resp, err := http.Get(n.httpBase + "/api/v1/node")
	require.NoError(t, err)
	return decode[nodeBlobStats](t, resp)
}

// waitForBlobRegistered ждёт, пока на ноде появится строка блоба (событие
// blob.registered применено): только тогда ссылка наблюдения разрешается в
// sha256 и блоб вообще может попасть в срез потребностей. Явная проверка убирает
// гонку с применением событий у пира.
func waitForBlobRegistered(t *testing.T, n *e2eNode, sha string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get(n.httpBase + "/api/v1/blobs")
		require.NoError(t, err)
		for _, b := range decode[[]domain.Blob](t, resp) {
			if strings.EqualFold(b.SHA256, sha) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("строка блоба %s не появилась на ноде %s (blob.registered не применён)", sha, n.id)
}

// TestBlobDeletedSync — blob.deleted доезжает до пиров событием, наблюдение
// инвалидируется у всех, файл не перекачивается на ноды, где его не было.
func TestBlobDeletedSync(t *testing.T) {
	const (
		idA = "01990000-0000-7000-8000-00000000004a"
		idB = "01990000-0000-7000-8000-00000000004b"
		idC = "01990000-0000-7000-8000-00000000004c"
	)
	ca := newTestCA(t)
	dirA, dirB, dirC := t.TempDir(), t.TempDir(), t.TempDir()

	grpcB := freePort(t)
	nodeA := startNode(t, idA, dirA, ca, []config.PeerConfig{
		{Addr: grpcB, NodeID: idB, Host: "node-" + short(idB)},
	}, "")
	nodeB := startNode(t, idB, dirB, ca, []config.PeerConfig{
		{Addr: nodeA.grpcAddr, NodeID: idA, Host: "node-" + short(idA)},
	}, grpcB)
	nodeA.waitForInitialSync(t)
	nodeB.waitForInitialSync(t)

	// A загружает фото и создаёт наблюдение, ссылающееся на него.
	payload := []byte("przewalski-photo-to-delete")
	blob := uploadComparisonPhoto(t, nodeA, payload)
	require.Len(t, blob.SHA256, 64)

	resp := postJSON(t, nodeA.httpBase+"/api/v1/observations", map[string]any{
		"observer_id": "observer-del",
		"location":    "51N 30E",
		"blob_ids":    []string{blob.SHA256},
	})
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	obs := decode[domain.Observation](t, resp)
	require.False(t, obs.Invalidated)

	// До удаления блоб доезжает до B (неполный синк по потребности).
	nodeA.syncOnce(t)
	// Явно дожидаемся строки блоба (blob.registered) на B: без неё ссылка
	// наблюдения не разрешается в sha256 и потребность не появилась бы, так что
	// проверки ниже не зависят от гонки с применением событий у пира.
	waitForBlobRegistered(t, nodeB, blob.SHA256)
	// Событие наблюдения уже на B, а блоб — ещё нет: это живая потребность.
	// Позитивная проверка, что срез вообще считается (referenced>0, missing>0),
	// а не только что после удаления там нули.
	statsPending := blobStats(t, nodeB)
	require.Greater(t, statsPending.Blobs.Referenced, 0, "живое наблюдение должно быть в потребностях")
	require.Greater(t, statsPending.Blobs.Missing, 0, "отсутствующий блоб должен попасть в missing")
	require.Equal(t, 0, statsPending.Blobs.Present, "файла на B ещё нет")
	nodeB.syncOnce(t)
	require.True(t, blobOnDisk(t, dirB, blob.SHA256), "blob должен доехать до B до удаления")
	statsPresent := blobStats(t, nodeB)
	require.Equal(t, 1, statsPresent.Blobs.Referenced)
	require.Equal(t, 1, statsPresent.Blobs.Present, "скачанный блоб должен считаться present")
	require.Equal(t, 0, statsPresent.Blobs.Missing)

	// A удаляет фото: событие blob.deleted, проекция мягко гасит метаданные.
	req, err := http.NewRequest(http.MethodDelete,
		nodeA.httpBase+"/api/v1/blobs/"+blob.SHA256+"?reason=test", nil)
	require.NoError(t, err)
	dresp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, dresp.Body)
	dresp.Body.Close()
	require.Equal(t, http.StatusNoContent, dresp.StatusCode)

	// Событие доезжает до B: наблюдение инвалидировано, файл на месте (история).
	nodeA.syncOnce(t)
	nodeB.syncOnce(t)

	gotB := observationByID(t, nodeB, obs.ID)
	require.NotNil(t, gotB, "наблюдение должно остаться в истории B")
	require.True(t, gotB.Invalidated, "наблюдение на B должно быть инвалидировано")
	require.NotNil(t, gotB.InvalidatedAt)
	require.True(t, blobOnDisk(t, dirB, blob.SHA256), "файл на B остаётся: он в истории наблюдения")

	// Метаданные блоба погашены — REST отдаёт 404, содержимое уже не достать.
	bresp, err := http.Get(nodeB.httpBase + "/api/v1/blobs/" + blob.SHA256)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, bresp.Body)
	bresp.Body.Close()
	require.Equal(t, http.StatusNotFound, bresp.StatusCode)

	// После инвалидации блоб выпадает из потребностей: пирам его качать незачем.
	require.Equal(t, 0, blobStats(t, nodeB).Blobs.Referenced)
	require.Equal(t, 0, blobStats(t, nodeB).Blobs.Missing)
	// На A файл тоже держится: ссылка из blob_ids ещё есть (история).
	require.True(t, blobOnDisk(t, dirA, blob.SHA256))

	// C впервые синхронизируется уже после удаления: событие и наблюдение
	// приезжают, а мёртвый файл — нет.
	nodeC := startNode(t, idC, dirC, ca, []config.PeerConfig{
		{Addr: nodeA.grpcAddr, NodeID: idA, Host: "node-" + short(idA)},
	}, "")
	nodeC.waitForInitialSync(t)
	nodeC.syncOnce(t)

	gotC := observationByID(t, nodeC, obs.ID)
	require.NotNil(t, gotC, "история наблюдения должна доехать до C")
	require.True(t, gotC.Invalidated, "на C наблюдение сразу инвалидировано: блоб удалён")

	require.False(t, blobOnDisk(t, dirC, blob.SHA256),
		"файл блоба инвалидированного наблюдения не должен перекачиваться")
	statsC := blobStats(t, nodeC)
	require.Equal(t, 0, statsC.Blobs.Referenced, "потребности C: только активные наблюдения")
	require.Equal(t, 0, statsC.Blobs.Missing)

	// Повторный цикл — без докачки и без переигрывания события.
	eventsBefore := nodeC.nodeInfo(t).EventCount
	nodeA.syncOnce(t)
	nodeC.syncOnce(t)
	require.False(t, blobOnDisk(t, dirC, blob.SHA256))
	require.Equal(t, eventsBefore, nodeC.nodeInfo(t).EventCount)
	require.Len(t, nodeC.listObservations(t), 1)
}
