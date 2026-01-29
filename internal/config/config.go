package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Default configuration values
const (
	DefaultMaxBodySize int64 = 2 * 1024 * 1024 // 2MB
	DefaultConfigPath        = "config.yaml"
)

// WebhookConfig holds configuration for webhook processing
type WebhookConfig struct {
	MaxRetries int `yaml:"max_retries"` // Max Retries for L2 extraction (default: 2)
}

// MCPServerConfig holds configuration for a single MCP server
type MCPServerConfig struct {
	Endpoint        string         `yaml:"endpoint"`
	Token           string         `yaml:"-"`                // From Env
	AuthHeader      string         `yaml:"auth_header"`      // Header name to use for token, e.g. "Bitbucket-Token"
	AllowedTools    []string       `yaml:"allowed_tools"`    // Whitelist of tools to expose
	ResponseFilters []FilterConfig `yaml:"response_filters"` // Output filters
}

type FilterConfig struct {
	Name    string                 `yaml:"name"`
	Options map[string]interface{} `yaml:"options"`
}

// PromptsConfig holds configuration for prompt loading
type PromptsConfig struct {
	Dir string `yaml:"dir"` // Root directory for prompt files
}

// Config holds the configuration for the PR review automation tool
type Config struct {
	Log struct {
		Level    string `yaml:"level"`  // DEBUG, INFO, WARN, ERROR
		Format   string `yaml:"format"` // text, json
		Output   string `yaml:"output"` // stdout, stderr, /path/to/file
		Rotation struct {
			MaxSize    int  `yaml:"max_size"`    // Megabytes
			MaxBackups int  `yaml:"max_backups"` // Number of old files to keep
			MaxAge     int  `yaml:"max_age"`     // Days to keep
			Compress   bool `yaml:"compress"`
		} `yaml:"rotation"`
	} `yaml:"log"`

	Server struct {
		Port             int           `yaml:"port"`
		ConcurrencyLimit int64         `yaml:"concurrency_limit"`
		ReadTimeout      time.Duration `yaml:"read_timeout"`
		WriteTimeout     time.Duration `yaml:"write_timeout"`
		ShutdownTimeout  time.Duration `yaml:"shutdown_timeout"`
		MaxBodySize      int64         `yaml:"max_body_size"`
		QueueSize        int           `yaml:"queue_size"`
		DebounceWindow   time.Duration `yaml:"debounce_window"`
		WebhookSecret    string        `yaml:"-"` // From Env
	} `yaml:"server"`

	LLM struct {
		Model    string        `yaml:"model"`
		Endpoint string        `yaml:"endpoint"`
		APIKey   string        `yaml:"api_key"` // From YAML or Env
		Timeout  time.Duration `yaml:"timeout"`
	} `yaml:"llm"`

	MCP struct {
		Timeout time.Duration `yaml:"timeout"`
		Retry   struct {
			Attempts   int           `yaml:"attempts"`
			Backoff    time.Duration `yaml:"backoff"`
			MaxBackoff time.Duration `yaml:"max_backoff"`
		} `yaml:"retry"`
		CircuitBreaker struct {
			FailureThreshold int           `yaml:"failure_threshold"`
			OpenDuration     time.Duration `yaml:"open_duration"`
		} `yaml:"circuit_breaker"`
		Bitbucket  MCPServerConfig `yaml:"bitbucket"`
		Jira       MCPServerConfig `yaml:"jira"`
		Confluence MCPServerConfig `yaml:"confluence"`
	} `yaml:"mcp"`

	Prompts PromptsConfig `yaml:"prompts"`

	Webhook WebhookConfig `yaml:"webhook"`

	Pipeline PipelineConfig `yaml:"pipeline"`

	Storage StorageConfig `yaml:"storage"`
}

// StorageConfig holds configuration for review persistence
type StorageConfig struct {
	Driver  string        `yaml:"driver"`  // sqlite
	DSN     string        `yaml:"dsn"`     // Connection string
	Timeout time.Duration `yaml:"timeout"` // Timeout for storage operations (default: 5s)
}

// PipelineConfig holds configuration for the 3-stage review pipeline
type PipelineConfig struct {
	Enabled               bool   `yaml:"enabled"`
	Backend               string `yaml:"backend"` // direct or agent
	MaxConcurrentComments int    `yaml:"max_concurrent_comments"`
	ResponseMaxStringLen  int    `yaml:"response_max_string_len"`

	Stage1Diff    Stage1Config       `yaml:"stage1_diff"`
	Stage2Context Stage2Config       `yaml:"stage2_context"`
	Stage3Review  Stage3Config       `yaml:"stage3_review"`
	CommentMerge  CommentMergeConfig `yaml:"comment_merge"`
}

type CommentMergeConfig struct {
	Enabled           bool   `yaml:"enabled"`
	HighSeverityMerge string `yaml:"high_severity_merge"` // "by_file" | "none" (none = Hybrid Mode)
	LowSeverityMerge  string `yaml:"low_severity_merge"`  // "to_summary" | "none"
}

type Stage1Config struct {
	PromptTemplate string `yaml:"prompt_template"`
}

type Stage2Config struct {
	PromptTemplate string `yaml:"prompt_template"`
	MaxExtraFiles  int    `yaml:"max_extra_files"`
	MaxFileSize    int    `yaml:"max_file_size"`
}

type Stage3Config struct {
	PromptTemplate   string            `yaml:"prompt_template"`
	Temperature      float64           `yaml:"temperature"`
	MaxContextTokens int               `yaml:"max_context_tokens"`
	Degradation      DegradationConfig `yaml:"degradation"`
}

type DegradationConfig struct {
	L1ContextLines int  `yaml:"l1_context_lines"` // L1: Lines of context to keep around changes (default: 50)
	L2ChunkByFile  bool `yaml:"l2_chunk_by_file"` // L2: Enable chunking by file (default: true)
	L3DiffOnly     bool `yaml:"l3_diff_only"`     // L3: Fallback to diff only (default: true)
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
	cfg.Server.QueueSize = 100 // Default Queue Size
	cfg.Server.DebounceWindow = 2 * time.Second
	cfg.Server.ReadTimeout = 10 * time.Second
	cfg.Server.WriteTimeout = 30 * time.Second
	cfg.Server.ShutdownTimeout = 30 * time.Second
	cfg.Server.MaxBodySize = DefaultMaxBodySize
	cfg.LLM.Endpoint = "https://api.openai.com/v1"
	cfg.LLM.Model = "gpt-4o"
	cfg.LLM.Timeout = 120 * time.Second
	cfg.MCP.Timeout = 30 * time.Second
	cfg.MCP.Retry.Attempts = 3
	cfg.MCP.Retry.Backoff = 1 * time.Second
	cfg.MCP.Retry.MaxBackoff = 30 * time.Second
	cfg.MCP.CircuitBreaker.FailureThreshold = 3
	cfg.MCP.CircuitBreaker.OpenDuration = 30 * time.Second
	cfg.Prompts.Dir = "prompts"
	cfg.Webhook.MaxRetries = 2

	// Pipeline defaults
	cfg.Pipeline.Enabled = true
	cfg.Pipeline.Backend = "direct"
	cfg.Pipeline.MaxConcurrentComments = 5     // Default limit
	cfg.Pipeline.ResponseMaxStringLen = 100000 // Default limit
	cfg.Pipeline.Stage1Diff.PromptTemplate = "pipeline/stage1.md"
	cfg.Pipeline.Stage2Context.PromptTemplate = "pipeline/stage2.md"
	cfg.Pipeline.Stage2Context.MaxExtraFiles = 5
	cfg.Pipeline.Stage2Context.MaxFileSize = 50000
	cfg.Pipeline.Stage3Review.PromptTemplate = "pipeline/stage3.md"
	cfg.Pipeline.Stage3Review.Temperature = 0.0
	cfg.Pipeline.Stage3Review.MaxContextTokens = 256000
	cfg.Pipeline.Stage3Review.Degradation.L1ContextLines = 50
	cfg.Pipeline.Stage3Review.Degradation.L2ChunkByFile = true
	cfg.Pipeline.Stage3Review.Degradation.L3DiffOnly = true
	cfg.Pipeline.CommentMerge.Enabled = true
	cfg.Pipeline.CommentMerge.HighSeverityMerge = "by_file"
	cfg.Pipeline.CommentMerge.LowSeverityMerge = "to_summary"

	// Log Rotation defaults
	cfg.Log.Rotation.MaxSize = 100
	cfg.Log.Rotation.MaxBackups = 10
	cfg.Log.Rotation.MaxAge = 7
	cfg.Log.Rotation.Compress = true

	// Storage defaults
	cfg.Storage.Timeout = 5 * time.Second

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
