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

// WebhookConfig holds configuration for webhook processing
type WebhookConfig struct {
	MaxRetries int `yaml:"max_retries"` // Max Retries for L2 extraction (default: 2)
}

// MCPServerConfig holds configuration for a single MCP server
type MCPServerConfig struct {
	Endpoint     string   `yaml:"endpoint"`
	Token        string   `yaml:"-"`             // From Env
	AuthHeader   string   `yaml:"auth_header"`   // Header name to use for token, e.g. "Bitbucket-Token"
	AllowedTools []string `yaml:"allowed_tools"` // Whitelist of tools to expose
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

	Webhook WebhookConfig `yaml:"webhook"`

	Agent AgentConfig `yaml:"agent"`

	Storage StorageConfig `yaml:"storage"`
}

// StorageConfig holds configuration for review persistence
type StorageConfig struct {
	Driver  string        `yaml:"driver"`  // sqlite
	DSN     string        `yaml:"dsn"`     // Connection string
	Timeout time.Duration `yaml:"timeout"` // Timeout for storage operations (default: 5s)
}

// AgentConfig holds configuration for the PR review agent
type AgentConfig struct {
	Backend               string `yaml:"backend"`                 // adk, langchain, direct (default: adk)
	MaxIterations         int    `yaml:"max_iterations"`          // Max agent loop iterations (default: 20)
	MaxToolCalls          int    `yaml:"max_tool_calls"`          // Max total tool calls per review (default: 50)
	MaxConcurrentComments int    `yaml:"max_concurrent_comments"` // Max concurrent comments posting (default: 5)
	DirectMode            bool   `yaml:"direct_mode"`             // Deprecated: use Backend: "direct" instead
	MaxDirectChars        int    `yaml:"max_direct_chars"`        // Max characters for direct mode (default: 40000)

	ResponseFilter struct {
		MaxStringLen int `yaml:"max_string_len"` // Max string length in tool output (default: 2000)
	} `yaml:"response_filter"`

	ChunkReview   ChunkReviewConfig   `yaml:"chunk_review"`
	DirectContext DirectContextConfig `yaml:"direct_context"`
}

// DirectContextConfig configures what context to fetch in direct mode
type DirectContextConfig struct {
	FetchCommitInfo  bool `yaml:"fetch_commit_info"`
	FetchFileContent bool `yaml:"fetch_file_content"`
	MaxFileSize      int  `yaml:"max_file_size"`
}

// ChunkReviewConfig holds configuration for chunked PR review
type ChunkReviewConfig struct {
	Enabled           bool `yaml:"enabled"`              // Enable chunked review for large PRs
	MaxTokensPerChunk int  `yaml:"max_tokens_per_chunk"` // Max tokens per chunk (default: 40000)
	MaxFilesPerChunk  int  `yaml:"max_files_per_chunk"`  // Max files per chunk (default: 10)
	ParallelChunks    int  `yaml:"parallel_chunks"`      // Max concurrent chunk reviews (default: 3)
	ContextLines      int  `yaml:"context_lines"`        // Context lines to preserve when splitting (default: 20)
	FoldDeletesOver   int  `yaml:"fold_deletes_over"`    // Fold deletes over N lines (default: 10)
	RemoveWhitespace  bool `yaml:"remove_whitespace"`    // Remove whitespace from diff (default: false)
	CompressSpaces    bool `yaml:"compress_spaces"`      // Compress consecutive spaces (default: true)
	RemoveBinaryDiff  bool `yaml:"remove_binary_diff"`   // Remove binary files from diff (default: true)
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
	cfg.Webhook.MaxRetries = 2

	// Agent defaults
	cfg.Agent.MaxIterations = 40
	cfg.Agent.MaxToolCalls = 50
	cfg.Agent.MaxConcurrentComments = 5
	cfg.Agent.MaxDirectChars = 40000
	cfg.Agent.ResponseFilter.MaxStringLen = 2000
	cfg.Agent.ChunkReview.MaxTokensPerChunk = 40000
	cfg.Agent.ChunkReview.MaxFilesPerChunk = 10
	cfg.Agent.ChunkReview.ContextLines = 20
	cfg.Agent.ChunkReview.FoldDeletesOver = 10
	cfg.Agent.ChunkReview.RemoveWhitespace = false
	cfg.Agent.ChunkReview.CompressSpaces = true
	cfg.Agent.ChunkReview.RemoveBinaryDiff = true

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
	if envLogOutput := getEnv("LOG_OUTPUT", ""); envLogOutput != "" {
		cfg.Log.Output = envLogOutput
	}
	if envLogMaxSize := getEnvInt("LOG_MAX_SIZE", 0); envLogMaxSize != 0 {
		cfg.Log.Rotation.MaxSize = envLogMaxSize
	}
	if envLogMaxBackups := getEnvInt("LOG_MAX_BACKUPS", 0); envLogMaxBackups != 0 {
		cfg.Log.Rotation.MaxBackups = envLogMaxBackups
	}
	if envLogMaxAge := getEnvInt("LOG_MAX_AGE", 0); envLogMaxAge != 0 {
		cfg.Log.Rotation.MaxAge = envLogMaxAge
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
