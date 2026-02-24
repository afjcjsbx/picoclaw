package tools

import (
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/skills"
)

type AgentContext struct {
	AgentID     string
	Workspace   string
	Model       string
	MaxTokens   int
	Temperature float64
}

type SpawnAllowlistChecker func(targetAgentID string) bool

func SetupSharedTools(
	registry *ToolRegistry,
	cfg *config.Config,
	msgBus *bus.MessageBus,
	provider providers.LLMProvider,
	agentCtx AgentContext,
	canSpawn SpawnAllowlistChecker,
) {
	// Web tools
	if searchTool := NewWebSearchTool(WebSearchToolOptions{
		BraveAPIKey:          cfg.Tools.Web.Brave.APIKey,
		BraveMaxResults:      cfg.Tools.Web.Brave.MaxResults,
		BraveEnabled:         cfg.Tools.Web.Brave.Enabled,
		TavilyAPIKey:         cfg.Tools.Web.Tavily.APIKey,
		TavilyBaseURL:        cfg.Tools.Web.Tavily.BaseURL,
		TavilyMaxResults:     cfg.Tools.Web.Tavily.MaxResults,
		TavilyEnabled:        cfg.Tools.Web.Tavily.Enabled,
		DuckDuckGoMaxResults: cfg.Tools.Web.DuckDuckGo.MaxResults,
		DuckDuckGoEnabled:    cfg.Tools.Web.DuckDuckGo.Enabled,
		PerplexityAPIKey:     cfg.Tools.Web.Perplexity.APIKey,
		PerplexityMaxResults: cfg.Tools.Web.Perplexity.MaxResults,
		PerplexityEnabled:    cfg.Tools.Web.Perplexity.Enabled,
	}); searchTool != nil {
		registry.Register(searchTool)
	}

	if cfg.Tools.Core.EnableWebFetch {
		registry.Register(NewWebFetchTool(50000))
	}

	// Hardware tools
	if cfg.Tools.Hardware.EnableI2C {
		registry.Register(NewI2CTool())
	}
	if cfg.Tools.Hardware.EnableSPI {
		registry.Register(NewSPITool())
	}

	// Message tool
	if cfg.Tools.Core.EnableMessage {
		messageTool := NewMessageTool()
		messageTool.SetSendCallback(func(channel, chatID, content string) error {
			msgBus.PublishOutbound(bus.OutboundMessage{
				Channel: channel,
				ChatID:  chatID,
				Content: content,
			})
			return nil
		})
		registry.Register(messageTool)
	}

	// Skills tools
	if cfg.Tools.Skills.Enabled {
		registryMgr := skills.NewRegistryManagerFromConfig(skills.RegistryConfig{
			MaxConcurrentSearches: cfg.Tools.Skills.MaxConcurrentSearches,
			ClawHub:               skills.ClawHubConfig(cfg.Tools.Skills.Registries.ClawHub),
		})
		searchCache := skills.NewSearchCache(
			cfg.Tools.Skills.SearchCache.MaxSize,
			time.Duration(cfg.Tools.Skills.SearchCache.TTLSeconds)*time.Second,
		)
		registry.Register(NewFindSkillsTool(registryMgr, searchCache))
		registry.Register(NewInstallSkillTool(registryMgr, agentCtx.Workspace))
	}

	// Spawn tool
	if cfg.Tools.Core.EnableSpawn {
		subagentManager := NewSubagentManager(provider, agentCtx.Model, agentCtx.Workspace, msgBus)
		subagentManager.SetLLMOptions(agentCtx.MaxTokens, agentCtx.Temperature)
		spawnTool := NewSpawnTool(subagentManager)
		spawnTool.SetAllowlistChecker(canSpawn)
		registry.Register(spawnTool)
	}
}

// SetupWorkspaceTools registers tools related to file system and execution
// centralizing the logic and decoupling it from the agent.
func SetupWorkspaceTools(registry *ToolRegistry, cfg *config.Config, workspace string, restrict bool) {
	if cfg.Tools.Filesystem.EnableRead {
		registry.Register(NewReadFileTool(workspace, restrict))
	}
	if cfg.Tools.Filesystem.EnableWrite {
		registry.Register(NewWriteFileTool(workspace, restrict))
	}
	if cfg.Tools.Filesystem.EnableList {
		registry.Register(NewListDirTool(workspace, restrict))
	}
	if cfg.Tools.Filesystem.EnableEdit {
		registry.Register(NewEditFileTool(workspace, restrict))
	}
	if cfg.Tools.Filesystem.EnableAppend {
		registry.Register(NewAppendFileTool(workspace, restrict))
	}
	if cfg.Tools.Exec.Enabled {
		registry.Register(NewExecToolWithConfig(workspace, restrict, cfg))
	}
}
