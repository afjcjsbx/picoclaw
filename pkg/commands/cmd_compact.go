package commands

import (
	"context"
	"strings"
)

func compactCommand() Definition {
	return Definition{
		Name:        "compact",
		Description: "Compact the conversation context, optionally focusing the summary on given details",
		Usage:       "/compact [focus]",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.CompactContext == nil {
				return req.Reply(unavailableMsg)
			}
			// Everything after the command token is the focus, e.g. "/compact focus on database migration".
			focus := strings.Join(strings.Fields(req.Text)[1:], " ")
			compacted, err := rt.CompactContext(focus)
			if err != nil {
				return req.Reply("Failed to compact context: " + err.Error())
			}
			if !compacted {
				return req.Reply("Nothing to compact yet.")
			}
			return req.Reply("Context compacted.")
		},
	}
}
