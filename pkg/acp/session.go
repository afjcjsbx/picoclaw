package acp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
)

type acpSession struct {
	id    string
	key   string
	model string
	mode  string

	mu             sync.RWMutex
	active         *acpTurn
	permissions    map[string]bool
	canceledTurnID string
	cumulativeCost float64
}

type acpTurn struct {
	id       string
	session  *acpSession
	done     chan struct{}
	once     sync.Once
	canceled atomic.Bool

	mu            sync.Mutex
	stopReason    string
	err           error
	text          string
	textMessageID string
	thought       string
	thoughtID     string
	finishReason  string
	streamed      bool
}

func newACPTurn(id string, session *acpSession) *acpTurn {
	return &acpTurn{
		id:      id,
		session: session,
		done:    make(chan struct{}),
	}
}

func (t *acpTurn) finish(stopReason string, err error) {
	t.once.Do(func() {
		t.mu.Lock()
		if t.canceled.Load() && stopReason == "end_turn" {
			stopReason = StopReasonCancelled
		}
		t.stopReason = stopReason
		t.err = err
		t.mu.Unlock()
		t.session.clearActive(t)
		close(t.done)
	})
}

func (t *acpTurn) result() (string, error) {
	<-t.done
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stopReason, t.err
}

func (t *acpTurn) completionStopReason() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch t.finishReason {
	case "length", "truncated":
		return "max_tokens"
	default:
		return "end_turn"
	}
}

func (s *acpSession) modelName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.model
}

func (s *acpSession) setModel(model string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.model = model
}

func (s *acpSession) modeName() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.mode
}

func (s *acpSession) setMode(mode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

func (s *acpSession) activeTurn() *acpTurn {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active
}

func (s *acpSession) setActive(turn *acpTurn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != nil {
		return fmt.Errorf("session already has an active turn")
	}
	s.active = turn
	return nil
}

func (s *acpSession) clearActive(turn *acpTurn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == turn {
		s.active = nil
	}
}

func (s *acpSession) isCancelledTurn(turnID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return turnID != "" && s.canceledTurnID == turnID
}

func (s *acpSession) rememberedPermission(key string) (bool, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	decision, ok := s.permissions[key]
	return decision, ok
}

func (s *acpSession) rememberPermission(key string, allowed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.permissions == nil {
		s.permissions = make(map[string]bool)
	}
	s.permissions[key] = allowed
}

func permissionScope(tool string, args map[string]any, command string) string {
	payload, _ := json.Marshal(args)
	value := tool + "\x00" + command + "\x00" + string(payload)
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func (r *Runtime) sessionByKey(key string) *acpSession {
	r.sessionsMu.RLock()
	defer r.sessionsMu.RUnlock()
	return r.byKey[key]
}

func (r *Runtime) sessionByID(id string) *acpSession {
	r.sessionsMu.RLock()
	defer r.sessionsMu.RUnlock()
	return r.sessions[id]
}

func (r *Runtime) addSession(state *acpSession) error {
	r.sessionsMu.Lock()
	defer r.sessionsMu.Unlock()
	if len(r.sessions) >= r.maxSessions {
		return fmt.Errorf("maximum ACP session count reached")
	}
	r.sessions[state.id] = state
	r.byKey[state.key] = state
	return nil
}

func (r *Runtime) modelOptions() []commands.ModelOption {
	return commands.CollectModelOptions(r.cfg)
}

func (r *Runtime) configOptions(state *acpSession) []SessionConfigOption {
	options := make([]SessionConfigOption, 0, 2)
	models := r.modelOptions()
	if len(models) > 0 {
		values := make([]SessionConfigSelectOption, 0, len(models))
		for _, model := range models {
			description := model.Provider
			values = append(values, SessionConfigSelectOption{
				Value:       model.Name,
				Name:        model.Name,
				Description: description,
			})
		}
		options = append(options, SessionConfigOption{
			ID:           "model",
			Name:         "Model",
			Description:  "Model used for this session",
			Category:     "model",
			Type:         "select",
			CurrentValue: state.modelName(),
			Options:      values,
		})
	}
	options = append(options, SessionConfigOption{
		ID:           "mode",
		Name:         "Session Mode",
		Description:  "Controls whether the agent may modify files or run commands",
		Category:     "mode",
		Type:         "select",
		CurrentValue: state.modeName(),
		Options: []SessionConfigSelectOption{
			{Value: "plan", Name: "Plan", Description: "Read-only exploration and planning"},
			{Value: "build", Name: "Build", Description: "Use tools with the configured approval policy"},
		},
	})
	return options
}

func (r *Runtime) modeState(state *acpSession) *SessionModeState {
	return &SessionModeState{
		CurrentModeID: state.modeName(),
		AvailableModes: []SessionMode{
			{ID: "plan", Name: "Plan", Description: "Read-only exploration and planning"},
			{ID: "build", Name: "Build", Description: "Use tools with the configured approval policy"},
		},
	}
}

func (r *Runtime) setConfigOption(state *acpSession, configID string, value any) error {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return fmt.Errorf("config option %q requires a string value", configID)
	}
	switch configID {
	case "model":
		for _, model := range r.modelOptions() {
			if model.Name == text {
				state.setModel(text)
				return r.notifyConfig(state)
			}
		}
		return fmt.Errorf("unknown model %q", text)
	case "mode":
		if text != "plan" && text != "build" {
			return fmt.Errorf("unknown mode %q", text)
		}
		state.setMode(text)
		return r.notifyConfig(state)
	default:
		return fmt.Errorf("unknown config option %q", configID)
	}
}

func (r *Runtime) notifyConfig(state *acpSession) error {
	if err := r.peer.Notify("session/update", SessionNotification{
		SessionID: state.id,
		Update: map[string]any{
			"sessionUpdate": "config_option_update",
			"configOptions": r.configOptions(state),
		},
	}); err != nil {
		return err
	}
	return r.peer.Notify("session/update", SessionNotification{
		SessionID: state.id,
		Update: map[string]any{
			"sessionUpdate": "current_mode_update",
			"currentModeId": state.modeName(),
		},
	})
}

func (r *Runtime) publishInbound(ctx context.Context, state *acpSession, turn *acpTurn, content string) error {
	return r.bus.PublishInbound(ctx, bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:   "acp",
			ChatID:    state.id,
			ChatType:  "direct",
			SenderID:  "local",
			MessageID: turn.id,
			Raw: map[string]string{
				"acp_turn_id": turn.id,
			},
		},
		Sender: bus.SenderInfo{
			Platform:    "acp",
			PlatformID:  state.id,
			CanonicalID: "acp:" + state.id,
		},
		Content:    content,
		SessionKey: state.key,
	})
}

type sessionPolicyHook struct {
	runtime *Runtime
}

func (h *sessionPolicyHook) BeforeLLM(
	_ context.Context,
	req *agent.LLMHookRequest,
) (*agent.LLMHookRequest, agent.HookDecision, error) {
	if req == nil || h.runtime == nil {
		return req, agent.HookDecision{Action: agent.HookActionContinue}, nil
	}
	state := h.runtime.sessionByKey(req.Meta.SessionKey)
	model := ""
	if state != nil {
		model = state.modelName()
	}
	if state == nil || model == "" || model == req.Model {
		return req, agent.HookDecision{Action: agent.HookActionContinue}, nil
	}
	next := req.Clone()
	next.Model = model
	return next, agent.HookDecision{Action: agent.HookActionModify}, nil
}

func (h *sessionPolicyHook) AfterLLM(
	_ context.Context,
	resp *agent.LLMHookResponse,
) (*agent.LLMHookResponse, agent.HookDecision, error) {
	return resp, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

func (h *sessionPolicyHook) ApproveTool(
	_ context.Context,
	req *agent.ToolApprovalRequest,
) (agent.ApprovalDecision, error) {
	if req == nil || h.runtime == nil {
		return agent.ApprovalDecision{Approved: true}, nil
	}
	state := h.runtime.sessionByKey(req.Meta.SessionKey)
	if state == nil || state.modeName() != "plan" {
		return agent.ApprovalDecision{Approved: true}, nil
	}
	if !isPlanSafeTool(req.Tool) {
		return agent.ApprovalDecision{Reason: "tool is not available in plan mode"}, nil
	}
	return agent.ApprovalDecision{Approved: true}, nil
}

func isPlanSafeTool(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "read_file", "list_dir", "search_files", "load_image",
		"tool_search_tool_regex", "tool_search_tool_bm25",
		"web_search", "web_fetch", "find_skills", "spawn_status", "todo":
		return true
	default:
		return false
	}
}
