// PicoClaw - Ultra-lightweight personal AI agent

package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/plugins"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func (al *AgentLoop) handleCommand(
	ctx context.Context,
	msg bus.InboundMessage,
	agent *AgentInstance,
	opts *processOptions,
) (string, bool) {
	normalizeProcessOptionsInPlace(opts)

	if !commands.HasCommandPrefix(msg.Content) {
		return "", false
	}

	if matched, handled, reply := al.applyExplicitSkillCommand(msg.Content, agent, opts); matched {
		return reply, handled
	}

	// Custom commands expand into a normal user prompt and continue through the
	// regular agent turn instead of replying directly.
	if al.expandCustomCommand(msg, agent, opts) {
		return "", false
	}

	if al.cmdRegistry == nil {
		return "", false
	}

	rt := al.buildCommandsRuntime(ctx, agent, opts)
	executor := commands.NewExecutor(al.cmdRegistry, rt)

	var commandReply string
	result := executor.Execute(ctx, commands.Request{
		Channel:  msg.Channel,
		ChatID:   msg.ChatID,
		SenderID: msg.SenderID,
		Text:     msg.Content,
		Reply: func(text string) error {
			commandReply = text
			return nil
		},
	})

	switch result.Outcome {
	case commands.OutcomeHandled:
		if result.Err != nil {
			return mapCommandError(result), true
		}
		if commandReply != "" {
			return commandReply, true
		}
		return "", true
	default: // OutcomePassthrough — let the message fall through to LLM
		return "", false
	}
}

// expandCustomCommand rewrites a user-defined prompt command into its expanded
// template and stores it in opts so the normal turn continues with that text.
// Built-in commands always take precedence.
func (al *AgentLoop) expandCustomCommand(
	msg bus.InboundMessage,
	agent *AgentInstance,
	opts *processOptions,
) bool {
	if opts == nil {
		return false
	}
	command, expanded, found := al.resolveCustomCommand(msg.Content, agent)
	if !found {
		return false
	}

	opts.Dispatch.UserMessage = expanded
	opts.UserMessage = expanded
	logger.InfoCF("agent", "Expanded custom command", map[string]any{
		"command": command.Name,
		"source":  command.Source,
	})
	return true
}

// expandCustomCommandContent expands a custom command before it is queued as a
// steering message during an active turn. Built-in slash commands are left
// untouched so tryHandleActiveTurnCommand can keep its current behavior.
func (al *AgentLoop) expandCustomCommandContent(content, agentID string) string {
	registry := al.GetRegistry()
	if registry == nil {
		return content
	}
	agent, ok := registry.GetAgent(agentID)
	if !ok || agent == nil {
		agent = registry.GetDefaultAgent()
	}
	if agent == nil {
		return content
	}
	_, expanded, found := al.resolveCustomCommand(content, agent)
	if !found {
		return content
	}
	return expanded
}

func (al *AgentLoop) resolveCustomCommand(
	content string,
	agent *AgentInstance,
) (commands.CustomCommand, string, bool) {
	if agent == nil || strings.TrimSpace(agent.Workspace) == "" {
		return commands.CustomCommand{}, "", false
	}

	cmdName, ok := commands.CommandName(content)
	if !ok {
		return commands.CustomCommand{}, "", false
	}
	if al.cmdRegistry != nil {
		if _, builtin := al.cmdRegistry.Lookup(cmdName); builtin {
			return commands.CustomCommand{}, "", false
		}
	}

	command, found := commands.LookupCustomCommand(
		commands.LoadCustomCommands(agent.Workspace),
		cmdName,
	)
	if !found {
		return commands.CustomCommand{}, "", false
	}

	expanded := commands.ExpandCustomCommandTemplate(
		command.Template,
		commands.CommandArguments(content),
	)
	if strings.TrimSpace(expanded) == "" {
		return commands.CustomCommand{}, "", false
	}
	return command, expanded, true
}

// commandDefinitions returns built-in commands plus custom command metadata for
// help output and command palettes.
func (al *AgentLoop) commandDefinitions(agent *AgentInstance) []commands.Definition {
	defs := commands.BuiltinDefinitions()
	if al.cmdRegistry != nil {
		defs = al.cmdRegistry.Definitions()
	}
	if agent == nil || strings.TrimSpace(agent.Workspace) == "" {
		return defs
	}
	return commands.MergeCustomCommandDefinitions(
		defs,
		commands.LoadCustomCommands(agent.Workspace),
	)
}

func (al *AgentLoop) applyExplicitSkillCommand(
	raw string,
	agent *AgentInstance,
	opts *processOptions,
) (matched bool, handled bool, reply string) {
	normalizeProcessOptionsInPlace(opts)

	cmdName, ok := commands.CommandName(raw)
	if !ok || cmdName != "use" {
		return false, false, ""
	}

	if agent == nil || agent.ContextBuilder == nil {
		return true, true, commandsUnavailableSkillMessage()
	}

	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) < 2 {
		return true, true, buildUseCommandHelp(agent)
	}

	arg := strings.TrimSpace(parts[1])
	if strings.EqualFold(arg, "clear") || strings.EqualFold(arg, "off") {
		if opts != nil {
			al.clearPendingSkills(opts.Dispatch.SessionKey)
		}
		return true, true, "Cleared pending skill override."
	}

	skillName, ok := agent.ContextBuilder.ResolveSkillName(arg)
	if !ok {
		return true, true, fmt.Sprintf("Unknown skill: %s\nUse /list skills to see installed skills.", arg)
	}

	if len(parts) < 3 {
		if opts == nil || strings.TrimSpace(opts.Dispatch.SessionKey) == "" {
			return true, true, commandsUnavailableSkillMessage()
		}
		al.setPendingSkills(opts.Dispatch.SessionKey, []string{skillName})
		return true, true, fmt.Sprintf(
			"Skill %q is armed for your next message. Send your next prompt normally, or use /use clear to cancel.",
			skillName,
		)
	}

	message := strings.TrimSpace(strings.Join(parts[2:], " "))
	if message == "" {
		return true, true, buildUseCommandHelp(agent)
	}

	if opts != nil {
		opts.ForcedSkills = append(opts.ForcedSkills, skillName)
		opts.Dispatch.UserMessage = message
		opts.UserMessage = message
	}

	return true, false, ""
}

func (al *AgentLoop) buildCommandsRuntime(
	ctx context.Context,
	agent *AgentInstance,
	opts *processOptions,
) *commands.Runtime {
	normalizeProcessOptionsInPlace(opts)

	registry := al.GetRegistry()
	cfg := al.GetConfig()
	rt := &commands.Runtime{
		Config:       cfg,
		VoiceMode:    al.voiceMode(opts.Dispatch.SessionKey),
		ListAgentIDs: registry.ListAgentIDs,
		ListDefinitions: func() []commands.Definition {
			return al.commandDefinitions(agent)
		},
		ListMCPServers: func(ctx context.Context) []commands.MCPServerInfo {
			if cfg == nil {
				return nil
			}

			if len(cfg.Tools.MCP.Servers) == 0 {
				return nil
			}

			if err := al.ensureMCPInitialized(ctx); err != nil {
				logger.WarnCF("agent", "Failed to refresh MCP status for command",
					map[string]any{
						"error": err.Error(),
					})
			}

			connected := make(map[string]int)
			if manager := al.mcp.getManager(); manager != nil {
				for serverName, conn := range manager.GetServers() {
					connected[serverName] = len(conn.Tools)
				}
			}

			servers := make([]commands.MCPServerInfo, 0, len(cfg.Tools.MCP.Servers))
			for serverName, serverCfg := range cfg.Tools.MCP.Servers {
				toolCount, isConnected := connected[serverName]
				servers = append(servers, commands.MCPServerInfo{
					Name:      serverName,
					Enabled:   serverCfg.Enabled,
					Deferred:  serverCfg.EffectiveDeferred(cfg.Tools.MCP.Discovery.Enabled),
					Connected: isConnected,
					ToolCount: toolCount,
				})
			}

			sort.Slice(servers, func(i, j int) bool {
				return strings.ToLower(servers[i].Name) < strings.ToLower(servers[j].Name)
			})

			return servers
		},
		ListMCPTools: func(ctx context.Context, serverName string) ([]commands.MCPToolInfo, error) {
			if cfg == nil {
				return nil, fmt.Errorf("command unavailable: config not loaded")
			}

			serverName = strings.TrimSpace(serverName)
			if serverName == "" {
				return nil, fmt.Errorf("server name is required")
			}

			resolvedName := ""
			var serverCfg config.MCPServerConfig
			for name, candidate := range cfg.Tools.MCP.Servers {
				if strings.EqualFold(name, serverName) {
					resolvedName = name
					serverCfg = candidate
					break
				}
			}
			if resolvedName == "" {
				return nil, fmt.Errorf("MCP server '%s' is not configured", serverName)
			}
			if !serverCfg.Enabled {
				return nil, fmt.Errorf("MCP server '%s' is configured but disabled", resolvedName)
			}
			if !cfg.Tools.IsToolEnabled("mcp") {
				return nil, fmt.Errorf("MCP integration is disabled")
			}

			if err := al.ensureMCPInitialized(ctx); err != nil {
				logger.WarnCF("agent", "Failed to initialize MCP runtime for command",
					map[string]any{
						"server": resolvedName,
						"error":  err.Error(),
					})
			}

			manager := al.mcp.getManager()
			if manager == nil {
				return nil, fmt.Errorf("MCP server '%s' is configured but not connected", resolvedName)
			}

			conn, ok := manager.GetServer(resolvedName)
			if !ok {
				return nil, fmt.Errorf("MCP server '%s' is configured but not connected", resolvedName)
			}

			toolInfos := make([]commands.MCPToolInfo, 0, len(conn.Tools))
			for _, tool := range conn.Tools {
				if tool == nil {
					continue
				}
				name := strings.TrimSpace(tool.Name)
				if name == "" {
					continue
				}

				description := strings.TrimSpace(tool.Description)
				if description == "" {
					description = fmt.Sprintf("MCP tool from %s server", resolvedName)
				}

				toolInfos = append(toolInfos, commands.MCPToolInfo{
					Name:        name,
					Description: description,
					Parameters:  summarizeMCPToolParameters(tool.InputSchema),
				})
			}
			sort.Slice(toolInfos, func(i, j int) bool {
				return toolInfos[i].Name < toolInfos[j].Name
			})
			return toolInfos, nil
		},
		GetEnabledChannels: func() []string {
			if al.channelManager == nil {
				return nil
			}
			return al.channelManager.GetEnabledChannels()
		},
		ListPlugins: func(ctx context.Context) []commands.PluginInfo {
			if cfg == nil {
				return nil
			}

			al.waitPlugins(ctx)
			items := plugins.Inventory(cfg.Plugins, cfg.WorkspacePath(), al.PluginStatuses())
			infos := make([]commands.PluginInfo, 0, len(items))
			for _, item := range items {
				infos = append(infos, commands.PluginInfo{
					ID:          item.ID,
					Name:        item.Name,
					Root:        item.Root,
					State:       item.State,
					Diagnostics: item.Diagnostics,
				})
			}
			return infos
		},
		GetActiveTurnTree: func() []commands.TurnInfo {
			if opts == nil {
				return nil
			}
			tree := al.GetActiveTurnTree(opts.Dispatch.SessionKey)
			if len(tree) == 0 {
				return nil
			}
			turns := make([]commands.TurnInfo, 0, len(tree))
			for _, info := range tree {
				turns = append(turns, commands.TurnInfo{
					TurnID:       info.TurnID,
					ParentTurnID: info.ParentTurnID,
					AgentID:      info.AgentID,
					UserMessage:  info.UserMessage,
					Phase:        string(info.Phase),
					StartedAt:    info.StartedAt,
				})
			}
			return turns
		},
		SwitchChannel: func(value string) error {
			if al.channelManager == nil {
				return fmt.Errorf("channel manager not initialized")
			}
			if _, exists := al.channelManager.GetChannel(value); !exists && value != "cli" {
				return fmt.Errorf("channel '%s' not found or not enabled", value)
			}
			return nil
		},
		SetVoiceMode: func(mode string) error {
			return al.setVoiceMode(opts.Dispatch.SessionKey, mode)
		},
	}
	rt.StopActiveTurn = func() (commands.StopResult, error) {
		if opts == nil {
			return commands.StopResult{}, fmt.Errorf("process options not available")
		}
		return al.stopActiveTurnForSession(opts.Dispatch.SessionKey)
	}
	if agent != nil && agent.ContextBuilder != nil {
		rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
	}
	rt.ReloadConfig = func() error {
		if al.reloadFunc == nil {
			return fmt.Errorf("reload not configured")
		}
		return al.reloadFunc()
	}
	if agent != nil {
		if agent.ContextBuilder != nil {
			rt.ListSkillNames = agent.ContextBuilder.ListSkillNames
		}
		rt.GetModelInfo = func() (string, string) {
			modelMu := agent.modelStateMutex()
			modelMu.RLock()
			defer modelMu.RUnlock()
			return agent.Model, resolvedCandidateProvider(agent.Candidates, cfg.Agents.Defaults.Provider)
		}
		rt.SwitchModel = func(value string) (string, error) {
			value = strings.TrimSpace(value)
			modelMu := agent.modelStateMutex()
			modelMu.Lock()
			defer modelMu.Unlock()
			modelFound := false
			for _, modelCfg := range cfg.ModelList {
				if modelCfg != nil && modelCfg.ModelName == value {
					modelFound = true
					break
				}
			}
			if !modelFound {
				return "", fmt.Errorf("model %q not found in model_list or providers", value)
			}

			nextCandidates := resolveModelCandidates(cfg, cfg.Agents.Defaults.Provider, value, agent.Fallbacks)
			if len(nextCandidates) == 0 {
				return "", fmt.Errorf("model %q did not resolve to any provider candidates", value)
			}
			modelCfg, err := resolvedCandidateModelConfig(cfg, nextCandidates[0], agent.Workspace)
			if err != nil {
				return "", err
			}
			nextProvider, _, err := providers.CreateProviderFromConfig(modelCfg)
			if err != nil {
				return "", fmt.Errorf("failed to initialize model %q: %w", value, err)
			}
			nextCandidateProviders := make(map[string]providers.LLMProvider)
			copyInitializedCandidateProviders(
				agent.CandidateProviders,
				nextCandidateProviders,
				agent.ImageCandidates,
			)
			copyInitializedCandidateProviders(
				agent.CandidateProviders,
				nextCandidateProviders,
				agent.LightCandidates,
			)
			inheritPrimaryProviderForCandidates(
				cfg,
				agent.Workspace,
				nextCandidates[0],
				nextCandidates[1:],
				nextProvider,
				nextCandidateProviders,
			)
			populateCandidateProvidersFromCandidates(
				cfg,
				agent.Workspace,
				nextCandidates[1:],
				nextCandidateProviders,
			)

			oldModel := agent.Model
			oldProvider := agent.Provider
			oldCandidateProviders := agent.CandidateProviders
			previousProviders := make(map[string]providers.LLMProvider, len(oldCandidateProviders)+1)
			for key, provider := range oldCandidateProviders {
				previousProviders[key] = provider
			}
			previousProviders["previous-primary"] = oldProvider
			agent.Model = value
			agent.Provider = nextProvider
			agent.Candidates = nextCandidates
			agent.CandidateProviders = nextCandidateProviders
			agent.ThinkingLevel = parseThinkingLevel(modelCfg.ThinkingLevel)
			agent.ThinkingLevelConfigured = isConfiguredThinkingLevel(modelCfg.ThinkingLevel)

			closeUnreferencedStatefulProviders(
				previousProviders,
				nextCandidateProviders,
				nextProvider,
				agent.LightProvider,
			)
			return oldModel, nil
		}

		rt.ClearHistory = func() error {
			if opts == nil {
				return fmt.Errorf("process options not available")
			}
			// /clear can arrive before any turn has persisted session scope
			// metadata (runAgentLoop records it per turn), so record it here to
			// let the ContextManager resolve which agent owns the session.
			ensureSessionMetadata(
				agent.Sessions,
				opts.Dispatch.SessionKey,
				opts.Dispatch.SessionScope,
				opts.Dispatch.SessionAliases,
			)
			if err := al.contextManager.Clear(ctx, opts.SessionKey); err != nil {
				return err
			}
			if tool, ok := agent.Tools.Get("todo"); ok {
				if todo, ok := tool.(*tools.TodoTool); ok {
					todo.ClearSession(agent.ID, opts.SessionKey)
				}
			}
			agent.ContextBuilder.ForgetMemorySnapshot(opts.SessionKey)
			return nil
		}

		rt.CompactContext = func(focus string, force bool) (commands.CompactOutcome, error) {
			if opts == nil {
				return commands.CompactOutcome{}, fmt.Errorf("process options not available")
			}
			// Same as /clear: the owning agent must be resolvable from the session.
			ensureSessionMetadata(
				agent.Sessions,
				opts.Dispatch.SessionKey,
				opts.Dispatch.SessionScope,
				opts.Dispatch.SessionAliases,
			)
			before, _ := al.currentContextUsage(agent, opts.SessionKey)
			outcome := &CompactOutcome{}
			if before != nil {
				outcome.TokensBefore = before.HistoryTokens
			}
			err := al.contextManager.Compact(ctx, &CompactRequest{
				SessionKey:         opts.SessionKey,
				Reason:             ContextCompressReasonManual,
				Budget:             usableContextTokens(agent),
				ActiveTurnMessages: al.activeTurnMessageCountForSession(opts.SessionKey),
				Instructions:       focus,
				Force:              force,
				Outcome:            outcome,
			})
			after, _ := al.currentContextUsage(agent, opts.SessionKey)
			if after != nil {
				outcome.TokensAfter = after.HistoryTokens
			}
			if errors.Is(err, ErrNothingToCompact) {
				if outcome.Reason == "" {
					outcome.Reason = CompactReasonNoCandidates
				}
				return commandCompactOutcome(outcome), nil
			}
			if err != nil {
				return commands.CompactOutcome{}, err
			}
			if outcome.Reason == "" {
				outcome.Reason = CompactReasonCompacted
			}
			return commandCompactOutcome(outcome), nil
		}

		rt.GetContextStats = func() *commands.ContextStats {
			if opts == nil || agent.Sessions == nil {
				return nil
			}
			usage, messageCount := al.currentContextUsage(agent, opts.SessionKey)
			if usage == nil {
				return nil
			}
			return &commands.ContextStats{
				UsedTokens:        usage.UsedTokens,
				TotalTokens:       usage.TotalTokens,
				HistoryTokens:     usage.HistoryTokens,
				CompressAtTokens:  usage.CompressAtTokens,
				SummarizeAtTokens: usage.SummarizeAtTokens,
				UsedPercent:       usage.UsedPercent,
				MessageCount:      messageCount,
			}
		}
	}
	return rt
}

func summarizeMCPToolParameters(schema any) []commands.MCPToolParameterInfo {
	properties := toolshared.SchemaProperties(schema)
	if len(properties) == 0 {
		return nil
	}
	params := make([]commands.MCPToolParameterInfo, 0, len(properties))
	for _, property := range properties {
		params = append(params, commands.MCPToolParameterInfo{
			Name:        property.Name,
			Type:        property.Type,
			Description: property.Description,
			Required:    property.Required,
		})
	}
	return params
}

func (al *AgentLoop) setPendingSkills(sessionKey string, skillNames []string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || len(skillNames) == 0 {
		return
	}

	filtered := make([]string, 0, len(skillNames))
	for _, name := range skillNames {
		name = strings.TrimSpace(name)
		if name != "" {
			filtered = append(filtered, name)
		}
	}
	if len(filtered) == 0 {
		return
	}

	al.pendingSkills.Store(sessionKey, filtered)
}

func commandCompactOutcome(outcome *CompactOutcome) commands.CompactOutcome {
	if outcome == nil {
		return commands.CompactOutcome{}
	}
	return commands.CompactOutcome{
		Compacted:          outcome.Compacted,
		Reason:             commands.CompactReason(outcome.Reason),
		TokensBefore:       outcome.TokensBefore,
		TokensAfter:        outcome.TokensAfter,
		Target:             outcome.Target,
		LeafSummaries:      outcome.LeafSummaries,
		CondensedSummaries: outcome.CondensedSummaries,
	}
}

func (al *AgentLoop) takePendingSkills(sessionKey string) []string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}

	value, ok := al.pendingSkills.LoadAndDelete(sessionKey)
	if !ok {
		return nil
	}

	skills, ok := value.([]string)
	if !ok {
		return nil
	}

	return append([]string(nil), skills...)
}

func (al *AgentLoop) clearPendingSkills(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}
	al.pendingSkills.Delete(sessionKey)
}
