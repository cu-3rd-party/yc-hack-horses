// Package app: сборка и жизненный цикл ноды (storage + transport + graceful shutdown).
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/cu3rd/yc-hack-horses/backend/internal/config"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/fsblob"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/migrate"
	"github.com/cu3rd/yc-hack-horses/backend/internal/storage/sqlstore"
	httpapi "github.com/cu3rd/yc-hack-horses/backend/internal/transport/http"
)

// App — собранная нода.
type App struct {
	cfg   *config.Config
	store *sqlstore.Store
	blobs *fsblob.Store
	hub   *httpapi.Hub
	srv   *http.Server
}

// New открывает хранилище, применяет миграции и собирает HTTP-сервер.
func New(cfg *config.Config) (*App, error) {
	if err := os.MkdirAll(cfg.Node.DataDir, 0o755); err != nil {
		return nil, fmt.Errorf("app: mkdir data dir: %w", err)
	}
	store, err := sqlstore.Open(cfg.Storage.Driver, cfg.Storage.DSN)
	if err != nil {
		return nil, err
	}
	if err := migrate.Apply(context.Background(), store.DB(), migrationsDir(cfg), cfg.Storage.Driver); err != nil {
		store.Close()
		return nil, err
	}
	blobs, err := fsblob.New(filepath.Join(cfg.Node.DataDir, "blobs"))
	if err != nil {
		store.Close()
		return nil, err
	}
	nodeID, err := loadOrCreateNodeID(cfg)
	if err != nil {
		store.Close()
		return nil, err
	}
	hub := httpapi.NewHub()
	handler := httpapi.NewRouter(httpapi.Deps{
		Store:      store,
		Blobs:      blobs,
		Hub:        hub,
		NodeID:     nodeID,
		RoleHint:   cfg.Node.RoleHint,
		AppVersion: cfg.AppVersion,
	})
	srv := &http.Server{
		Addr:              cfg.Network.ListenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return &App{cfg: cfg, store: store, blobs: blobs, hub: hub, srv: srv}, nil
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

// Run запускает HTTP-сервер и останавливает его по отмене ctx (SIGINT/SIGTERM в cmd).
func (a *App) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		slog.Info("http listening", "addr", a.srv.Addr, "driver", a.cfg.Storage.Driver)
		errCh <- a.srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := a.srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("app: shutdown: %w", err)
	}
	if err := <-errCh; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	if err := a.store.Close(); err != nil {
		return fmt.Errorf("app: close storage: %w", err)
	}
	slog.Info("node stopped")
	return nil
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
