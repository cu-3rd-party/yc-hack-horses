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
		"/api/v1/blobs/{sha256}":       {"get", "delete"},
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
	// Observation: контракт blob_ids — обе формы ссылок (blobs.id — канонический
	// UUIDv7; sha256 — поддерживаемая альтернатива) + поля инвалидации (blob.deleted).
	// Запрещены взаимоисключающие формулировки: sha256-каноничность и отсечение
	// legacy-ссылок по blobs.id противоречат ядру M1 (blobRefPatterns резолвит обе формы).
	//
	// Пометка о согласованности: описанный контракт резолвинга blobs.id→sha256
	// (blobRefPatterns/таблица blobs) реализуется ядром ветки feat/loop-1; ядро
	// мержится тем же релизом, что и эта спека, поэтому спека и ядро согласованы.
	//
	// canonicalPhrase — точная формулировка каноничности из docs.go; проверка
	// привязана к ней, а не к отдельным словам «канонический»/«blobs.id», чтобы
	// мутационный переворот полярности (каноничен sha256) не проходил тест.
	const canonicalPhrase = "blobs.id (UUIDv7) — канонический контракт"
	forbidden := []string{
		"только для ссылок по sha256",
		"не для legacy",
		"не сопоставляются",
		"канонический контракт — sha256",
		"sha256 — канонический контракт",
		"sha256 — канонический",
		"hex sha256-хэши файлов наблюдения — канонический",
	}
	assertNoForbidden := func(where, text string) {
		for _, bad := range forbidden {
			if strings.Contains(text, bad) {
				t.Fatalf("%s: взаимоисключающая/ложная формулировка %q в описании %q", where, bad, text)
			}
		}
	}

	obsSchema := raw.Components.Schemas["Observation"]
	props, _ := obsSchema["properties"].(map[string]any)
	if props == nil {
		t.Fatal("Observation: нет properties")
	}
	for _, field := range []string{"blob_ids", "invalidated", "invalidated_at", "invalidation_reason"} {
		if _, ok := props[field]; !ok {
			t.Fatalf("Observation: нет поля %s", field)
		}
	}
	blobIDsProp, _ := props["blob_ids"].(map[string]any)
	itemSchema, _ := blobIDsProp["items"].(map[string]any)
	itemDesc, _ := itemSchema["description"].(string)
	for _, form := range []string{"blobs.id", "sha256"} {
		if !strings.Contains(itemDesc, form) {
			t.Fatalf("Observation.blob_ids.items: описание %q не упоминает форму ссылки %q", itemDesc, form)
		}
	}
	obsBlobIDsDesc, _ := blobIDsProp["description"].(string)
	for _, form := range []string{"blobs.id", "sha256"} {
		if !strings.Contains(obsBlobIDsDesc, form) {
			t.Fatalf("Observation.blob_ids.description: описание %q не упоминает форму ссылки %q", obsBlobIDsDesc, form)
		}
	}
	if !strings.Contains(obsBlobIDsDesc, canonicalPhrase) {
		t.Fatalf("Observation.blob_ids.description: %q не фиксирует каноничность blobs.id точной формулировкой %q", obsBlobIDsDesc, canonicalPhrase)
	}
	assertNoForbidden("Observation.blob_ids", itemDesc)
	assertNoForbidden("Observation.blob_ids", obsBlobIDsDesc)

	// Observation.invalidated: инвалидация срабатывает для обеих форм ссылок.
	invalidatedProp, _ := props["invalidated"].(map[string]any)
	invalidatedDesc, _ := invalidatedProp["description"].(string)
	for _, form := range []string{"blobs.id", "sha256"} {
		if !strings.Contains(invalidatedDesc, form) {
			t.Fatalf("Observation.invalidated: описание %q не упоминает форму ссылки %q", invalidatedDesc, form)
		}
	}
	assertNoForbidden("Observation.invalidated", invalidatedDesc)

	// Observation.invalidation_reason: фактический формат "blob deleted: <sha256>"
	// либо "blob deleted: <sha256> (<reason>)".
	reasonProp, _ := props["invalidation_reason"].(map[string]any)
	reasonDesc, _ := reasonProp["description"].(string)
	if !strings.Contains(reasonDesc, "blob deleted: <sha256>") {
		t.Fatalf("Observation.invalidation_reason: описание %q не фиксирует формат \"blob deleted: <sha256>\"", reasonDesc)
	}

	// CreateObservationRequest: та же полярность контракта (blobs.id каноничен, sha256 — альтернатива).
	createObsSchema := raw.Components.Schemas["CreateObservationRequest"]
	createObsProps, _ := createObsSchema["properties"].(map[string]any)
	if createObsProps == nil {
		t.Fatal("CreateObservationRequest: нет properties")
	}
	createBlobIDs, _ := createObsProps["blob_ids"].(map[string]any)
	if createBlobIDs == nil {
		t.Fatal("CreateObservationRequest: нет поля blob_ids")
	}
	createItem, _ := createBlobIDs["items"].(map[string]any)
	createItemDesc, _ := createItem["description"].(string)
	for _, form := range []string{"blobs.id", "sha256"} {
		if !strings.Contains(createItemDesc, form) {
			t.Fatalf("CreateObservationRequest.blob_ids.items: описание %q не упоминает форму ссылки %q", createItemDesc, form)
		}
	}
	createBlobIDsDesc, _ := createBlobIDs["description"].(string)
	if !strings.Contains(createBlobIDsDesc, canonicalPhrase) {
		t.Fatalf("CreateObservationRequest.blob_ids.description: %q не фиксирует каноничность blobs.id точной формулировкой %q", createBlobIDsDesc, canonicalPhrase)
	}
	assertNoForbidden("CreateObservationRequest.blob_ids", createItemDesc)
	assertNoForbidden("CreateObservationRequest.blob_ids", createBlobIDsDesc)

	// DELETE /api/v1/blobs/{sha256}: path-параметр sha256 + query-параметр reason.
	deleteOp := raw.Paths["/api/v1/blobs/{sha256}"]["delete"]
	params, _ := deleteOp["parameters"].([]any)
	var hasReason, hasSha256Path bool
	for _, p := range params {
		pm, _ := p.(map[string]any)
		if pm["name"] == "reason" && pm["in"] == "query" {
			hasReason = true
		}
		if pm["name"] == "sha256" && pm["in"] == "path" {
			hasSha256Path = true
		}
	}
	if !hasReason {
		t.Fatal("DELETE /api/v1/blobs/{sha256}: не описан query-параметр reason")
	}
	if !hasSha256Path {
		t.Fatal("DELETE /api/v1/blobs/{sha256}: не описан path-параметр sha256")
	}

	// DELETE description: инвалидация и удержание файла — для обеих форм ссылок.
	deleteDesc, _ := deleteOp["description"].(string)
	for _, form := range []string{"blobs.id", "sha256"} {
		if !strings.Contains(deleteDesc, form) {
			t.Fatalf("DELETE /api/v1/blobs/{sha256}: описание не упоминает форму ссылки %q", form)
		}
	}
	assertNoForbidden("DELETE /api/v1/blobs/{sha256}", deleteDesc)

	// Загрузчик спеки указывает на локальный эндпоинт.
	if !strings.Contains(pageStr, "data-url=\"/api/scalar/openapi.yaml\"") {
		t.Fatalf("Scalar HTML должен грузить локальную спеку, страница:\n%s", pageStr)
	}
}
