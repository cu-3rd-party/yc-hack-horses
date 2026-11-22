// TestMain: поднимаем роутер на временной sqlite-базе и каталоге блобов.
package integration

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/inference"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	httpapi "github.com/cu3rd/yc-hack-horses/backend/internal/transport/http"

	// регистрация драйвера sqlite в database/sql
	_ "modernc.org/sqlite"
)

var (
	testRoutes http.Handler
	// testStore — тот же стор, что у роутера: тестам нужен прямой доступ к
	// журналу (например, подсчёт событий по event_type).
	testStore *sqlstore.Store
)

func TestMain(m *testing.M) {
	dataDir, err := os.MkdirTemp("", "integration-data-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dataDir)

	cfg := config.Default()
	cfg.Node.ID = "01900000-0000-7000-8000-000000000001"
	cfg.Node.DataDir = dataDir
	cfg.Storage.DSN = filepath.Join(dataDir, "test.db")

	store, err := sqlstore.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		panic(err)
	}
	testStore = store
	if err := migrate.Apply(context.Background(), store.DB(), "../../migrations", "sqlite"); err != nil {
		panic(err)
	}
	blobs, err := fsblob.New(filepath.Join(dataDir, "blobs"))
	if err != nil {
		panic(err)
	}
	testRoutes = httpapi.NewRouter(httpapi.Deps{
		Store:      store,
		Blobs:      blobs,
		Hub:        httpapi.NewHub(),
		NodeID:     cfg.Node.ID,
		RoleHint:   cfg.Node.RoleHint,
		AppVersion: cfg.AppVersion,
		Engine:     inference.NewReference(blobs), // baseline-движок для интеграционных тестов ADR-0012
	})
	code := m.Run()
	_ = store.Close()
	os.Exit(code)
}
