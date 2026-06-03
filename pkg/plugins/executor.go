package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/commands"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/tools"
)

const defaultCommandTimeout = 30 * time.Second

type CommandTool struct {
	plugin Plugin
	spec   ToolManifest
}

type execPayload struct {
	Plugin    string         `json:"plugin"`
	PluginKey string         `json:"plugin_key"`
	Tool      string         `json:"tool,omitempty"`
	Command   string         `json:"command,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	RawArgs   string         `json:"raw_args,omitempty"`
	Channel   string         `json:"channel,omitempty"`
	ChatID    string         `json:"chat_id,omitempty"`
}

func NewCommandTool(plugin Plugin, spec ToolManifest) *CommandTool {
	return &CommandTool{plugin: plugin, spec: spec}
}

func (t *CommandTool) Name() string {
	return strings.TrimSpace(t.spec.Name)
}

func (t *CommandTool) Description() string {
	return strings.TrimSpace(t.spec.Description)
}

func (t *CommandTool) Parameters() map[string]any {
	if len(t.spec.Parameters) > 0 {
		return t.spec.Parameters
	}
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *CommandTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	payload := execPayload{
		Plugin:    t.plugin.Manifest.Name,
		PluginKey: t.plugin.Key,
		Tool:      t.Name(),
		Arguments: args,
		Channel:   tools.ToolChannel(ctx),
		ChatID:    tools.ToolChatID(ctx),
	}
	stdout, stderr, err := runPluginCommand(ctx, t.plugin, t.spec.Command, t.spec.Dir, t.spec.Env, t.spec.TimeoutMS, payload)
	if err != nil {
		message := strings.TrimSpace(stderr)
		if message == "" {
			message = err.Error()
		}
		return tools.ErrorResult(message).WithError(err)
	}
	return toolResultFromOutput(stdout)
}

func commandDefinitionFromManifest(plugin Plugin, spec CommandManifest) (commands.Definition, bool) {
	name := strings.TrimSpace(spec.Name)
	if name == "" || len(spec.Command) == 0 {
		return commands.Definition{}, false
	}

	return commands.Definition{
		Name:        name,
		Description: strings.TrimSpace(spec.Description),
		Usage:       strings.TrimSpace(spec.Usage),
		Aliases:     append([]string(nil), spec.Aliases...),
		Handler: func(ctx context.Context, req commands.Request, rt *commands.Runtime) error {
			payload := execPayload{
				Plugin:    plugin.Manifest.Name,
				PluginKey: plugin.Key,
				Command:   name,
				RawArgs:   commandRawArgs(req.Text),
				Channel:   req.Channel,
				ChatID:    req.ChatID,
			}
			stdout, stderr, err := runPluginCommand(
				ctx,
				plugin,
				spec.Command,
				spec.Dir,
				spec.Env,
				spec.TimeoutMS,
				payload,
			)
			if err != nil {
				message := strings.TrimSpace(stderr)
				if message == "" {
					message = err.Error()
				}
				return req.Reply(message)
			}
			return req.Reply(commandOutputText(stdout))
		},
	}, true
}

func runPluginCommand(
	ctx context.Context,
	plugin Plugin,
	command []string,
	dir string,
	env map[string]string,
	timeoutMS int,
	payload any,
) (string, string, error) {
	if len(command) == 0 {
		return "", "", fmt.Errorf("command is required")
	}
	timeout := defaultCommandTimeout
	if timeoutMS > 0 {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	resolvedCommand := resolveCommand(plugin.Dir, command)
	cmd := exec.CommandContext(runCtx, resolvedCommand[0], resolvedCommand[1:]...)
	cmd.Dir = resolvePluginPath(plugin.Dir, dir)
	if cmd.Dir == "" {
		cmd.Dir = plugin.Dir
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return "", "", fmt.Errorf("encode plugin payload: %w", err)
	}
	cmd.Stdin = bytes.NewReader(payloadBytes)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = os.Environ()
	for key, value := range mergeEnv(pluginEnv(plugin), env) {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	if err := isolation.Start(cmd); err != nil {
		return stdout.String(), stderr.String(), err
	}
	err = cmd.Wait()
	if runCtx.Err() != nil {
		return stdout.String(), stderr.String(), runCtx.Err()
	}
	if err != nil {
		return stdout.String(), stderr.String(), err
	}
	return stdout.String(), stderr.String(), nil
}

func toolResultFromOutput(output string) *tools.ToolResult {
	output = strings.TrimSpace(output)
	if output == "" {
		return tools.SilentResult("Plugin tool completed with no output.")
	}

	var result tools.ToolResult
	if err := json.Unmarshal([]byte(output), &result); err == nil && result.ContentForLLM() != "" {
		return &result
	}

	var generic map[string]any
	if err := json.Unmarshal([]byte(output), &generic); err == nil {
		if content := genericJSONText(generic); content != "" {
			return tools.SilentResult(content)
		}
	}

	return tools.SilentResult(output)
}

func commandOutputText(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return "Plugin command completed with no output."
	}
	var result struct {
		Text    string `json:"text"`
		Message string `json:"message"`
		ForUser string `json:"for_user"`
		ForLLM  string `json:"for_llm"`
	}
	if err := json.Unmarshal([]byte(output), &result); err == nil {
		for _, value := range []string{result.Text, result.Message, result.ForUser, result.ForLLM} {
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return output
}

func genericJSONText(value map[string]any) string {
	for _, key := range []string{"for_llm", "text", "message", "result"} {
		if raw, ok := value[key].(string); ok && strings.TrimSpace(raw) != "" {
			return strings.TrimSpace(raw)
		}
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func commandRawArgs(text string) string {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) <= 1 {
		return ""
	}
	commandToken := fields[0]
	idx := strings.Index(text, commandToken)
	if idx < 0 {
		return strings.Join(fields[1:], " ")
	}
	return strings.TrimSpace(text[idx+len(commandToken):])
}
