package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
)

var _ Client = (*HTTPClient)(nil)

// HTTPClient implements the Client interface for "stateless" MCP servers (REST type).
type HTTPClient struct {
	url        string
	headers    map[string]string
	httpClient *http.Client
	nextID     uint64
}

func NewHTTPClient(url string, headers map[string]string) *HTTPClient {
	return &HTTPClient{
		url:        url,
		headers:    headers,
		httpClient: &http.Client{},
	}
}

func (c *HTTPClient) sendRequest(ctx context.Context, method string, params any) (*JSONRPCMessage, error) {
	id := fmt.Sprintf("%d", atomic.AddUint64(&c.nextID, 1))
	paramsRaw, _ := json.Marshal(params)

	reqMsg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  paramsRaw,
	}
	reqBytes, _ := json.Marshal(reqMsg)

	req, err := http.NewRequestWithContext(ctx, "POST", c.url, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}

	// Permissive headers to bypass WAF and Cloudflare firewalls
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set(
		"User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	)

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP error %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result JSONRPCMessage
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("invalid json response: %w", err)
	}

	if result.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", result.Error.Code, result.Error.Message)
	}

	return &result, nil
}

func (c *HTTPClient) Initialize(ctx context.Context) error {
	_, err := c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "picoclaw", "version": "1.0.0"},
		"capabilities":    map[string]any{},
	})

	if err == nil {
		// MCP protocol requires 'notifications/initialized' after successful initialize
		initMsg := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
		req, _ := http.NewRequest("POST", c.url, bytes.NewBufferString(initMsg))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")

		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		go c.httpClient.Do(req)
	}
	return err
}

func (c *HTTPClient) ListTools(ctx context.Context) (*ListToolsResult, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result ListToolsResult
	err = json.Unmarshal(resp.Result, &result)
	return &result, err
}

func (c *HTTPClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	resp, err := c.sendRequest(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var result CallToolResult
	err = json.Unmarshal(resp.Result, &result)
	return &result, err
}

func (c *HTTPClient) Close() {
	// The HTTP protocol is stateless, there is no fixed connection to close!
}
