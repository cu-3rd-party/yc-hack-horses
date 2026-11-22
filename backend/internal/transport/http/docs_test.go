package http

// docs_test.go: Scalar-эндпоинты монтируются только при DocsEnabled; спека —
// валидный OpenAPI 3, содержащий все фактические маршруты роутера.

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	"gopkg.in/yaml.v3"
)

// newDocsStore — sqlite-хранилище в памяти для Deps без реальных обработчиков
// (docs-эндпоинты Store не читают, но Deps.Store нужен NewRouter).
func newDocsStore(t *testing.T) *sqlstore.Store {
	t.Helper()
	st, err := sqlstore.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func docsRouter(store *sqlstore.Store, docs bool) *httptest.Server {
	srv := httptest.NewServer(NewRouter(Deps{
		Store:       store,
		NodeID:      "test-node",
		RoleHint:    "local",
		AppVersion:  "test",
		DocsEnabled: docs,
	}))
	return srv
}

// TestScalarDisabled404 — без флага эндпоинты документации отсутствуют (404).
func TestScalarDisabled404(t *testing.T) {
	st := newDocsStore(t)
	srv := docsRouter(st, false)
	defer srv.Close()

	for _, path := range []string{"/api/scalar", "/api/scalar/openapi.yaml"} {
		resp, err := srv.Client().Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatalf("GET %s: ожидался 404 без флага, получен %d", path, resp.StatusCode)
		}
	}
}

// TestScalarEnabled — с флагом: HTML-страница и валидный OpenAPI 3 YAML.
func TestScalarEnabled(t *testing.T) {
	st := newDocsStore(t)
	srv := docsRouter(st, true)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/scalar")
	if err != nil {
		t.Fatalf("GET /api/scalar: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("GET /api/scalar: ожидался 200, получен %d", resp.StatusCode)
	}
	page, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("GET /api/scalar: %v", err)
	}
	pageStr := string(page)
	if !strings.Contains(pageStr, `id="api-reference"`) || !strings.Contains(pageStr, "data-url=") {
		t.Fatalf("Scalar HTML не содержит загрузчик спеки:\n%s", pageStr)
	}

	resp2, err := srv.Client().Get(srv.URL + "/api/scalar/openapi.yaml")
	if err != nil {
		t.Fatalf("GET openapi.yaml: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("GET openapi.yaml: ожидался 200, получен %d", resp2.StatusCode)
	}
	if ct := resp2.Header.Get("Content-Type"); !strings.Contains(ct, "yaml") {
		t.Fatalf("Content-Type спеки: %q, ожидался yaml", ct)
	}

	// Спека — валидный OpenAPI 3 (парсинг YAML + структурные проверки).
	var raw struct {
		OpenAPI    string                               `yaml:"openapi"`
		Paths      map[string]map[string]map[string]any `yaml:"paths"`
		Components struct {
			Schemas map[string]map[string]any `yaml:"schemas"`
		} `yaml:"components"`
	}
	specBytes, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	if err := yaml.Unmarshal(specBytes, &raw); err != nil {
		t.Fatalf("openapi.yaml не валидный YAML: %v", err)
	}
	if !strings.HasPrefix(raw.OpenAPI, "3.") {
		t.Fatalf("openapi.yaml: версия %q, ожидалась 3.x", raw.OpenAPI)
	}
	for _, schema := range []string{"Horse", "Observation", "TraitSnapshot", "Blob", "NodeInfo", "Error", "ComparisonResult", "ComparisonDecision", "CancelResult", "ObservationPhoto"} {
		if _, ok := raw.Components.Schemas[schema]; !ok {
			t.Fatalf("openapi.yaml: нет схемы %s", schema)
		}
	}
	// Все фактические маршруты роутера должны быть описаны.
	wantPaths := []string{
		"/health", "/readyz",
		"/api/v1/horses", "/api/v1/horses/{id}",
		"/api/v1/observations",
		"/api/v1/blobs", "/api/v1/blobs/{sha256}",
		"/api/v1/node",
		"/api/v1/events",
		"/api/v1/inference/traits", "/api/v1/trait_snapshots/{id}", "/api/v1/inference/similar",
	}
	for _, p := range wantPaths {
		item, ok := raw.Paths[p]
		if !ok || len(item) == 0 {
			t.Fatalf("openapi.yaml: нет пути %s", p)
		}
	}
	// Методы на ключевых путях.
	for path, methods := range map[string][]string{
		"/api/v1/horses":                  {"get", "post"},
		"/api/v1/horses/{id}":             {"get", "patch"},
		"/api/v1/observations":            {"get", "post"},
		"/api/v1/observations/{id}":       {"delete"},
		"/api/v1/comparisons":             {"post"},
		"/api/v1/comparisons/{id}/cancel": {"post"},
		"/api/v1/comparisons/{id}/detections/{detection}/decision": {"put"},
		"/api/v1/blobs":                {"get", "post"},
		"/api/v1/blobs/{sha256}":       {"get"},
		"/api/v1/inference/traits":     {"post"},
		"/api/v1/trait_snapshots/{id}": {"patch"},
		"/api/v1/inference/similar":    {"get"},
	} {
		item := raw.Paths[path]
		for _, m := range methods {
			if _, ok := item[m]; !ok {
				t.Fatalf("openapi.yaml: путь %s без метода %s", path, m)
			}
		}
	}
	// Загрузчик спеки указывает на локальный эндпоинт.
	if !strings.Contains(pageStr, "data-url=\"/api/scalar/openapi.yaml\"") {
		t.Fatalf("Scalar HTML должен грузить локальную спеку, страница:\n%s", pageStr)
	}
}
