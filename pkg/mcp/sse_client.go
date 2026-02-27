package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

var _ Client = (*SSEClient)(nil)

// SSEClient implements the Client interface for remote MCP servers via HTTP/SSE.
type SSEClient struct {
	sseURL    string
	postURL   string
	postReady chan struct{}
	headers   map[string]string

	httpClient *http.Client
	sseResp    *http.Response

	nextID  uint64
	pending map[string]chan *JSONRPCMessage
	mu      sync.Mutex

	ctx    context.Context
	cancel context.CancelFunc
}

func NewSSEClient(ctx context.Context, sseEndpoint string, headers map[string]string) (*SSEClient, error) {
	clientCtx, cancel := context.WithCancel(context.Background())

	c := &SSEClient{
		sseURL:     sseEndpoint,
		postURL:    sseEndpoint,
		postReady:  make(chan struct{}),
		headers:    headers,
		httpClient: &http.Client{},
		pending:    make(map[string]chan *JSONRPCMessage),
		ctx:        clientCtx,
		cancel:     cancel,
	}

	req, err := http.NewRequestWithContext(clientCtx, "GET", sseEndpoint, nil)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create SSE request: %w", err)
	}

	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to connect to SSE endpoint: %w", err)
	}

	// 1. TRAPPOLA PER ERRORI HTTP CLAMOROSI
	if resp.StatusCode != http.StatusOK {
		// Leggiamo un pezzo del body per capire l'errore reale
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("unexpected status code %d. Body: %s", resp.StatusCode, string(bodyBytes))
	}

	// 2. TRAPPOLA PER FALSI POSITIVI (HTML/JSON invece di SSE)
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/event-stream") {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf(
			"server did not return an SSE stream (got %s). Response: %s",
			contentType,
			string(bodyBytes),
		)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		return nil, fmt.Errorf("unexpected status code from SSE: %d", resp.StatusCode)
	}

	c.sseResp = resp

	close(c.postReady)
	// Avvia il loop di lettura in background
	go c.readLoop()

	// Timeout esplicito per l'handshake iniziale per evitare di bloccare l'Agente in eterno!
	select {
	case <-c.postReady:
		logger.DebugCF("mcp_sse", "SSE Client completely ready", map[string]any{"postURL": c.postURL})
	case <-time.After(15 * time.Second):
		c.Close()
		return nil, fmt.Errorf("timeout (15s) waiting for 'endpoint' event from remote SSE server")
	case <-ctx.Done():
		c.Close()
		return nil, fmt.Errorf("context cancelled while waiting for SSE endpoint")
	}

	return c, nil
}

func (c *SSEClient) readLoop() {
	defer c.cancel()
	defer c.sseResp.Body.Close()

	scanner := bufio.NewScanner(c.sseResp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	var currentEvent string
	var dataBuffer strings.Builder

	for scanner.Scan() {
		line := scanner.Text()

		logger.DebugCF("mcp_sse", "RAW SSE LINE", map[string]any{"line": line})
		line = strings.TrimRight(line, "\r")

		// Una riga vuota indica la fine dell'evento corrente
		if line == "" {
			if currentEvent == "endpoint" {
				c.resolvePostURL(dataBuffer.String())

				// Chiudiamo il canale in modo sicuro per sbloccare l'inizializzazione
				select {
				case <-c.postReady:
				default:
					close(c.postReady)
				}
			} else if dataBuffer.Len() > 0 && currentEvent != "endpoint" {
				// Processa gli eventi standard JSON-RPC in arrivo dal server
				c.handleMessage([]byte(dataBuffer.String()))
			}

			// Reset per l'evento successivo
			currentEvent = ""
			dataBuffer.Reset()
			continue
		}

		if strings.HasPrefix(line, "event:") {
			currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			logger.DebugCF("mcp_sse", "Received SSE event", map[string]any{"event": currentEvent})
		} else if strings.HasPrefix(line, "data:") {
			// Estrarre i dati rimuovendo lo spazio iniziale che lo standard a volte aggiunge
			dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			dataBuffer.WriteString(dataStr)
		}
	}

	if err := scanner.Err(); err != nil {
		logger.ErrorCF("mcp_sse", "Error reading SSE stream", map[string]any{"error": err})
	}
}

func (c *SSEClient) resolvePostURL(endpointPath string) {
	base, _ := url.Parse(c.sseURL)
	ref, err := url.Parse(endpointPath)
	if err == nil {
		c.postURL = base.ResolveReference(ref).String()
	} else {
		c.postURL = endpointPath
	}
}

func (c *SSEClient) handleMessage(data []byte) {
	var msg JSONRPCMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return
	}

	if msg.ID != nil {
		id := *msg.ID
		c.mu.Lock()
		ch, exists := c.pending[id]
		c.mu.Unlock()

		if exists {
			ch <- &msg
		}
	}
}

func (c *SSEClient) sendRequest(ctx context.Context, method string, params any) (*JSONRPCMessage, error) {
	id := fmt.Sprintf("%d", atomic.AddUint64(&c.nextID, 1))
	paramsRaw, _ := json.Marshal(params)

	reqMsg := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  paramsRaw,
	}
	reqBytes, _ := json.Marshal(reqMsg)

	respCh := make(chan *JSONRPCMessage, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	req, err := http.NewRequestWithContext(ctx, "POST", c.postURL, bytes.NewReader(reqBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "picoclaw/1.0")

	for k, v := range c.headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf(
			"POST request failed with status: %d. Server says: %s",
			resp.StatusCode,
			string(bodyBytes),
		)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, fmt.Errorf("sse connection closed")
	case result := <-respCh:
		if result.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", result.Error.Code, result.Error.Message)
		}
		return result, nil
	}
}

func (c *SSEClient) Initialize(ctx context.Context) error {
	_, err := c.sendRequest(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo":      map[string]any{"name": "picoclaw", "version": "1.0.0"},
		"capabilities":    map[string]any{},
	})

	if err == nil {
		initMsg := `{"jsonrpc":"2.0","method":"notifications/initialized"}`
		req, _ := http.NewRequest("POST", c.postURL, strings.NewReader(initMsg))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "picoclaw/1.0")

		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		go func() {
			resp, err := c.httpClient.Do(req)
			if err == nil {
				if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
					bodyBytes, _ := io.ReadAll(resp.Body)
					logger.ErrorCF("mcp_sse", "Initialize notification failed", map[string]any{
						"status": resp.StatusCode,
						"body":   string(bodyBytes),
					})
				}
				resp.Body.Close()
			}
		}()
	}
	return err
}

func (c *SSEClient) ListTools(ctx context.Context) (*ListToolsResult, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var result ListToolsResult
	err = json.Unmarshal(resp.Result, &result)
	return &result, err
}

func (c *SSEClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	resp, err := c.sendRequest(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return nil, err
	}
	var result CallToolResult
	err = json.Unmarshal(resp.Result, &result)
	return &result, err
}

func (c *SSEClient) Close() {
	c.cancel()
}
