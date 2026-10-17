// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

// Package openairesponses implements the OpenAI Responses API over plain HTTP
// with Bearer API-key auth, for gateways that expose a /responses endpoint
// (e.g. OpenCode Go). The OAuth-based Codex flow lives in the oauth package.
package openairesponses

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	"github.com/sipeed/picoclaw/pkg/providers/common"
	orc "github.com/sipeed/picoclaw/pkg/providers/openai_responses_common"
	"github.com/sipeed/picoclaw/pkg/providers/protocoltypes"
)

type (
	LLMResponse    = protocoltypes.LLMResponse
	Message        = protocoltypes.Message
	ToolDefinition = protocoltypes.ToolDefinition
)

// Provider talks to POST {apiBase}/responses. It is non-streaming.
type Provider struct {
	apiKey        string
	apiBase       string
	userAgent     string
	httpClient    *http.Client
	customHeaders map[string]string
	session       common.SessionHeader
}

// NewProvider creates a Responses API provider. A non-positive
// requestTimeoutSeconds keeps the default timeout.
func NewProvider(
	apiKey, apiBase, proxy, userAgent string,
	requestTimeoutSeconds int,
	customHeaders map[string]string,
) *Provider {
	client := common.NewHTTPClient(proxy)
	if requestTimeoutSeconds > 0 {
		client.Timeout = time.Duration(requestTimeoutSeconds) * time.Second
	}
	return &Provider{
		apiKey:        apiKey,
		apiBase:       strings.TrimRight(apiBase, "/"),
		userAgent:     userAgent,
		httpClient:    client,
		customHeaders: customHeaders,
	}
}

// SetSessionHeader makes every request carry options["session_key"] (or
// fallback when absent) in the named header; an explicit custom header wins.
func (p *Provider) SetSessionHeader(name, fallback string) {
	p.session = common.SessionHeader{Name: strings.TrimSpace(name), Fallback: fallback}
}

func (p *Provider) GetDefaultModel() string { return "" }

func (p *Provider) Chat(
	ctx context.Context,
	messages []Message,
	tools []ToolDefinition,
	model string,
	options map[string]any,
) (*LLMResponse, error) {
	if p.apiKey == "" {
		return nil, fmt.Errorf("API key not configured")
	}

	input, instructions := orc.TranslateMessages(messages)
	params := responses.ResponseNewParams{
		Model: model,
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		Store: openai.Opt(false),
	}
	if instructions != "" {
		params.Instructions = openai.Opt(instructions)
	}
	if len(tools) > 0 {
		params.Tools = orc.TranslateTools(tools, false)
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: openai.Opt(responses.ToolChoiceOptionsAuto),
		}
	}
	if maxTokens, ok := common.AsInt(options["max_tokens"]); ok {
		params.MaxOutputTokens = openai.Opt(int64(maxTokens))
	}
	// Temperature is deliberately not sent: Responses-family models are mostly
	// reasoning models that reject sampling parameters (Codex omits it too).
	if key, ok := options["prompt_cache_key"].(string); ok && key != "" {
		params.PromptCacheKey = openai.Opt(key)
	}

	req, err := common.NewJSONRequest(ctx, http.MethodPost, p.apiBase+"/responses", params)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	if p.userAgent != "" {
		req.Header.Set("User-Agent", p.userAgent)
	}
	for k, v := range p.customHeaders {
		if strings.TrimSpace(k) != "" {
			req.Header.Set(k, v)
		}
	}
	p.session.Apply(req.Header, options)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, common.HandleErrorResponse(resp, p.apiBase)
	}
	return orc.ParseResponseBody(resp.Body)
}
