// Package app: сборка и жизненный цикл ноды (storage + transport + graceful shutdown).
//
// Запуск (cmd/node web):
//   - HTTP REST/WS на network.listen_addr;
//   - gRPC-сервер sync на network.grpc_listen_addr (mTLS из security.* при
//     сконфигурированных сертификатах; иначе plain — только loopback, см. grpc.go);
//   - sync-цикл движка internal/sync по конфигу sync.enabled/interval:
//     статические пиры (sync.peers), discovery-кандидаты (network.mdns_enabled)
//     и central (sync.central_url, с VerifyCentralIdentity);
//   - mDNS-анонс своей ноды и поиск пиров (internal/transport/discovery).
package app

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/enroll"
	"github.com/cu3rd/yc-hack-horses/backend/internal/identity"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
	"github.com/cu3rd/yc-hack-horses/backend/internal/modelassets"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	syncengine "github.com/cu3rd/yc-hack-horses/backend/internal/sync"
	"github.com/cu3rd/yc-hack-horses/backend/internal/transport/discovery"
	httpapi "github.com/cu3rd/yc-hack-horses/backend/internal/transport/http"

	"google.golang.org/grpc"
)

// syncEnabledForCert проверяет, что клиентский серт ноды несёт node-identity
// (URI SAN) — иначе нода не может пройти caps-проверку как пир и исходящий
// sync бессмыслен (см. security.CapabilitiesForCert). Серт может быть не
// сконфигурирован (plain-режим) — sync в этом случае не запрещаем: проверка
// происходит на стороне пира.
func syncEnabledForCert(certFile string) bool {
	if certFile == "" {
		return true // без серта решение остаётся за пиром (loopback/plain)
	}
	pemBytes, err := os.ReadFile(certFile)
	if err != nil {
		slog.Warn("sync: чтение клиентского серта", "err", err)
		return true // пусть sync попытается и получит понятную ошибку
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return true
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return true
	}
	_, idErr := identity.NodeIDFromCert(cert)
	return idErr == nil
}

// App — собранная нода.
type App struct {
	cfg   *config.Config
	store *sqlstore.Store
	blobs *fsblob.Store
	hub   *httpapi.Hub
	srv   *http.Server

	nodeID string
	engine *syncengine.Engine
	pstore *projectedStore
	sync   *syncRuntime

	grpcSrv *grpc.Server
	grpcLn  net.Listener
	// muxGRPCSrv — gRPC-сервер для h2c-мультиплекс-порта (grpc_multiplex):
	// TLS терминирует ingress, аутентификация — по серту из заголовка.
	muxGRPCSrv *grpc.Server

	announcer *discovery.Announcer
	// bg хранит функции остановки фоновых контуров (sync, discovery, gRPC).
	mu    sync.Mutex
	bg    []func()
	doneW sync.WaitGroup
}

// New открывает хранилище, применяет миграции и собирает все компоненты ноды.
// Слушающие сокеты не открываются — их открывает Run (см. startGRPC).
func New(cfg *config.Config) (*App, error) {
	if err := os.MkdirAll(cfg.Node.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("app: mkdir data dir: %w", err)
	}
	store, err := sqlstore.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return nil, err
	}
	if err := migrate.Apply(context.Background(), store.DB(), migrationsDir(cfg), cfg.Storage.Driver); err != nil {
		store.Close() //nolint:errcheck // ошибка миграции важнее
		return nil, err
	}
	blobs, err := fsblob.New(filepath.Join(cfg.Node.DataDir, "blobs"))
	if err != nil {
		store.Close() //nolint:errcheck // ошибка сборки важнее
		return nil, err
	}
	nodeID, err := loadOrCreateNodeID(cfg)
	if err != nil {
		store.Close() //nolint:errcheck // ошибка сборки важнее
		return nil, err
	}
	if err := validateGRPCListenAddr(cfg); err != nil {
		store.Close() //nolint:errcheck // ошибка сборки важнее
		return nil, err
	}

	identity := syncengine.NodeIdentity{
		NodeID:     nodeID,
		Role:       cfg.Node.RoleHint,
		AppVersion: cfg.AppVersion,
	}
	// Движок пишет удалённые события через projectedStore: Append + проекции
	// в одной транзакции (локальные события проектируются HTTP-хендлерами).
	pstore := newProjectedStore(store, blobs)
	engine := syncengine.NewEngine(pstore, blobs, identity, syncengine.Config{
		BatchLimit: cfg.Sync.BatchSize,
		// Неполный синк (issue #17): качаем только blob'ы, на которые ссылается
		// эта нода, и не больше MaxBlobsPerSync за цикл — датасет 16ГБ целиком
		// по всем устройствам не гоняем.
		NeededBlobHashes: func(ctx context.Context) ([]string, error) {
			// Хэши активных наблюдений этой ноды: инвалидированные (фото
			// удалено событием blob.deleted) в потребности не входят — их
			// записи живут только в истории, а файлы пирам качать незачем.
			return syncengine.ReferencedBlobHashes(ctx, store)
		},
	}, slog.Default())

	a := &App{cfg: cfg, store: store, blobs: blobs, nodeID: nodeID, engine: engine, pstore: pstore}
	a.sync = newSyncRuntime(cfg, engine, identity, slog.Default())
	a.hub = httpapi.NewHub()
	a.sync.setStatsPublisher(func(stats []PeerStats) { a.publishSyncStats(stats) })

	// CV-движок (ADR-0012): по умолчанию UnavailableEngine — приложение не
	// блокируется без модели; reference — baseline-эвристика на blobs;
	// yolo — детекция YOLO ONNX + кропы (ADR-0012, V1). Движок yolo лениво
	// грузит модель: без весов/библиотеки деградирует в ErrModelUnavailable.
	var cvEngine inference.InferenceEngine = inference.NewUnavailable()
	var comparisonModel comparison.Analyzer
	var assets *modelassets.Manager
	modelMode := "disabled"
	switch cfg.Inference.Engine {
	case "reference":
		cvEngine = inference.NewReference(blobs)
	case "yolo":
		modelPath := cfg.Inference.ModelPath
		if modelPath == "" {
			modelPath = filepath.Join(cfg.Node.DataDir, "models", "yolov8n.onnx")
		}
		partsDir := cfg.Inference.ReIDModelDir
		if partsDir == "" {
			partsDir = filepath.Join(cfg.Node.DataDir, "models")
		}
		detector := yolo.New(yolo.Config{
			ModelPath:      modelPath,
			RuntimeLibPath: cfg.Inference.RuntimeLib,
			ReIDModelDir:   cfg.Inference.ReIDModelDir,
			IncludeMasks:   true,
			Blobs:          blobs,
		})
		cvEngine = detector
		comparisonModel = comparison.LocalModel{Detector: detector}
		if cfg.Inference.ComparisonURL == "" {
			modelMode = "local"
			host, _, _ := net.SplitHostPort(cfg.Network.ListenAddr)
			// Пустой хост (":8080" — все интерфейсы, кейс central в self-service)
			// тоже разрешаем: иначе менеджер моделей не создаётся и
			// /api/v1/models навсегда отдаёт ready=false.
			if ip := net.ParseIP(host); host == "localhost" || host == "" || host == "0.0.0.0" || host == "::" || (ip != nil && ip.IsLoopback()) {
				detector.ModelsChanged(partsDir)
				assets = modelassets.New(modelPath, partsDir, cfg.Inference.ModelBaseURL, func() { detector.ModelsChanged(partsDir) })
				// Веса не запечены в образ — тянем недостающие сразу при старте
				// в $DATA_DIR/models (live on PVC у центральной ноды).
				assets.AutoDownload()
				a.addStop(assets.Close)
			}
		}
		a.addStop(func() { _ = detector.Close() })
	}
	if cfg.Inference.ComparisonURL != "" {
		modelMode = "external"
		comparisonModel = comparison.ModelEndpoint{URL: cfg.Inference.ComparisonURL}
	}
	// Enrollment-сервис (ADR-0014): только при enrollment.enabled —
	// конфигурация central. Ошибка конфигурации не должна валить ноду
	// без enrollment: сервис не собирается, эндпоинт остаётся выключен.
	var enrollSvc *enroll.Service
	if cfg.Enrollment.Enabled {
		svc, err := enroll.NewService(enroll.ServerConfig{
			Token:     cfg.Enrollment.Token,
			CAFile:    cfg.Security.CAFile,
			CAKeyFile: cfg.Enrollment.CAKeyFile,
			CertDays:  cfg.Enrollment.CertDays,
			DataDir:   cfg.Node.DataDir,
		})
		if err != nil {
			slog.Error("enroll: сервис не запущен", "err", err)
		} else {
			enrollSvc = svc
		}
	}
	handler := httpapi.NewRouter(httpapi.Deps{
		Store:           store,
		Blobs:           blobs,
		Hub:             a.hub,
		NodeID:          nodeID,
		RoleHint:        cfg.Node.RoleHint,
		AppVersion:      cfg.AppVersion,
		SyncStats:       a.syncStatsSnapshot,
		BlobStats:       a.blobStatsSnapshot,
		Engine:          cvEngine,
		ComparisonModel: comparisonModel,
		Models:          assets, ModelMode: modelMode, RuntimePath: cfg.Inference.RuntimeLib,
		DocsEnabled: cfg.Api.DocsEnabled,
		WebDir:      cfg.Api.WebDir,
		Enrollment:  enrollSvc,
	})
	srvHandler := http.Handler(handler)
	if cfg.Network.GRPCMultiplex {
		muxSrv, err := newMuxGRPCServer(cfg, identity, a.pstore, a.blobs, slog.Default())
		if err != nil {
			store.Close() //nolint:errcheck // ошибка сборки важнее
			return nil, err
		}
		a.muxGRPCSrv = muxSrv
		wrapped, err := newMuxHandler(muxSrv, handler)
		if err != nil {
			store.Close() //nolint:errcheck // ошибка сборки важнее
			return nil, err
		}
		srvHandler = wrapped
		a.addStop(func() { muxSrv.GracefulStop() })
		slog.Info("grpc multiplex enabled", "addr", cfg.Network.ListenAddr)
	}
	a.srv = &http.Server{
		Addr:              cfg.Network.ListenAddr,
		Handler:           srvHandler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return a, nil
}

// blobStatsSnapshot — срез состояния блобов для /api/v1/node: сколько ссылок
// из активных наблюдений и сколько из них ещё не на диске (неполный синк).
// Потребности считает тот же хелпер, что и движок синка (ReferencedBlobHashes):
// инвалидированные наблюдения не в счёт, а blob_ids уже приведены к sha256, так
// что срез и движок не расходятся на пустых/legacy-записях.
func (a *App) blobStatsSnapshot() any {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hashes, err := syncengine.ReferencedBlobHashes(ctx, a.store)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	var valid []string
	for _, h := range hashes {
		if storage.ValidSha256Hex(h) {
			valid = append(valid, h)
		}
	}
	missing, err := a.blobs.ListMissing(ctx, valid)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return map[string]any{
		"referenced": len(hashes),
		"present":    len(valid) - len(missing),
		"missing":    len(missing),
	}
}

// migrationsDir ищет каталог migrations/ рядом с бинарником и в рабочей директории.
func migrationsDir(cfg *config.Config) string {
	candidates := []string{
		os.Getenv("MIGRATIONS_DIR"),
		"migrations",
		filepath.Join(filepath.Dir(os.Args[0]), "migrations"),
	}
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(dir, cfg.Storage.Driver)); err == nil && info.IsDir() {
			return dir
		}
	}
	return "migrations"
}

// loadOrCreateNodeID читает <data_dir>/node.json или создаёт новый UUIDv7.
func loadOrCreateNodeID(cfg *config.Config) (string, error) {
	if cfg.Node.ID != "" {
		return cfg.Node.ID, nil
	}
	path := filepath.Join(cfg.Node.DataDir, "node.json")
	if data, err := os.ReadFile(path); err == nil {
		var stored struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &stored) == nil && stored.ID != "" {
			return stored.ID, nil
		}
	}
	// uuid.NewV7 не падает на Linux (источник энтропии ОС)
	id, err := newUUIDv7()
	if err != nil {
		return "", fmt.Errorf("app: generate node id: %w", err)
	}
	data, _ := json.MarshalIndent(map[string]string{"id": id}, "", "  ")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("app: persist node id: %w", err)
	}
	slog.Info("node id generated", "node_id", id, "path", path)
	return id, nil
}

// Run запускает все контуры ноды и останавливает их по отмене ctx
// (SIGINT/SIGTERM в cmd). HTTP и gRPC слушают в отдельных горутинах.
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http listening", "addr", a.srv.Addr, "driver", a.cfg.Storage.Driver)
		errCh <- a.srv.ListenAndServe()
	}()

	if err := a.startGRPC(); err != nil {
		_ = a.srv.Close()
		return err
	}
	if a.cfg.Sync.Enabled && !syncEnabledForCert(a.cfg.Security.CertFile) {
		// Серт без URI SAN node_id не может аутентифицировать ноду как пира
		// (CapabilitiesForCert → CapNone): sync-цикл запускать бессмысленно.
		// Так бывает у central-нод за ingress, чей серт — только серверный
		// (DNS SAN); иначе центр вечно дёргает сам себя и пишет Unauthenticated.
		slog.Info("sync loop disabled: client cert has no node identity (URI SAN)")
	} else if a.cfg.Sync.Enabled {
		// central — исходящий sync: адрес из sync.central_url, доверие по
		// VerifyCentralIdentity (hostname + серверный серт), см. sync.go.
		if a.cfg.Sync.CentralURL != "" {
			a.sync.addCentral(centralDialAddr(a.cfg.Sync.CentralURL))
		}
		syncCtx, cancel := context.WithCancel(context.Background())
		a.addStop(cancel)
		a.doneW.Add(1)
		go func() {
			defer a.doneW.Done()
			a.sync.runLoop(syncCtx)
		}()
		slog.Info("sync loop started", "interval", a.cfg.Sync.Interval.String(),
			"central_url", a.cfg.Sync.CentralURL, "static_peers", len(a.cfg.Sync.Peers))
	}
	if a.cfg.Network.MdnsEnabled {
		if err := a.startDiscovery(); err != nil {
			slog.Warn("mdns discovery disabled", "err", err)
		}
	}
	a.startBlobReaper()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			a.stopBackground()
			// Дожидаемся фоновых горутин перед выходом: closed DB + работающий
			// рипер/синк — источник паники и потерянных записей при остановке.
			a.doneW.Wait()
			return err
		}
		return nil
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := a.srv.Shutdown(shutdownCtx)
	// Останавливаем фоновые контуры и ждём горутины в любом случае, даже если
	// Shutdown вернул ошибку: closed DB + работающий рипер/синк — источник
	// паники и потерянных записей при остановке.
	a.stopBackground()
	a.doneW.Wait()
	// errCh читаем только при успешном Shutdown. Если Shutdown не уложился в
	// таймаут, сервер мог не остановиться, и ListenAndServe ещё не вернулся —
	// безусловное чтение errCh подвесило бы Run. При ошибке Shutdown она и так
	// информативнее ошибки сервера, поэтому сразу её и возвращаем.
	var serveErr error
	if shutdownErr == nil {
		serveErr = <-errCh
	}
	if shutdownErr != nil {
		return fmt.Errorf("app: shutdown: %w", shutdownErr)
	}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	if err := a.store.Close(); err != nil {
		return fmt.Errorf("app: close storage: %w", err)
	}
	slog.Info("node stopped")
	return nil
}

// addStop регистрирует функцию остановки фонового контура.
func (a *App) addStop(fn func()) {
	a.mu.Lock()
	a.bg = append(a.bg, fn)
	a.mu.Unlock()
}

func (a *App) stopBackground() {
	a.mu.Lock()
	bgs := a.bg
	a.bg = nil
	a.mu.Unlock()
	for _, fn := range bgs {
		fn()
	}
}

// startGRPC открывает сокет network.grpc_listen_addr и запускает SyncService.
func (a *App) startGRPC() error {
	grpcSrv, err := newGRPCServer(a.cfg, syncengine.NodeIdentity{
		NodeID:     a.nodeID,
		Role:       a.cfg.Node.RoleHint,
		AppVersion: a.cfg.AppVersion,
	}, a.pstore, a.blobs, slog.Default())
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", a.cfg.Network.GRPCListenAddr)
	if err != nil {
		return fmt.Errorf("app: grpc listen %s: %w", a.cfg.Network.GRPCListenAddr, err)
	}
	a.grpcSrv = grpcSrv
	a.grpcLn = ln
	slog.Info("grpc listening", "addr", ln.Addr().String(), "mtls", tlsEnabled(a.cfg))
	a.doneW.Add(1)
	go func() {
		defer a.doneW.Done()
		_ = grpcSrv.Serve(ln)
	}()
	a.addStop(func() { grpcSrv.GracefulStop() })
	return nil
}

// startDiscovery регистрирует ноду в mDNS и ищет пиров; найденные адреса
// идут в реестр кандидатов движка (доверие — только mTLS, не discovery).
func (a *App) startDiscovery() error {
	_, port, err := net.SplitHostPort(a.cfg.Network.GRPCListenAddr)
	if err != nil {
		return err
	}
	var grpcPort int
	if _, err := fmt.Sscanf(port, "%d", &grpcPort); err != nil {
		return fmt.Errorf("app: mdns grpc port: %w", err)
	}
	announcer, err := discovery.Announce(a.nodeID, grpcPort)
	if err != nil {
		return err
	}
	a.announcer = announcer
	a.addStop(func() { announcer.Shutdown() })

	browser := discovery.NewBrowser(a.cfg.Network.ServiceName)
	discCtx, cancel := context.WithCancel(context.Background())
	a.addStop(cancel)
	a.doneW.Add(1)
	go func() {
		defer a.doneW.Done()
		browser.Run(discCtx, a.cfg.Sync.Interval, func(peer discovery.PeerFound) {
			if peer.NodeID == a.nodeID {
				return
			}
			a.sync.addDiscoveredPeer(peer.NodeID, peer.Addr)
			slog.Debug("discovery: peer candidate", "node_id", peer.NodeID, "addr", peer.Addr)
		})
	}()
	return nil
}

// startBlobReaper запускает фоновый рипер файлов удалённых блобов: при старте
// и далее с фиксированным интервалом он прогоняет мягко удалённые блобы через
// storage.PruneBlobs. Закрывает дыру, когда нода упала между коммитом проекции
// blob.deleted и физическим удалением файла (повторного события не будет —
// журнал дедуплицируется по node_id+sequence). Локальные блобы по-прежнему
// чистятся сразу в HTTP-хендлере; рипер — страховка от крэша.
func (a *App) startBlobReaper() {
	reaperCtx, cancel := context.WithCancel(context.Background())
	a.addStop(cancel)
	// sqlstore.Store сам реализует DeletedBlobLister/BlobReferencer/…:
	// SQL остаётся в sqlstore, контрактный пакет storage — только интерфейсы.
	a.doneW.Add(1)
	go func() {
		defer a.doneW.Done()
		storage.RunBlobReaper(reaperCtx, a.store, a.store, a.blobs, slog.Default())
	}()
	slog.Info("blob reaper started", "interval", storage.BlobReaperInterval.String())
}

// syncStatsSnapshot — значение для /api/v1/node (nil-безопасно).
func (a *App) syncStatsSnapshot() any { return a.sync.StatsSnapshot() }

// publishSyncStats отправляет кадр счётчиков sync в WS.
func (a *App) publishSyncStats(stats []PeerStats) {
	frame, err := json.Marshal(map[string]any{"type": "sync_stats", "peers": stats})
	if err != nil {
		slog.Error("marshal sync stats frame", "err", err)
		return
	}
	a.hub.Publish(frame)
}

// SyncNow выполняет один проход цикла sync по всем пирам и возвращает
// суммарные отправленные/принятые события. Используется тестами e2e и
// ручными триггерами; обычный ритм задаёт runLoop.
func (a *App) SyncNow(ctx context.Context) (sent int, received int, err error) {
	before := a.sync.StatsSnapshot()
	a.sync.runCycle(ctx)
	after := a.sync.StatsSnapshot()
	sum := func(ss []PeerStats) (int, int) {
		var s, r int
		for _, st := range ss {
			s += st.EventsSent
			r += st.EventsReceived
		}
		return s, r
	}
	sentBefore, recvBefore := sum(before)
	sent, received = sum(after)
	return sent - sentBefore, received - recvBefore, nil
}

// Migrate применяет миграции и закрывает хранилище (команда migrate).
func Migrate(cfg *config.Config) error {
	store, err := sqlstore.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return err
	}
	defer store.Close()
	return migrate.Apply(context.Background(), store.DB(), migrationsDir(cfg), cfg.Storage.Driver)
}
