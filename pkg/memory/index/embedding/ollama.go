package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const (
	defaultOllamaModel   = "nomic-embed-text"
	defaultOllamaBaseURL = "http://localhost:11434"
	ollamaMaxInput       = 8192
)

// OllamaProvider implements the Provider interface using Ollama's local embedding API.
type OllamaProvider struct {
	config  Config
	client  *http.Client
	model   string
	baseURL string
}

// NewOllama creates a new OllamaProvider from the given configuration.
func NewOllama(cfg Config) *OllamaProvider {
	model := cfg.Model
	if model == "" {
		model = defaultOllamaModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOllamaBaseURL
	}

	return &OllamaProvider{
		config:  cfg,
		client:  &http.Client{},
		model:   model,
		baseURL: baseURL,
	}
}

// Name returns the provider name.
func (p *OllamaProvider) Name() string {
	return "ollama"
}

// Model returns the configured embedding model name.
func (p *OllamaProvider) Model() string {
	return p.model
}

// MaxInput returns the maximum number of input tokens supported.
func (p *OllamaProvider) MaxInput() int {
	return ollamaMaxInput
}

// EmbedQuery embeds a single text string.
func (p *OllamaProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("ollama embedding: empty response for query")
	}
	return results[0], nil
}

// ollamaEmbedRequest is the JSON payload sent to the Ollama embed endpoint.
type ollamaEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// ollamaEmbedResponse is the JSON response from the Ollama embed endpoint.
type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

// EmbedBatch embeds multiple texts in a single API call and returns vectors
// in the same order as the input texts.
func (p *OllamaProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := ollamaEmbedRequest{
		Model: p.model,
		Input: texts,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("ollama embedding: marshal request: %w", err)
	}

	url := p.baseURL + "/api/embed"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("ollama embedding: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Apply any custom headers from the configuration.
	for k, v := range p.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embedding: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("ollama embedding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embedding: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp ollamaEmbedResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("ollama embedding: unmarshal response: %w", err)
	}

	if len(embResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("ollama embedding: expected %d embeddings, got %d", len(texts), len(embResp.Embeddings))
	}

	return embResp.Embeddings, nil
}
