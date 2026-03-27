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
	defaultGeminiModel   = "gemini-embedding-001"
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
	geminiMaxInput       = 2048
)

// GeminiProvider implements the Provider interface using Google's Gemini embedding API.
type GeminiProvider struct {
	config  Config
	client  *http.Client
	model   string
	apiKey  string
	baseURL string
}

// NewGemini creates a new GeminiProvider from the given configuration.
func NewGemini(cfg Config) *GeminiProvider {
	model := cfg.Model
	if model == "" {
		model = defaultGeminiModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}

	return &GeminiProvider{
		config:  cfg,
		client:  &http.Client{},
		model:   model,
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
	}
}

// Name returns the provider name.
func (p *GeminiProvider) Name() string {
	return "gemini"
}

// Model returns the configured embedding model name.
func (p *GeminiProvider) Model() string {
	return p.model
}

// MaxInput returns the maximum number of input tokens supported.
func (p *GeminiProvider) MaxInput() int {
	return geminiMaxInput
}

// EmbedQuery embeds a single text string.
func (p *GeminiProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("gemini embedding: empty response for query")
	}
	return results[0], nil
}

// geminiBatchRequest is the JSON payload sent to the Gemini batchEmbedContents endpoint.
type geminiBatchRequest struct {
	Requests []geminiEmbedRequest `json:"requests"`
}

// geminiEmbedRequest represents a single embedding request within a batch.
type geminiEmbedRequest struct {
	Model   string        `json:"model"`
	Content geminiContent `json:"content"`
}

// geminiContent holds the content parts for a Gemini embedding request.
type geminiContent struct {
	Parts []geminiPart `json:"parts"`
}

// geminiPart holds a single text part.
type geminiPart struct {
	Text string `json:"text"`
}

// geminiBatchResponse is the JSON response from the Gemini batchEmbedContents endpoint.
type geminiBatchResponse struct {
	Embeddings []geminiEmbeddingValues `json:"embeddings"`
}

// geminiEmbeddingValues holds a single embedding vector.
type geminiEmbeddingValues struct {
	Values []float32 `json:"values"`
}

// EmbedBatch embeds multiple texts in a single API call and returns vectors
// in the same order as the input texts.
func (p *GeminiProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	modelRef := "models/" + p.model

	requests := make([]geminiEmbedRequest, len(texts))
	for i, text := range texts {
		requests[i] = geminiEmbedRequest{
			Model: modelRef,
			Content: geminiContent{
				Parts: []geminiPart{{Text: text}},
			},
		}
	}

	reqBody := geminiBatchRequest{
		Requests: requests,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:batchEmbedContents?key=%s", p.baseURL, p.model, p.apiKey)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	// Apply any custom headers from the configuration.
	for k, v := range p.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gemini embedding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini embedding: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp geminiBatchResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("gemini embedding: unmarshal response: %w", err)
	}

	if len(embResp.Embeddings) != len(texts) {
		return nil, fmt.Errorf("gemini embedding: expected %d embeddings, got %d", len(texts), len(embResp.Embeddings))
	}

	vectors := make([][]float32, len(embResp.Embeddings))
	for i, e := range embResp.Embeddings {
		vectors[i] = e.Values
	}

	return vectors, nil
}
