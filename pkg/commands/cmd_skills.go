package commands

import (
	"context"
	"fmt"
	"strings"
)

func skillsCommand() Definition {
	return Definition{
		Name:        "skills",
		Description: "List installed skills and how to invoke them",
		Usage:       "/skills",
		Handler: func(_ context.Context, req Request, rt *Runtime) error {
			if rt == nil || rt.ListSkillNames == nil {
				return req.Reply(unavailableMsg)
			}

			names := rt.ListSkillNames()
			if len(names) == 0 {
				return req.Reply("No installed skills")
			}

			return req.Reply(fmt.Sprintf(
				"Installed Skills:\n- %s\n\nUse /<skill-name> <message> to invoke one immediately, /<skill-name> to arm it for your next message, or /use <skill> <message> if you prefer the explicit form.",
				strings.Join(names, "\n- "),
			))
		},
	}
}
