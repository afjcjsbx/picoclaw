package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

func (s *workspaceState) checkNested(
	args []word,
	redirs []*syntax.Redirect,
	depth int,
) error {
	if len(args) == 0 || args[0].dynamic {
		return nil
	}
	name := baseCommand(args[0].text)

	if isShellCommand(name) {
		invocation := parseShellInvocation(args[1:])
		if invocation.hasScript {
			if invocation.script.dynamic {
				return newWorkspaceError("dynamic shell script cannot be validated under workspace restriction")
			}
			return checkWorkspaceScript(invocation.script.text, s.nestedPolicy(), depth+1)
		}
		// `bash <<EOF` executes the heredoc body as a script. Quoted and
		// unquoted bodies are both inspected; expansion is applied first when
		// the outer shell would have expanded it. A heredoc on a shell that
		// already has a script file or -c argument is stdin data, not a
		// script, and is left alone.
		if invocation.readsStdin {
			for _, redir := range redirs {
				if redir == nil {
					continue
				}
				if redir.Op != syntax.Hdoc && redir.Op != syntax.DashHdoc &&
					redir.Op != syntax.WordHdoc {
					continue
				}
				bodyWord := redir.Hdoc
				if bodyWord == nil {
					// <<< here-strings keep their data in Word instead of Hdoc.
					bodyWord = redir.Word
				}
				if bodyWord == nil {
					continue
				}
				body := s.expandWord(bodyWord, false)
				if body.dynamic {
					return newWorkspaceError("dynamic shell script cannot be validated under workspace restriction")
				}
				if err := checkWorkspaceScript(body.text, s.nestedPolicy(), depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if name == "env" {
		opts := parseEnvArgs(args[1:])
		if opts.splitString {
			return newWorkspaceError("env -S cannot be validated under workspace restriction")
		}
		if opts.dynamic {
			return newWorkspaceError("dynamic command cannot be validated under workspace restriction")
		}
		if opts.hasChdir {
			return newWorkspaceError("env -C cannot be validated under workspace restriction")
		}
		if opts.ignoreEnv || opts.unsetDynamic {
			return newWorkspaceError("environment clearing cannot be validated under workspace restriction")
		}
		for _, name := range opts.unset {
			if isPathEnvName(name) {
				return newWorkspaceError("path environment override cannot be validated under workspace restriction")
			}
		}
		for _, assignment := range opts.assignments {
			envName, _, _ := strings.Cut(assignment.text, "=")
			if isPathEnvName(envName) {
				return newWorkspaceError("path environment override cannot be validated under workspace restriction")
			}
		}
		if len(opts.command) == 0 {
			return nil
		}
		return s.checkSimple(opts.command, nil, depth+1, false)
	}

	switch name {
	case "eval", "trap":
		// These execute a string the static pass cannot follow without
		// reimplementing the shell; fail closed under workspace restriction.
		return newWorkspaceError(name + " cannot be validated under workspace restriction")
	case "exec":
		if nested, ok := execCommand(args[1:]); ok {
			return s.checkSimple(nested, nil, depth+1, false)
		}
		return nil
	case "find":
		for _, nested := range findExecCommands(args[1:]) {
			if err := s.checkSimple(nested, nil, depth+1, false); err != nil {
				return err
			}
		}
		return nil
	}

	if spec, ok := wrapperSpecs[name]; ok {
		words := args[1:]
		if name == "command" && hasCommandLookupFlag(words) {
			return nil
		}
		nested, found, dynamic := wrapperNested(words, spec)
		if dynamic {
			return newWorkspaceError("dynamic command cannot be validated under workspace restriction")
		}
		if found && len(nested) > 0 {
			return s.checkSimple(nested, nil, depth+1, false)
		}
	}
	return nil
}
