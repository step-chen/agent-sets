package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Default configuration values
const (
	DefaultMaxBodySize int64 = 2 * 1024 * 1024 // 2MB
	DefaultConfigPath        = "config.yaml"
)

// MCPServerConfig holds configuration for a single MCP server
type MCPServerConfig struct {
	Endpoint     string   `yaml:"endpoint"`
	Token        string   `yaml:"-"`             // From Env
	AllowedTools []string `yaml:"allowed_tools"` // Whitelist of tools to expose
}

// PromptsConfig holds configuration for prompt loading
type PromptsConfig struct {
	Dir string `yaml:"dir"` // Root directory for prompt files
}

// Config holds the configuration for the PR review automation tool
type Config struct {
	Log struct {
		Level  string `yaml:"level"`  // DEBUG, INFO, WARN, ERROR
		Format string `yaml:"format"` // text, json
		Output string `yaml:"output"` // stdout, stderr, /path/to/file
	} `yaml:"log"`

	Server struct {
		Port             int           `yaml:"port"`
		ConcurrencyLimit int64         `yaml:"concurrency_limit"`
		ReadTimeout      time.Duration `yaml:"read_timeout"`
		WriteTimeout     time.Duration `yaml:"write_timeout"`
		MaxBodySize      int64         `yaml:"max_body_size"`
		WebhookSecret    string        `yaml:"-"` // From Env
	} `yaml:"server"`

	LLM struct {
		Model    string `yaml:"model"`
		Endpoint string `yaml:"endpoint"`
		APIKey   string `yaml:"api_key"` // From YAML or Env
	} `yaml:"llm"`

	MCP struct {
		Retry struct {
			Attempts   int           `yaml:"attempts"`
			Backoff    time.Duration `yaml:"backoff"`
			MaxBackoff time.Duration `yaml:"max_backoff"`
		} `yaml:"retry"`
		Bitbucket  MCPServerConfig `yaml:"bitbucket"`
		Jira       MCPServerConfig `yaml:"jira"`
		Confluence MCPServerConfig `yaml:"confluence"`
	} `yaml:"mcp"`

	Prompts PromptsConfig `yaml:"prompts"`

	Storage StorageConfig `yaml:"storage"`
}

// StorageConfig holds configuration for review persistence
type StorageConfig struct {
	Driver string `yaml:"driver"` // sqlite
	DSN    string `yaml:"dsn"`    // Connection string
}

// GetLogLevel returns the slog.Level based on Log.Level string
func (c *Config) GetLogLevel() slog.Level {
	switch strings.ToUpper(c.Log.Level) {
	case "DEBUG":
		return slog.LevelDebug
	case "WARN", "WARNING":
		return slog.LevelWarn
	case "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// LoadConfig loads configuration from YAML file and supplements with environment variables
func LoadConfig() *Config {
	cfg := &Config{}

	// Set some defaults before loading
	cfg.Log.Level = "INFO"
	cfg.Log.Format = "text"
	cfg.Log.Output = "stdout"
	cfg.Server.Port = 8080
	cfg.Server.ConcurrencyLimit = 10
	cfg.Server.ReadTimeout = 10 * time.Second
	cfg.Server.WriteTimeout = 30 * time.Second
	cfg.Server.MaxBodySize = DefaultMaxBodySize
	cfg.LLM.Endpoint = "https://api.openai.com/v1"
	cfg.LLM.Model = "gpt-4o"
	cfg.MCP.Retry.Attempts = 3
	cfg.MCP.Retry.Backoff = 1 * time.Second
	cfg.MCP.Retry.MaxBackoff = 30 * time.Second
	cfg.Prompts.Dir = "prompts"

	// Try to load from YAML
	configPath := getEnv("CONFIG_PATH", DefaultConfigPath)
	data, err := os.ReadFile(configPath)
	if err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			slog.Error("unmarshal config failed", "error", err, "path", configPath)
			os.Exit(1)
		}
		slog.Info("config loaded", "path", configPath)
	} else {
		if !os.IsNotExist(err) {
			slog.Error("read config failed", "error", err, "path", configPath)
			os.Exit(1)
		}
		slog.Info("config not found, using defaults", "path", configPath)
	}

	// Always supplement/override with environment variables for secrets and critical items
	cfg.LLM.APIKey = getEnv("LLM_API_KEY", cfg.LLM.APIKey)
	cfg.Server.WebhookSecret = getEnv("WEBHOOK_SECRET", cfg.Server.WebhookSecret)

	cfg.MCP.Bitbucket.Token = getEnv("BITBUCKET_MCP_TOKEN", cfg.MCP.Bitbucket.Token)
	cfg.MCP.Jira.Token = getEnv("JIRA_MCP_TOKEN", cfg.MCP.Jira.Token)
	cfg.MCP.Confluence.Token = getEnv("CONFLUENCE_MCP_TOKEN", cfg.MCP.Confluence.Token)

	// Support for existing environment variables for backward compatibility (optional but keep some common ones)
	if envPort := getEnvInt("PORT", 0); envPort != 0 {
		cfg.Server.Port = envPort
	}
	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		cfg.Log.Level = envLogLevel
	}
	if envLogFormat := os.Getenv("LOG_FORMAT"); envLogFormat != "" {
		cfg.Log.Format = envLogFormat
	}
	if envLogOutput := os.Getenv("LOG_OUTPUT"); envLogOutput != "" {
		cfg.Log.Output = envLogOutput
	}

	return cfg
}

// Validate validates the configuration
func (c *Config) Validate() error {
	var errs []string

	if c.LLM.APIKey == "" {
		errs = append(errs, "LLM_API_KEY is required")
	}

	if c.Server.Port < 1 || c.Server.Port > 65535 {
		errs = append(errs, fmt.Sprintf("invalid server port: %d", c.Server.Port))
	}

	// At least one MCP endpoint should be configured
	if c.MCP.Bitbucket.Endpoint == "" && c.MCP.Jira.Endpoint == "" && c.MCP.Confluence.Endpoint == "" {
		errs = append(errs, "at least one MCP endpoint must be configured")
	}

	if len(errs) > 0 {
		return fmt.Errorf("config invalid: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Helper functions for reading environment variables

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	valueStr := getEnv(key, "")
	if valueStr == "" {
		return fallback
	}
	if value, err := strconv.Atoi(valueStr); err == nil {
		return value
	}
	return fallback
}
