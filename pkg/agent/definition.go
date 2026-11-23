package agent

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gomarkdown/markdown/parser"
	"gopkg.in/yaml.v3"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
	"github.com/sipeed/picoclaw/pkg/logger"
)

// AgentDefinitionSource identifies which agent bootstrap file produced the definition.
type AgentDefinitionSource string

const (
	// AgentDefinitionSourceAgent indicates the new AGENT.md format.
	AgentDefinitionSourceAgent AgentDefinitionSource = "AGENT.md"
	// AgentDefinitionSourceAgents indicates the legacy AGENTS.md format.
	AgentDefinitionSourceAgents AgentDefinitionSource = "AGENTS.md"
)

// AgentFrontmatter holds machine-readable AGENT.md configuration.
//
// Known fields are exposed directly for convenience. Fields keeps the full
// parsed frontmatter so future refactors can read additional keys without
// changing the loader contract again.
type AgentFrontmatter struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Tools       []string       `json:"tools,omitempty"`
	Model       string         `json:"model,omitempty"`
	MaxTurns    *int           `json:"maxTurns,omitempty"`
	Skills      []string       `json:"skills,omitempty"`
	MCPServers  []string       `json:"mcpServers,omitempty"`
	Fields      map[string]any `json:"-"`
}

// AgentPromptDefinition represents the parsed AGENT.md or AGENTS.md prompt file.
type AgentPromptDefinition struct {
	Path           string           `json:"path"`
	Raw            string           `json:"raw"`
	Body           string           `json:"body"`
	RawFrontmatter string           `json:"raw_frontmatter,omitempty"`
	Frontmatter    AgentFrontmatter `json:"frontmatter"`
	FrontmatterErr string           `json:"frontmatter_error,omitempty"`
}

// SoulDefinition represents the resolved SOUL.md file linked to the agent.
type SoulDefinition struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// UserDefinition represents the resolved USER.md file linked to the workspace.
type UserDefinition struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// AgentContextDefinition captures the workspace agent definition in a runtime-friendly shape.
type AgentContextDefinition struct {
	Source AgentDefinitionSource  `json:"source,omitempty"`
	Agent  *AgentPromptDefinition `json:"agent,omitempty"`
	Soul   *SoulDefinition        `json:"soul,omitempty"`
	User   *UserDefinition        `json:"user,omitempty"`
}

// LoadAgentDefinition parses the workspace agent bootstrap files.
//
// It prefers the new AGENT.md format and its paired SOUL.md file. When the
// structured files are absent, it falls back to the legacy AGENTS.md layout so
// the current runtime can transition incrementally.
func (cb *ContextBuilder) LoadAgentDefinition() AgentContextDefinition {
	maxChars, _ := cb.bootstrapLimits()
	return loadAgentDefinitionWithLimit(cb.workspace, maxChars)
}

func loadAgentDefinition(workspace string) AgentContextDefinition {
	return loadAgentDefinitionWithLimit(workspace, config.DefaultMaxBootstrapChars)
}

func loadAgentDefinitionWithLimit(workspace string, maxChars int) AgentContextDefinition {
	if maxChars <= 0 {
		maxChars = config.DefaultMaxBootstrapChars
	}

	definition := AgentContextDefinition{}
	definition.User = loadUserDefinition(workspace, maxChars)

	agentPath := filepath.Join(workspace, string(AgentDefinitionSourceAgent))
	if content, ok := readBootstrapFile(agentPath); ok {
		prompt := parseAgentPromptDefinition(agentPath, content)
		capAgentPromptDefinition(&prompt, maxChars, relativeWorkspacePath(workspace, agentPath))
		definition.Source = AgentDefinitionSourceAgent
		definition.Agent = &prompt

		soulPath := filepath.Join(workspace, "SOUL.md")
		if content, ok := readBootstrapFile(soulPath); ok {
			definition.Soul = &SoulDefinition{
				Path:    soulPath,
				Content: capBootstrapContent(content, maxChars, relativeWorkspacePath(workspace, soulPath)),
			}
		}
		return definition
	}

	legacyPath := filepath.Join(workspace, string(AgentDefinitionSourceAgents))
	if content, ok := readBootstrapFile(legacyPath); ok {
		prompt := AgentPromptDefinition{
			Path: legacyPath,
			Raw:  content,
			Body: content,
		}
		capAgentPromptDefinition(&prompt, maxChars, relativeWorkspacePath(workspace, legacyPath))
		definition.Source = AgentDefinitionSourceAgents
		definition.Agent = &prompt
	}

	defaultSoulPath := filepath.Join(workspace, "SOUL.md")
	if definition.Source != "" || fileutil.Exists(defaultSoulPath) {
		if content, ok := readBootstrapFile(defaultSoulPath); ok {
			definition.Soul = &SoulDefinition{
				Path:    defaultSoulPath,
				Content: capBootstrapContent(content, maxChars, relativeWorkspacePath(workspace, defaultSoulPath)),
			}
		}
	}

	return definition
}

func loadUserDefinition(workspace string, maxChars int) *UserDefinition {
	userPath := filepath.Join(workspace, "USER.md")
	if content, ok := readBootstrapFile(userPath); ok {
		return &UserDefinition{
			Path:    userPath,
			Content: capBootstrapContent(content, maxChars, relativeWorkspacePath(workspace, userPath)),
		}
	}

	return nil
}

// capAgentPromptDefinition bounds the parsed prompt and its retained raw copy.
// Frontmatter is parsed before this runs, so the machine-readable fields keep
// their full values even when the body is shortened.
func capAgentPromptDefinition(prompt *AgentPromptDefinition, maxChars int, label string) {
	if prompt == nil {
		return
	}
	prompt.Body = capBootstrapContent(prompt.Body, maxChars, label)
	prompt.Raw = capBootstrapContent(prompt.Raw, maxChars, label)
}

// capBootstrapContent applies the per-file cap and returns the content
// unchanged when it already fits.
func capBootstrapContent(content string, maxChars int, label string) string {
	capped, _ := truncateBootstrapText(content, maxChars, label)
	return capped
}

// bootstrapSourcePaths returns the workspace files whose mtime can invalidate
// the cached system prompt. It only stats the files, so a large bootstrap file
// is not read again on every prompt-cache check.
func bootstrapSourcePaths(workspace string) []string {
	agentPath := filepath.Join(workspace, string(AgentDefinitionSourceAgent))
	paths := []string{
		agentPath,
		filepath.Join(workspace, "SOUL.md"),
	}
	if !bootstrapFileReadable(agentPath) {
		paths = append(
			paths,
			filepath.Join(workspace, string(AgentDefinitionSourceAgents)),
			filepath.Join(workspace, "IDENTITY.md"),
		)
	}
	return uniquePaths(paths)
}

// bootstrapFileReadable mirrors the first step of loadAgentDefinition: the
// structured layout wins only when AGENT.md can actually be read.
func bootstrapFileReadable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = file.Close()
	return true
}

func parseAgentPromptDefinition(path, content string) AgentPromptDefinition {
	frontmatter, body := splitAgentFrontmatter(content)
	parsedFrontmatter, err := parseAgentFrontmatter(path, frontmatter)
	return AgentPromptDefinition{
		Path:           path,
		Raw:            content,
		Body:           body,
		RawFrontmatter: frontmatter,
		Frontmatter:    parsedFrontmatter,
		FrontmatterErr: errorString(err),
	}
}

func parseAgentFrontmatter(path, frontmatter string) (AgentFrontmatter, error) {
	frontmatter = strings.TrimSpace(frontmatter)
	if frontmatter == "" {
		return AgentFrontmatter{}, nil
	}

	rawFields := make(map[string]any)
	if err := yaml.Unmarshal([]byte(frontmatter), &rawFields); err != nil {
		logger.WarnCF("agent", "Failed to parse AGENT.md frontmatter", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return AgentFrontmatter{}, err
	}

	var typed struct {
		Name        string   `yaml:"name"`
		Description string   `yaml:"description"`
		Tools       []string `yaml:"tools"`
		Model       string   `yaml:"model"`
		MaxTurns    *int     `yaml:"maxTurns"`
		Skills      []string `yaml:"skills"`
		MCPServers  []string `yaml:"mcpServers"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &typed); err != nil {
		logger.WarnCF("agent", "Failed to decode AGENT.md frontmatter fields", map[string]any{
			"path":  path,
			"error": err.Error(),
		})
		return AgentFrontmatter{}, err
	}

	return AgentFrontmatter{
		Name:        strings.TrimSpace(typed.Name),
		Description: strings.TrimSpace(typed.Description),
		Tools:       append([]string(nil), typed.Tools...),
		Model:       strings.TrimSpace(typed.Model),
		MaxTurns:    typed.MaxTurns,
		Skills:      append([]string(nil), typed.Skills...),
		MCPServers:  append([]string(nil), typed.MCPServers...),
		Fields:      rawFields,
	}, nil
}

func splitAgentFrontmatter(content string) (frontmatter, body string) {
	normalized := string(parser.NormalizeNewlines([]byte(content)))
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return "", content
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return "", content
	}

	frontmatter = strings.Join(lines[1:end], "\n")
	body = strings.Join(lines[end+1:], "\n")
	body = strings.TrimLeft(body, "\n")
	return frontmatter, body
}

func relativeWorkspacePath(workspace, path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	relativePath, err := filepath.Rel(workspace, path)
	if err == nil && relativePath != "." && !strings.HasPrefix(relativePath, "..") {
		return filepath.ToSlash(relativePath)
	}
	return filepath.Clean(path)
}

func uniquePaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		cleaned := filepath.Clean(path)
		if slices.Contains(result, cleaned) {
			continue
		}
		result = append(result, cleaned)
	}
	return result
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
