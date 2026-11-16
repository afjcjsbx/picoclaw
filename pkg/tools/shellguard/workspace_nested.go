package shellguard

import "mvdan.cc/sh/v3/syntax"

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
		if len(opts.command) == 0 {
			return nil
		}
		if opts.hasChdir && !opts.chdir.dynamic && opts.chdir.text != "" && opts.chdir.text != "-" {
			saved := append([]string(nil), s.cwds...)
			s.addCwd(s.resolveCwd(opts.chdir.text))
			err := s.checkSimple(opts.command, nil, depth+1, false)
			s.cwds = saved
			return err
		}
		return s.checkSimple(opts.command, nil, depth+1, false)
	}

	switch name {
	case "eval":
		script, ok := joinLiteralWords(args[1:])
		if !ok {
			return newWorkspaceError("dynamic eval cannot be validated under workspace restriction")
		}
		return checkWorkspaceScript(script, s.nestedPolicy(), depth+1)
	case "trap":
		return s.checkTrap(args[1:], depth)
	case "exec":
		return s.checkExec(args[1:], depth)
	case "find":
		return s.checkFindExec(args[1:], depth)
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

// envChdirTarget returns the directory passed to env -C/--chdir.
func envChdirTarget(args []word) (word, bool) {
	opts := parseEnvArgs(args)
	if !opts.hasChdir {
		return word{}, false
	}
	return word{text: opts.chdir.text, dynamic: opts.chdir.dynamic}, true
}

func (s *workspaceState) checkTrap(args []word, depth int) error {
	action, found, dynamic := trapAction(args)
	if dynamic {
		return newWorkspaceError("dynamic trap action cannot be validated under workspace restriction")
	}
	if !found {
		return nil
	}
	return checkWorkspaceScript(action.text, s.nestedPolicy(), depth+1)
}

// checkExec inspects the command that exec replaces the shell with. exec is
// both a shell builtin and an execve wrapper, so a nested shell script would
// otherwise escape the AST pass.
func (s *workspaceState) checkExec(args []word, depth int) error {
	nested, ok := execCommand(args)
	if !ok {
		return nil
	}
	return s.checkSimple(nested, nil, depth+1, false)
}

// checkFindExec inspects commands launched by find -exec/-execdir/-ok/-okdir.
// find runs them directly, so a nested sh -c script is just as reachable as it
// is from a top-level command.
func (s *workspaceState) checkFindExec(args []word, depth int) error {
	for _, nested := range findExecCommands(args) {
		if err := s.checkSimple(nested, nil, depth+1, false); err != nil {
			return err
		}
	}
	return nil
}
