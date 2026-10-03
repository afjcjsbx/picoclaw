package config

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/netbind"
)

const DefaultGatewayLogLevel = "warn"

const (
	DefaultGatewayLogMaxSizeMB  = 2
	DefaultGatewayLogMaxBackups = 3
)

type GatewayConfig struct {
	Host          string `json:"host"                env:"PICOCLAW_GATEWAY_HOST"`
	Port          int    `json:"port"                env:"PICOCLAW_GATEWAY_PORT"`
	HotReload     bool   `json:"hot_reload"          env:"PICOCLAW_GATEWAY_HOT_RELOAD"`
	LogLevel      string `json:"log_level,omitempty" env:"PICOCLAW_LOG_LEVEL"`
	LogMaxSizeMB  int    `json:"log_max_size_mb,omitempty" env:"PICOCLAW_GATEWAY_LOG_MAX_SIZE_MB"`
	LogMaxBackups int    `json:"log_max_backups,omitempty" env:"PICOCLAW_GATEWAY_LOG_MAX_BACKUPS"`
}

func canonicalGatewayLogLevel(level logger.LogLevel) string {
	switch level {
	case logger.DEBUG:
		return "debug"
	case logger.INFO:
		return "info"
	case logger.WARN:
		return "warn"
	case logger.ERROR:
		return "error"
	case logger.FATAL:
		return "fatal"
	default:
		return DefaultGatewayLogLevel
	}
}

func normalizeGatewayLogLevel(logLevel string) string {
	if level, ok := logger.ParseLevel(logLevel); ok {
		return canonicalGatewayLogLevel(level)
	}
	return DefaultGatewayLogLevel
}

// EffectiveGatewayLogLevel returns the normalized runtime log level from a loaded config.
// Invalid or empty values fall back to the package default.
func EffectiveGatewayLogLevel(cfg *Config) string {
	if cfg == nil {
		return DefaultGatewayLogLevel
	}
	return normalizeGatewayLogLevel(cfg.Gateway.LogLevel)
}

func resolveGatewayHostFromEnv(baseHost string) (string, error) {
	envHost, ok := os.LookupEnv(EnvGatewayHost)
	if !ok {
		return normalizeGatewayHostInput(baseHost)
	}

	envHost = strings.TrimSpace(envHost)
	if envHost == "" {
		return normalizeGatewayHostInput(baseHost)
	}

	return normalizeGatewayHostInput(envHost)
}

func normalizeGatewayHostInput(host string) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		host = strings.TrimSpace(DefaultConfig().Gateway.Host)
	}
	if host == "" {
		host = "localhost"
	}
	return netbind.NormalizeHostInput(host)
}

// ResolveGatewayLogLevel reads the configured gateway log level without triggering
// the full config loader, so startup code can apply logging before config load logs run.
// The PICOCLAW_LOG_LEVEL environment variable overrides the file value.
func ResolveGatewayLogLevel(path string) string {
	cfg := struct {
		Gateway GatewayConfig `json:"gateway"`
	}{
		Gateway: GatewayConfig{LogLevel: DefaultGatewayLogLevel},
	}

	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.WarnCF("config", "failed to parse gateway config, using defaults", map[string]any{
				"path":  path,
				"error": err.Error(),
			})
		}
	}

	if envLevel := os.Getenv("PICOCLAW_LOG_LEVEL"); envLevel != "" {
		cfg.Gateway.LogLevel = envLevel
	}

	return normalizeGatewayLogLevel(cfg.Gateway.LogLevel)
}

// ResolveGatewayLogRotation reads log retention settings before normal config
// loading so logs can be bounded from the first startup message.
func ResolveGatewayLogRotation(path string) (sizeMB, backups int) {
	cfg := struct {
		Gateway GatewayConfig `json:"gateway"`
	}{Gateway: GatewayConfig{
		LogMaxSizeMB:  DefaultGatewayLogMaxSizeMB,
		LogMaxBackups: DefaultGatewayLogMaxBackups,
	}}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			logger.WarnCF("config", "failed to parse gateway log rotation settings, using defaults", map[string]any{
				"path": path, "error": err.Error(),
			})
		}
	}
	if value := os.Getenv("PICOCLAW_GATEWAY_LOG_MAX_SIZE_MB"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.Gateway.LogMaxSizeMB = parsed
		}
	}
	if value := os.Getenv("PICOCLAW_GATEWAY_LOG_MAX_BACKUPS"); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			cfg.Gateway.LogMaxBackups = parsed
		}
	}
	sizeMB, backups = cfg.Gateway.LogMaxSizeMB, cfg.Gateway.LogMaxBackups
	if sizeMB < 1 || sizeMB > 1024 {
		logger.WarnCF("config", "invalid gateway log_max_size_mb; using default", map[string]any{"value": sizeMB})
		sizeMB = DefaultGatewayLogMaxSizeMB
	}
	if backups < 0 || backups > 100 {
		logger.WarnCF("config", "invalid gateway log_max_backups; using default", map[string]any{"value": backups})
		backups = DefaultGatewayLogMaxBackups
	}
	return sizeMB, backups
}
