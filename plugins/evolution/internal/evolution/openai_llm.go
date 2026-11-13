package evolution

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultLLMTimeout     = 45 * time.Second
	maxLLMResponseBytes   = 2 << 20
	openAIChatCompletions = "/chat/completions"
)

// OpenAICompatibleLLM is a tiny stdlib-only client for OpenAI-compatible
// /chat/completions endpoints (OpenAI, OpenRouter, Groq, vLLM, llama.cpp,
// Ollama's OpenAI compatibility layer, and similar servers).
type OpenAICompatibleLLM struct {
	baseURL string
	apiKey  string
	model   string
	client  *http.Client
}

func NewOpenAICompatibleLLM(cfg LLMConfig) (*OpenAICompatibleLLM, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		return nil, fmt.Errorf("llm_base_url is required")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("llm_base_url must start with http:// or https://")
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("llm_model is required")
	}
	timeout := defaultLLMTimeout
	if cfg.Timeout > 0 {
		timeout = time.Duration(cfg.Timeout) * time.Second
	}
	return &OpenAICompatibleLLM{
		baseURL: baseURL,
		apiKey:  strings.TrimSpace(cfg.APIKey),
		model:   model,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        1,
				MaxIdleConnsPerHost: 1,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

func (c *OpenAICompatibleLLM) GetDefaultModel() string {
	if c == nil {
		return ""
	}
	return c.model
}

type openAIChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *OpenAICompatibleLLM) Chat(
	ctx context.Context,
	messages []Message,
	model string,
	temperature float64,
) (string, error) {
	if c == nil {
		return "", fmt.Errorf("llm is not configured")
	}
	if strings.TrimSpace(model) == "" {
		model = c.model
	}
	body, err := json.Marshal(openAIChatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: temperature,
		Stream:      false,
	})
	if err != nil {
		return "", err
	}

	url := c.baseURL
	if !strings.HasSuffix(url, openAIChatCompletions) {
		url += openAIChatCompletions
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(io.LimitReader(resp.Body, maxLLMResponseBytes))
	if err != nil {
		return "", err
	}
	var decoded openAIChatResponse
	if err := json.Unmarshal(payload, &decoded); err != nil {
		return "", fmt.Errorf("decode llm response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(payload))
		if decoded.Error != nil && decoded.Error.Message != "" {
			message = decoded.Error.Message
		}
		if len(message) > 500 {
			message = message[:500]
		}
		return "", fmt.Errorf("llm request failed: status %d: %s", resp.StatusCode, message)
	}
	if decoded.Error != nil && decoded.Error.Message != "" {
		return "", fmt.Errorf("llm error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("llm returned no choices")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}
