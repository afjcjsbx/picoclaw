package embedding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
)

const (
	defaultOpenAIModel   = "text-embedding-3-small"
	defaultOpenAIBaseURL = "https://api.openai.com"
	openAIMaxInput       = 8191
)

// Config holds configuration for an embedding provider.
type Config struct {
	Provider string
	Model    string
	APIKey   string
	BaseURL  string
	Headers  map[string]string
}

// OpenAIProvider implements the Provider interface using OpenAI's embedding API.
type OpenAIProvider struct {
	config  Config
	client  *http.Client
	model   string
	apiKey  string
	baseURL string
}

// NewOpenAI creates a new OpenAIProvider from the given configuration.
func NewOpenAI(cfg Config) *OpenAIProvider {
	model := cfg.Model
	if model == "" {
		model = defaultOpenAIModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultOpenAIBaseURL
	}

	return &OpenAIProvider{
		config:  cfg,
		client:  &http.Client{},
		model:   model,
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
	}
}

// Name returns the provider name.
func (p *OpenAIProvider) Name() string {
	return "openai"
}

// Model returns the configured embedding model name.
func (p *OpenAIProvider) Model() string {
	return p.model
}

// MaxInput returns the maximum number of input tokens supported.
func (p *OpenAIProvider) MaxInput() int {
	return openAIMaxInput
}

// EmbedQuery embeds a single text string.
func (p *OpenAIProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("openai embedding: empty response for query")
	}
	return results[0], nil
}

// embeddingRequest is the JSON payload sent to the OpenAI embeddings endpoint.
type embeddingRequest struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embeddingResponse is the JSON response from the OpenAI embeddings endpoint.
type embeddingResponse struct {
	Data []embeddingData `json:"data"`
}

// embeddingData holds a single embedding vector and its position index.
type embeddingData struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

// EmbedBatch embeds multiple texts in a single API call and returns vectors
// in the same order as the input texts.
func (p *OpenAIProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embeddingRequest{
		Input: texts,
		Model: p.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai embedding: marshal request: %w", err)
	}

	url := p.baseURL + "/v1/embeddings"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openai embedding: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Apply any custom headers from the configuration.
	for k, v := range p.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai embedding: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai embedding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai embedding: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("openai embedding: unmarshal response: %w", err)
	}

	if len(embResp.Data) != len(texts) {
		return nil, fmt.Errorf("openai embedding: expected %d embeddings, got %d", len(texts), len(embResp.Data))
	}

	// Sort by index to ensure vectors match the input order.
	sort.Slice(embResp.Data, func(i, j int) bool {
		return embResp.Data[i].Index < embResp.Data[j].Index
	})

	vectors := make([][]float32, len(embResp.Data))
	for i, d := range embResp.Data {
		vectors[i] = d.Embedding
	}

	return vectors, nil
}
