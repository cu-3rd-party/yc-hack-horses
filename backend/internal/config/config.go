// Package config: конфигурация ноды. Приоритет: значения по умолчанию (как в
// config.example.yaml) → YAML-файл → переменные окружения.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// PeerConfig — статический пир ноды.
type PeerConfig struct {
	// Addr — адрес gRPC-сервера пира (host:port).
	Addr string `yaml:"addr"`
	// NodeID — ожидаемый node_id пира (проверяется по URI SAN серта).
	// Пусто — проверяется только валидность серта и наличие przewalski://node/.
	NodeID string `yaml:"node_id"`
	// Host — TLS ServerName для проверки серверного серта пира (DNS SAN).
	// Пусто — берётся host из Addr.
	Host string `yaml:"host"`
}

// Config — полная конфигурация ноды. Поля зеркалят config.example.yaml.
type Config struct {
	Node struct {
		ID       string `yaml:"id"`
		DataDir  string `yaml:"data_dir"`
		RoleHint string `yaml:"role_hint"`
	} `yaml:"node"`
	Storage struct {
		Driver string `yaml:"driver"`
		DSN    string `yaml:"dsn"`
	} `yaml:"storage"`
	Network struct {
		ListenAddr     string `yaml:"listen_addr"`
		GRPCListenAddr string `yaml:"grpc_listen_addr"`
		MdnsEnabled    bool   `yaml:"mdns_enabled"`
		ServiceName    string `yaml:"service_name"`
	} `yaml:"network"`
	Security struct {
		CertFile        string `yaml:"cert_file"`
		KeyFile         string `yaml:"key_file"`
		CAFile          string `yaml:"ca_file"`
		CentralHostname string `yaml:"central_hostname"`
	} `yaml:"security"`
	Sync struct {
		Enabled    bool          `yaml:"enabled"`
		CentralURL string        `yaml:"central_url"`
		BatchSize  int           `yaml:"batch_size"`
		Interval   time.Duration `yaml:"interval"`
		// Peers — статически заданные пиры (адреса gRPC-серверов). Discovery
		// добавляет кандидатов динамически; доверие в обоих случаях только mTLS.
		Peers []PeerConfig `yaml:"peers"`
	} `yaml:"sync"`
	// Inference — локальный CV-движок (ADR-0012).
	Inference struct {
		// Engine — unavailable | reference (default: unavailable).
		Engine string `yaml:"engine"`
	} `yaml:"inference"`
	LogLevel   string `yaml:"log_level"`
	AppVersion string `yaml:"app_version"`
}

// Default возвращает конфиг со значениями из config.example.yaml.
func Default() *Config {
	c := &Config{}
	c.Node.DataDir = "./data"
	c.Node.RoleHint = "local"
	c.Storage.Driver = "sqlite"
	c.Storage.DSN = "./data/node.db"
	c.Network.ListenAddr = "127.0.0.1:8080"
	c.Network.GRPCListenAddr = "0.0.0.0:9443"
	c.Network.MdnsEnabled = true
	c.Network.ServiceName = "_przewalski-node._tcp.local"
	c.Security.CentralHostname = "yc-hack-horses.cu3rd.ru"
	c.Sync.Enabled = true
	c.Sync.CentralURL = "https://yc-hack-horses.cu3rd.ru"
	c.Sync.BatchSize = 256
	c.Sync.Interval = 30 * time.Second
	c.LogLevel = "info"
	c.Inference.Engine = "unavailable"
	c.AppVersion = "0.0.1-dev"
	return c
}

// Load читает YAML (если path не пуст), затем применяет env-переопределения и валидирует.
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: read %s: %w", path, err)
		}
		if err := yaml.Unmarshal(data, c); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
	}
	c.applyEnv()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// applyEnv переопределяет значения из окружения (имена — DATABASE_URL, LISTEN_ADDR и т.п.).
func (c *Config) applyEnv() {
	envString("NODE_ID", &c.Node.ID)
	envString("DATA_DIR", &c.Node.DataDir)
	envString("ROLE_HINT", &c.Node.RoleHint)
	envString("STORAGE_DRIVER", &c.Storage.Driver)
	// DATABASE_URL — общепринятое имя для DSN
	envString("DATABASE_URL", &c.Storage.DSN)
	envString("NODE_DSN", &c.Storage.DSN)
	envString("LISTEN_ADDR", &c.Network.ListenAddr)
	envString("GRPC_LISTEN_ADDR", &c.Network.GRPCListenAddr)
	envBool("MDNS_ENABLED", &c.Network.MdnsEnabled)
	envString("SERVICE_NAME", &c.Network.ServiceName)
	envString("CERT_FILE", &c.Security.CertFile)
	envString("KEY_FILE", &c.Security.KeyFile)
	envString("CA_FILE", &c.Security.CAFile)
	envString("CENTRAL_HOSTNAME", &c.Security.CentralHostname)
	envBool("SYNC_ENABLED", &c.Sync.Enabled)
	envString("SYNC_CENTRAL_URL", &c.Sync.CentralURL)
	envInt("SYNC_BATCH_SIZE", &c.Sync.BatchSize)
	envDuration("SYNC_INTERVAL", &c.Sync.Interval)
	envString("INFER_ENGINE", &c.Inference.Engine)
	envString("LOG_LEVEL", &c.LogLevel)
	envString("APP_VERSION", &c.AppVersion)
}

func envString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func envBool(key string, dst *bool) {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			*dst = b
		}
	}
}

func envInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			*dst = n
		}
	}
}

func envDuration(key string, dst *time.Duration) {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			*dst = d
		}
	}
}

// Validate проверяет значения конфигурации до запуска сервисов.
func (c *Config) Validate() error {
	switch c.Storage.Driver {
	case "sqlite", "postgres":
	default:
		return fmt.Errorf("config: storage.driver %q: допустимо sqlite | postgres", c.Storage.Driver)
	}
	if c.Storage.DSN == "" {
		return fmt.Errorf("config: storage.dsn пуст")
	}
	if _, _, err := net.SplitHostPort(c.Network.ListenAddr); err != nil {
		return fmt.Errorf("config: network.listen_addr %q: %w", c.Network.ListenAddr, err)
	}
	if _, _, err := net.SplitHostPort(c.Network.GRPCListenAddr); err != nil {
		return fmt.Errorf("config: network.grpc_listen_addr %q: %w", c.Network.GRPCListenAddr, err)
	}
	for _, p := range c.Sync.Peers {
		if _, _, err := net.SplitHostPort(p.Addr); err != nil {
			return fmt.Errorf("config: sync.peers addr %q: %w", p.Addr, err)
		}
	}
	if c.Sync.Interval <= 0 {
		return fmt.Errorf("config: sync.interval должен быть > 0")
	}
	if c.Sync.BatchSize <= 0 {
		return fmt.Errorf("config: sync.batch_size должен быть > 0")
	}
	switch c.Inference.Engine {
	case "unavailable", "reference":
	default:
		return fmt.Errorf("config: inference.engine %q: допустимо unavailable | reference", c.Inference.Engine)
	}
	return nil
}
