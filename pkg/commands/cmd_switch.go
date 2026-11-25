package commands

import (
	"context"
	"fmt"
	"strings"
)

func switchCommand() Definition {
	return Definition{
		Name:        "switch",
		Description: "Switch model",
		SubCommands: []SubCommand{
			{
				Name:        "model",
				Description: "Switch to a different model",
				ArgsUsage:   "[<index>|<name>]",
				Handler:     switchModelHandler(),
			},
			{
				Name:        "channel",
				Description: "Moved to /check channel",
				Handler: func(_ context.Context, req Request, _ *Runtime) error {
					return req.Reply("This command has moved. Please use: /check channel <name>")
				},
			},
		},
	}
}

func switchModelHandler() Handler {
	return func(_ context.Context, req Request, rt *Runtime) error {
		if rt == nil || rt.SwitchModel == nil {
			return req.Reply(unavailableMsg)
		}

		options := CollectModelOptions(rt.Config)

		// Bare /switch model lists the numbered choices instead of failing.
		value, ok := switchModelTarget(req.Text)
		if !ok {
			if len(options) == 0 {
				return req.Reply("No models configured")
			}
			return req.Reply(FormatModelOptions(options) +
				"\n\nUse /switch model <index> or /switch model <name>.")
		}

		target, candidates := ResolveModelOption(options, value)
		if len(candidates) > 0 {
			return req.Reply(fmt.Sprintf(
				"Ambiguous model %q. Candidates:\n- %s\n\nUse /switch model <index> to pick one.",
				value,
				strings.Join(candidates, "\n- "),
			))
		}
		if target == "" {
			// No local match (or no config available): let the runtime decide and
			// report the authoritative error.
			target = value
		}

		oldModel, err := rt.SwitchModel(target)
		if err != nil {
			return req.Reply(err.Error())
		}
		return req.Reply(fmt.Sprintf("Switched model from %s to %s", oldModel, target))
	}
}

// switchModelTarget extracts the requested value from "/switch model <value>"
// and the legacy "/switch model to <value>" form. ok is false when no value was
// supplied.
func switchModelTarget(text string) (value string, ok bool) {
	value = nthToken(text, 2)
	if normalizeCommandName(value) == "to" {
		value = nthToken(text, 3)
	}
	value = strings.TrimSpace(value)
	return value, value != ""
}
