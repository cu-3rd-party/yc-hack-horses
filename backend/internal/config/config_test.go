package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsValid(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	if c.Storage.Driver != "sqlite" || c.Network.ListenAddr != "127.0.0.1:8080" || c.Sync.BatchSize != 256 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestLoadFromYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	yamlPath := filepath.Join(dir, "node.yaml")
	yamlContent := `
node:
  id: "test-node"
  data_dir: "` + dir + `"
storage:
  driver: "postgres"
  dsn: "postgres://localhost/test"
network:
  listen_addr: "127.0.0.1:9090"
sync:
  interval: "1m"
`
	if err := os.WriteFile(yamlPath, []byte(yamlContent), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(yamlPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Storage.Driver != "postgres" || c.Network.ListenAddr != "127.0.0.1:9090" || c.Sync.Interval != time.Minute {
		t.Fatalf("yaml values not applied: %+v", c)
	}

	// env переопределяет yaml
	t.Setenv("DATABASE_URL", "postgres://env/db")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:10000")
	t.Setenv("SYNC_INTERVAL", "5s")
	c, err = Load(yamlPath)
	if err != nil {
		t.Fatalf("load with env: %v", err)
	}
	if c.Storage.DSN != "postgres://env/db" || c.Network.ListenAddr != "127.0.0.1:10000" || c.Sync.Interval != 5*time.Second {
		t.Fatalf("env overrides not applied: %+v", c)
	}
}

func TestValidationRejectsBadDriver(t *testing.T) {
	t.Setenv("STORAGE_DRIVER", "mongodb")
	if _, err := Load(""); err == nil {
		t.Fatal("expected validation error for unknown driver")
	}
}
