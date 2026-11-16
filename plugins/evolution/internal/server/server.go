// Package server implements the PicoClaw process-hook protocol for the
// optional evolution plugin. It consumes agent.turn.end notifications, records
// a slim learning case, and runs the cold path when configured.
package server

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/plugins/evolution/internal/evolution"
	logger "github.com/sipeed/picoclaw/plugins/evolution/internal/plog"
)

const (
	maxMessageBytes = 16 << 20
	eventQueueSize  = 32
	// The hot path only needs the summary (160 chars) and final output
	// (1200 chars). Bound the event fields before they enter the queue so a
	// pathological turn cannot pin a large string in memory.
	maxEventText      = 4096
	maxToolExecutions = 128
	maxSkillSnapshots = 32
	maxSkillNames     = 32
)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      uint64          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Options configures a Server. DataDir should be the plugin data directory
// (PLUGIN_DATA); state is written below it and never inside an agent
// workspace.
type Options struct {
	DataDir string
	Config  evolution.Config
	Now     func() time.Time
}

type Server struct {
	mu      sync.Mutex
	opts    Options
	cfg     evolution.Config
	runtime *evolution.Runtime
	runner  *evolution.ColdPathRunner

	ctx       context.Context
	cancel    context.CancelFunc
	out       io.Writer
	writeMu   sync.Mutex
	events    chan evolution.TurnCaseInput
	workers   sync.WaitGroup
	startOnce sync.Once
	closed    bool
	closeOnce sync.Once
	scheduled bool

	workspacesMu sync.Mutex
	workspaces   map[string]struct{}
}

func New(opts Options) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		opts:       opts,
		cfg:        opts.Config,
		ctx:        ctx,
		cancel:     cancel,
		events:     make(chan evolution.TurnCaseInput, eventQueueSize),
		workspaces: make(map[string]struct{}),
	}
}

// Configure rebuilds the runtime from the host-supplied string map. It is
// called on hook.hello and replaces any previous configuration.
func (s *Server) Configure(values map[string]string) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return fmt.Errorf("server is closed")
	}
	if s.opts.DataDir == "" {
		s.mu.Unlock()
		return fmt.Errorf("plugin data directory is not configured")
	}
	cfg := evolution.ParseConfig(values)
	llmCfg := evolution.ParseLLMConfig(values)
	if err := cfg.Validate(); err != nil {
		s.mu.Unlock()
		return err
	}
	s.resolveLLMAPIKey(&llmCfg)

	llm := evolution.LLM(nil)
	if cfg.Enabled && llmCfg.Enabled() {
		client, err := evolution.NewOpenAICompatibleLLM(llmCfg)
		if err != nil {
			logger.WarnCF("evolution", "LLM configuration ignored", map[string]any{"error": err.Error()})
		} else {
			llm = client
		}
	}

	stateDir := s.stateDirForWorkspace
	if cfg.StateDir != "" {
		override := cfg.StateDir
		if !filepath.IsAbs(override) {
			override = filepath.Join(s.opts.DataDir, override)
		}
		stateDir = func(string) string { return override }
	}
	var patternClusterer evolution.PatternClusterer = evolution.NewHeuristicPatternClusterer(
		cfg.EffectiveMinTaskCount(),
		s.opts.Now,
	)
	if llm != nil {
		patternClusterer = evolution.NewLLMPatternClusterer(
			llm,
			llmCfg.Model,
			patternClusterer,
			cfg.EffectiveMinTaskCount(),
			s.opts.Now,
		)
	}
	runtime, err := evolution.NewRuntime(evolution.RuntimeOptions{
		Config:           cfg,
		Now:              s.opts.Now,
		PatternClusterer: patternClusterer,
		GeneratorFactory: func(workspace string) evolution.DraftGenerator {
			return evolution.NewDraftGeneratorForWorkspace(workspace, llm, llmCfg.Model)
		},
		SuccessJudgeFactory: func(workspace string) evolution.SuccessJudge {
			return evolution.NewLLMTaskSuccessJudge(llm, llmCfg.Model, &evolution.HeuristicSuccessJudge{})
		},
		ApplierFactory: func(workspace string) *evolution.Applier {
			return evolution.NewApplier(evolution.NewPaths(workspace, stateDir(workspace)), nil)
		},
		StateDirForWorkspace: stateDir,
	})
	if err != nil {
		s.mu.Unlock()
		return err
	}

	oldRunner := s.runner
	s.cfg = cfg
	s.runtime = runtime
	s.runner = nil
	if cfg.RunsColdPathAutomatically() {
		s.runner = evolution.NewColdPathRunnerWithErrorHandler(runtime, func(err error) {
			logger.WarnCF("evolution", "Cold path run failed", map[string]any{"error": err.Error()})
		})
	}
	if cfg.RunsColdPathScheduled() && !s.scheduled {
		s.scheduled = true
		s.startSchedulerLocked(cfg.EffectiveColdPathTimes())
	}
	s.mu.Unlock()

	if oldRunner != nil {
		_ = oldRunner.Close()
	}
	return nil
}

func (s *Server) stateDirForWorkspace(workspace string) string {
	sum := sha1.Sum([]byte(workspace))
	return filepath.Join(s.opts.DataDir, "state", hex.EncodeToString(sum[:8]))
}

func (s *Server) resolveLLMAPIKey(cfg *evolution.LLMConfig) {
	if cfg == nil || cfg.APIKey != "" || cfg.APIKeyFile == "" {
		return
	}
	path := cfg.APIKeyFile
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.opts.DataDir, path)
	}
	file, err := os.Open(path)
	if err != nil {
		logger.WarnCF("evolution", "LLM API key file could not be read", map[string]any{"error": err.Error()})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 8<<10))
	if err != nil {
		logger.WarnCF("evolution", "LLM API key file could not be read", map[string]any{"error": err.Error()})
		return
	}
	cfg.APIKey = strings.TrimSpace(string(data))
}

// Run reads JSON-RPC lines until the input closes.
func (s *Server) Run(in io.Reader, out io.Writer) error {
	s.writeMu.Lock()
	s.out = out
	s.writeMu.Unlock()
	s.startWorker()

	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64*1024), maxMessageBytes)
	for scanner.Scan() {
		s.handleLine(scanner.Bytes())
	}
	err := scanner.Err()
	s.Close()
	return err
}

// RunColdPathOnce performs a single cold-path pass. It is used by the
// evolution-hook --cold-path CLI mode for users who prefer manual runs.
func (s *Server) RunColdPathOnce(ctx context.Context, workspace string) error {
	s.mu.Lock()
	runtime := s.runtime
	s.mu.Unlock()
	if runtime == nil {
		return fmt.Errorf("evolution runtime is not configured")
	}
	return runtime.RunColdPathOnce(ctx, workspace)
}

func (s *Server) handleLine(line []byte) {
	var msg rpcMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		logger.WarnCF("evolution", "Invalid hook message", map[string]any{"error": err.Error()})
		return
	}
	switch msg.Method {
	case "hook.hello":
		var params struct {
			Config map[string]string `json:"config"`
		}
		if len(msg.Params) > 0 {
			_ = json.Unmarshal(msg.Params, &params)
		}
		if err := s.Configure(params.Config); err != nil {
			s.respondError(msg.ID, -32000, err.Error())
			return
		}
		s.respond(msg.ID, map[string]any{"ok": true, "name": "evolution"})
	case "hook.runtime_event":
		s.handleRuntimeEvent(msg.Params)
	case "hook.before_llm", "hook.after_llm", "hook.before_tool", "hook.after_tool":
		s.respond(msg.ID, map[string]any{"action": "continue"})
	case "hook.approve_tool":
		s.respond(msg.ID, map[string]any{"approved": true})
	default:
		if msg.ID != 0 {
			s.respond(msg.ID, map[string]any{"ok": true})
		}
	}
}

func (s *Server) respond(id uint64, result any) {
	if id == 0 {
		return
	}
	s.writeMessage(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func (s *Server) respondError(id uint64, code int, message string) {
	if id == 0 {
		return
	}
	s.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   rpcError{Code: code, Message: message},
	})
}

func (s *Server) writeMessage(msg any) {
	body, err := json.Marshal(msg)
	if err != nil {
		return
	}
	body = append(body, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if s.out != nil {
		_, _ = s.out.Write(body)
	}
}

func (s *Server) handleRuntimeEvent(params json.RawMessage) {
	if len(params) == 0 {
		return
	}
	var evt runtimeEvent
	if err := json.Unmarshal(params, &evt); err != nil {
		logger.WarnCF("evolution", "Invalid runtime event", map[string]any{"error": err.Error()})
		return
	}
	if evt.Kind != "agent.turn.end" {
		return
	}
	if strings.EqualFold(strings.TrimSpace(evt.Scope.SessionKey), "heartbeat") {
		return
	}
	workspace := strings.TrimSpace(evt.Payload.Workspace)
	if workspace == "" {
		return
	}
	s.rememberWorkspace(workspace)
	input := evolution.TurnCaseInput{
		Workspace:           workspace,
		TurnID:              evt.Scope.TurnID,
		SessionKey:          evt.Scope.SessionKey,
		AgentID:             evt.Scope.AgentID,
		Status:              evt.Payload.Status,
		UserMessage:         boundedText(evt.Payload.UserMessage),
		FinalContent:        boundedText(evt.Payload.FinalContent),
		ActiveSkillNames:    boundedNames(evt.Payload.ActiveSkills),
		AttemptedSkillNames: boundedNames(evt.Payload.AttemptedSkills),
		FinalSuccessfulPath: boundedNames(evt.Payload.FinalSuccessfulPath),
	}
	snapshots := evt.Payload.SkillContextSnapshots
	if len(snapshots) > maxSkillSnapshots {
		snapshots = snapshots[:maxSkillSnapshots]
	}
	for _, snapshot := range snapshots {
		input.SkillContextSnapshots = append(input.SkillContextSnapshots, evolution.SkillContextSnapshot{
			Sequence:   snapshot.Sequence,
			Trigger:    snapshot.Trigger,
			SkillNames: boundedNames(snapshot.SkillNames),
		})
	}
	executions := evt.Payload.ToolExecutions
	if len(executions) > maxToolExecutions {
		executions = executions[:maxToolExecutions]
	}
	for _, exec := range executions {
		input.ToolExecutions = append(input.ToolExecutions, evolution.ToolExecutionRecord{
			Name:         exec.Name,
			Success:      exec.Success,
			ErrorSummary: boundedText(exec.ErrorSummary),
			SkillNames:   boundedNames(exec.SkillNames),
		})
	}

	select {
	case s.events <- input:
	default:
		logger.WarnCF("evolution", "Event queue full, dropping turn", map[string]any{"turn_id": input.TurnID})
	}
}

func (s *Server) startWorker() {
	s.startOnce.Do(func() {
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			for {
				select {
				case <-s.ctx.Done():
					return
				case input := <-s.events:
					s.process(input)
				}
			}
		}()
	})
}

func (s *Server) process(input evolution.TurnCaseInput) {
	s.mu.Lock()
	runtime := s.runtime
	runner := s.runner
	cfg := s.cfg
	s.mu.Unlock()
	if runtime == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := runtime.FinalizeTurn(ctx, input); err != nil {
		logger.WarnCF("evolution", "Finalize turn failed", map[string]any{
			"turn_id": input.TurnID,
			"error":   err.Error(),
		})
		return
	}
	if runner != nil && cfg.RunsColdPathAfterTurn() {
		runner.Trigger(input.Workspace)
	}
}

func (s *Server) rememberWorkspace(workspace string) {
	s.workspacesMu.Lock()
	defer s.workspacesMu.Unlock()
	s.workspaces[workspace] = struct{}{}
}

func (s *Server) snapshotWorkspaces() []string {
	s.workspacesMu.Lock()
	defer s.workspacesMu.Unlock()
	out := make([]string, 0, len(s.workspaces))
	for workspace := range s.workspaces {
		out = append(out, workspace)
	}
	sort.Strings(out)
	return out
}

type coldPathTime struct {
	hour   int
	minute int
}

func parseColdPathTimes(values []string) []coldPathTime {
	out := make([]coldPathTime, 0, len(values))
	seen := make(map[coldPathTime]struct{}, len(values))
	for _, value := range values {
		parts := strings.Split(strings.TrimSpace(value), ":")
		if len(parts) != 2 {
			continue
		}
		hour, err := strconv.Atoi(parts[0])
		if err != nil || hour < 0 || hour > 23 {
			continue
		}
		minute, err := strconv.Atoi(parts[1])
		if err != nil || minute < 0 || minute > 59 {
			continue
		}
		item := coldPathTime{hour: hour, minute: minute}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].hour != out[j].hour {
			return out[i].hour < out[j].hour
		}
		return out[i].minute < out[j].minute
	})
	return out
}

func nextColdPathTime(now time.Time, schedule []coldPathTime) time.Time {
	for _, item := range schedule {
		candidate := time.Date(now.Year(), now.Month(), now.Day(), item.hour, item.minute, 0, 0, now.Location())
		if candidate.After(now) {
			return candidate
		}
	}
	first := schedule[0]
	tomorrow := now.AddDate(0, 0, 1)
	return time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), first.hour, first.minute, 0, 0, now.Location())
}

func (s *Server) startSchedulerLocked(times []string) {
	schedule := parseColdPathTimes(times)
	if len(schedule) == 0 {
		logger.WarnCF("evolution", "No valid cold path schedule times configured", map[string]any{"times": times})
		return
	}
	s.workers.Add(1)
	go func() {
		defer s.workers.Done()
		now := time.Now()
		if s.opts.Now != nil {
			now = s.opts.Now()
		}
		timer := time.NewTimer(time.Until(nextColdPathTime(now, schedule)))
		defer timer.Stop()
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-timer.C:
				s.mu.Lock()
				runner := s.runner
				s.mu.Unlock()
				if runner != nil {
					for _, workspace := range s.snapshotWorkspaces() {
						runner.Trigger(workspace)
					}
				}
				now = time.Now()
				if s.opts.Now != nil {
					now = s.opts.Now()
				}
				timer.Reset(time.Until(nextColdPathTime(now, schedule)))
			}
		}
	}()
}

// Close stops the worker and any background cold-path work.
func (s *Server) Close() {
	if s == nil {
		return
	}
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		runner := s.runner
		s.runner = nil
		s.mu.Unlock()
		s.cancel()
		if runner != nil {
			_ = runner.Close()
		}
		s.workers.Wait()
	})
}

func boundedText(value string) string {
	if len(value) <= maxEventText {
		return value
	}
	cut := maxEventText
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

func boundedNames(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	if len(values) > maxSkillNames {
		values = values[:maxSkillNames]
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}
