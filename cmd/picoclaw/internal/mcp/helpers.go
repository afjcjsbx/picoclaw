package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	picomcp "github.com/sipeed/picoclaw/pkg/mcp"
)

var (
	editorCommand = exec.Command
	serverProbe   = defaultServerProbe

	mcpConfigSchemaOnce sync.Once
	mcpConfigSchema     *jsonschema.Resolved
	errMcpConfigSchema  error
)

const mcpConfigSchemaJSON = `{
  "type": "object",
  "properties": {
    "tools": {
      "type": "object",
      "properties": {
        "mcp": {
          "type": "object",
          "properties": {
            "enabled": { "type": "boolean" },
            "discovery": { "type": "object", "additionalProperties": true },
            "max_inline_text_chars": { "type": "integer" },
            "servers": {
              "type": "object",
              "additionalProperties": {
                "type": "object",
                "properties": {
                  "enabled": { "type": "boolean" },
                  "deferred": { "type": "boolean" },
                  "command": { "type": "string" },
                  "args": {
                    "type": "array",
                    "items": { "type": "string" }
                  },
                  "env": {
                    "type": "object",
                    "additionalProperties": { "type": "string" }
                  },
                  "env_file": { "type": "string" },
                  "inherit_env": { "type": "boolean" },
                  "type": {
                    "type": "string",
                    "enum": ["stdio", "http", "sse"]
                  },
                  "url": { "type": "string" },
                  "oauth": {
                    "type": "object",
                    "properties": {
                      "client_id": { "type": "string" },
                      "issuer": { "type": "string" },
                      "scopes": { "type": "array", "items": { "type": "string" } },
                      "callback_port": { "type": "integer", "minimum": 0, "maximum": 65535 }
                    },
                    "additionalProperties": false
                  },
                  "headers": {
                    "type": "object",
                    "additionalProperties": { "type": "string" }
                  }
                },
                "required": ["enabled"],
                "anyOf": [
                  { "required": ["command"] },
                  { "required": ["url"] }
                ],
                "additionalProperties": false
              }
            }
          },
          "required": ["enabled"],
          "additionalProperties": true
        }
      },
      "required": ["mcp"],
      "additionalProperties": true
    }
  },
  "required": ["tools"],
  "additionalProperties": true
}`

func loadConfig() (*config.Config, error) {
	cfg, err := config.LoadConfig(internal.GetConfigPath())
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	return cfg, nil
}

func saveValidatedConfig(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	normalizedCfg := normalizedConfigForSave(cfg)

	data, err := json.Marshal(normalizedCfg)
	if err != nil {
		return fmt.Errorf("failed to serialize config: %w", err)
	}

	if err := validateConfigDocument(data); err != nil {
		return err
	}

	if err := config.SaveConfig(internal.GetConfigPath(), normalizedCfg); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	return nil
}

func normalizedConfigForSave(cfg *config.Config) *config.Config {
	clone := *cfg
	if cfg.Tools.MCP.Servers == nil {
		return &clone
	}

	clone.Tools = cfg.Tools
	clone.Tools.MCP = cfg.Tools.MCP
	clone.Tools.MCP.Servers = make(map[string]config.MCPServerConfig, len(cfg.Tools.MCP.Servers))
	for name, server := range cfg.Tools.MCP.Servers {
		if server.Type != "" {
			server.Type = config.NormalizeMCPTransportType(server.Type)
		}
		clone.Tools.MCP.Servers[name] = server
	}

	return &clone
}

func validateConfigDocument(data []byte) error {
	var instance map[string]any
	if err := json.Unmarshal(data, &instance); err != nil {
		return fmt.Errorf("failed to decode serialized config: %w", err)
	}

	schema, err := loadMCPConfigSchema()
	if err != nil {
		return fmt.Errorf("failed to load MCP config schema: %w", err)
	}

	if err := schema.Validate(instance); err != nil {
		return fmt.Errorf("config validation failed: %w", err)
	}

	return nil
}

func loadMCPConfigSchema() (*jsonschema.Resolved, error) {
	mcpConfigSchemaOnce.Do(func() {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(mcpConfigSchemaJSON), &schema); err != nil {
			errMcpConfigSchema = err
			return
		}
		mcpConfigSchema, errMcpConfigSchema = schema.Resolve(nil)
	})

	return mcpConfigSchema, errMcpConfigSchema
}

// displayTransportType returns the transport name for CLI output. A config
// that resolves to no transport (neither URL nor command) is shown as
// "unknown" instead of an empty cell.
func displayTransportType(server config.MCPServerConfig) string {
	transport := config.EffectiveMCPTransportType(server)
	if transport == "" {
		return "unknown"
	}
	return transport
}

func renderServerTarget(server config.MCPServerConfig) string {
	transport := displayTransportType(server)
	if transport == "http" || transport == "sse" {
		if server.URL == "" {
			return "<missing url>"
		}
		return server.URL
	}

	parts := append([]string{server.Command}, server.Args...)
	rendered := strings.TrimSpace(strings.Join(parts, " "))
	if rendered == "" {
		return "<missing command>"
	}
	return rendered
}

func sortedServerNames(servers map[string]config.MCPServerConfig) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parseEnvAssignments(values []string) (map[string]string, error) {
	return parseAssignments(values, "env assignment", []string{"="}, "KEY=value", false)
}

func parseHeaderAssignments(values []string) (map[string]string, error) {
	return parseAssignments(values, "header", []string{":", "="}, "'Name: Value' or 'Name=Value'", true)
}

// parseAssignments parses repeated "key<separator>value" CLI assignments,
// trying each separator in order. Header values are trimmed; environment
// values stay verbatim so trailing spaces and similar content survive.
func parseAssignments(
	values []string,
	kind string,
	separators []string,
	expected string,
	trimValue bool,
) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}

	result := make(map[string]string, len(values))
	for _, entry := range values {
		key, value, found := "", "", false
		for _, separator := range separators {
			if key, value, found = strings.Cut(entry, separator); found {
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("invalid %s %q: expected %s", kind, entry, expected)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid %s %q: key cannot be empty", kind, entry)
		}
		if trimValue {
			value = strings.TrimSpace(value)
		}
		result[key] = value
	}

	return result, nil
}

func looksLikeRemoteURL(target string) bool {
	parsedURL, err := url.ParseRequestURI(target)
	if err != nil {
		return false
	}
	if parsedURL.Host == "" {
		return false
	}
	switch strings.ToLower(parsedURL.Scheme) {
	case "http", "https":
		return true
	default:
		return false
	}
}

func isLocalCommandPath(command string) bool {
	if command == "" {
		return false
	}
	if looksLikeRemoteURL(command) {
		return false
	}
	return filepath.IsAbs(command) ||
		filepath.VolumeName(command) != "" ||
		strings.HasPrefix(command, "."+string(os.PathSeparator)) ||
		strings.HasPrefix(command, ".."+string(os.PathSeparator)) ||
		command == "." ||
		command == ".." ||
		strings.ContainsRune(command, os.PathSeparator)
}

func validateLocalCommandPath(command string) error {
	if !isLocalCommandPath(command) {
		return nil
	}

	path := fileutil.ExpandHome(command)
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local command %q does not exist", command)
		}
		return fmt.Errorf("failed to stat local command %q: %w", command, err)
	}
	if info.IsDir() {
		return fmt.Errorf("local command %q is a directory", command)
	}
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return fmt.Errorf("local command %q is not executable", command)
	}
	return nil
}

// probeServer starts a throwaway manager for one server and extracts a value
// from its connection. The status probe and the show probe share this
// bootstrap so the manager lifecycle lives in one place.
func probeServer[T any](
	ctx context.Context,
	name string,
	server config.MCPServerConfig,
	workspacePath string,
	extract func(*picomcp.ServerConnection) T,
) (T, error) {
	var zero T

	mgr := picomcp.NewManager()
	defer func() { _ = mgr.Close() }()

	server.Enabled = true
	mcpCfg := config.MCPConfig{
		ToolConfig: config.ToolConfig{Enabled: true},
		Servers: map[string]config.MCPServerConfig{
			name: server,
		},
	}

	if err := mgr.LoadFromMCPConfig(ctx, mcpCfg, workspacePath); err != nil {
		return zero, err
	}

	conn, ok := mgr.GetServer(name)
	if !ok {
		return zero, fmt.Errorf("server %q did not register a connection", name)
	}

	return extract(conn), nil
}

// defaultServerProbe returns the number of tools the server exposes.
func defaultServerProbe(
	ctx context.Context,
	name string,
	server config.MCPServerConfig,
	workspacePath string,
) (int, error) {
	return probeServer(ctx, name, server, workspacePath, func(conn *picomcp.ServerConnection) int {
		return len(conn.Tools)
	})
}

func confirmOverwrite(r io.Reader, w io.Writer, name string) (bool, error) {
	if _, err := fmt.Fprintf(w, "MCP server %q already exists. Overwrite? [y/N]: ", name); err != nil {
		return false, err
	}

	var answer string
	if _, err := fmt.Fscanln(r, &answer); err != nil {
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		return false, err
	}

	answer = strings.TrimSpace(strings.ToLower(answer))
	return answer == "y" || answer == "yes", nil
}
