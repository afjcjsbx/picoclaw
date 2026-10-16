package providers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

// Every model of the OpenCode Go docs table plus the extras served by /v1/models.
func TestOpenCodeGoRoute(t *testing.T) {
	cases := map[string]struct {
		family opencodeGoFamily
		path   string
	}{
		"grok-4.7":                   {opencodeGoResponses, "/openai/v1"},
		"grok-4.6":                   {opencodeGoResponses, "/openai/v1"},
		"gpt-6-luna":                 {opencodeGoResponses, "/openai/v1"},
		"gpt-5.6-luna":               {opencodeGoResponses, "/openai/v1"},
		"muse-spark-1.3-contributor": {opencodeGoResponses, "/openai/v1"},
		"Muse-Spark-1.2-Contributor": {opencodeGoResponses, "/openai/v1"},
		"claude-haiku-5-5":           {opencodeGoMessages, "/anthropic/v1"},
		"minimax-m3":                 {opencodeGoMessages, "/anthropic/v1"},
		"minimax-m2.7":               {opencodeGoMessages, "/anthropic/v1"},
		"qwen3.8-max":                {opencodeGoMessages, "/anthropic/v1"},
		"qwen3.7-plus":               {opencodeGoMessages, "/anthropic/v1"},
		"gemini-4-argon-contributor": {opencodeGoGoogle, "/google/v1beta"},
		"kimi-k3":                    {opencodeGoChat, "/openai/v1"},
		"glm-5.3-flash":              {opencodeGoChat, "/openai/v1"},
		"deepseek-v4-pro":            {opencodeGoChat, "/openai/v1"},
		"mimo-v2.5-pro":              {opencodeGoChat, "/openai/v1"},
		"longcat-2.0":                {opencodeGoChat, "/openai/v1"},
		"hy3":                        {opencodeGoChat, "/openai/v1"},
		"space-bunny":                {opencodeGoChat, "/openai/v1"},
		"some-future-model":          {opencodeGoChat, "/openai/v1"},
	}
	for model, want := range cases {
		family, path := opencodeGoRoute(model)
		if family != want.family || path != want.path {
			t.Errorf("opencodeGoRoute(%q) = (%d, %q), want (%d, %q)", model, family, path, want.family, want.path)
		}
	}
}

func TestOpenCodeGoCatalogEntry(t *testing.T) {
	if got := DefaultAPIBaseForProtocol("opencode-go"); got != "https://opencode.ai/inference/go" {
		t.Fatalf("default api base = %q", got)
	}
	if !IsHTTPAPIProtocol("opencode-go") {
		t.Fatal("opencode-go should be an HTTP API protocol")
	}
}

func TestOpenCodeGoRequiresAPIKey(t *testing.T) {
	cfg := &config.ModelConfig{ModelName: "go", Model: "opencode-go/kimi-k3"}
	if _, _, err := CreateProviderFromConfig(cfg); err == nil {
		t.Fatal("expected error without api_key")
	}
}

// Each family must hit its own path with its own auth scheme, carry the
// session header and the PicoClaw user agent, and honor custom headers.
func TestOpenCodeGoEndToEnd(t *testing.T) {
	const (
		responsesBody = `{"id":"r","object":"response","created_at":1,"output":[{"type":"message","id":"m","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"status":"completed","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`
		messagesBody  = `{"id":"m","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
		chatBody      = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
		googleBody    = `{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]},"finishReason":"STOP"}]}`
	)
	tests := []struct {
		model      string
		body       string
		wantPath   string
		authHeader string
		wantAuth   string
	}{
		{"opencode-go/grok-4.7", responsesBody, "/openai/v1/responses", "Authorization", "Bearer k"},
		{"opencode-go/qwen3.8-max", messagesBody, "/anthropic/v1/messages", "X-Api-Key", "k"},
		{"opencode-go/kimi-k3", chatBody, "/openai/v1/chat/completions", "Authorization", "Bearer k"},
		{
			"opencode-go/gemini-4-argon-contributor", googleBody,
			"/google/v1beta/models/gemini-4-argon-contributor:generateContent", "X-Goog-Api-Key", "k",
		},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			var gotPath string
			var gotHeader http.Header
			var gotBody map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath, gotHeader = r.URL.Path, r.Header.Clone()
				raw, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(raw, &gotBody)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			cfg := &config.ModelConfig{
				ModelName:     "go",
				Model:         tt.model,
				APIBase:       srv.URL + "/",
				CustomHeaders: map[string]string{"X-Custom": "yes"},
			}
			cfg.SetAPIKey("k")
			provider, modelID, err := CreateProviderFromConfig(cfg)
			if err != nil {
				t.Fatalf("CreateProviderFromConfig: %v", err)
			}
			resp, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, modelID,
				map[string]any{"session_key": "agent:main:chat 1", "max_tokens": 64, "temperature": 0.5})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
			if resp.Content != "ok" {
				t.Fatalf("content = %q", resp.Content)
			}
			if gotPath != tt.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tt.wantPath)
			}
			if got := gotHeader.Get(tt.authHeader); got != tt.wantAuth {
				t.Errorf("%s = %q, want %q", tt.authHeader, got, tt.wantAuth)
			}
			if got := gotHeader.Get("x-opencode-session"); got != "agent:main:chat 1" {
				t.Errorf("x-opencode-session = %q", got)
			}
			if got := gotHeader.Get("X-Custom"); got != "yes" {
				t.Errorf("X-Custom = %q", got)
			}
			if got := gotHeader.Get("User-Agent"); got == "" || got[:8] != "PicoClaw" {
				t.Errorf("User-Agent = %q, want PicoClaw/*", got)
			}
			if tt.wantPath == "/openai/v1/responses" {
				if gotBody["model"] != "grok-4.7" || gotBody["store"] != false ||
					gotBody["max_output_tokens"] != float64(64) {
					t.Errorf("unexpected responses body: %v", gotBody)
				}
				if _, ok := gotBody["temperature"]; ok {
					t.Error("responses body must not carry temperature")
				}
			}
		})
	}
}

// An explicit custom header must win over the injected session id.
func TestOpenCodeGoExplicitSessionHeaderWins(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-opencode-session")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := &config.ModelConfig{
		ModelName:     "go",
		Model:         "opencode-go/kimi-k3",
		APIBase:       srv.URL,
		CustomHeaders: map[string]string{"x-opencode-session": "pinned"},
	}
	cfg.SetAPIKey("k")
	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, modelID,
		map[string]any{"session_key": "other"}); err != nil {
		t.Fatal(err)
	}
	if got != "pinned" {
		t.Fatalf("x-opencode-session = %q, want pinned", got)
	}
}

// The API answers 400 MissingSessionID without the header, so calls with no
// session_key (summaries, subagents) must still send one.
func TestOpenCodeGoFallbackSession(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-opencode-session")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	cfg := &config.ModelConfig{ModelName: "go-kimi", Model: "opencode-go/kimi-k3", APIBase: srv.URL}
	cfg.SetAPIKey("k")
	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, modelID,
		map[string]any{"max_tokens": 64}); err != nil {
		t.Fatal(err)
	}
	if got != "picoclaw-go-kimi" {
		t.Fatalf("x-opencode-session = %q, want picoclaw-go-kimi", got)
	}
}
