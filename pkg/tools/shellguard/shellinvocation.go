package shellguard

import "strings"

func isShellCommand(name string) bool {
	switch name {
	case "sh", "bash", "zsh", "dash", "fish", "csh", "tcsh", "ksh", "pwsh", "powershell", "cmd":
		return true
	}
	return false
}

// shellValueFlags are shell options that consume the following word, so it is
// not mistaken for the -c script or a script file.
var shellValueFlags = map[string]bool{
	"-o": true, "-O": true, "--rcfile": true, "--init-file": true,
}

// shellInvocation is the statically known script source of a shell invocation.
// hasScript reports a literal -c/--command argument. readsStdin reports that
// the shell would execute stdin, so a heredoc on the command is a script.
type shellInvocation struct {
	script     word
	hasScript  bool
	readsStdin bool
	opaque     bool
}

// parseShellInvocation extracts the -c script or stdin mode from a shell
// argument vector. The guards share it so risk classification, workspace
// validation and canonical deny forms agree on shell flags.
func parseShellInvocation(words []word) shellInvocation {
	invocation := shellInvocation{readsStdin: true}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.dynamic {
			return shellInvocation{opaque: true}
		}
		text := w.text
		switch {
		case text == "--":
			if i+1 < len(words) {
				return shellInvocation{opaque: true}
			}
			return invocation
		case text == "-":
			return invocation
		case text == "--command":
			if i+1 < len(words) {
				return shellInvocation{script: words[i+1], hasScript: true}
			}
			return shellInvocation{opaque: true}
		case strings.HasPrefix(text, "--command="):
			return shellInvocation{
				script:    word{text: strings.TrimPrefix(text, "--command="), dynamic: w.dynamic},
				hasScript: true,
			}
		case shellValueFlags[text]:
			i++
		case text == "-c":
			if i+1 < len(words) {
				return shellInvocation{script: words[i+1], hasScript: true}
			}
			return shellInvocation{opaque: true}
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--"):
			flags := text[1:]
			if strings.Contains(flags, "c") {
				if i+1 < len(words) {
					return shellInvocation{script: words[i+1], hasScript: true}
				}
				return shellInvocation{opaque: true}
			}
			if strings.Contains(flags, "s") {
				return invocation
			}
		case !strings.HasPrefix(text, "-"):
			return shellInvocation{opaque: true}
		}
	}
	return invocation
}
