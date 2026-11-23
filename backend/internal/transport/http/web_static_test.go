package http

// web_static_test.go: статика MPA (ADR-0015) — маршруты html-входов и
// Cache-Control (PR-ревью #35): html без кэша, /assets/ — immutable.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
)

func webStaticRouter(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("index.html", "<!doctype html><title>ЛошID</title>")
	write("system.html", "<!doctype html><title>ЛошID · Система</title>")
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join("assets", "main-abc123.js"), "// js")
	st, err := sqlstore.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	srv := httptest.NewServer(NewRouter(Deps{
		Store:      st,
		NodeID:     "test-node",
		RoleHint:   "local",
		AppVersion: "test",
		WebDir:     dir,
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebStaticCacheControl — html: no-cache (эвристическое кэширование html
// после релиза ссылается на старые hashed-имена → 404), /assets/: immutable.
func TestWebStaticCacheControl(t *testing.T) {
	srv := webStaticRouter(t)
	cases := []struct {
		path, want string
	}{
		{"/", "no-cache"},
		{"/index.html", "no-cache"},
		{"/system.html", "no-cache"},
		{"/assets/main-abc123.js", "public, max-age=31536000, immutable"},
	}
	for _, tc := range cases {
		resp, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatalf("GET %s: %v", tc.path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: ожидался 200, получен %d", tc.path, resp.StatusCode)
		}
		if got := resp.Header.Get("Cache-Control"); got != tc.want {
			t.Errorf("GET %s: Cache-Control=%q, ожидалось %q", tc.path, got, tc.want)
		}
		resp.Body.Close()
	}
}
