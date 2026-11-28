package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestMCPStdioExecutionHelper(t *testing.T) {
	if os.Getenv("PICOCLAW_MCP_EXECUTION_HELPER") != "1" {
		return
	}
	marker := os.Getenv("PICOCLAW_MCP_EXECUTION_MARKER")
	if marker == "" {
		os.Exit(2)
	}
	if err := os.WriteFile(marker, []byte("started"), 0o600); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestNativeStdioRequiresLocalCommandExecution(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "unset"},
		{name: "false", value: "false", set: true},
		{name: "invalid", value: "invalid", set: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			marker := filepath.Join(dir, "mcp-started")
			envFile := filepath.Join(dir, ".env")
			const secretLine = "PRIVATE_FILE_CONTENT_WITHOUT_EQUALS"
			if err := os.WriteFile(envFile, []byte(secretLine), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(config.LocalCommandExecutionEnv, tt.value)
			if !tt.set {
				if err := os.Unsetenv(config.LocalCommandExecutionEnv); err != nil {
					t.Fatal(err)
				}
			}

			manager := NewManager()
			defer manager.Close()
			err := manager.LoadFromMCPConfig(t.Context(), config.MCPConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
				Servers: map[string]config.MCPServerConfig{
					"disabled": {
						Enabled: true,
						Type:    "stdio",
						Command: os.Args[0],
						Args:    []string{"-test.run=^TestMCPStdioExecutionHelper$"},
						Env: map[string]string{
							"PICOCLAW_MCP_EXECUTION_HELPER": "1",
							"PICOCLAW_MCP_EXECUTION_MARKER": marker,
						},
						EnvFile: envFile,
					},
				},
			}, dir)
			if !errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
				t.Fatalf("LoadFromMCPConfig() error = %v, want local command execution disabled", err)
			}
			if strings.Contains(err.Error(), secretLine) {
				t.Fatalf("disabled env_file contents leaked through error: %v", err)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("disabled MCP command executed: marker stat error = %v", statErr)
			}
		})
	}
}

func TestNativeStdioAllowsExplicitOptIn(t *testing.T) {
	t.Setenv(config.LocalCommandExecutionEnv, "true")
	marker := filepath.Join(t.TempDir(), "mcp-started")
	manager := NewManager()
	defer manager.Close()

	err := manager.LoadFromMCPConfig(t.Context(), config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			"allowed": {
				Enabled: true,
				Type:    "stdio",
				Command: os.Args[0],
				Args:    []string{"-test.run=^TestMCPStdioExecutionHelper$"},
				Env: map[string]string{
					"PICOCLAW_MCP_EXECUTION_HELPER": "1",
					"PICOCLAW_MCP_EXECUTION_MARKER": marker,
				},
			},
		},
	}, t.TempDir())
	if err == nil || errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
		t.Fatalf("LoadFromMCPConfig() error = %v, want helper handshake failure after execution", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("opted-in MCP command did not execute: %v", statErr)
	}
}

func TestPluginStdioRequiresLocalCommandExecution(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		name := "disabled"
		if allowed {
			name = "allowed"
		}
		t.Run(name, func(t *testing.T) {
			value := "false"
			if allowed {
				value = "true"
			}
			t.Setenv(config.LocalCommandExecutionEnv, value)
			marker := filepath.Join(t.TempDir(), "plugin-mcp-started")
			manager := NewManager()
			defer manager.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()

			err := manager.ConnectPluginServer(ctx, "plugin", config.MCPServerConfig{
				Type:    "stdio",
				Command: os.Args[0],
				Args:    []string{"-test.run=^TestMCPStdioExecutionHelper$"},
			}, PluginRuntimeOptions{
				Lifetime: ctx,
				Environment: []string{
					"PICOCLAW_MCP_EXECUTION_HELPER=1",
					"PICOCLAW_MCP_EXECUTION_MARKER=" + marker,
				},
				Timeout: time.Second,
			})
			if allowed {
				if err == nil || errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
					t.Fatalf("ConnectPluginServer() error = %v, want helper handshake failure after execution", err)
				}
				if _, statErr := os.Stat(marker); statErr != nil {
					t.Fatalf("opted-in plugin MCP command did not execute: %v", statErr)
				}
				return
			}
			if !errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
				t.Fatalf("ConnectPluginServer() error = %v, want local command execution disabled", err)
			}
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("disabled plugin MCP command executed: marker stat error = %v", statErr)
			}
		})
	}
}

func TestLoadEnvFileRequiresLocalCommandExecution(t *testing.T) {
	t.Setenv(config.LocalCommandExecutionEnv, "false")
	_, err := loadEnvFile(filepath.Join(t.TempDir(), ".env"))
	if !errors.Is(err, config.ErrLocalCommandExecutionDisabled) {
		t.Fatalf("loadEnvFile() error = %v, want local command execution disabled", err)
	}
}

func TestLoadEnvFileErrorDoesNotExposeContents(t *testing.T) {
	t.Setenv(config.LocalCommandExecutionEnv, "true")
	envFile := filepath.Join(t.TempDir(), ".env")
	const secretLine = "PRIVATE_FILE_CONTENT_WITHOUT_EQUALS"
	if err := os.WriteFile(envFile, []byte("VALID=value\n"+secretLine), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := loadEnvFile(envFile)
	if err == nil {
		t.Fatal("loadEnvFile() unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secretLine) {
		t.Fatalf("loadEnvFile() exposed file contents: %v", err)
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("loadEnvFile() error omitted line number: %v", err)
	}
}
