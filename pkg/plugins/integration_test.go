package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools"
)

// The test executable doubles as a real, offline MCP subprocess. It is copied
// into the package so the command obeys Agent Plugins containment rules.
func TestPluginHelper(t *testing.T) {
	if os.Getenv("PICOCLAW_PLUGIN_HELPER") != "1" {
		return
	}
	if os.Getenv("PLUGIN_TEST_HANG") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "plugin-test", Version: "1"}, nil)
	type input struct {
		Name string `json:"name"`
		Mode string `json:"mode,omitempty"`
	}
	sdk.AddTool(
		server,
		&sdk.Tool{Name: "greet", Description: "Greet a person"},
		func(ctx context.Context, _ *sdk.CallToolRequest, in input) (*sdk.CallToolResult, any, error) {
			if in.Mode == "crash" {
				os.Exit(7)
			}
			if in.Mode == "wait" {
				<-ctx.Done()
				return nil, nil, ctx.Err()
			}
			cwd, _ := os.Getwd()
			text := "Hello, " + in.Name + "!\nroot=" + os.Getenv(
				"PLUGIN_ROOT",
			) + "\ndata=" + os.Getenv(
				"PLUGIN_DATA",
			) + "\ncwd=" + cwd + "\nvalue=" + os.Getenv(
				"VALUE",
			) + "\nsecret=" + os.Getenv(
				"PLUGIN_TEST_PRIVATE_SECRET",
			)
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: text}}}, nil, nil
		},
	)
	if err := server.Run(context.Background(), &sdk.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func writeTestFile(t *testing.T, root, path string, data []byte) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixturePlugin(t *testing.T) string {
	t.Helper()
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	writeTestFile(
		t,
		root,
		"skills/greet/SKILL.md",
		[]byte("---\nname: greet\ndescription: Say hello\n---\nGreet the user.\n"),
	)
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(filepath.Join(root, "helper.exe"), os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	_, err = io.Copy(out, in)
	closeErr := out.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	return root
}

func serverFixture(extra map[string]string) map[string]any {
	env := map[string]string{"PICOCLAW_PLUGIN_HELPER": "1", "VALUE": "${PLUGIN_DATA}/${UNKNOWN}"}
	for key, value := range extra {
		env[key] = value
	}
	return map[string]any{
		"type":    "stdio",
		"command": "./helper.exe",
		"args":    []string{"-test.run=^TestPluginHelper$"},
		"env":     env,
	}
}

func writeServers(t *testing.T, root string, servers map[string]any) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"$schema": MCPSchema, "mcpServers": servers})
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, root, "mcp.json", data)
}

func testConfig(root, data string) config.PluginsConfig {
	return config.PluginsConfig{
		Enabled:       true,
		Directories:   []string{filepath.Join(root, "absent")},
		DataDir:       data,
		InitTimeoutMS: 3000,
		CallTimeoutMS: 1000,
		Entries:       map[string]config.PluginEntryConfig{"demo": {Enabled: true, Path: root}},
	}
}

func TestPluginLoadAndExecuteIntegration(t *testing.T) {
	t.Setenv("PLUGIN_TEST_PRIVATE_SECRET", "must not leak")
	root := fixturePlugin(t)
	writeServers(
		t,
		root,
		map[string]any{
			"good": serverFixture(nil),
			"bad":  map[string]any{"type": "stdio", "command": "does-not-exist-picoclaw-plugin"},
		},
	)
	registry := tools.NewToolRegistry()
	var names []string
	var skillCount int
	m := NewManager(
		testConfig(root, t.TempDir()),
		root,
		func(_ context.Context, capabilities Capabilities) []Diagnostic {
			skillCount += len(capabilities.Skills)
			for _, tool := range capabilities.Tools {
				if _, err := registry.RegisterUnique(tool, false); err != nil {
					t.Error(err)
				}
				names = append(names, tool.Name())
			}
			return nil
		},
	)
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if skillCount != 1 || len(names) != 1 {
		t.Fatalf("skills=%d tools=%v status=%+v", skillCount, names, m.Statuses())
	}
	status := m.Statuses()[0]
	if status.State != Degraded {
		t.Fatalf("status=%+v", status)
	}
	result := registry.Execute(ctx, names[0], map[string]any{"name": "Ada"})
	if result.IsError || !strings.Contains(result.ForLLM, "Hello, Ada!") ||
		!strings.Contains(result.ForLLM, "cwd="+root) ||
		strings.Contains(result.ForLLM, "must not leak") ||
		!strings.Contains(result.ForLLM, "/${UNKNOWN}") {
		t.Fatalf("result=%+v", result)
	}
	if got := registry.Execute(ctx, names[0], map[string]any{}); !got.IsError {
		t.Fatal("invalid arguments accepted")
	}
	start := time.Now()
	if got := registry.Execute(ctx, names[0], map[string]any{"name": "Ada", "mode": "wait"}); !got.IsError {
		t.Fatal("timeout did not fail")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("call timeout not enforced")
	}
	if got := registry.Execute(ctx, names[0], map[string]any{"name": "Ada", "mode": "crash"}); !got.IsError {
		t.Fatal("crash not isolated")
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if got := registry.Execute(ctx, names[0], map[string]any{"name": "Ada"}); !got.IsError {
		t.Fatal("closed plugin accepted call")
	}
}

func TestPluginRemoteMCPHeaderEnv(t *testing.T) {
	t.Setenv("PICOCLAW_TEST_MCP_TOKEN", "test-token")
	remote := sdk.NewServer(&sdk.Implementation{Name: "authenticated", Version: "1"}, nil)
	sdk.AddTool(
		remote,
		&sdk.Tool{Name: "ping"},
		func(_ context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, any, error) {
			return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: "pong"}}}, nil, nil
		},
	)
	handler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return remote }, nil)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer upstream.Close()

	root := t.TempDir()
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	serverConfig := map[string]any{
		"type": "streamable-http", "url": upstream.URL + "/mcp",
		"headers": map[string]string{"Authorization": "Bearer ${PICOCLAW_TEST_MCP_TOKEN}"},
	}
	writeServers(t, root, map[string]any{"remote": serverConfig})
	cfg := testConfig(root, t.TempDir())
	var packageTools []*PluginTool
	packageManager := NewManager(cfg, root, func(_ context.Context, c Capabilities) []Diagnostic {
		packageTools = append(packageTools, c.Tools...)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := packageManager.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	packageStatuses := packageManager.Statuses()
	if len(packageTools) != 0 || len(packageStatuses) != 1 || packageStatuses[0].State != Degraded {
		t.Fatalf(
			"package header unexpectedly expanded: tools=%d status=%+v",
			len(packageTools),
			packageStatuses,
		)
	}
	if err := packageManager.Close(); err != nil {
		t.Fatal(err)
	}
	override, err := json.Marshal(serverConfig)
	if err != nil {
		t.Fatal(err)
	}
	entry := cfg.Entries["demo"]
	entry.MCPOverrides = map[string]json.RawMessage{"remote": override}
	cfg.Entries["demo"] = entry
	var registered []*PluginTool
	m := NewManager(cfg, root, func(_ context.Context, c Capabilities) []Diagnostic {
		registered = append(registered, c.Tools...)
		return nil
	})
	defer m.Close()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if len(registered) != 1 || len(m.Statuses()) != 1 || m.Statuses()[0].State != Ready {
		t.Fatalf("tools=%d status=%+v", len(registered), m.Statuses())
	}
	result := registered[0].Execute(ctx, map[string]any{})
	if result.IsError || !strings.Contains(result.ForLLM, "pong") {
		t.Fatalf("authenticated MCP call failed: %+v", result)
	}
}

func TestPluginFailureBoundariesAndPersistence(t *testing.T) {
	root := fixturePlugin(t)
	writeTestFile(t, root, "mcp.json", []byte(`{"$schema":"wrong","mcpServers":{}}`))
	data := t.TempDir()
	cfg := testConfig(root, data)
	for round := range 2 {
		count := 0
		m := NewManager(
			cfg,
			root,
			func(_ context.Context, capabilities Capabilities) []Diagnostic {
				count += len(capabilities.Skills)
				return nil
			},
		)
		if err := m.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
		if count != 1 || m.Statuses()[0].State != Degraded {
			t.Fatalf("count=%d status=%+v", count, m.Statuses())
		}
		state := filepath.Join(data, "demo", "persist.txt")
		if round == 0 {
			if err := os.WriteFile(state, []byte("preserved"), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if bytes, err := os.ReadFile(state); err != nil || string(bytes) != "preserved" {
			t.Fatalf("data lost: %q %v", bytes, err)
		}
		_ = m.Close()
	}
	writeTestFile(t, root, "plugin.json", []byte(`{"name":"invalid"}`))
	m := NewManager(cfg, root, func(context.Context, Capabilities) []Diagnostic {
		t.Error("invalid manifest published capabilities")
		return nil
	})
	defer m.Close()
	_ = m.Wait(context.Background())
	if m.Statuses()[0].State != Failed {
		t.Fatal(m.Statuses())
	}
}

func TestPluginInitializationTimeout(t *testing.T) {
	root := fixturePlugin(t)
	writeServers(
		t,
		root,
		map[string]any{
			"a-hung": serverFixture(map[string]string{"PLUGIN_TEST_HANG": "1"}),
			"b-good": serverFixture(nil),
		},
	)
	cfg := testConfig(root, t.TempDir())
	cfg.InitTimeoutMS = 200
	var names []string
	m := NewManager(cfg, root, func(_ context.Context, capabilities Capabilities) []Diagnostic {
		for _, tool := range capabilities.Tools {
			names = append(names, tool.Name())
		}
		return nil
	})
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := m.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("tools=%v status=%+v", names, m.Statuses())
	}
}

func TestManagerConcurrentStartClose(t *testing.T) {
	m := NewManager(config.PluginsConfig{}, t.TempDir(), nil)
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); m.Start(); _ = m.Statuses(); _ = m.Close() }()
	}
	wg.Wait()
}
