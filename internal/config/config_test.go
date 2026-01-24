package config

import (
	"os"
	"testing"
	"time"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear environment variables to test defaults
	os.Unsetenv("LLM_ENDPOINT")
	os.Unsetenv("LLM_API_KEY")
	os.Unsetenv("PORT")
	os.Unsetenv("CONCURRENCY_LIMIT")
	os.Unsetenv("READ_TIMEOUT")
	os.Unsetenv("WRITE_TIMEOUT")
	os.Unsetenv("MAX_BODY_SIZE")
	os.Unsetenv("CONFIG_PATH")

	cfg := LoadConfig()

	if cfg.Server.Port != 8080 {
		t.Errorf("expected port 8080, got %d", cfg.Server.Port)
	}

	if cfg.Server.ConcurrencyLimit != 10 {
		t.Errorf("expected concurrency limit 10, got %d", cfg.Server.ConcurrencyLimit)
	}

	if cfg.Server.ReadTimeout != 10*time.Second {
		t.Errorf("expected read timeout 10s, got %v", cfg.Server.ReadTimeout)
	}

	if cfg.Server.WriteTimeout != 30*time.Second {
		t.Errorf("expected write timeout 30s, got %v", cfg.Server.WriteTimeout)
	}

	if cfg.Server.MaxBodySize != 2*1024*1024 {
		t.Errorf("expected max body size 2MB, got %d", cfg.Server.MaxBodySize)
	}
}

func TestLoadConfig_MCPEndpointsFromEnv(t *testing.T) {
	os.Setenv("BITBUCKET_MCP_TOKEN", "bb-token")
	os.Setenv("JIRA_MCP_TOKEN", "jira-token")
	os.Setenv("CONFLUENCE_MCP_TOKEN", "conf-token")
	defer func() {
		os.Unsetenv("BITBUCKET_MCP_TOKEN")
		os.Unsetenv("JIRA_MCP_TOKEN")
		os.Unsetenv("CONFLUENCE_MCP_TOKEN")
	}()

	cfg := LoadConfig()

	if cfg.MCP.Bitbucket.Token != "bb-token" {
		t.Errorf("expected bitbucket token, got %s", cfg.MCP.Bitbucket.Token)
	}

	if cfg.MCP.Jira.Token != "jira-token" {
		t.Errorf("expected jira token, got %s", cfg.MCP.Jira.Token)
	}

	if cfg.MCP.Confluence.Token != "conf-token" {
		t.Errorf("expected confluence token, got %s", cfg.MCP.Confluence.Token)
	}
}

func TestLoadConfig_YAML(t *testing.T) {
	yamlContent := `
log:
  level: DEBUG
server:
  port: 1234
  concurrency_limit: 5
llm:
  model: custom-model
mcp:
  bitbucket:
    endpoint: http://custom-bb:8080
`
	tmpfile, err := os.CreateTemp("", "config*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpfile.Name())

	if _, err := tmpfile.Write([]byte(yamlContent)); err != nil {
		t.Fatal(err)
	}
	if err := tmpfile.Close(); err != nil {
		t.Fatal(err)
	}

	os.Setenv("CONFIG_PATH", tmpfile.Name())
	defer os.Unsetenv("CONFIG_PATH")

	cfg := LoadConfig()

	if cfg.Log.Level != "DEBUG" {
		t.Errorf("expected Log.Level DEBUG, got %s", cfg.Log.Level)
	}
	if cfg.Server.Port != 1234 {
		t.Errorf("expected Port 1234, got %d", cfg.Server.Port)
	}
	if cfg.LLM.Model != "custom-model" {
		t.Errorf("expected LLM Model custom-model, got %s", cfg.LLM.Model)
	}
	if cfg.MCP.Bitbucket.Endpoint != "http://custom-bb:8080" {
		t.Errorf("expected Bitbucket Endpoint, got %s", cfg.MCP.Bitbucket.Endpoint)
	}
}
