package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/domain"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	httpapi "github.com/cu3rd/yc-hack-horses/backend/internal/transport/http"

	_ "modernc.org/sqlite"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// inferenceImagePNG — детерминированное синтетическое изображение для тестов.
func inferenceImagePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 120, 240))
	for y := 0; y < 240; y++ {
		var c color.RGBA
		switch {
		case y < 60:
			c = color.RGBA{R: 30, G: 30, B: 30, A: 255}
		case y > 180:
			c = color.RGBA{R: 245, G: 245, B: 245, A: 255}
		default:
			c = color.RGBA{R: 200, G: 160, B: 110, A: 255}
		}
		for x := 0; x < 120; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadInferenceBlob(t *testing.T, data []byte) string {
	t.Helper()
	body := &bytes.Buffer{}
	w := multipart.NewWriter(body)
	fw, err := w.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/blobs", body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	res := httptest.NewRecorder()
	testRoutes.ServeHTTP(res, req)
	if res.Code != http.StatusCreated && res.Code != http.StatusOK {
		t.Fatalf("upload blob: status %d: %s", res.Code, res.Body.String())
	}
	var blob struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &blob); err != nil {
		t.Fatal(err)
	}
	return blob.SHA256
}

// Сценарий ADR-0012: предсказание движком → подтверждение человеком →
// оригинальное предсказание сохранено в raw_predicted_traits.
func TestInferenceTraitsPredictAndConfirm(t *testing.T) {
	sha := uploadInferenceBlob(t, inferenceImagePNG(t))

	// 1. предсказание
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+sha+`"}`))
	res := httptest.NewRecorder()
	testRoutes.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("predict: status %d: %s", res.Code, res.Body.String())
	}
	var pred struct {
		ID           string          `json:"id"`
		ModelVersion string          `json:"model_version"`
		Traits       json.RawMessage `json:"traits"`
		Confidence   float64         `json:"confidence"`
		Fingerprint  string          `json:"fingerprint"`
		Confirmed    bool            `json:"confirmed"`
		Edited       bool            `json:"edited"`
		RawPredicted json.RawMessage `json:"raw_predicted_traits"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &pred); err != nil {
		t.Fatal(err)
	}
	if pred.ModelVersion != "reference-v1" {
		t.Fatalf("model_version = %q", pred.ModelVersion)
	}
	if pred.Confidence <= 0 || pred.Confidence >= 1 {
		t.Fatalf("confidence = %v", pred.Confidence)
	}
	if pred.Fingerprint == "" || pred.Confirmed || pred.Edited {
		t.Fatalf("unexpected snapshot state: %+v", pred)
	}

	// 2. подтверждение человеком с правкой окраса
	confirm := `{"traits":{"coat":"bay","muzzle":"light","markings":"none","scars":"visible"}}`
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/trait_snapshots/"+pred.ID,
		strings.NewReader(confirm))
	res2 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res2, req2)
	if res2.Code != http.StatusOK {
		t.Fatalf("confirm: status %d: %s", res2.Code, res2.Body.String())
	}
	var confirmed struct {
		ID           string          `json:"id"`
		Traits       json.RawMessage `json:"traits"`
		RawPredicted json.RawMessage `json:"raw_predicted_traits"`
		Confirmed    bool            `json:"confirmed"`
		Edited       bool            `json:"edited"`
		Fingerprint  string          `json:"fingerprint"`
	}
	if err := json.Unmarshal(res2.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}

	// 3. оригинальное предсказание НЕ затёрто
	if !confirmed.Confirmed || !confirmed.Edited {
		t.Fatalf("confirmed/edited flags: %+v", confirmed)
	}
	if !strings.Contains(string(confirmed.Traits), `"coat":"bay"`) {
		t.Fatalf("edited traits must contain bay: %s", confirmed.Traits)
	}
	if !strings.Contains(string(confirmed.RawPredicted), `"coat"`) {
		t.Fatalf("raw_predicted_traits must preserve original prediction: %s", confirmed.RawPredicted)
	}
	if confirmed.RawPredicted == nil {
		t.Fatal("raw_predicted_traits must be set")
	}
}

// createObservationForBlob — наблюдение с блобом (для привязки снимка).
func createObservationForBlob(t *testing.T, sha string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/observations",
		strings.NewReader(`{"observer_id":"o1","blob_ids":["`+sha+`"]}`))
	res := httptest.NewRecorder()
	testRoutes.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("observation: status %d: %s", res.Code, res.Body.String())
	}
	var obs struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &obs); err != nil {
		t.Fatal(err)
	}
	return obs.ID
}

func TestInferenceSimilar(t *testing.T) {
	sha := uploadInferenceBlob(t, inferenceImagePNG(t))
	obsID := createObservationForBlob(t, sha)

	// снимок для этого блоба, привязанный к наблюдению
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+sha+`","observation_id":"`+obsID+`"}`))
	res := httptest.NewRecorder()
	testRoutes.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("predict: status %d: %s", res.Code, res.Body.String())
	}
	var pred struct {
		ID          string `json:"id"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &pred); err != nil {
		t.Fatal(err)
	}

	// подтверждаем, чтобы snapshot попал в similar-индекс
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/trait_snapshots/"+pred.ID,
		strings.NewReader(`{"traits":{"coat":"dun","muzzle":"light"}}`))
	res2 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res2, req2)
	if res2.Code != http.StatusOK {
		t.Fatalf("confirm: status %d: %s", res2.Code, res2.Body.String())
	}

	// создаём второй снимок с тем же набором признаков и подтверждаем его
	req3 := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+sha+`"}`))
	res3 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res3, req3)
	if res3.Code != http.StatusCreated {
		t.Fatalf("predict#2: status %d: %s", res3.Code, res3.Body.String())
	}
	var pred2 struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res3.Body.Bytes(), &pred2); err != nil {
		t.Fatal(err)
	}
	req4 := httptest.NewRequest(http.MethodPatch, "/api/v1/trait_snapshots/"+pred2.ID,
		strings.NewReader(`{"traits":{"coat":"dun","muzzle":"light"}}`))
	res4 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res4, req4)
	if res4.Code != http.StatusOK {
		t.Fatalf("confirm#2: status %d: %s", res4.Code, res4.Body.String())
	}

	// поиск похожих: сам снимок исключён, второй найден по fingerprint
	req5 := httptest.NewRequest(http.MethodGet, "/api/v1/inference/similar?blob_id="+sha+"&limit=5", nil)
	res5 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res5, req5)
	if res5.Code != http.StatusOK {
		t.Fatalf("similar: status %d: %s", res5.Code, res5.Body.String())
	}
	var resp struct {
		Disclaimer string `json:"disclaimer"`
		Results    []struct {
			ID string `json:"id"`
		} `json:"results"`
	}
	if err := json.Unmarshal(res5.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Disclaimer == "" || !strings.Contains(resp.Disclaimer, "НЕ биологическая идентичность") {
		t.Fatalf("disclaimer missing: %q", resp.Disclaimer)
	}
	if len(resp.Results) != 1 || resp.Results[0].ID != pred2.ID {
		t.Fatalf("want exactly the sibling snapshot, got %+v", resp.Results)
	}
}

// TestInferenceEngineUnavailable: роутер без Engine → UnavailableEngine →
// 503 с явным сообщением; приложение не блокируется (ADR-0012).
func TestInferenceEngineUnavailable(t *testing.T) {
	dataDir := t.TempDir()
	store, err := sqlstore.Open("sqlite", filepath.Join(dataDir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	if err := migrate.Apply(ctx, store.DB(), "../../migrations", "sqlite"); err != nil {
		t.Fatal(err)
	}
	blobs, err := fsblob.New(filepath.Join(dataDir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	routes := httpapi.NewRouter(httpapi.Deps{Store: store, Blobs: blobs, Hub: httpapi.NewHub(), NodeID: "n1"})

	// регистрируем блоб напрямую в сторах
	sha := sha256Hex(inferenceImagePNG(t))
	if err := blobs.Put(ctx, sha, "image/png", inferenceImagePNG(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertBlob(ctx, domain.Blob{ID: "b1", SHA256: sha, MimeType: "image/png", Size: int64(len(inferenceImagePNG(t)))}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+sha+`"}`))
	res := httptest.NewRecorder()
	routes.ServeHTTP(res, req)
	if res.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 for unavailable engine, got %d: %s", res.Code, res.Body.String())
	}
	if !strings.Contains(res.Body.String(), "model unavailable") {
		t.Fatalf("body must explain model unavailable: %s", res.Body.String())
	}
}

// uniqueInferencePNG — валидное изображение, детерминированное, но уникальное
// для теста (цвет выводится из имени теста): другой sha256 изолирует тест от
// снимков и наблюдений соседних тестов, использующих inferenceImagePNG.
func uniqueInferencePNG(t *testing.T) []byte {
	t.Helper()
	sum := sha256.Sum256([]byte(t.Name()))
	img := image.NewRGBA(image.Rect(0, 0, 120, 240))
	for y := 0; y < 240; y++ {
		for x := 0; x < 120; x++ {
			img.SetRGBA(x, y, color.RGBA{
				R: uint8((int(sum[0]) + x) % 256),
				G: uint8((int(sum[1]) + y) % 256),
				B: uint8((int(sum[2]) + x + y) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// После delete -> ре-аплоад блоба (без нового снимка) поиск похожих по этому
// блобу отдаёт 404: старый снимок привязан к инвалидированному наблюдению и
// выпадает из поиска, а нового снимка нет. Поведение закреплено как ожидаемое.
func TestInferenceSimilarAfterDeleteReupload(t *testing.T) {
	content := uniqueInferencePNG(t)
	sha := uploadInferenceBlob(t, content)
	obsID := createObservationForBlob(t, sha)

	// снимок привязан к наблюдению и подтверждён человеком
	req := httptest.NewRequest(http.MethodPost, "/api/v1/inference/traits",
		strings.NewReader(`{"blob_id":"`+sha+`","observation_id":"`+obsID+`"}`))
	res := httptest.NewRecorder()
	testRoutes.ServeHTTP(res, req)
	if res.Code != http.StatusCreated {
		t.Fatalf("predict: status %d: %s", res.Code, res.Body.String())
	}
	var pred struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &pred); err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPatch, "/api/v1/trait_snapshots/"+pred.ID,
		strings.NewReader(`{"traits":{"coat":"bay","muzzle":"light"}}`))
	res2 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res2, req2)
	if res2.Code != http.StatusOK {
		t.Fatalf("confirm: status %d: %s", res2.Code, res2.Body.String())
	}

	// до удаления поиск похожих работает
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/inference/similar?blob_id="+sha, nil)
	res3 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res3, req3)
	if res3.Code != http.StatusOK {
		t.Fatalf("similar before delete: status %d: %s", res3.Code, res3.Body.String())
	}

	// удаляем блоб и ре-аплоадим тот же файл (revive), без нового снимка
	del := httptest.NewRequest(http.MethodDelete, "/api/v1/blobs/"+sha, nil)
	dres := httptest.NewRecorder()
	testRoutes.ServeHTTP(dres, del)
	if dres.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d: %s", dres.Code, dres.Body.String())
	}
	if again := uploadInferenceBlob(t, content); again != sha {
		t.Fatalf("reupload sha = %s, want %s", again, sha)
	}

	// similar по блобу без нового снимка — 404
	req4 := httptest.NewRequest(http.MethodGet, "/api/v1/inference/similar?blob_id="+sha, nil)
	res4 := httptest.NewRecorder()
	testRoutes.ServeHTTP(res4, req4)
	if res4.Code != http.StatusNotFound {
		t.Fatalf("similar after delete+reupload: want 404, got %d: %s", res4.Code, res4.Body.String())
	}
}
