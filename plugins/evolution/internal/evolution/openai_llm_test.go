package evolution_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/plugins/evolution/internal/evolution"
)

func TestOpenAICompatibleLLM_Chat(t *testing.T) {
	var received openAIChatRequestForTest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("authorization = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":" hello "}}]}`))
	}))
	defer server.Close()

	client, err := evolution.NewOpenAICompatibleLLM(evolution.LLMConfig{
		BaseURL: server.URL + "/v1",
		APIKey:  "secret",
		Model:   "model-x",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleLLM: %v", err)
	}
	got, err := client.Chat(context.Background(), []evolution.Message{{Role: "user", Content: "hi"}}, "", 0)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if got != "hello" {
		t.Fatalf("content = %q, want hello", got)
	}
	if received.Model != "model-x" || len(received.Messages) != 1 || received.Messages[0].Content != "hi" {
		t.Fatalf("request = %+v", received)
	}
	if received.Temperature != 0 {
		t.Fatalf("temperature = %v, want 0", received.Temperature)
	}
}

func TestOpenAICompatibleLLM_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
	}))
	defer server.Close()

	client, err := evolution.NewOpenAICompatibleLLM(evolution.LLMConfig{
		BaseURL: server.URL,
		Model:   "model-x",
	})
	if err != nil {
		t.Fatalf("NewOpenAICompatibleLLM: %v", err)
	}
	_, err = client.Chat(context.Background(), []evolution.Message{{Role: "user", Content: "hi"}}, "", 0)
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("error = %v, want bad key", err)
	}
}

func TestNewOpenAICompatibleLLM_Validation(t *testing.T) {
	if _, err := evolution.NewOpenAICompatibleLLM(evolution.LLMConfig{Model: "x"}); err == nil {
		t.Fatal("expected missing base URL error")
	}
	if _, err := evolution.NewOpenAICompatibleLLM(evolution.LLMConfig{BaseURL: "ftp://example.com"}); err == nil {
		t.Fatal("expected invalid scheme error")
	}
	if _, err := evolution.NewOpenAICompatibleLLM(evolution.LLMConfig{BaseURL: "https://example.com"}); err == nil {
		t.Fatal("expected missing model error")
	}
}

type openAIChatRequestForTest struct {
	Model       string              `json:"model"`
	Messages    []evolution.Message `json:"messages"`
	Temperature float64             `json:"temperature"`
}
