package acp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	acpchannel "github.com/sipeed/picoclaw/pkg/channels/acp"
	"github.com/sipeed/picoclaw/pkg/config"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/session"
)

// RuntimeOptions configures a standalone ACP runtime.
type RuntimeOptions struct {
	Config       *config.Config
	Workspace    string
	Version      string
	Peer         *Peer
	DefaultModel string
	MaxSessions  int
	Provider     providers.LLMProvider
	ModelID      string
}

// Runtime owns one ACP connection and its isolated PicoClaw agent runtime.
type Runtime struct {
	cfg          *config.Config
	workspace    string
	version      string
	peer         *Peer
	maxSessions  int
	defaultModel string

	bus            *bus.MessageBus
	agentLoop      *agent.AgentLoop
	channelManager *channels.Manager
	channel        *acpchannel.Channel

	sessionsMu sync.RWMutex
	sessions   map[string]*acpSession
	byKey      map[string]*acpSession

	permissionsMu sync.Mutex
	permissions   map[string]map[string]context.CancelFunc

	initializedMu sync.RWMutex
	initialized   bool

	runCancel context.CancelFunc
	runDone   chan struct{}
	runErr    error
	closeOnce sync.Once
}

// NewRuntime creates the ACP runtime. Call Serve to start it.
func NewRuntime(opts RuntimeOptions) (*Runtime, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("acp config is required")
	}
	if opts.Peer == nil {
		return nil, fmt.Errorf("acp peer is required")
	}
	if strings.TrimSpace(opts.Workspace) == "" {
		return nil, fmt.Errorf("acp workspace is required")
	}
	if opts.MaxSessions <= 0 {
		opts.MaxSessions = 32
	}
	workspace, err := filepath.Abs(opts.Workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve acp workspace: %w", err)
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve acp workspace: %w", err)
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return nil, fmt.Errorf("acp workspace is not a directory")
	}
	opts.Workspace = workspace

	cfg := *opts.Config
	cfg.Channels = config.ChannelsConfig{}
	cfg.Session.Dimensions = []string{"chat"}
	cfg.Session.DmScope = "per-channel"
	cfg.Agents.Defaults.Workspace = opts.Workspace
	cfg.Tools.Exec.AllowRemote = true
	if opts.DefaultModel != "" {
		cfg.Agents.Defaults.ModelName = opts.DefaultModel
	}

	provider := opts.Provider
	modelID := opts.ModelID
	if provider == nil {
		provider, modelID, err = providers.CreateProvider(&cfg)
		if err != nil {
			return nil, fmt.Errorf("create acp provider: %w", err)
		}
		cfg.Agents.Defaults.ModelName = providers.DefaultModelRef(&cfg, modelID)
	} else if modelID == "" {
		modelID = provider.GetDefaultModel()
		if modelID != "" {
			cfg.Agents.Defaults.ModelName = providers.DefaultModelRef(&cfg, modelID)
		}
	}

	messageBus := bus.NewMessageBus()
	agentLoop := agent.NewAgentLoop(&cfg, messageBus, provider)
	messageBus.SetEventPublisher(agentLoop.RuntimeEventBus())

	manager, err := channels.NewManager(
		&cfg,
		messageBus,
		nil,
		channels.WithRuntimeEvents(agentLoop.RuntimeEventBus()),
	)
	if err != nil {
		messageBus.Close()
		agentLoop.Close()
		return nil, fmt.Errorf("create acp channel manager: %w", err)
	}

	runtime := &Runtime{
		cfg:            &cfg,
		workspace:      opts.Workspace,
		version:        opts.Version,
		peer:           opts.Peer,
		maxSessions:    opts.MaxSessions,
		defaultModel:   cfg.Agents.Defaults.ModelName,
		bus:            messageBus,
		agentLoop:      agentLoop,
		channelManager: manager,
		sessions:       make(map[string]*acpSession),
		byKey:          make(map[string]*acpSession),
		permissions:    make(map[string]map[string]context.CancelFunc),
		runDone:        make(chan struct{}),
	}

	channel := acpchannel.New(messageBus, runtime)
	runtime.channel = channel
	manager.RegisterChannel("acp", channel)
	agentLoop.SetChannelManager(manager)
	agentLoop.RegisterApprovalBroker("acp", runtime)

	hook := agent.NamedHook("acp-session-policy", &sessionPolicyHook{runtime: runtime})
	hook.Priority = 50
	if err := agentLoop.MountHook(hook); err != nil {
		_ = manager.StopAll(context.Background())
		messageBus.Close()
		agentLoop.Close()
		return nil, fmt.Errorf("mount acp session policy: %w", err)
	}

	runtime.registerHandlers()
	return runtime, nil
}

// Serve starts the agent runtime and serves ACP messages until the peer closes.
func (r *Runtime) Serve(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	r.runCancel = cancel

	if err := r.channelManager.StartAll(runCtx); err != nil {
		cancel()
		r.closeResources()
		return fmt.Errorf("start acp channel: %w", err)
	}
	_, err := r.agentLoop.RuntimeEventBus().Channel().OfKind(
		runtimeevents.KindAgentTurnEnd,
		runtimeevents.KindAgentLLMResponse,
	).Subscribe(
		runCtx,
		runtimeevents.SubscribeOptions{
			Name:         "acp-turn-completion",
			Buffer:       64,
			Concurrency:  runtimeevents.Concurrent,
			Backpressure: runtimeevents.DropNewest,
		},
		r.handleTurnEnd,
	)
	if err != nil {
		cancel()
		r.closeResources()
		return fmt.Errorf("subscribe acp turn events: %w", err)
	}

	go func() {
		defer close(r.runDone)
		if runErr := r.agentLoop.Run(runCtx); runErr != nil && runCtx.Err() == nil {
			r.runErr = runErr
			logger.ErrorCF("acp", "Agent runtime stopped", map[string]any{"error": runErr.Error()})
			r.peer.Close()
		}
	}()

	err = r.peer.Serve(ctx)
	cancel()
	r.shutdown()
	r.peer.Wait()
	if err == nil && r.runErr != nil {
		return fmt.Errorf("acp agent runtime: %w", r.runErr)
	}
	return err
}

func (r *Runtime) shutdown() {
	r.closeOnce.Do(func() {
		r.cancelAllPermissions()
		r.sessionsMu.RLock()
		sessions := make([]*acpSession, 0, len(r.sessions))
		for _, state := range r.sessions {
			sessions = append(sessions, state)
		}
		r.sessionsMu.RUnlock()
		for _, state := range sessions {
			if turn := state.activeTurn(); turn != nil {
				turn.canceled.Store(true)
				_ = r.agentLoop.CancelSession(state.key)
				turn.finish(StopReasonCancelled, nil)
			}
		}
		if r.runCancel != nil {
			r.runCancel()
		}
		if r.runDone != nil {
			<-r.runDone
		}
		_ = r.channelManager.StopAll(context.Background())
		r.bus.Close()
		r.agentLoop.Close()
	})
}

func (r *Runtime) closeResources() {
	r.closeOnce.Do(func() {
		_ = r.channelManager.StopAll(context.Background())
		r.bus.Close()
		r.agentLoop.Close()
	})
}

func (r *Runtime) defaultAgentID() string {
	if r.agentLoop == nil {
		return "default"
	}
	if defaultAgent := r.agentLoop.GetRegistry().GetDefaultAgent(); defaultAgent != nil {
		return defaultAgent.ID
	}
	return "default"
}

func (r *Runtime) newSessionID() string {
	return uuid.NewString()
}

func (r *Runtime) sessionKey(id string) string {
	alias := "agent:" + r.defaultAgentID() + ":acp:" + id
	return session.BuildOpaqueSessionKey(alias)
}
