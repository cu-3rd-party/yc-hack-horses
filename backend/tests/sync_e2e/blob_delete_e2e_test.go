// E2E: удаление фотографий событием blob.deleted в кластере из трёх нод
// (sqlite + fsblob, реальные TCP-сокеты, mTLS по сертификатам, сгенерированным
// на ходу). Топология — звезда: A в центре, B и C синхронизируются только с A
// (в сценарии out-of-order A знает только B, иначе его пуш C обгоняет удаление).
// Всё идёт через реальный контур internal/app (HTTP REST + sync-движок), как в
// cmd/node web, поэтому проверяются и проекции, и обмен событиями, и blob-файлы.
package sync_e2e_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"

	_ "modernc.org/sqlite"
)

// Идентификаторы нод выбраны так, чтобы лексикографический порядок node_id
// совпадал с порядком A < B < C: журнал событий отдаётся и применяется в
// порядке (node_id, sequence), и от этого порядка зависит исход конкурирующих
// blob.deleted/blob.registered в одном батче (см. сценарий out-of-order).
const (
	blobNodeA = "01990000-0000-7000-8000-0000000000a1"
	blobNodeB = "01990000-0000-7000-8000-0000000000b1"
	blobNodeC = "01990000-0000-7000-8000-0000000000c1"
)

// startBlobCluster поднимает три ноды звездой: A знает B и C, B и C — только A.
func startBlobCluster(t *testing.T) (nodeA, nodeB, nodeC *e2eNode) {
	t.Helper()
	return startBlobClusterWith(t, true)
}

// startBlobClusterIsolatedHub — та же звезда, но A знает только B: C тянет
// события исключительно из журнала A и не получает пушей раньше времени.
// Нужен там, где важен порядок применения событий на C.
func startBlobClusterIsolatedHub(t *testing.T) (nodeA, nodeB, nodeC *e2eNode) {
	t.Helper()
	return startBlobClusterWith(t, false)
}

func startBlobClusterWith(t *testing.T, hubKnowsC bool) (nodeA, nodeB, nodeC *e2eNode) {
	t.Helper()
	ca := newTestCA(t)
	grpcB, grpcC := freePort(t), freePort(t)
	hubPeers := []config.PeerConfig{{Addr: grpcB, NodeID: blobNodeB, Host: "node-" + short(blobNodeB)}}
	if hubKnowsC {
		hubPeers = append(hubPeers, config.PeerConfig{Addr: grpcC, NodeID: blobNodeC, Host: "node-" + short(blobNodeC)})
	}
	nodeA = startNode(t, blobNodeA, t.TempDir(), ca, hubPeers, "")
	peerA := config.PeerConfig{Addr: nodeA.grpcAddr, NodeID: blobNodeA, Host: "node-" + short(blobNodeA)}
	nodeB = startNode(t, blobNodeB, t.TempDir(), ca, []config.PeerConfig{peerA}, grpcB)
	nodeC = startNode(t, blobNodeC, t.TempDir(), ca, []config.PeerConfig{peerA}, grpcC)
	nodeA.waitAllInitialSync(t)
	nodeB.waitAllInitialSync(t)
	nodeC.waitAllInitialSync(t)
	return nodeA, nodeB, nodeC
}

// waitAllInitialSync ждёт стартового цикла по всем статическим пирам (у A их два).
func (n *e2eNode) waitAllInitialSync(t *testing.T) {
	t.Helper()
	want := len(n.cfg.Sync.Peers)
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		peers := n.nodeInfo(t).Sync
		done := len(peers) == want && want > 0
		for _, p := range peers {
			if p.LastSyncAt.IsZero() {
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("нода %s: стартовый цикл sync не завершился по всем пирам", n.id)
}

// uploadBlobTo загружает файл через multipart и проверяет, что sha256 в ответе
// соответствует отправленному содержимому.
func uploadBlobTo(t *testing.T, n *e2eNode, content []byte) (domain.Blob, int) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "photo.jpg")
	require.NoError(t, err)
	_, err = fw.Write(content)
	require.NoError(t, err)
	require.NoError(t, mw.Close())

	req, err := http.NewRequest(http.MethodPost, n.httpBase+"/api/v1/blobs", &buf)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body := readAll(t, resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("нода %s: POST /api/v1/blobs: код %d, тело: %s", n.id, resp.StatusCode, body)
	}
	var b domain.Blob
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("нода %s: разбор ответа POST /api/v1/blobs: %v, тело: %s", n.id, err, body)
	}
	want := fmt.Sprintf("%x", sha256sum(t, content))
	if b.SHA256 != want {
		t.Fatalf("нода %s: sha256 в ответе %q, ожидался %q", n.id, b.SHA256, want)
	}
	return b, resp.StatusCode
}

// addObservationRef создаёт наблюдение, ссылающееся на блоб: это и есть
// «потребность» в файле для неполной blob-синхронизации других нод.
func (n *e2eNode) addObservationRef(t *testing.T, sha, observer string) domain.Observation {
	t.Helper()
	resp := postJSON(t, n.httpBase+"/api/v1/observations", map[string]any{
		"observer_id": observer,
		"location":    "52.10N 30.20E",
		"blob_ids":    []string{sha},
	})
	if resp.StatusCode != http.StatusCreated {
		body := readAll(t, resp.Body)
		resp.Body.Close()
		t.Fatalf("нода %s: POST /api/v1/observations: код %d, тело: %s", n.id, resp.StatusCode, body)
	}
	obs := decode[domain.Observation](t, resp)
	if obs.Invalidated {
		t.Fatalf("нода %s: только что созданное наблюдение %s сразу помечено неактуальным", n.id, obs.ID)
	}
	return obs
}

// findObservation ищет наблюдение в выдаче ноды (nil — нет такого).
func (n *e2eNode) findObservation(t *testing.T, id string) *domain.Observation {
	t.Helper()
	for _, o := range n.listObservations(t) {
		if o.ID == id {
			got := o
			return &got
		}
	}
	return nil
}

// deleteBlobOn вызывает DELETE /api/v1/blobs/{sha256} и возвращает код ответа.
// Пустая причина означает DELETE без параметра ?reason: тогда запрос уходит без
// строки запроса, как его отправил бы обычный клиент без уточнения.
func deleteBlobOn(t *testing.T, n *e2eNode, sha, reason string) int {
	t.Helper()
	u := n.httpBase + "/api/v1/blobs/" + sha
	if reason != "" {
		u += "?reason=" + url.QueryEscape(reason)
	}
	req, err := http.NewRequest(http.MethodDelete, u, nil)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// fetchBlob читает блоб по REST: код и тело.
func fetchBlob(t *testing.T, n *e2eNode, sha string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(n.httpBase + "/api/v1/blobs/" + sha)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, body
}

// blobFilePath — путь файла блоба в data_dir ноды (см. storage/fsblob).
func blobFilePath(n *e2eNode, sha string) string {
	return filepath.Join(n.cfg.Node.DataDir, "blobs", sha[:2], sha)
}

// expectBlobLive проверяет полный контракт «блоб жив»: 200 по REST, байты
// совпадают, файл лежит на диске.
func expectBlobLive(t *testing.T, n *e2eNode, sha string, want []byte, when string) {
	t.Helper()
	code, body := fetchBlob(t, n, sha)
	if code != http.StatusOK {
		t.Fatalf("%s: нода %s обязана отдавать блоб %s (200), получено %d", when, n.id, sha[:12], code)
	}
	if !bytes.Equal(body, want) {
		t.Fatalf("%s: нода %s: содержимое блоба %s не совпало (получено %d байт, ожидалось %d)",
			when, n.id, sha[:12], len(body), len(want))
	}
	if _, err := os.Stat(blobFilePath(n, sha)); err != nil {
		t.Fatalf("%s: нода %s: файл блоба %s отсутствует на диске: %v", when, n.id, sha[:12], err)
	}
}

// expectBlobGone проверяет, что блоб мягко удалён и API отдаёт 404.
func expectBlobGone(t *testing.T, n *e2eNode, sha string, when string) {
	t.Helper()
	code, body := fetchBlob(t, n, sha)
	if code != http.StatusNotFound {
		t.Fatalf("%s: нода %s обязана отдавать 404 по удалённому блобу %s, получено %d (тело: %q)",
			when, n.id, sha[:12], code, string(body))
	}
}

// expectObservationInvalidated проверяет метку неактуальности наблюдения.
// Ожидаемая причина берётся напрямую из ядра (sqlstore.InvalidationReason),
// поэтому проверяется точный контракт: суффикс "(reason)" присутствует только
// для содержательной причины и отсутствует при DELETE без ?reason.
func expectObservationInvalidated(t *testing.T, n *e2eNode, obsID, sha, reason, when string) {
	t.Helper()
	obs := n.findObservation(t, obsID)
	if obs == nil {
		t.Fatalf("%s: нода %s: наблюдение %s не найдено", when, n.id, obsID)
	}
	if !obs.Invalidated {
		t.Fatalf("%s: нода %s: наблюдение %s должно быть invalidated после удаления фото", when, n.id, obsID)
	}
	if obs.InvalidatedAt == nil {
		t.Fatalf("%s: нода %s: наблюдение %s: invalidated=true, но invalidated_at пуст", when, n.id, obsID)
	}
	want := sqlstore.InvalidationReason(sha, reason)
	if obs.InvalidationReason != want {
		t.Fatalf("%s: нода %s: наблюдение %s: причина инвалидации %q, ожидалась %q",
			when, n.id, obsID, obs.InvalidationReason, want)
	}
}

// eventTypesForAggregate возвращает типы событий агрегата в порядке вставки
// (rowid). По нему видно фактический порядок применения проекций, независимо
// от того, как события выглядели в батче на проводе.
func eventTypesForAggregate(t *testing.T, n *e2eNode, aggregateID string) []string {
	t.Helper()
	dsn := "file:" + filepath.Join(n.cfg.Node.DataDir, "node.db") + "?mode=ro"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("нода %s: открытие журнала только на чтение: %v", n.id, err)
	}
	defer db.Close() //nolint:errcheck // read-only
	rows, err := db.Query(`SELECT event_type FROM events WHERE aggregate_id = ? ORDER BY rowid`, aggregateID)
	if err != nil {
		t.Fatalf("нода %s: чтение журнала событий: %v", n.id, err)
	}
	defer rows.Close() //nolint:errcheck // read-only
	var out []string
	for rows.Next() {
		var et string
		if err := rows.Scan(&et); err != nil {
			t.Fatalf("нода %s: чтение журнала событий: %v", n.id, err)
		}
		out = append(out, et)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("нода %s: чтение журнала событий: %v", n.id, err)
	}
	return out
}

// firstIndex возвращает индекс первого вхождения s (-1 — нет).
func firstIndex(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

// TestE2EBlobDeletePropagatesToPeers — сценарий 1: удаление фото на ноде A
// доезжает до B и C. Проверяется 404 по REST, инвалидация наблюдений с
// причиной из события и сохранение файла, пока на него есть ссылка.
func TestE2EBlobDeletePropagatesToPeers(t *testing.T) {
	nodeA, nodeB, nodeC := startBlobCluster(t)

	content := []byte("e2e photo that must disappear everywhere " + time.Now().UTC().Format(time.RFC3339Nano))
	blob, status := uploadBlobTo(t, nodeA, content)
	require.Equal(t, http.StatusCreated, status, "первая загрузка на A должна давать 201")
	obs := nodeA.addObservationRef(t, blob.SHA256, "observer-A")

	// B и C получают и событие blob.registered, и наблюдение, и сам файл.
	nodeB.syncOnce(t)
	nodeC.syncOnce(t)
	expectBlobLive(t, nodeB, blob.SHA256, content, "после первой синхронизации")
	expectBlobLive(t, nodeC, blob.SHA256, content, "после первой синхронизации")
	for _, n := range []*e2eNode{nodeB, nodeC} {
		got := n.findObservation(t, obs.ID)
		if got == nil {
			t.Fatalf("нода %s: наблюдение %s не приехало до удаления фото", n.id, obs.ID)
		}
		if got.Invalidated {
			t.Fatalf("нода %s: наблюдение %s помечено неактуальным до удаления фото", n.id, obs.ID)
		}
	}

	const reason = "фото отозвано владельцем"
	if code := deleteBlobOn(t, nodeA, blob.SHA256, reason); code != http.StatusNoContent {
		t.Fatalf("нода A: DELETE /api/v1/blobs/%s: код %d, ожидался 204", blob.SHA256[:12], code)
	}
	expectBlobGone(t, nodeA, blob.SHA256, "сразу после DELETE на A")
	expectObservationInvalidated(t, nodeA, obs.ID, blob.SHA256, reason, "сразу после DELETE на A")

	// B подтягивает blob.deleted и применяет его к своим проекциям.
	nodeB.syncOnce(t)
	expectBlobGone(t, nodeB, blob.SHA256, "после синхронизации с A")
	expectObservationInvalidated(t, nodeB, obs.ID, blob.SHA256, reason, "после синхронизации с A")
	if _, err := os.Stat(blobFilePath(nodeB, blob.SHA256)); err != nil {
		t.Fatalf("нода B: файл %s удалён, хотя на блоб ссылается наблюдение: %v", blob.SHA256[:12], err)
	}

	// C — то же самое, но через второй цикл (C ходит только в A).
	nodeC.syncOnce(t)
	expectBlobGone(t, nodeC, blob.SHA256, "после синхронизации с A")
	expectObservationInvalidated(t, nodeC, obs.ID, blob.SHA256, reason, "после синхронизации с A")

	// Удаление разошлось по кластеру: на каждой ноде ровно три события
	// (blob.registered, observation.added, blob.deleted). Точное равенство
	// проверяет и отсутствие дубликатов событий при синхронизации.
	for _, n := range []*e2eNode{nodeA, nodeB, nodeC} {
		if got := n.nodeInfo(t).EventCount; got != 3 {
			t.Fatalf("нода %s: событий %d, ожидалось 3 (registered + observation + deleted)", n.id, got)
		}
	}
}

// TestE2EReuploadAfterDeleteConverges — сценарий 2: удаление на A и ре-аплоад
// того же sha256 на B. Оба события доезжают до C, и все три ноды приходят к
// одному состоянию: блоб активен, содержимое на месте.
func TestE2EReuploadAfterDeleteConverges(t *testing.T) {
	nodeA, nodeB, nodeC := startBlobCluster(t)

	content := []byte("e2e photo deleted on A and uploaded again on B " + time.Now().UTC().Format(time.RFC3339Nano))
	blob, status := uploadBlobTo(t, nodeA, content)
	require.Equal(t, http.StatusCreated, status)
	obs := nodeA.addObservationRef(t, blob.SHA256, "observer-A")

	nodeB.syncOnce(t)
	nodeC.syncOnce(t)
	expectBlobLive(t, nodeB, blob.SHA256, content, "до удаления")
	expectBlobLive(t, nodeC, blob.SHA256, content, "до удаления")

	if code := deleteBlobOn(t, nodeA, blob.SHA256, "чистка на A"); code != http.StatusNoContent {
		t.Fatalf("нода A: DELETE: код %d, ожидался 204", code)
	}
	// B узнаёт об удалении и уже потом загружает тот же файл заново — это
	// ре-аплоад (blob.registered после blob.deleted), а не дедупликация.
	nodeB.syncOnce(t)
	expectBlobGone(t, nodeB, blob.SHA256, "после применения blob.deleted с A")

	reuploaded, status := uploadBlobTo(t, nodeB, content)
	if status != http.StatusCreated {
		t.Fatalf("нода B: ре-аплоад %s должен регистрировать блоб заново (201), получено %d", blob.SHA256[:12], status)
	}
	require.Equal(t, blob.SHA256, reuploaded.SHA256)
	expectBlobLive(t, nodeB, blob.SHA256, content, "сразу после ре-аплоада на B")

	// C получает удаление от A, затем регистрацию от B.
	nodeC.syncOnce(t)
	expectBlobGone(t, nodeC, blob.SHA256, "после применения blob.deleted с A")

	// A подтягивает ре-аплоад с B, C — уже от A.
	nodeA.syncOnce(t)
	nodeC.syncOnce(t)

	// Финальное состояние консистентно на всех нодах.
	for _, n := range []*e2eNode{nodeA, nodeB, nodeC} {
		expectBlobLive(t, n, blob.SHA256, content, "финальное состояние после ре-аплоада")
		// Точное равенство счётчика событий проверяет и отсутствие дубликатов
		// (каждое событие кластера учтено на ноде ровно один раз).
		if got := n.nodeInfo(t).EventCount; got != 4 {
			t.Fatalf("нода %s: событий %d, ожидалось 4 (registered, observation, deleted, registered)", n.id, got)
		}
	}
	// Метка неактуальности наблюдения ре-аплоадом не снимается (append-only):
	// это документированное поведение, а не расхождение проекций.
	for _, n := range []*e2eNode{nodeA, nodeB, nodeC} {
		expectObservationInvalidated(t, n, obs.ID, blob.SHA256, "чистка на A", "финальное состояние после ре-аплоада")
	}
}

// TestE2EDeletedBeforeRegistered — сценарий 3 (out-of-order): на ноду C
// сначала приезжает blob.deleted, и только потом blob.registered того же
// sha256. События лежат в одном батче, порядок применения задаётся сортировкой
// (node_id, sequence): удаление с A (id ...a1) идёт раньше регистрации с B
// (id ...b1). Ожидание: после регистрации блоб активен, файл жив, наблюдение
// не инвалидировано.
//
// Внимание: зелёный исход держится на лексикографическом порядке node_id в
// этой сортировке — «удаление раньше регистрации» здесь артефакт порядка
// (node_id, sequence), а не гарантия причинности событий. Смена констант
// blobNodeA/blobNodeB (например, на такие, где id B меньше id A) перевернёт
// порядок применения и изменит исход сценария.
func TestE2EDeletedBeforeRegistered(t *testing.T) {
	nodeA, nodeB, nodeC := startBlobClusterIsolatedHub(t)

	content := []byte("e2e photo: delete arrives before register " + time.Now().UTC().Format(time.RFC3339Nano))
	blob, status := uploadBlobTo(t, nodeB, content)
	require.Equal(t, http.StatusCreated, status)
	obs := nodeB.addObservationRef(t, blob.SHA256, "observer-B")

	// A получает регистрацию и наблюдение (и файл — он ему нужен как
	// держателю ссылки), после чего удаляет блоб у себя.
	nodeA.syncOnce(t)
	expectBlobLive(t, nodeA, blob.SHA256, content, "A после синхронизации с B")
	if code := deleteBlobOn(t, nodeA, blob.SHA256, "удалено на A раньше регистрации на C"); code != http.StatusNoContent {
		t.Fatalf("нода A: DELETE: код %d, ожидался 204", code)
	}

	// C тянет всё с A одним батчем: blob.deleted (собственное событие A,
	// sequence 1) идёт до blob.registered (событие B, sequence 1).
	nodeC.syncOnce(t)

	order := eventTypesForAggregate(t, nodeC, blob.SHA256)
	if firstIndex(order, domain.EventBlobDeleted) < 0 || firstIndex(order, domain.EventBlobRegistered) < 0 {
		t.Fatalf("нода C: в журнале нет обоих событий блоба %s: %v", blob.SHA256[:12], order)
	}
	if firstIndex(order, domain.EventBlobDeleted) > firstIndex(order, domain.EventBlobRegistered) {
		t.Fatalf("сценарий не воспроизведён: на C blob.registered применён раньше blob.deleted: %v", order)
	}

	// Ключевая проверка: порядок «удаление, затем регистрация» не должен
	// оставлять блоб мёртвым.
	expectBlobLive(t, nodeC, blob.SHA256, content, "C после out-of-order доставки (deleted → registered)")
	got := nodeC.findObservation(t, obs.ID)
	if got == nil {
		t.Fatalf("нода C: наблюдение %s не приехало", obs.ID)
	}
	if got.Invalidated {
		t.Fatalf("нода C: наблюдение %s инвалидировано, хотя регистрация блоба пришла после удаления", obs.ID)
	}
}

// TestE2EBlobDeleteWithoutReason — сценарий 5: DELETE без параметра ?reason.
// Ядро подавляет суффикс "(reason)" при пустой причине, поэтому наблюдение
// обязано получить причину "blob deleted: <sha256>" без уточнения. Проверяем и
// на ноде-инициаторе, и после распространения события на пиры: подавление
// суффикса не должно зависеть от того, где применена проекция.
func TestE2EBlobDeleteWithoutReason(t *testing.T) {
	nodeA, nodeB, nodeC := startBlobCluster(t)

	content := []byte("e2e photo deleted without reason " + time.Now().UTC().Format(time.RFC3339Nano))
	blob, status := uploadBlobTo(t, nodeA, content)
	require.Equal(t, http.StatusCreated, status, "первая загрузка на A должна давать 201")
	obs := nodeA.addObservationRef(t, blob.SHA256, "observer-A")

	nodeB.syncOnce(t)
	nodeC.syncOnce(t)
	expectBlobLive(t, nodeB, blob.SHA256, content, "после первой синхронизации")
	expectBlobLive(t, nodeC, blob.SHA256, content, "после первой синхронизации")

	// DELETE без ?reason: причина пуста, суффикс "(reason)" не добавляется.
	if code := deleteBlobOn(t, nodeA, blob.SHA256, ""); code != http.StatusNoContent {
		t.Fatalf("нода A: DELETE без reason: код %d, ожидался 204", code)
	}
	expectBlobGone(t, nodeA, blob.SHA256, "сразу после DELETE на A")
	expectObservationInvalidated(t, nodeA, obs.ID, blob.SHA256, "", "сразу после DELETE на A")

	nodeB.syncOnce(t)
	expectBlobGone(t, nodeB, blob.SHA256, "после синхронизации с A")
	expectObservationInvalidated(t, nodeB, obs.ID, blob.SHA256, "", "после синхронизации с A")

	nodeC.syncOnce(t)
	expectBlobGone(t, nodeC, blob.SHA256, "после синхронизации с A")
	expectObservationInvalidated(t, nodeC, obs.ID, blob.SHA256, "", "после синхронизации с A")

	// Удаление разошлось без дубликатов: registered + observation + deleted.
	for _, n := range []*e2eNode{nodeA, nodeB, nodeC} {
		if got := n.nodeInfo(t).EventCount; got != 3 {
			t.Fatalf("нода %s: событий %d, ожидалось 3 (registered + observation + deleted)", n.id, got)
		}
	}
}
