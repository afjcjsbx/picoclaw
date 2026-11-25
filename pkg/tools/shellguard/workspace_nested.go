package shellguard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// checkNested inspects what a command launches: the scripts it runs and the
// commands it execs. What a launcher starts comes from resolveLaunch; this
// only applies the workspace policy to it.
func (s *workspaceState) checkNested(
	args []word,
	redirs []*syntax.Redirect,
	depth int,
) error {
	l := resolveLaunch(args)
	if l.dynamicName {
		return nil
	}

	switch l.name {
	case "eval", "trap":
		// These execute a string the static pass cannot follow without
		// reimplementing the shell; fail closed under workspace restriction.
		return newWorkspaceError(l.name + " cannot be validated under workspace restriction")
	}
	if l.env != nil {
		if err := checkEnvLaunch(l.env); err != nil {
			return err
		}
	}
	if l.opaque != "" {
		return newWorkspaceError("dynamic command cannot be validated under workspace restriction")
	}

	if l.bareShell {
		// `bash <<EOF` executes the heredoc body as a script. Quoted and
		// unquoted bodies are both inspected; expansion is applied first when
		// the outer shell would have expanded it. A heredoc on a shell that
		// already has a script file or -c argument is stdin data, not a
		// script, and is left alone.
		if l.readsStdin {
			return s.checkHeredocScripts(redirs, depth)
		}
		return nil
	}

	for _, script := range l.scripts {
		if script.dynamic {
			return newWorkspaceError("dynamic shell script cannot be validated under workspace restriction")
		}
		if err := checkWorkspaceScript(script.text, s.nestedPolicy(), depth+1); err != nil {
			return err
		}
	}
	for _, argv := range l.argvs {
		if err := s.checkSimple(argv, nil, depth+1, false); err != nil {
			return err
		}
	}
	return nil
}

func (s *workspaceState) checkHeredocScripts(redirs []*syntax.Redirect, depth int) error {
	for _, redir := range redirs {
		if redir == nil {
			continue
		}
		body, ok := heredocScriptWord(redir, s.expander())
		if !ok {
			continue
		}
		if body.dynamic {
			return newWorkspaceError("dynamic shell script cannot be validated under workspace restriction")
		}
		if err := checkWorkspaceScript(body.text, s.nestedPolicy(), depth+1); err != nil {
			return err
		}
	}
	return nil
}

// checkEnvLaunch rejects env options the static pass cannot follow: a split
// string, a changed directory, a cleared or altered path environment.
func checkEnvLaunch(opts *envArgs) error {
	switch {
	case opts.splitString:
		return newWorkspaceError("env -S cannot be validated under workspace restriction")
	case opts.dynamic:
		return newWorkspaceError("dynamic command cannot be validated under workspace restriction")
	case opts.hasChdir:
		return newWorkspaceError("env -C cannot be validated under workspace restriction")
	case opts.ignoreEnv || opts.unsetDynamic:
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
	return nil
}
