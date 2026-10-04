package commands

import (
	"context"
	"strings"
)

func voiceCommand() Definition {
	return Definition{
		Name:        "voice",
		Description: "Set automatic text-to-speech mode",
		Usage:       "/voice [off|on|tts|status]",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			parts := strings.Fields(req.Text)
			mode := "status"
			if len(parts) > 1 {
				mode = strings.ToLower(parts[1])
			}
			if len(parts) > 2 {
				return req.Reply("Usage: /voice [off|on|tts|status]")
			}

			switch mode {
			case "status":
				current := "off"
				if rt != nil && rt.VoiceMode != "" {
					current = rt.VoiceMode
				}
				return req.Reply("Voice mode: " + current + ".")
			case "off", "on", "tts":
				if rt == nil || rt.SetVoiceMode == nil {
					return req.Reply(unavailableMsg)
				}
				if err := rt.SetVoiceMode(mode); err != nil {
					return req.Reply(err.Error())
				}
				return req.Reply("Voice mode set to " + mode + ".")
			default:
				return req.Reply("Usage: /voice [off|on|tts|status]")
			}
		},
	}
}
