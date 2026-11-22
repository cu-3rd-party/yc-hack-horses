// inference_yolo_test.go: интеграционные тесты эндпоинтов inference с движком
// YOLO (httptest, без сокета; хранилище — sqlite в памяти с миграциями).
package http

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference/yolo"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	migrations "github.com/cu3rd/yc-hack-horses/backend/migrations"
)

// testEnv — изолированное окружение для интеграционных тестов.
type testEnv struct {
	handler http.Handler
	blobs   *fsblob.Store
}

func newTestEnv(t *testing.T, engine func(inference.BlobFetcher) inference.InferenceEngine) *testEnv {
	t.Helper()
	store, err := sqlstore.Open("sqlite", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	require.NoError(t, migrate.ApplyFS(context.Background(), store.DB(), migrations.FS(), "sqlite"))
	blobs, err := fsblob.New(t.TempDir())
	require.NoError(t, err)
	env := &testEnv{handler: NewRouter(Deps{
		Store:      store,
		Blobs:      blobs,
		Engine:     engine(blobs),
		NodeID:     "test-node",
		AppVersion: "test",
	}), blobs: blobs}
	return env
}

// testPNG — маленькое тестовое изображение.
func testPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{R: 160, G: 120, B: 80, A: 255})
		}
	}
	var buf bytes.Buffer
	// ошибка невозможна для RGBA
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// uploadBlob загружает данные через POST /api/v1/blobs и возвращает id.
func (e *testEnv) uploadBlob(t *testing.T, data []byte) string {
	t.Helper()
	var body strings.Builder
	boundary := "testboundary"
	body.WriteString("--" + boundary + "\r\n")
	body.WriteString("Content-Disposition: form-data; name=\"file\"; filename=\"test.png\"\r\n")
	body.WriteString("Content-Type: image/png\r\n\r\n")
	body.Write(data)
	body.WriteString("\r\n--" + boundary + "--\r\n")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blobs", strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var blob struct {
		SHA256 string `json:"sha256"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &blob))
	require.NotEmpty(t, blob.SHA256)
	return blob.SHA256
}

// TestInferenceTraitsYoloUnavailable — движок YOLO без весов/библиотеки:
// эндпоинт отвечает 503 с подсказкой, приложение не блокируется (ADR-0012).
func TestInferenceTraitsYoloUnavailable(t *testing.T) {
	env := newTestEnv(t, func(blobs inference.BlobFetcher) inference.InferenceEngine {
		return yolo.New(yolo.Config{
			ModelPath:      "/nonexistent/yolov8n.onnx",
			RuntimeLibPath: "/nonexistent/libonnxruntime.so",
			Blobs:          blobs,
		})
	})
	blobID := env.uploadBlob(t, testPNG())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+blobID+`"}`))
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code, rec.Body.String())
	require.Contains(t, rec.Body.String(), "model unavailable")
}

// TestInferenceTraitsReferenceCreatesSnapshot — baseline-эндпоинт: снимок
// создаётся с confirmed=false, оригинальное предсказание сохраняется,
// ответ содержит дисклеймер (human-in-the-loop, ADR-0012).
func TestInferenceTraitsReferenceCreatesSnapshot(t *testing.T) {
	env := newTestEnv(t, func(blobs inference.BlobFetcher) inference.InferenceEngine {
		return inference.NewReference(blobs)
	})
	blobID := env.uploadBlob(t, testPNG())
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+blobID+`"}`))
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	var ts struct {
		ID                 string          `json:"id"`
		Confirmed          bool            `json:"confirmed"`
		Edited             bool            `json:"edited"`
		RawPredictedTraits json.RawMessage `json:"raw_predicted_traits"`
		Confidence         float64         `json:"confidence"`
		Disclaimer         string          `json:"disclaimer"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &ts))
	require.False(t, ts.Confirmed) // предзаполнение ждёт подтверждения человеком
	require.False(t, ts.Edited)
	require.NotEmpty(t, ts.ID)
	require.InDelta(t, 0.3, ts.Confidence, 1e-9)
	require.Contains(t, ts.Disclaimer, "подтверждения человеком")

	// PATCH: подтверждение человеком, оригинал сохраняется в raw_predicted_traits
	patch := httptest.NewRequest(http.MethodPatch, "/api/v1/trait_snapshots/"+ts.ID,
		strings.NewReader(`{"traits":{"coat":"dun"}}`))
	rec2 := httptest.NewRecorder()
	env.handler.ServeHTTP(rec2, patch)
	require.Equal(t, http.StatusOK, rec2.Code, rec2.Body.String())
	var confirmed struct {
		Confirmed          bool            `json:"confirmed"`
		Edited             bool            `json:"edited"`
		RawPredictedTraits json.RawMessage `json:"raw_predicted_traits"`
		Confidence         float64         `json:"confidence"`
	}
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &confirmed))
	require.True(t, confirmed.Confirmed)
	require.True(t, confirmed.Edited)
	require.InDelta(t, 1.0, confirmed.Confidence, 1e-9)
	require.NotEmpty(t, confirmed.RawPredictedTraits) // оригинал не затёрт
}

// TestInferenceTraitsInvalidBlob — несуществующий blob_id → 404.
func TestInferenceTraitsInvalidBlob(t *testing.T) {
	env := newTestEnv(t, func(blobs inference.BlobFetcher) inference.InferenceEngine {
		return inference.NewReference(blobs)
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"no-such-blob"}`))
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}

// TestInferenceEngineFromConfig — конфиг валидирует engine=yolo с путями.
func TestInferenceEngineFromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Inference.Engine = "yolo"
	require.Error(t, cfg.Validate()) // без runtime_lib
	cfg.Inference.RuntimeLib = "infra/models/libonnxruntime.so"
	require.NoError(t, cfg.Validate())
	cfg.Inference.ModelPath = "infra/models/yolov8n.onnx"
	require.NoError(t, cfg.Validate())
}
