// Package http: REST/WS транспорт ноды (chi). Роутер собирается из Deps,
// поэтому тесты поднимают его в процессе без сокета.
package http

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/comparison"
	"github.com/cu3rd/yc-hack-horses/backend/internal/enroll"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/reid"
	"github.com/cu3rd/yc-hack-horses/backend/internal/modelassets"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// Deps — зависимости транспортного слоя.
type Deps struct {
	ComparisonModel        comparison.Analyzer
	// CushkiHead — конфиг CUshki v6 (fusion_config.json); nil — скоринг
	// без калибровки (дефолтное поведение).
	CushkiHead *reid.CushkiConfig
	Models                 *modelassets.Manager
	ModelMode, RuntimePath string
	Store                  *sqlstore.Store
	Blobs                  storage.BlobStore
	Hub                    *Hub
	NodeID                 string
	RoleHint               string
	AppVersion             string
	// Engine — локальный CV-движок (ADR-0012); nil = UnavailableEngine:
	// приложение не блокируется, признаки вводит человек.
	Engine inference.InferenceEngine
	// Clock подменяется в тестах; nil = time.Now.
	Clock func() time.Time
	// SyncStats — снапшот счётчиков sync-движка (events_sent/received/last_sync_at
	// по каждому пиру); nil, если sync-цикл не запущен. Отдаётся на /api/v1/node.
	SyncStats func() any
	// BlobStats — срез состояния блобов (referenced/present/missing) для
	// /api/v1/node; nil, если blob-синхронизация не запущена.
	BlobStats func() any
	// Enrollment — сервис выдачи клиентских сертификатов (ADR-0007
	// amended, ADR-0014). nil — enrollment выключен: POST /api/v1/enroll
	// отвечает 404. Конфигурируется только на central-ноде.
	Enrollment *enroll.Service
	// DocsEnabled — включить Scalar UI (/api/scalar) и OpenAPI-спеку.
	// Включается только на central-ноде (api.docs_enabled / API_DOCS_ENABLED).
	DocsEnabled bool
	WebDir      string
}

// NewRouter собирает chi-роутер со всеми маршрутами v1.
func NewRouter(d Deps) http.Handler {
	if d.Clock == nil {
		d.Clock = time.Now
	}
	if d.Hub == nil {
		d.Hub = NewHub()
	}
	if d.Engine == nil {
		d.Engine = inference.NewUnavailable() // ADR-0012: без модели приложение не блокируется
	}
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLogger)
	r.Use(middleware.Recoverer)

	if d.WebDir != "" {
		files := http.FileServer(http.Dir(d.WebDir))
		// MPA (ADR-0015): каждая страница — отдельный html-вход в корне dist.
		// Маршруты перечислены явно, чтобы не перекрыть /health, /readyz,
		// /docs и /api (chi: статические маршруты приоритетнее параметров,
		// но явный список безопаснее и 404 без SPA-fallback сохраняется).
		routes := []string{
			"/",
			"/gallery.html", "/observations.html", "/system.html",
			"/assets/{file}", "/pdfjs/{group}/{file}", "/photos/{file}", "/licenses/{file}",
		}
		for _, route := range routes {
			r.Get(route, files.ServeHTTP)
			r.Head(route, files.ServeHTTP)
		}
	}

	r.Get("/health", handleHealth)
	r.Get("/readyz", handleReady(d))
	if d.DocsEnabled {
		mountDocs(r)
	}

	r.Route("/api/v1", func(v1 chi.Router) {
		v1.Get("/horses", listHorses(d))
		v1.Post("/horses", createHorse(d))
		v1.Patch("/horses/{id}", patchHorse(d))
		v1.Get("/horses/{id}", getHorse(d))
		v1.Put("/horses/{id}/passport", putHorsePassport(d))
		v1.Get("/horses/{id}/passport", getHorsePassport(d))

		v1.Get("/observations", listObservations(d))
		v1.Post("/observations", createObservation(d))
		v1.Get("/models", modelStatus(d))
		v1.Post("/models/{id}/{action}", changeModel(d))
		v1.Post("/comparisons", createComparison(d))
		v1.Put("/comparisons/{id}/detections/{detection}/decision", decideComparison(d))
		v1.Post("/comparisons/{id}/cancel", cancelComparison(d))

		v1.Post("/blobs", uploadBlob(d))
		v1.Get("/blobs", listBlobs(d))
		v1.Get("/blobs/{sha256}", getBlob(d))
		v1.Delete("/blobs/{sha256}", deleteBlob(d))

		// CV-inference (ADR-0012): предсказание, подтверждение человеком, поиск похожих.
		v1.Post("/inference/traits", postInferenceTraits(d))
		v1.Patch("/trait_snapshots/{id}", patchTraitSnapshot(d))
		v1.Get("/inference/similar", getSimilarTraits(d))

		v1.Post("/enroll", handleEnroll(d))

		v1.Get("/node", nodeInfo(d))
		v1.Get("/events", streamEvents(d))
	})

	// JSON 404/405 вместо текстовых
	r.NotFound(notFoundJSON)
	r.MethodNotAllowed(methodNotAllowedJSON)
	return r
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		next.ServeHTTP(ww, r)
		slog.Debug("http request",
			"method", r.Method, "path", r.URL.Path, "status", ww.Status(),
			"bytes", ww.BytesWritten(), "duration", time.Since(start).String())
	})
}

func handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func handleReady(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := contextWithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := d.Store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	}
}

func notFoundJSON(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"message": "not found"})
}

func methodNotAllowedJSON(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"message": "method not allowed"})
}

// --- вспомогательные функции ответов/ошибок ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"message": msg})
}

// notFoundOrServerError повторяет соглашение шаблона: 404 для storage.ErrNotFound, иначе 500.
func notFoundOrServerError(w http.ResponseWriter, err error, what string) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+" not found")
		return
	}
	slog.Error("internal error", "what", what, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}
