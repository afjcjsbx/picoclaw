package commands

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// TurnInfo is a command-facing view of an active turn. It mirrors the fields of
// agent.ActiveTurnInfo that /subagents needs, to avoid a circular dependency.
type TurnInfo struct {
	TurnID       string
	ParentTurnID string
	AgentID      string
	UserMessage  string
	Phase        string
	StartedAt    time.Time
}

const subagentPreviewMaxRunes = 80

func subagentsCommand() Definition {
	return Definition{
		Name:        "subagents",
		Description: "Show running tasks and subagents in this chat",
		Usage:       "/subagents",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.GetActiveTurnTree == nil {
				return req.Reply(unavailableMsg)
			}
			return req.Reply(FormatSubagentsReply(rt.GetActiveTurnTree(), time.Now()))
		},
	}
}

// FormatSubagentsReply renders the active task tree of the current chat.
// turns[0] is the main task (the request being handled); the remaining entries
// are subagents spawned by it, linked through ParentTurnID.
func FormatSubagentsReply(turns []TurnInfo, now time.Time) string {
	if len(turns) == 0 {
		return "Nothing is running in this chat right now."
	}

	childrenOf := make(map[string][]TurnInfo, len(turns))
	for _, turn := range turns[1:] {
		childrenOf[turn.ParentTurnID] = append(childrenOf[turn.ParentTurnID], turn)
	}

	var tree strings.Builder
	subagentNum := 0

	// render writes one turn as a header line plus a preview line, then recurses
	// into its subagents. head is the branch marker for this line, indent is the
	// prefix used for its detail line and for its children's branches.
	var render func(turn TurnInfo, head, indent string, isRoot bool)
	render = func(turn TurnInfo, head, indent string, isRoot bool) {
		name := "Main task"
		if !isRoot {
			subagentNum++
			name = fmt.Sprintf("Subagent %d", subagentNum)
		}

		parts := []string{name}
		if turn.AgentID != "" {
			parts = append(parts, "agent: "+turn.AgentID)
		}
		parts = append(parts, phaseLabel(turn.Phase))
		if !turn.StartedAt.IsZero() {
			parts = append(parts, formatElapsed(now.Sub(turn.StartedAt)))
		}
		tree.WriteString(head + strings.Join(parts, " · ") + "\n")

		if preview := compactPreview(turn.UserMessage, subagentPreviewMaxRunes); preview != "" {
			tree.WriteString(indent + `"` + preview + `"` + "\n")
		}

		children := childrenOf[turn.TurnID]
		for i, child := range children {
			if i == len(children)-1 {
				render(child, indent+"└─ ", indent+"   ", false)
			} else {
				render(child, indent+"├─ ", indent+"│  ", false)
			}
		}
	}
	render(turns[0], "", "  ", true)

	var sb strings.Builder
	sb.WriteString("**Running in this chat:** 1 main task, " + subagentCountLabel(len(turns)-1) + "\n")
	sb.WriteString("Main task = the request being handled. Subagents = helper tasks it started.\n\n")
	sb.WriteString("```text\n")
	sb.WriteString(tree.String())
	sb.WriteString("```")
	return sb.String()
}

func subagentCountLabel(n int) string {
	switch n {
	case 0:
		return "no subagents"
	case 1:
		return "1 subagent"
	default:
		return fmt.Sprintf("%d subagents", n)
	}
}

// phaseLabel maps an internal turn phase to a short description of what the
// task is doing right now.
func phaseLabel(phase string) string {
	switch phase {
	case "setup":
		return "starting"
	case "running":
		return "thinking"
	case "tools":
		return "using tools"
	case "finalizing":
		return "finishing"
	case "completed":
		return "done"
	case "aborted":
		return "stopping"
	case "":
		return "working"
	default:
		return phase
	}
}

// formatElapsed renders a duration as whole seconds, e.g. "12s" or "1m12s".
func formatElapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}

// compactPreview collapses whitespace and truncates to maxRunes characters.
func compactPreview(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return string(runes[:maxRunes-1]) + "…"
}
