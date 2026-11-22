// Package config: конфигурация ноды. Приоритет: значения по умолчанию (как в
// config.example.yaml) → YAML-файл → переменные окружения.
package config

import (
	"fmt"
	"net"
	"net/url"
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
		// GRPCMultiplex — gRPC и REST живут на одном listen_addr (h2c):
		// запросы с content-type application/grpc и ProtoMajor==2 уходят
		// в gRPC-сервер, остальное — в REST. Для central-ноды за ingress:
		// TLS терминируется на ingress, клиентский серт приходит в
		// заголовке X-Forwarded-Tls-Client-Cert и проверяется интерцептором
		// против security.ca_file (см. internal/app/grpcmux.go).
		GRPCMultiplex bool   `yaml:"grpc_multiplex"`
		MdnsEnabled   bool   `yaml:"mdns_enabled"`
		ServiceName   string `yaml:"service_name"`
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
		// Engine — unavailable | reference | yolo (default: unavailable).
		Engine        string `yaml:"engine"`
		ComparisonURL string `yaml:"comparison_url"`
		ModelBaseURL  string `yaml:"model_base_url"`
		// ModelPath — путь к ONNX-весам YOLO. Пустой путь включает выбор из DATA_DIR/models.
		ModelPath string `yaml:"model_path"`
		// RuntimeLib — путь к onnxruntime shared library (engine=yolo).
		RuntimeLib   string `yaml:"runtime_lib"`
		ReIDModelDir string `yaml:"reid_model_dir"`
	} `yaml:"inference"`
	// Enrollment — автоматическая выдача клиентских сертификатов
	// (ADR-0007 amended, ADR-0014). Включается только на central-ноде;
	// локальные ноды держат выключенным.
	Enrollment struct {
		// Enabled — включить POST /api/v1/enroll. false — эндпоинт
		// отвечает 404 (поведение до внедрения enrollment).
		Enabled bool `yaml:"enabled"`
		// Token — enrollment-токен; выдаётся оператором, клиент вводит
		// его один раз при первом запуске приложения.
		Token string `yaml:"token"`
		// CAKeyFile — приватный ключ CA, которым подписываются выдачи
		// (серт — security.ca_file).
		CAKeyFile string `yaml:"ca_key_file"`
		// CertDays — срок жизни выдаваемого серта (0 → 365, ADR-0007
		// amended: leaf-серты enrollment-нод — 365 дней).
		CertDays int `yaml:"cert_days"`
	} `yaml:"enrollment"`
	// Api — HTTP API ноды.
	Api struct {
		// DocsEnabled — Scalar UI (/api/scalar) и OpenAPI-спека. Включается
		// только на central-ноде; локальные ноды держат выключенным.
		DocsEnabled bool   `yaml:"docs_enabled"`
		WebDir      string `yaml:"web_dir"`
	} `yaml:"api"`
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
	c.Api.DocsEnabled = false   // документация — только у central-ноды
	c.Enrollment.CertDays = 365 // ADR-0007 amended: leaf enrollment-нод — 1 год
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
	envBool("GRPC_MULTIPLEX", &c.Network.GRPCMultiplex)
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
	envString("INFER_MODEL_BASE_URL", &c.Inference.ModelBaseURL)
	envString("INFER_COMPARISON_URL", &c.Inference.ComparisonURL)
	envString("INFER_ENGINE", &c.Inference.Engine)
	envString("INFER_MODEL_PATH", &c.Inference.ModelPath)
	envString("INFER_RUNTIME_LIB", &c.Inference.RuntimeLib)
	envString("INFER_REID_MODEL_DIR", &c.Inference.ReIDModelDir)
	envString("WEB_DIR", &c.Api.WebDir)
	envBool("API_DOCS_ENABLED", &c.Api.DocsEnabled)
	envBool("ENROLLMENT_ENABLED", &c.Enrollment.Enabled)
	envString("ENROLLMENT_TOKEN", &c.Enrollment.Token)
	envString("ENROLLMENT_CA_KEY_FILE", &c.Enrollment.CAKeyFile)
	envInt("ENROLLMENT_CERT_DAYS", &c.Enrollment.CertDays)
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
	if c.Inference.ComparisonURL != "" {
		u, err := url.Parse(c.Inference.ComparisonURL)
		if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
			return fmt.Errorf("config: inference.comparison_url must be an HTTP(S) endpoint")
		}
	}
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
	// grpc_multiplex: аутентификация gRPC-клиентов идёт по серту из
	// заголовка ingress, поэтому без ca_file мультиплекс запрещён
	// (fail closed: анонимный gRPC наружу не открывается).
	if c.Network.GRPCMultiplex && c.Security.CAFile == "" {
		return fmt.Errorf("config: network.grpc_multiplex=true требует security.ca_file — " +
			"без него gRPC-клиенты за ingress не могут быть аутентифицированы")
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
	if c.Enrollment.Enabled {
		if c.Enrollment.Token == "" {
			return fmt.Errorf("config: enrollment.enabled требует enrollment.token (ENROLLMENT_TOKEN)")
		}
		if c.Enrollment.CAKeyFile == "" {
			return fmt.Errorf("config: enrollment.enabled требует enrollment.ca_key_file (ENROLLMENT_CA_KEY_FILE)")
		}
		if c.Security.CAFile == "" {
			return fmt.Errorf("config: enrollment.enabled требует security.ca_file — CA-серт, которым проверяются выдачи")
		}
	}
	switch c.Inference.Engine {
	case "unavailable", "reference":
	case "yolo":
		if c.Inference.RuntimeLib == "" {
			return fmt.Errorf("config: inference.engine yolo: требуется inference.runtime_lib")
		}
	default:
		return fmt.Errorf("config: inference.engine %q: допустимо unavailable | reference | yolo", c.Inference.Engine)
	}
	return nil
}
