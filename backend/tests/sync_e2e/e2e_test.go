// E2E: две ноды (sqlite + fsblob, реальные TCP-сокеты, mTLS по сертификатам,
// сгенерированным on the fly) — сквозной sync через сборку internal/app
// (то, что запускает cmd/node web).
package sync_e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	apppkg "github.com/cu3rd/yc-hack-horses/backend/internal/app"
	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"

	"github.com/stretchr/testify/require"
)

// nodeHTTPInfo — часть ответа /api/v1/node, нужная тестам.
type nodeHTTPInfo struct {
	NodeID     string             `json:"node_id"`
	EventCount int                `json:"event_count"`
	Sync       []apppkg.PeerStats `json:"sync"`
}

// e2eNode — запущенная нода в тесте.
type e2eNode struct {
	id       string
	a        *apppkg.App
	cfg      *config.Config
	httpBase string
	grpcAddr string
	cancel   context.CancelFunc
	runErr   chan error
}

// freePort резервирует и тут же освобождает TCP-порт на loopback.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// startNode собирает и запускает ноду: HTTP REST/WS + gRPC mTLS + sync-контур.
// grpcAddrOverride — заранее выбранный gRPC-адрес ("" — авто).
func startNode(t *testing.T, id, dataDir string, ca *testCA, peers []config.PeerConfig, grpcAddrOverride string, options ...func(*config.Config)) *e2eNode {
	t.Helper()
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	certFile, keyFile, caFile := ca.writeCertDir(t, filepath.Join(dataDir, "certs"), id, "node-"+short(id))
	httpAddr := freePort(t)
	grpcAddr := grpcAddrOverride
	if grpcAddr == "" {
		grpcAddr = freePort(t)
	}

	cfg := config.Default()
	cfg.Node.ID = id
	cfg.Node.DataDir = dataDir
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.DSN = filepath.Join(dataDir, "node.db")
	cfg.Network.ListenAddr = httpAddr
	cfg.Network.GRPCListenAddr = grpcAddr
	cfg.Network.MdnsEnabled = false // mDNS-мультикаст в тестовой среде недоступен — статические пиры
	cfg.Security.CertFile = certFile
	cfg.Security.KeyFile = keyFile
	cfg.Security.CAFile = caFile
	cfg.Sync.Enabled = true
	cfg.Sync.CentralURL = ""
	cfg.Sync.Interval = time.Hour // фоновый цикл не мешает: тесты зовут SyncNow явно
	cfg.Sync.Peers = peers
	for _, apply := range options {
		apply(cfg)
	}
	return startNodeWithConfig(t, cfg)
}

// short — короткий суффикс для DNS SAN.
func short(id string) string {
	if len(id) > 8 {
		return id[len(id)-8:]
	}
	return id
}

func startNodeWithConfig(t *testing.T, cfg *config.Config) *e2eNode {
	t.Helper()
	n := &e2eNode{
		id:       cfg.Node.ID,
		cfg:      cfg,
		httpBase: "http://" + cfg.Network.ListenAddr,
		grpcAddr: cfg.Network.GRPCListenAddr,
	}
	n.start(t)
	t.Cleanup(func() {
		if n.cancel == nil {
			return
		}
		n.cancel()
		select {
		case <-n.runErr:
		case <-time.After(15 * time.Second):
			t.Errorf("node %s did not stop in time", n.id)
		}
	})
	return n
}

func (n *e2eNode) start(t *testing.T) {
	t.Helper()
	a, err := apppkg.New(n.cfg)
	require.NoError(t, err)
	n.a = a
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	runErr := make(chan error, 1)
	n.runErr = runErr
	go func() { runErr <- a.Run(ctx) }()
	waitHealthy(t, n.httpBase)
}

// waitHealthy ждёт готовности HTTP API ноды.
func waitHealthy(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/health")
		if err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("node at %s did not become healthy", base)
}

// --- REST helpers ---

func postJSON(t *testing.T, url string, v any) *http.Response {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	return resp
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(b)
}

// decodeBlob — blob REST-ответ может быть 200 (дедуп) или 201 (создан).
func decodeBlob(t *testing.T, resp *http.Response) domain.Blob {
	t.Helper()
	defer resp.Body.Close()
	body := readAll(t, resp.Body)
	require.Contains(t, []int{http.StatusOK, http.StatusCreated}, resp.StatusCode, "body: %s", body)
	var v domain.Blob
	require.NoError(t, json.Unmarshal([]byte(body), &v))
	return v
}

func decode[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	defer resp.Body.Close()
	body := readAll(t, resp.Body)
	require.Less(t, resp.StatusCode, 300, "body: %s", body)
	require.GreaterOrEqual(t, resp.StatusCode, 200, "body: %s", body)
	var v T
	require.NoError(t, json.Unmarshal([]byte(body), &v))
	return v
}

func (n *e2eNode) listHorses(t *testing.T) []domain.Horse {
	resp, err := http.Get(n.httpBase + "/api/v1/horses")
	require.NoError(t, err)
	return decode[[]domain.Horse](t, resp)
}

func (n *e2eNode) listObservations(t *testing.T) []domain.Observation {
	resp, err := http.Get(n.httpBase + "/api/v1/observations")
	require.NoError(t, err)
	return decode[[]domain.Observation](t, resp)
}

func (n *e2eNode) nodeInfo(t *testing.T) nodeHTTPInfo {
	resp, err := http.Get(n.httpBase + "/api/v1/node")
	require.NoError(t, err)
	return decode[nodeHTTPInfo](t, resp)
}

// The sync loop runs once at startup, before its first hourly tick. Wait for
// that attempt to finish before measuring transfers from a manual cycle.
func (n *e2eNode) waitForInitialSync(t *testing.T) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		peers := n.nodeInfo(t).Sync
		if len(peers) == 1 && !peers[0].LastSyncAt.IsZero() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("initial sync did not finish on %s", n.id)
}

// syncOnce — один цикл sync ноды (как один тик runLoop).
func (n *e2eNode) syncOnce(t *testing.T) (int, int) {
	t.Helper()
	sent, received, err := n.a.SyncNow(context.Background())
	require.NoError(t, err)
	return sent, received
}

// TestE2EInvalidPlainGRPCOnPublicAddr — plain gRPC без сертификатов запрещён
// вне loopback (правило доверия из acceptance criteria 1).
func TestE2EInvalidPlainGRPCOnPublicAddr(t *testing.T) {
	t.Setenv("MIGRATIONS_DIR", "../../migrations")
	cfg := config.Default()
	cfg.Node.DataDir = t.TempDir()
	cfg.Storage.DSN = filepath.Join(t.TempDir(), "node.db")
	cfg.Network.ListenAddr = "127.0.0.1:0"
	cfg.Network.GRPCListenAddr = "0.0.0.0:19443"
	cfg.Sync.Enabled = false
	_, err := apppkg.New(cfg)
	require.Error(t, err)
	require.Contains(t, err.Error(), "loopback")
}

// TestE2ETwoNodesSync — основной сценарий: события+проекции, отсутствие
// дубликатов, возобновление после обрыва, blob по gRPC, неповеренный серт.
func TestE2ETwoNodesSync(t *testing.T) {
	const (
		idA = "01990000-0000-7000-8000-00000000000a"
		idB = "01990000-0000-7000-8000-00000000000b"
	)
	ca := newTestCA(t)
	dirA, dirB := t.TempDir(), t.TempDir()

	grpcB := freePort(t)
	nodeA := startNode(t, idA, dirA, ca, []config.PeerConfig{{
		Addr: grpcB, NodeID: idB, Host: "node-" + short(idB),
	}}, "")
	nodeB := startNode(t, idB, dirB, ca, []config.PeerConfig{
		{Addr: nodeA.grpcAddr, NodeID: idA, Host: "node-" + short(idA)},
	}, grpcB)
	nodeA.waitForInitialSync(t)
	nodeB.waitForInitialSync(t)

	t.Run("horse and observation sync to B", func(t *testing.T) {
		// (а) A создаёт лошадь и наблюдение через REST
		resp := postJSON(t, nodeA.httpBase+"/api/v1/horses", map[string]any{
			"metadata": map[string]string{"region": "Chernobyl"},
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		horse := decode[domain.Horse](t, resp)

		resp = postJSON(t, nodeA.httpBase+"/api/v1/observations", map[string]any{
			"horse_id":    horse.ID,
			"observer_id": "observer-1",
			"location":    "52N 30E",
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)
		obs := decode[domain.Observation](t, resp)

		// B тянет события с A
		sent, received := nodeB.syncOnce(t)
		require.Equal(t, 0, sent)
		require.Equal(t, 2, received)

		// события на B: vector_clock содержит прогресс A, event_count вырос
		info := nodeB.nodeInfo(t)
		require.Equal(t, 2, info.EventCount)

		// проекции на B: лошадь и наблюдение читаются через REST B
		var gotHorse *domain.Horse
		for _, h := range nodeB.listHorses(t) {
			if h.ID == horse.ID {
				gotHorse = &h
			}
		}
		require.NotNil(t, gotHorse, "horse projection missing on B")
		require.Equal(t, "active", gotHorse.IdentityStatus)
		require.Equal(t, "Chernobyl", gotHorse.Metadata["region"])

		var gotObs *domain.Observation
		for _, o := range nodeB.listObservations(t) {
			if o.ID == obs.ID {
				gotObs = &o
			}
		}
		require.NotNil(t, gotObs, "observation projection missing on B")
		require.Equal(t, horse.ID, gotObs.HorseID)
		require.Equal(t, "observer-1", gotObs.ObserverID)
	})

	t.Run("resync does not duplicate", func(t *testing.T) {
		// (б) повторный sync: ничего не едет, счётчики не растут
		sent, received := nodeB.syncOnce(t)
		require.Equal(t, 0, sent)
		require.Equal(t, 0, received)
		info := nodeB.nodeInfo(t)
		require.Equal(t, 2, info.EventCount)
		require.Len(t, nodeB.listHorses(t), 1)
		require.Len(t, nodeB.listObservations(t), 1)
	})

	t.Run("node info exposes sync counters", func(t *testing.T) {
		// (критерий 4) счётчики sync на /api/v1/node
		info := nodeB.nodeInfo(t)
		require.NotEmpty(t, info.Sync)
		require.Equal(t, nodeA.grpcAddr, info.Sync[0].Addr)
		require.Equal(t, 2, info.Sync[0].EventsReceived)
	})

	t.Run("resume after disconnect", func(t *testing.T) {
		// (в) обрыв: A останавливается, перезапускается на тех же портах —
		// B восстанавливает sync (закрытый клиент не мешает новому циклу).
		nodeA.cancel()
		select {
		case <-nodeA.runErr:
		case <-time.After(15 * time.Second):
			t.Fatal("node A did not stop")
		}
		nodeA.start(t) // тот же cfg, тот же порт gRPC
		nodeA.waitForInitialSync(t)

		resp := postJSON(t, nodeA.httpBase+"/api/v1/observations", map[string]any{
			"observer_id": "observer-2",
			"location":    "51N 29E",
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		sent, received := nodeB.syncOnce(t)
		require.Equal(t, 0, sent)
		require.Equal(t, 1, received)
		require.Len(t, nodeB.listObservations(t), 2)

		// повторный цикл после возобновления — без роста
		sent, received = nodeB.syncOnce(t)
		require.Equal(t, 0, sent)
		require.Equal(t, 0, received)
	})

	t.Run("blob travels over grpc", func(t *testing.T) {
		// (г) blob загружен по REST на A и привязан к наблюдению; после обмена
		// событиями B качает blob по потребности (неполный синк), sha256 совпал
		payload := []byte("przewalski-photo-binary-\xf0\x9f\x90\x8e-data")
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		fw, err := mw.CreateFormFile("file", "photo.bin")
		require.NoError(t, err)
		_, err = fw.Write(payload)
		require.NoError(t, err)
		require.NoError(t, mw.Close())

		req, err := http.NewRequest(http.MethodPost, nodeA.httpBase+"/api/v1/blobs", &buf)
		require.NoError(t, err)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		blob := decodeBlob(t, resp)
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// наблюдение на A ссылается на blob: для B он становится потребностью
		resp = postJSON(t, nodeA.httpBase+"/api/v1/observations", map[string]any{
			"observer_id": "observer-3",
			"location":    "51N 30E",
			"blob_ids":    []string{blob.SHA256},
		})
		require.Equal(t, http.StatusCreated, resp.StatusCode)

		// цикл на A: A пушит события blob.registered и observation.added
		nodeA.syncOnce(t)
		// цикл на B: B применяет события и качает недостающий blob по потребности
		nodeB.syncOnce(t)

		// blob доступен на B по REST, содержимое совпадает с исходным
		bresp, err := http.Get(nodeB.httpBase + "/api/v1/blobs/" + blob.SHA256)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, bresp.StatusCode)
		got := readAll(t, bresp.Body)
		bresp.Body.Close()
		require.Equal(t, len(payload), len(got))
		require.Equal(t, fmt.Sprintf("%x", sha256sum(t, payload)), blob.SHA256)
		require.True(t, bytes.Equal(payload, []byte(got)), "blob content mismatch on B")

		// повторный цикл blob-sync — без дублей (дедупликация по sha256)
		nodeA.syncOnce(t)
		bresp2, err := http.Get(nodeB.httpBase + "/api/v1/blobs/" + blob.SHA256)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, bresp2.StatusCode)
		_, _ = io.Copy(io.Discard, bresp2.Body)
		bresp2.Body.Close()
	})

	t.Run("untrusted certificate rejected", func(t *testing.T) {
		// (д) клиент с сертом чужого CA — mTLS-handshake отклонён
		otherCA := newTestCA(t)
		certFile, keyFile, _ := otherCA.writeCertDir(t, t.TempDir(), "01990000-0000-7000-8000-00000000000f", "rogue-node")
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		require.NoError(t, err)
		conn, err := tls.Dial("tcp", nodeA.grpcAddr, &tls.Config{
			Certificates: []tls.Certificate{cert},
			ServerName:   "node-" + short(idA),
			MinVersion:   tls.VersionTLS12,
		})
		if err == nil {
			// рукопожатие не должно завершиться успешно
			conn.Close()
			t.Fatal("handshake with untrusted client certificate succeeded")
		}
	})
}

// TestE2EWrongNodeIDRejected — серт валидного CA, но ожидаемый node_id пира
// не совпадает с тем, что пир отдал в handshake: соединение не используется,
// ошибка фиксируется в счётчиках sync (доверие — не по факту адреса).
func TestE2EWrongNodeIDRejected(t *testing.T) {
	const (
		idA = "01990000-0000-7000-8000-00000000001a"
		idB = "01990000-0000-7000-8000-00000000001b"
	)
	ca := newTestCA(t)
	nodeA := startNode(t, idA, t.TempDir(), ca, nil, "")
	nodeB := startNode(t, idB, t.TempDir(), ca, []config.PeerConfig{
		{Addr: nodeA.grpcAddr, NodeID: idA, Host: "node-" + short(idA)},
	}, "")
	sent, received := nodeB.syncOnce(t)
	require.Equal(t, 0, sent)
	require.Equal(t, 0, received) // пустые ноды — обмен пустой, но handshake прошёл

	// третья нода ждёт от B ЧУЖОЙ node_id: handshake отдаёт реальный id —
	// расхождение должно попасть в last_error, а не в данные.
	nodeC := startNode(t, "01990000-0000-7000-8000-00000000001c", t.TempDir(), ca,
		[]config.PeerConfig{{
			Addr:   nodeB.grpcAddr,
			NodeID: "01990000-0000-7000-8000-0000000000ff",
			Host:   "node-" + short(idB),
		}}, "")
	nodeC.syncOnce(t)
	info := nodeC.nodeInfo(t)
	require.NotEmpty(t, info.Sync)
	require.Contains(t, info.Sync[0].LastError, "node id")
	// при этом у B нет событий от C и наоборот
	require.Equal(t, 0, nodeC.nodeInfo(t).EventCount)
}

// sha256sum — хэш тестовых данных.
func sha256sum(t *testing.T, data []byte) []byte {
	t.Helper()
	sum := sha256.Sum256(data)
	return sum[:]
}
