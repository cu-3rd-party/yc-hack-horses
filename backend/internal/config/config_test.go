package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDefaultsValid(t *testing.T) {
	c := Default()
	require.NoError(t, c.Validate(), "defaults must be valid")
	require.Equal(t, "sqlite", c.Storage.Driver)
	require.Equal(t, "127.0.0.1:8080", c.Network.ListenAddr)
	require.Equal(t, 256, c.Sync.BatchSize)
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
	require.NoError(t, os.WriteFile(yamlPath, []byte(yamlContent), 0o600))
	c, err := Load(yamlPath)
	require.NoError(t, err, "load")
	require.Equal(t, "postgres", c.Storage.Driver)
	require.Equal(t, "127.0.0.1:9090", c.Network.ListenAddr)
	require.Equal(t, time.Minute, c.Sync.Interval)

	// env переопределяет yaml
	t.Setenv("DATABASE_URL", "postgres://env/db")
	t.Setenv("LISTEN_ADDR", "127.0.0.1:10000")
	t.Setenv("SYNC_INTERVAL", "5s")
	c, err = Load(yamlPath)
	require.NoError(t, err, "load with env")
	require.Equal(t, "postgres://env/db", c.Storage.DSN)
	require.Equal(t, "127.0.0.1:10000", c.Network.ListenAddr)
	require.Equal(t, 5*time.Second, c.Sync.Interval)
}

func TestValidationRejectsBadDriver(t *testing.T) {
	t.Setenv("STORAGE_DRIVER", "mongodb")
	_, err := Load("")
	require.Error(t, err, "expected validation error for unknown driver")
}

func TestHorseIDEngineRequiresPaths(t *testing.T) {
	c := Default()
	c.Inference.Engine = "horseid"
	require.Error(t, c.Validate(), "без путей horseid невалиден")
	c.Inference.ModelPath = "/app/models/yolo11s.onnx"
	require.Error(t, c.Validate(), "нужен runtime_lib")
	c.Inference.RuntimeLib = "/app/models/libonnxruntime.so"
	require.Error(t, c.Validate(), "нужен reid_model_dir")
	c.Inference.ReIDModelDir = "/app/models"
	require.NoError(t, c.Validate(), "полный набор путей валиден")
}
