package a2a

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
)

// codeContextBusy is a JSON-RPC server error (-32000..-32099 is implementation
// defined); the spec has no code for "one task per context".
const codeContextBusy = -32010

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *proto.RPCError `json:"error,omitempty"`
}

func (c *Channel) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+proto.WellKnownPath, c.handleCard)
	mux.HandleFunc("POST "+proto.RPCPath, c.handleRPC)
	return mux
}

// handleCard is public by design (discovery); it is rebuilt per request, never cached.
func (c *Channel) handleCard(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(c.cfg.URL, "/")
	if base == "" {
		base = "http://" + r.Host
	}
	name, desc := c.cfg.Name, c.cfg.Description
	if name == "" {
		name = "PicoClaw"
	}
	if desc == "" {
		desc = "Personal AI agent: answers questions and runs tools (files, web, shell) on its host."
	}
	card := proto.AgentCard{
		Name: name, Description: desc, Version: config.GetVersion(),
		SupportedInterfaces: []proto.Interface{{
			URL: base + proto.RPCPath, ProtocolBinding: proto.BindingJSONRPC, ProtocolVersion: proto.Version,
		}},
		Capabilities:       proto.Capabilities{Streaming: true, PushNotifications: true},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []proto.Skill{{
			ID: "assistant", Name: "General assistant", Description: desc, Tags: []string{"assistant"},
		}},
	}
	if len(c.tokens) > 0 {
		card.SecuritySchemes = map[string]proto.SecurityScheme{
			"bearerAuth": {HTTPAuth: &proto.HTTPAuthScheme{Scheme: "Bearer"}},
		}
		card.SecurityRequirements = []proto.SecurityRequirement{
			{Schemes: map[string]proto.StringList{"bearerAuth": {List: []string{}}}},
		}
	}
	writeJSON(w, http.StatusOK, card)
}

func (c *Channel) handleRPC(w http.ResponseWriter, r *http.Request) {
	if !c.rl.allow(clientIP(r), time.Now()) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	who, ok := c.authorize(r)
	if !ok {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !versionOK(r.Header.Get(proto.VersionHeader)) {
		writeErr(w, nil, proto.CodeVersion, "only A2A "+proto.Version+" is supported")
		return
	}
	var req rpcRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, c.maxBody)).Decode(&req); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		writeErr(w, nil, proto.CodeParse, "parse error (batches are not supported)")
		return
	}
	switch req.Method {
	case "SendMessage", "SendStreamingMessage":
		c.handleSend(w, r, who, req)
	case "GetTask":
		c.handleGet(w, who, req)
	case "ListTasks":
		c.handleList(w, who, req)
	case "CancelTask":
		c.handleCancel(w, who, req)
	case "SubscribeToTask":
		c.handleSubscribe(w, r, who, req)
	case "CreateTaskPushNotificationConfig", "GetTaskPushNotificationConfig",
		"ListTaskPushNotificationConfigs", "DeleteTaskPushNotificationConfig":
		c.handlePush(w, who, req)
	case "GetExtendedAgentCard":
		writeErr(w, req.ID, proto.CodeNoExtendedCard, "extended agent card not configured")
	default:
		if strings.Contains(req.Method, "/") { // slash-style names (message/send, tasks/get) are not 1.0.0
			writeErr(
				w,
				req.ID,
				proto.CodeVersion,
				"only A2A 1.0.0 is supported: use PascalCase methods such as SendMessage",
			)
			return
		}
		writeErr(w, req.ID, proto.CodeMethodNotFound, "method not found")
	}
}

func (c *Channel) handleSend(w http.ResponseWriter, r *http.Request, who string, req rpcRequest) {
	var p proto.SendParams
	if json.Unmarshal(req.Params, &p) != nil || p.Message.MessageID == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "params.message.messageId is required")
		return
	}
	m := p.Message
	if m.TaskID != "" {
		writeErr(
			w,
			req.ID,
			proto.CodeUnsupportedOp,
			"continuing a task is not supported; send a new message with the same contextId",
		)
		return
	}
	text, ok := textOf(m.Parts)
	if !ok {
		writeErr(w, req.ID, proto.CodeContentType, "only text parts are supported")
		return
	}
	if text == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "message has no text")
		return
	}
	var hist *int
	var hook *pushCfg
	if cfg := p.Configuration; cfg != nil {
		hist = cfg.HistoryLength
		if len(cfg.AcceptedOutputModes) > 0 && !slices.ContainsFunc(cfg.AcceptedOutputModes, acceptsText) {
			writeErr(w, req.ID, proto.CodeContentType, "only text/plain output is available")
			return
		}
		if cfg.PushConfig != nil {
			pc, err := c.newPush(cfg.PushConfig)
			if err != nil {
				writeErr(w, req.ID, proto.CodeInvalidParams, "taskPushNotificationConfig: "+err.Error())
				return
			}
			hook = &pc
		}
	}
	ctxID := m.ContextID // empty: submit generates one
	if ctxID != "" && !validID(ctxID) {
		writeErr(w, req.ID, proto.CodeInvalidParams, "contextId must be 1-128 chars of [A-Za-z0-9._-]")
		return
	}
	stream := req.Method == "SendStreamingMessage"
	t, existed, err := c.reg.submit(who, m.MessageID, ctxID, text, stream, c.maxConc)
	switch {
	case errors.Is(err, errCtxBusy):
		writeErr(w, req.ID, codeContextBusy, err.Error())
		return
	case errors.Is(err, errMsgDup):
		writeErr(w, req.ID, proto.CodeInvalidParams, err.Error())
		return
	case err != nil: // registry full or too many running: shed load, client may retry
		w.Header().Set("Retry-After", "5")
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	if !existed {
		if hook != nil {
			_ = c.reg.addPush(t, *hook) // fresh task: cannot be terminal or full
		}
		c.start(t, text)
	} else if stream {
		c.reg.markStreaming(t)
	}
	if stream {
		c.serveStream(w, r, req.ID, t)
		return
	}
	if cfg := p.Configuration; cfg == nil || !cfg.ReturnImmediately {
		select {
		case <-t.done:
		case <-r.Context().Done(): // caller left; the task keeps running and a retry re-attaches
			return
		case <-c.quitCh():
			return
		}
	}
	writeResult(w, req.ID, proto.SendResult{Task: ptr(c.reg.snapshot(t, hist, true))})
}

func (c *Channel) handleGet(w http.ResponseWriter, who string, req rpcRequest) {
	var p struct {
		ID            string `json:"id"`
		HistoryLength *int   `json:"historyLength"`
	}
	if json.Unmarshal(req.Params, &p) != nil || p.ID == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "params.id is required")
		return
	}
	t := c.reg.get(who, p.ID)
	if t == nil {
		writeErr(w, req.ID, proto.CodeTaskNotFound, "task not found")
		return
	}
	writeResult(w, req.ID, c.reg.snapshot(t, p.HistoryLength, true))
}

func (c *Channel) handleList(w http.ResponseWriter, who string, req rpcRequest) {
	var p struct {
		ContextID        string `json:"contextId"`
		Status           string `json:"status"`
		PageSize         int    `json:"pageSize"`
		PageToken        string `json:"pageToken"`
		HistoryLength    *int   `json:"historyLength"`
		IncludeArtifacts bool   `json:"includeArtifacts"`
	}
	if len(req.Params) > 0 && json.Unmarshal(req.Params, &p) != nil {
		writeErr(w, req.ID, proto.CodeInvalidParams, "invalid params")
		return
	}
	offset := 0
	if p.PageToken != "" {
		var err error
		if offset, err = strconv.Atoi(p.PageToken); err != nil || offset < 0 {
			writeErr(w, req.ID, proto.CodeInvalidParams, "invalid pageToken")
			return
		}
	}
	size := p.PageSize
	if size <= 0 {
		size = 50
	}
	size = min(size, 100)
	tasks, total := c.reg.list(who, p.ContextID, p.Status, size, offset, p.HistoryLength, p.IncludeArtifacts)
	next := ""
	if offset+size < total {
		next = pageToken(offset + size)
	}
	writeResult(w, req.ID, map[string]any{
		"tasks": tasks, "nextPageToken": next, "pageSize": size, "totalSize": total,
	})
}

func (c *Channel) handleCancel(w http.ResponseWriter, who string, req rpcRequest) {
	var p struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(req.Params, &p) != nil || p.ID == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "params.id is required")
		return
	}
	t := c.reg.get(who, p.ID)
	if t == nil {
		writeErr(w, req.ID, proto.CodeTaskNotFound, "task not found")
		return
	}
	if !c.abort(t, proto.StateCanceled, "canceled by client") {
		writeErr(w, req.ID, proto.CodeTaskNotCancelable, "task already finished")
		return
	}
	writeResult(w, req.ID, c.reg.snapshot(t, nil, true))
}

func (c *Channel) handleSubscribe(w http.ResponseWriter, r *http.Request, who string, req rpcRequest) {
	var p struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(req.Params, &p) != nil || p.ID == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "params.id is required")
		return
	}
	t := c.reg.get(who, p.ID)
	if t == nil {
		writeErr(w, req.ID, proto.CodeTaskNotFound, "task not found")
		return
	}
	if snap := c.reg.snapshot(t, new(int), false); proto.IsTerminal(snap.Status.State) {
		writeErr(w, req.ID, proto.CodeUnsupportedOp, "task is already in a terminal state")
		return
	}
	c.reg.markStreaming(t)
	c.serveStream(w, r, req.ID, t)
}

// serveStream writes the task snapshot, then live frames, as SSE. A client that
// disconnects does not cancel the task: it can re-attach with SubscribeToTask.
func (c *Channel) serveStream(w http.ResponseWriter, r *http.Request, id json.RawMessage, t *task) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	snap, ch := c.reg.subscribe(t)
	w.WriteHeader(http.StatusOK)

	var buf bytes.Buffer
	idJSON := id
	if len(idJSON) == 0 {
		idJSON = json.RawMessage("null")
	}
	write := func(payload []byte) bool {
		buf.Reset()
		buf.WriteString(`data: {"jsonrpc":"2.0","id":`)
		buf.Write(idJSON)
		buf.WriteString(`,"result":`)
		buf.Write(payload)
		buf.WriteString("}\n\n")
		_, err := w.Write(buf.Bytes())
		return err == nil && rc.Flush() == nil
	}
	first, err := json.Marshal(proto.StreamResult{Task: &snap})
	if err != nil || !write(first) || ch == nil {
		return
	}
	defer c.reg.unsubscribe(t, ch)
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()
	for {
		select {
		case b, ok := <-ch:
			if !ok || !write(b) {
				return
			}
		case <-tick.C: // keeps proxies from reaping an idle stream during long tool runs
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil || rc.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-c.quitCh():
			return
		}
	}
}

// textOf joins text parts; ok is false if any other kind of part is present.
func textOf(parts []proto.Part) (string, bool) {
	var b strings.Builder
	for _, p := range parts {
		if p.Text == nil {
			return "", false
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(*p.Text)
	}
	return strings.TrimSpace(b.String()), true
}

// versionOK accepts an absent header, "1.0" and patch releases such as "1.0.0".
func versionOK(v string) bool {
	return v == "" || v == proto.Version || strings.HasPrefix(v, proto.Version+".")
}

func acceptsText(mode string) bool { return mode == "text/plain" || mode == "text/*" || mode == "*/*" }

// validID bounds client-chosen contextIds: they end up in session keys and file names.
func validID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// authorize maps the bearer token to a principal. Without configured tokens
// the listener is loopback-only (enforced in New) and everyone is "local".
func (c *Channel) authorize(r *http.Request) (string, bool) {
	if len(c.tokens) == 0 {
		return "local", true
	}
	h := r.Header.Get("Authorization")
	if len(h) < 8 || !strings.EqualFold(h[:7], "bearer ") {
		return "", false
	}
	sum := sha256.Sum256([]byte(strings.TrimSpace(h[7:])))
	who := ""
	for _, t := range c.tokens { // no early exit: constant work per request
		if subtle.ConstantTimeCompare(sum[:], t.sum[:]) == 1 {
			who = t.id
		}
	}
	return who, who != ""
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// limiter is a fixed-window per-IP counter. State is bounded: expired windows
// are purged once the map grows, and new IPs are refused if it is still full.
type limiter struct {
	mu  sync.Mutex
	max int
	win map[string]*window
}

type window struct {
	start time.Time
	n     int
}

const (
	limiterPurgeAt = 1024 // start dropping expired windows
	limiterMax     = 4096 // hard bound on tracked IPs
)

func (l *limiter) allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.win[ip]
	if w != nil && now.Sub(w.start) < time.Minute {
		w.n++
		return w.n <= l.max
	}
	if w == nil && len(l.win) >= limiterPurgeAt {
		for k, v := range l.win {
			if now.Sub(v.start) >= time.Minute {
				delete(l.win, k)
			}
		}
		if len(l.win) >= limiterMax {
			return false
		}
	}
	l.win[ip] = &window{start: now, n: 1}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeResult(w http.ResponseWriter, id json.RawMessage, result any) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Result: result})
}

func writeErr(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	writeJSON(w, http.StatusOK, rpcResponse{JSONRPC: "2.0", ID: id, Error: &proto.RPCError{Code: code, Message: msg}})
}

func ptr[T any](v T) *T { return &v }
