package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOpenAI_Name(t *testing.T) {
	p := NewOpenAI(Config{APIKey: "test"})
	if p.Name() != "openai" {
		t.Errorf("expected 'openai', got %q", p.Name())
	}
}

func TestOpenAI_DefaultModel(t *testing.T) {
	p := NewOpenAI(Config{APIKey: "test"})
	if p.Model() != "text-embedding-3-small" {
		t.Errorf("expected default model, got %q", p.Model())
	}
}

func TestOpenAI_CustomModel(t *testing.T) {
	p := NewOpenAI(Config{APIKey: "test", Model: "text-embedding-3-large"})
	if p.Model() != "text-embedding-3-large" {
		t.Errorf("expected custom model, got %q", p.Model())
	}
}

func TestOpenAI_EmbedBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("expected /v1/embeddings, got %s", r.URL.Path)
		}
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-key" {
			t.Errorf("expected 'Bearer test-key', got %q", auth)
		}

		var req embeddingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}

		// Return mock embeddings (reversed order to test index sorting)
		resp := embeddingResponse{
			Data: make([]embeddingData, len(req.Input)),
		}
		for i := range req.Input {
			resp.Data[i] = embeddingData{
				Embedding: []float32{float32(i) * 0.1, float32(i) * 0.2},
				Index:     i,
			}
		}
		// Reverse to test sorting
		for i, j := 0, len(resp.Data)-1; i < j; i, j = i+1, j-1 {
			resp.Data[i], resp.Data[j] = resp.Data[j], resp.Data[i]
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAI(Config{
		APIKey:  "test-key",
		BaseURL: server.URL,
	})

	vectors, err := p.EmbedBatch(context.Background(), []string{"hello", "world", "test"})
	if err != nil {
		t.Fatalf("EmbedBatch: %v", err)
	}
	if len(vectors) != 3 {
		t.Fatalf("expected 3 vectors, got %d", len(vectors))
	}

	// Verify order is correct (sorted by index)
	if vectors[0][0] != 0.0 {
		t.Errorf("first vector should be [0.0, 0.0], got %v", vectors[0])
	}
	if vectors[1][0] != 0.1 {
		t.Errorf("second vector should be [0.1, 0.2], got %v", vectors[1])
	}
}

func TestOpenAI_EmbedQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := embeddingResponse{
			Data: []embeddingData{
				{Embedding: []float32{0.5, 0.6, 0.7}, Index: 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	p := NewOpenAI(Config{APIKey: "test-key", BaseURL: server.URL})
	vec, err := p.EmbedQuery(context.Background(), "test query")
	if err != nil {
		t.Fatalf("EmbedQuery: %v", err)
	}
	if len(vec) != 3 {
		t.Errorf("expected 3 dims, got %d", len(vec))
	}
}

func TestOpenAI_ErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer server.Close()

	p := NewOpenAI(Config{APIKey: "bad-key", BaseURL: server.URL})
	_, err := p.EmbedBatch(context.Background(), []string{"test"})
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}

func TestOpenAI_EmptyInput(t *testing.T) {
	p := NewOpenAI(Config{APIKey: "test"})
	vectors, err := p.EmbedBatch(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vectors != nil {
		t.Errorf("expected nil for empty input, got %v", vectors)
	}
}
