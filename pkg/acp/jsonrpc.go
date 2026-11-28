package acp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"sync/atomic"
)

const (
	defaultMaxMessageBytes = 1 << 20
	initialScannerBuffer   = 64 << 10
)

// ErrPeerClosed indicates that an ACP connection has been closed.
var ErrPeerClosed = errors.New("acp: peer closed")

// JSON-RPC error codes used by ACP and PicoClaw.
const (
	CodeParseError      = -32700
	CodeInvalidRequest  = -32600
	CodeMethodNotFound  = -32601
	CodeInvalidParams   = -32602
	CodeInternalError   = -32603
	CodeAgentError      = -32000
	CodeSessionNotFound = -32002
	CodeCancelled       = -32800
)

// RPCError is a JSON-RPC error object and also implements error.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements error.
func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Handler handles one ACP request and returns its result.
type Handler func(ctx context.Context, params json.RawMessage) (any, error)

// NotificationHandler handles one ACP notification.
type NotificationHandler func(ctx context.Context, params json.RawMessage)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// Peer is a bidirectional JSON-RPC 2.0 connection over newline-delimited JSON.
// It supports concurrent requests and agent-to-client requests such as
// session/request_permission.
type Peer struct {
	reader          io.Reader
	writer          io.Writer
	maxMessageBytes int64

	writeMu sync.Mutex

	handlersMu    sync.RWMutex
	handlers      map[string]Handler
	notifications map[string]NotificationHandler

	requestMu sync.Mutex
	nextID    atomic.Uint64
	pending   map[string]chan rpcMessage

	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

// NewPeer creates an ACP JSON-RPC peer.
func NewPeer(reader io.Reader, writer io.Writer, maxMessageBytes int64) *Peer {
	if maxMessageBytes <= 0 {
		maxMessageBytes = defaultMaxMessageBytes
	}
	return &Peer{
		reader:          reader,
		writer:          writer,
		maxMessageBytes: maxMessageBytes,
		handlers:        make(map[string]Handler),
		notifications:   make(map[string]NotificationHandler),
		pending:         make(map[string]chan rpcMessage),
		done:            make(chan struct{}),
	}
}

// Register installs a request handler.
func (p *Peer) Register(method string, handler Handler) {
	p.handlersMu.Lock()
	defer p.handlersMu.Unlock()
	p.handlers[method] = handler
}

// OnNotification installs a notification handler.
func (p *Peer) OnNotification(method string, handler NotificationHandler) {
	p.handlersMu.Lock()
	defer p.handlersMu.Unlock()
	p.notifications[method] = handler
}

// Serve reads ACP messages until EOF, an error, or context cancellation.
func (p *Peer) Serve(ctx context.Context) error {
	go func() {
		select {
		case <-ctx.Done():
			p.Close()
		case <-p.done:
		}
	}()

	defer p.Close()

	scanner := bufio.NewScanner(p.reader)
	bufferSize := initialScannerBuffer
	if p.maxMessageBytes < int64(bufferSize) {
		bufferSize = int(p.maxMessageBytes)
	}
	scanner.Buffer(make([]byte, bufferSize), int(p.maxMessageBytes))
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if line[0] == '[' {
			_ = p.writeError(nil, &RPCError{Code: CodeInvalidRequest, Message: "batch requests are not supported"})
			continue
		}

		var msg rpcMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			_ = p.writeError(nil, &RPCError{Code: CodeParseError, Message: "parse error"})
			continue
		}
		if err := p.validate(msg); err != nil {
			_ = p.writeError(msg.ID, err)
			continue
		}
		p.dispatch(ctx, msg)
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, ErrPeerClosed) {
		select {
		case <-p.done:
			return nil
		default:
			return fmt.Errorf("read acp stream: %w", err)
		}
	}
	return nil
}

// Wait blocks until all dispatched request handlers have returned.
func (p *Peer) Wait() {
	p.wg.Wait()
}

// Call sends a request and waits for its response.
func (p *Peer) Call(ctx context.Context, method string, params, result any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal acp request: %w", err)
	}
	if len(payload) == 0 || bytes.Equal(payload, []byte("null")) {
		payload = []byte("{}")
	}

	id := strconv.FormatUint(p.nextID.Add(1), 10)
	rawID := json.RawMessage(id)
	key, _ := requestIDKey(rawID)
	responseCh := make(chan rpcMessage, 1)
	p.requestMu.Lock()
	select {
	case <-p.done:
		p.requestMu.Unlock()
		return ErrPeerClosed
	default:
	}
	p.pending[key] = responseCh
	p.requestMu.Unlock()

	msg := rpcMessage{
		JSONRPC: "2.0",
		ID:      rawID,
		Method:  method,
		Params:  payload,
	}
	if err := p.write(msg); err != nil {
		p.requestMu.Lock()
		delete(p.pending, key)
		p.requestMu.Unlock()
		select {
		case <-p.done:
			return ErrPeerClosed
		default:
			return err
		}
	}

	select {
	case <-ctx.Done():
		p.requestMu.Lock()
		delete(p.pending, key)
		p.requestMu.Unlock()
		return ctx.Err()
	case <-p.done:
		return ErrPeerClosed
	case response, ok := <-responseCh:
		if !ok {
			return ErrPeerClosed
		}
		if response.Error != nil {
			return response.Error
		}
		if result == nil {
			return nil
		}
		if len(response.Result) == 0 {
			return fmt.Errorf("acp response %s has no result", method)
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode acp response %s: %w", method, err)
		}
		return nil
	}
}

// Notify sends a JSON-RPC notification.
func (p *Peer) Notify(method string, params any) error {
	payload, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal acp notification: %w", err)
	}
	return p.write(rpcMessage{JSONRPC: "2.0", Method: method, Params: payload})
}

// Close releases pending requests and closes the reader when possible.
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		close(p.done)
		if closer, ok := p.reader.(io.Closer); ok {
			_ = closer.Close()
		}
		p.requestMu.Lock()
		for id, responseCh := range p.pending {
			delete(p.pending, id)
			close(responseCh)
		}
		p.requestMu.Unlock()
	})
}

func (p *Peer) dispatch(ctx context.Context, msg rpcMessage) {
	if msg.Method != "" {
		if len(msg.ID) == 0 {
			p.handleNotification(ctx, msg)
			return
		}
		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			p.handleRequest(ctx, msg)
		}()
		return
	}
	p.handleResponse(msg)
}

func (p *Peer) handleRequest(ctx context.Context, msg rpcMessage) {
	p.handlersMu.RLock()
	handler := p.handlers[msg.Method]
	p.handlersMu.RUnlock()
	if handler == nil {
		_ = p.writeError(msg.ID, &RPCError{Code: CodeMethodNotFound, Message: "method not found"})
		return
	}

	result, err := handler(ctx, msg.Params)
	if err != nil {
		_ = p.writeError(msg.ID, rpcErrorFrom(err))
		return
	}
	payload, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		_ = p.writeError(msg.ID, &RPCError{Code: CodeInternalError, Message: "marshal response"})
		return
	}
	_ = p.write(rpcMessage{JSONRPC: "2.0", ID: msg.ID, Result: payload})
}

func (p *Peer) handleNotification(ctx context.Context, msg rpcMessage) {
	p.handlersMu.RLock()
	handler := p.notifications[msg.Method]
	p.handlersMu.RUnlock()
	if handler != nil {
		handler(ctx, msg.Params)
	}
}

func (p *Peer) handleResponse(msg rpcMessage) {
	key, ok := requestIDKey(msg.ID)
	if !ok {
		return
	}
	p.requestMu.Lock()
	responseCh := p.pending[key]
	delete(p.pending, key)
	p.requestMu.Unlock()
	if responseCh != nil {
		responseCh <- msg
	}
}

func (p *Peer) validate(msg rpcMessage) *RPCError {
	if msg.JSONRPC != "2.0" {
		return &RPCError{Code: CodeInvalidRequest, Message: "invalid json-rpc version"}
	}
	if msg.Method != "" {
		if len(msg.ID) != 0 {
			if _, ok := requestIDKey(msg.ID); !ok {
				return &RPCError{Code: CodeInvalidRequest, Message: "invalid request id"}
			}
		}
		return nil
	}
	if len(msg.ID) == 0 {
		return &RPCError{Code: CodeInvalidRequest, Message: "response requires an id"}
	}
	if _, ok := requestIDKey(msg.ID); !ok {
		return &RPCError{Code: CodeInvalidRequest, Message: "invalid response id"}
	}
	if (len(msg.Result) == 0) == (msg.Error == nil) {
		return &RPCError{Code: CodeInvalidRequest, Message: "response requires result or error"}
	}
	return nil
}

func (p *Peer) write(msg rpcMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal acp message: %w", err)
	}
	payload = append(payload, '\n')

	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if _, err := p.writer.Write(payload); err != nil {
		return fmt.Errorf("write acp message: %w", err)
	}
	if flusher, ok := p.writer.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return fmt.Errorf("flush acp message: %w", err)
		}
	}
	return nil
}

func (p *Peer) writeError(id json.RawMessage, rpcErr *RPCError) error {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return p.write(rpcMessage{JSONRPC: "2.0", ID: id, Error: rpcErr})
}

func rpcErrorFrom(err error) *RPCError {
	var rpcErr *RPCError
	if errors.As(err, &rpcErr) {
		return rpcErr
	}
	return &RPCError{Code: CodeInternalError, Message: err.Error()}
}

func requestIDKey(id json.RawMessage) (string, bool) {
	if len(id) == 0 {
		return "", false
	}
	decoder := json.NewDecoder(bytes.NewReader(id))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", false
	}
	switch typed := value.(type) {
	case nil:
		return "null", true
	case string:
		return "s:" + typed, true
	case json.Number:
		if _, err := typed.Int64(); err != nil {
			return "", false
		}
		return "n:" + typed.String(), true
	default:
		return "", false
	}
}
