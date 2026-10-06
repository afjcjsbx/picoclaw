package agentctx

import "strings"

func ToolUseSystemPromptRule() string {
	return "**ALWAYS use tools** - When you need to perform an action (schedule reminders, send messages, execute commands, etc.), you MUST call the appropriate tool. Do NOT just say you'll do it or pretend to do it."
}

func FilterNamesByTurnProfile(names []string, allowed []string) []string {
	if len(names) == 0 {
		return nil
	}
	allowedSet := CleanAllowedSet(allowed)
	if len(allowedSet) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := allowedSet[strings.ToLower(strings.TrimSpace(name))]; ok {
			out = append(out, name)
		}
	}
	return out
}

func CleanAllowedSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		out[value] = struct{}{}
	}
	return out
}
