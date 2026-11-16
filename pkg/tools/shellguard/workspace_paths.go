package shellguard

import (
	"path/filepath"
	"strings"
)

func (s *workspaceState) checkPathFlags(args []word) error {
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "-C" || text == "--directory" || text == "--chdir":
			if i+1 < len(args) {
				if err := s.checkPathFlagValue(args[i+1]); err != nil {
					return err
				}
				i++
			}
		case strings.HasPrefix(text, "-C") && len(text) > 2:
			if err := s.checkPathFlagValue(word{
				text:    text[2:],
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		case strings.HasPrefix(text, "--directory="):
			if err := s.checkPathFlagValue(word{
				text:    strings.TrimPrefix(text, "--directory="),
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		case strings.HasPrefix(text, "--chdir="):
			if err := s.checkPathFlagValue(word{
				text:    strings.TrimPrefix(text, "--chdir="),
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *workspaceState) checkPathFlagValue(value word) error {
	if value.dynamic || value.homePath {
		return nil
	}
	if !isWorkspaceURL(value.text) && hasParentComponent(value.text) {
		return newWorkspaceError("path traversal detected")
	}
	return s.checkPath(value.text)
}

// checkSymlinkSource rejects `ln -s .. name`, which creates an outside-pointing
// symlink without any absolute path or slash traversal. Every non-flag operand
// is checked because ln accepts multiple sources plus a link name or target
// directory. A later cd or file operation through such a link would otherwise
// leave the workspace.
func (s *workspaceState) checkSymlinkSource(args []word) error {
	if len(args) == 0 || baseCommand(args[0].text) != "ln" {
		return nil
	}
	for i := 1; i < len(args); i++ {
		text := args[i].text
		if text == "--" {
			continue
		}
		if strings.HasPrefix(text, "-") && text != "-" {
			continue
		}
		if !isWorkspaceURL(args[i].text) && hasParentComponent(args[i].text) {
			return newWorkspaceError("path traversal detected")
		}
		if args[i].dynamic {
			continue
		}
		if err := s.checkPath(args[i].text); err != nil {
			return err
		}
	}
	return nil
}

// checkDirChange validates a directory-changing command. track is true only
// for the shell's own builtins in the current script; wrapper recursion and
// child processes pass false so their cd cannot move the outer shell.
func (s *workspaceState) checkDirChange(args []word, track bool) error {
	args = unwrapExecPrefix(args)
	if len(args) == 0 {
		return nil
	}
	switch baseCommand(args[0].text) {
	case "cd":
		return s.checkCD(args[1:], track)
	case "pushd":
		return s.checkPushd(args[1:], track)
	case "env":
		return s.checkEnvChdir(args[1:])
	}
	return nil
}

// unwrapExecPrefix strips command(1) and builtin(1) wrappers so `command cd`
// and `builtin cd` are validated like a direct cd. A command lookup (-v/-V)
// does not execute the argument and yields no executable command.
func unwrapExecPrefix(args []word) []word {
	for len(args) > 0 {
		name := baseCommand(args[0].text)
		if name != "command" && name != "builtin" {
			return args
		}
		if name == "command" && hasCommandLookupFlag(args[1:]) {
			return nil
		}
		i := 1
		for i < len(args) && strings.HasPrefix(args[i].text, "-") && args[i].text != "-" {
			i++
		}
		args = args[i:]
	}
	return args
}

func (s *workspaceState) checkCD(args []word, track bool) error {
	target, ok := cdTarget(args)
	if !ok {
		home, resolved := s.effectiveHome()
		if !resolved {
			return newWorkspaceError("directory change cannot be validated under workspace restriction")
		}
		if err := s.checkPath(home); err != nil {
			return err
		}
		if track {
			s.addCwd(home)
		}
		return nil
	}
	return s.checkDirTarget(target, track)
}

func cdTarget(args []word) (word, bool) {
	i := 0
	for i < len(args) {
		text := args[i].text
		if text == "--" {
			i++
			break
		}
		if text == "-" {
			break
		}
		if strings.HasPrefix(text, "-") && text != "" {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return word{}, false
	}
	return args[i], true
}

func (s *workspaceState) checkPushd(args []word, track bool) error {
	i := 0
	for i < len(args) {
		text := args[i].text
		if text == "--" {
			i++
			break
		}
		if text == "-n" {
			i++
			continue
		}
		if isStackRotation(text) {
			// The directory stack only contains entries that were validated
			// when they were pushed; rotating it stays inside the workspace.
			return nil
		}
		if strings.HasPrefix(text, "-") && text != "-" && text != "" {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil
	}
	return s.checkDirTarget(args[i], track)
}

func (s *workspaceState) checkEnvChdir(args []word) error {
	opts := parseEnvArgs(args)
	if opts.splitString {
		return newWorkspaceError("env -S cannot be validated under workspace restriction")
	}
	if opts.dynamic {
		return newWorkspaceError("directory change cannot be validated under workspace restriction")
	}
	for _, assignment := range opts.assignments {
		name, _, _ := strings.Cut(assignment.text, "=")
		switch name {
		case "HOME", "PWD", "OLDPWD", "CDPATH":
			return newWorkspaceError(
				"path environment override cannot be validated under workspace restriction",
			)
		}
	}
	if opts.ignoreEnv {
		s.homeChanged = true
	}
	for _, name := range opts.unset {
		if name == "HOME" {
			s.homeChanged = true
		}
	}
	if opts.unsetDynamic {
		s.homeChanged = true
		s.pwdOverridden = true
	}
	if opts.hasChdir {
		return s.checkDirTarget(word{
			text:    opts.chdir.text,
			dynamic: opts.chdir.dynamic,
		}, false)
	}
	return nil
}

func (s *workspaceState) checkDirTarget(target word, track bool) error {
	if target.dynamic || target.text == "" || target.text == "-" {
		return newWorkspaceError("directory change cannot be validated under workspace restriction")
	}
	if !isWorkspaceURL(target.text) && hasParentComponent(target.text) {
		return newWorkspaceError("path traversal detected")
	}
	if s.cdpathSet && !filepath.IsAbs(target.text) &&
		!strings.HasPrefix(target.text, "./") &&
		!strings.HasPrefix(target.text, "../") && target.text != "." {
		return newWorkspaceError("directory change cannot be validated under workspace restriction")
	}
	if err := s.checkPath(target.text); err != nil {
		return err
	}
	if track {
		s.addCwd(s.resolveCwd(target.text))
	}
	return nil
}

func isStackRotation(text string) bool {
	if len(text) < 2 || (text[0] != '+' && text[0] != '-') {
		return false
	}
	for i := 1; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}
