package commands

import "context"

func resumeCommand() Definition {
	return Definition{
		Name:        "resume",
		Description: "Resume the previous task in this session",
		Usage:       "/resume",
		Handler: func(ctx context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.ResumeSession == nil {
				return req.Reply(unavailableMsg)
			}
			response, err := rt.ResumeSession(ctx)
			if err != nil {
				return req.Reply("Failed to resume: " + err.Error())
			}
			if response == "" {
				return req.Reply("Nothing to resume in this session.")
			}
			return req.Reply(response)
		},
	}
}
