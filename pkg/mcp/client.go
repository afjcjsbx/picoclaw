package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// Client is the unified interface for communicating with an MCP server (either local or remote)
type Client interface {
	Initialize(ctx context.Context) error
	ListTools(ctx context.Context) (*ListToolsResult, error)
	CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error)
	Close()
}

// StdioClient handles the JSON-RPC 2.0 connection with an MCP server via stdio.
type StdioClient struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	nextID uint64 // Thread-safe ID generator for JSON-RPC requests.

	// pending keeps track of pending requests
	pending map[string]chan *JSONRPCMessage
	mu      sync.Mutex

	// ctx and cancel to manage the background process life cycle
	ctx    context.Context
	cancel context.CancelFunc
}

// Let's make sure at compile time that StdioClient implements the Client interface
var _ Client = (*StdioClient)(nil)

// NewStdioClient starts an MCP server process and establishes communication channels.
func NewStdioClient(ctx context.Context, command string, args []string, env []string) (*StdioClient, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	if len(env) > 0 {
		cmd.Env = env
	}

	// Forwards server errors (stderr) to PicoClaw console for easy debugging
	cmd.Stderr = os.Stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}

	// Starts the MCP server process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start mcp server process: %w", err)
	}

	clientCtx, cancel := context.WithCancel(context.Background())

	client := &StdioClient{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[string]chan *JSONRPCMessage),
		ctx:     clientCtx,
		cancel:  cancel,
	}

	// Starts the reading loop in the background
	go client.readLoop()

	return client, nil
}

// sendRequest sends a JSON-RPC message and waits for a response or context timeout.
func (c *StdioClient) sendRequest(ctx context.Context, method string, params any) (*JSONRPCMessage, error) {
	id := fmt.Sprintf("%d", atomic.AddUint64(&c.nextID, 1))

	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req := JSONRPCMessage{
		JSONRPC: "2.0",
		ID:      &id,
		Method:  method,
		Params:  paramsRaw,
	}

	reqBytes, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	// Create the response channel for this specific request
	respCh := make(chan *JSONRPCMessage, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	// Send the request by writing to stdin with a newline (standard MCP stdio)
	reqBytes = append(reqBytes, '\n')
	if _, err := c.stdin.Write(reqBytes); err != nil {
		return nil, fmt.Errorf("failed to write request: %w", err)
	}

	// Wait for response or deletion of context
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.ctx.Done():
		return nil, fmt.Errorf("client closed while waiting for response")
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	}
}

// readLoop continuously reads from the stdout of the MCP process and routes the responses.
func (c *StdioClient) readLoop() {
	defer c.cancel()

	scanner := bufio.NewScanner(c.stdout)
	// Increase the buffer if JSON payloads can exceed 64KB
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()

		var msg JSONRPCMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			logger.ErrorCF("mcp_client", "Failed to unmarshal JSON-RPC message", map[string]any{"error": err})
			continue
		}

		// If it is a response to our request
		if msg.ID != nil {
			id := *msg.ID
			c.mu.Lock()
			ch, exists := c.pending[id]
			c.mu.Unlock()

			if exists {
				ch <- &msg
			} else {
				logger.DebugCF("mcp_client", "Received response for unknown/expired ID", map[string]any{"id": id})
			}
		} else if msg.Method != "" {
			// If it is a notification from the server
			logger.DebugCF("mcp_client", "Received notification", map[string]any{"method": msg.Method})
		}
	}

	if err := scanner.Err(); err != nil {
		logger.ErrorCF("mcp_client", "Error reading stdout", map[string]any{"error": err})
	}
}

// Initialize performs the mandatory handshake for MCP
func (c *StdioClient) Initialize(ctx context.Context) error {
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]any{
			"name":    "picoclaw",
			"version": "1.0.0",
		},
		"capabilities": map[string]any{},
	}

	_, err := c.sendRequest(ctx, "initialize", params)
	if err != nil {
		return err
	}

	// After initialization, MCP prompts to send the notification "initialized"
	initializedMsg := `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	_, err = c.stdin.Write([]byte(initializedMsg))

	return err
}

func (c *StdioClient) ListTools(ctx context.Context) (*ListToolsResult, error) {
	resp, err := c.sendRequest(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}

	var result ListToolsResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tools list: %w", err)
	}

	return &result, nil
}

// CallTool runs a specific tool on the server
func (c *StdioClient) CallTool(ctx context.Context, name string, args map[string]any) (*CallToolResult, error) {
	params := map[string]any{
		"name":      name,
		"arguments": args,
	}

	resp, err := c.sendRequest(ctx, "tools/call", params)
	if err != nil {
		return nil, err
	}

	var result CallToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal call tool result: %w", err)
	}

	return &result, nil
}

func (c *StdioClient) Close() {
	c.cancel()
	c.stdin.Close()
	_ = c.cmd.Wait()
}
