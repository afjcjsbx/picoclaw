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
	defaultVoyageModel   = "voyage-4-large"
	defaultVoyageBaseURL = "https://api.voyageai.com"
	voyageMaxInput       = 16000
)

// VoyageProvider implements the Provider interface using Voyage AI's embedding API.
type VoyageProvider struct {
	config  Config
	client  *http.Client
	model   string
	apiKey  string
	baseURL string
}

// NewVoyage creates a new VoyageProvider from the given configuration.
func NewVoyage(cfg Config) *VoyageProvider {
	model := cfg.Model
	if model == "" {
		model = defaultVoyageModel
	}

	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultVoyageBaseURL
	}

	return &VoyageProvider{
		config:  cfg,
		client:  &http.Client{},
		model:   model,
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
	}
}

// Name returns the provider name.
func (p *VoyageProvider) Name() string {
	return "voyage"
}

// Model returns the configured embedding model name.
func (p *VoyageProvider) Model() string {
	return p.model
}

// MaxInput returns the maximum number of input tokens supported.
func (p *VoyageProvider) MaxInput() int {
	return voyageMaxInput
}

// EmbedQuery embeds a single text string.
func (p *VoyageProvider) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	results, err := p.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("voyage embedding: empty response for query")
	}
	return results[0], nil
}

// EmbedBatch embeds multiple texts in a single API call and returns vectors
// in the same order as the input texts.
func (p *VoyageProvider) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}

	reqBody := embeddingRequest{
		Input: texts,
		Model: p.model,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("voyage embedding: marshal request: %w", err)
	}

	url := p.baseURL + "/v1/embeddings"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("voyage embedding: create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	// Apply any custom headers from the configuration.
	for k, v := range p.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("voyage embedding: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("voyage embedding: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("voyage embedding: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var embResp embeddingResponse
	if err := json.Unmarshal(respBody, &embResp); err != nil {
		return nil, fmt.Errorf("voyage embedding: unmarshal response: %w", err)
	}

	if len(embResp.Data) != len(texts) {
		return nil, fmt.Errorf("voyage embedding: expected %d embeddings, got %d", len(texts), len(embResp.Data))
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
