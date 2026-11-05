// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestCreateProviderFromConfig_AppliesOpenRouterProviderSort(t *testing.T) {
	var requestBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message":       map[string]any{"content": "ok"},
					"finish_reason": "stop",
				},
			},
		})
	}))
	defer server.Close()

	cfg := &config.ModelConfig{
		ModelName:    "or-fast",
		Provider:     "openrouter",
		Model:        "openai/gpt-4o",
		APIBase:      server.URL,
		ProviderSort: "throughput",
		APIKeys:      config.SimpleSecureStrings("sk-or-test"),
	}

	provider, modelID, err := CreateProviderFromConfig(cfg)
	if err != nil {
		t.Fatalf("CreateProviderFromConfig() error = %v", err)
	}
	if _, err := provider.Chat(
		t.Context(),
		[]Message{{Role: "user", Content: "hi"}},
		nil,
		modelID,
		nil,
	); err != nil {
		t.Fatalf("Chat() error = %v", err)
	}

	providerConfig, ok := requestBody["provider"].(map[string]any)
	if !ok {
		t.Fatalf("request provider = %#v, want map", requestBody["provider"])
	}
	if got := providerConfig["sort"]; got != "throughput" {
		t.Fatalf("provider.sort = %#v, want throughput", got)
	}
}
