package plugins

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/net/http/httpguts"

	"github.com/sipeed/picoclaw/pkg/config"
)

type ServerSpec struct {
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

func ParseMCP(data []byte) (map[string]ServerSpec, []Diagnostic, error) {
	fields, err := object(data)
	if err != nil {
		return nil, nil, err
	}
	for key := range fields {
		if key != "$schema" && key != "mcpServers" {
			return nil, nil, fmt.Errorf("unknown MCP field %q", key)
		}
	}
	var schema string
	if err = decodeField(fields["$schema"], &schema); err != nil || schema != MCPSchema {
		return nil, nil, fmt.Errorf("missing or unsupported MCP $schema")
	}
	entries, err := object(fields["mcpServers"])
	if err != nil {
		return nil, nil, fmt.Errorf("mcpServers: %w", err)
	}
	servers := map[string]ServerSpec{}
	var diagnostics []Diagnostic
	for _, name := range sortedKeys(entries) {
		spec, err := parseServer(entries[name])
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Component: "mcp:" + name, Message: err.Error()})
			continue
		}
		servers[name] = spec
	}
	return servers, diagnostics, nil
}

func parseServer(raw []byte) (ServerSpec, error) {
	var spec ServerSpec
	fields, err := object(raw)
	if err != nil {
		return spec, err
	}
	if err := decodeField(fields["type"], &spec.Type); err != nil {
		return spec, fmt.Errorf("type: %w", err)
	}
	allowed := map[string]bool{"type": true}
	switch spec.Type {
	case "stdio":
		for _, key := range []string{"command", "args", "env", "cwd"} {
			allowed[key] = true
		}
	case "streamable-http", "sse":
		allowed["url"], allowed["headers"] = true, true
	default:
		return spec, fmt.Errorf("unsupported MCP transport %q", spec.Type)
	}
	for key, value := range fields {
		if !allowed[key] {
			return spec, fmt.Errorf("invalid field %q for %s", key, spec.Type)
		}
		if string(value) == "null" {
			return spec, fmt.Errorf("%s must not be null", key)
		}
		if key == "args" {
			var args []json.RawMessage
			if err := json.Unmarshal(value, &args); err != nil {
				return spec, err
			}
			for _, arg := range args {
				var s string
				if err := decodeField(arg, &s); err != nil {
					return spec, err
				}
			}
		}
		if key == "env" || key == "headers" {
			obj, err := object(value)
			if err != nil {
				return spec, err
			}
			for _, v := range obj {
				var s string
				if err := decodeField(v, &s); err != nil {
					return spec, err
				}
			}
		}
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		return spec, err
	}
	if spec.Type == "stdio" {
		if err := validateCommand(spec.Command); err != nil {
			return spec, err
		}
		if _, ok := fields["cwd"]; ok && spec.Cwd == "" {
			return spec, fmt.Errorf("cwd must not be empty")
		}
		for key, value := range spec.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) || reservedEnv(key) {
				return spec, fmt.Errorf("invalid or reserved environment key %q", key)
			}
		}
	} else {
		if err := validateEndpoint(spec.URL); err != nil {
			return spec, err
		}
		seen := map[string]bool{}
		for key, value := range spec.Headers {
			canonical := strings.ToLower(key)
			if seen[canonical] || !httpguts.ValidHeaderFieldName(key) || !httpguts.ValidHeaderFieldValue(value) {
				return spec, fmt.Errorf("invalid or duplicate header %q", key)
			}
			seen[canonical] = true
		}
	}
	return spec, nil
}

func validateCommand(command string) error {
	if command == "" || strings.ContainsRune(command, 0) {
		return fmt.Errorf("command must be one executable token")
	}
	if strings.HasPrefix(command, "./") {
		return nil
	}
	if command == "." || command == ".." || strings.ContainsAny(command, "/\\:") || filepath.IsAbs(command) {
		return fmt.Errorf("command must be bare or begin with ./")
	}
	return nil
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u == nil || u.Host == "" || u.User != nil || u.Fragment != "" || strings.Contains(endpoint, "#") ||
		(u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("invalid MCP endpoint")
	}
	if u.Scheme == "http" && u.Hostname() != "localhost" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("non-loopback MCP endpoints require HTTPS")
		}
	}
	return nil
}

func (s ServerSpec) RuntimeConfig(root, data string) (config.MCPServerConfig, string, []string, error) {
	cfg := config.MCPServerConfig{Enabled: true, Type: s.Type, URL: s.URL, Headers: s.Headers}
	if s.Type != "stdio" {
		return cfg, "", nil, nil
	}
	var err error
	if strings.HasPrefix(s.Command, "./") {
		cfg.Command, err = ResolvePath(root, s.Command)
	} else {
		cfg.Command, err = exec.LookPath(s.Command)
	}
	if err != nil {
		return cfg, "", nil, err
	}
	dir, err := workingDirectory(root, data, s.Cwd)
	if err != nil {
		return cfg, "", nil, err
	}
	for _, arg := range s.Args {
		cfg.Args = append(cfg.Args, Expand(arg, root, data))
	}
	return cfg, dir, Environment(root, data, s.Env), nil
}
