package shellguard

import "strings"

// launch describes what a command starts besides itself. The risk, canonical
// and workspace passes all need to know where the nested code sits in a
// launcher's argv (sh -c, eval, find -exec, timeout, runuser -c, ...); they
// share this one description and only decide what to do with it.
type launch struct {
	// name is the base name of the command.
	name string
	// dynamicName reports that argv[0] cannot be resolved statically.
	dynamicName bool
	// scripts are shell texts the command parses and runs: sh -c, eval, trap,
	// runuser -c, env -S. A dynamic script cannot be read.
	scripts []word
	// argvs are commands the launcher execs directly: find -exec, exec and the
	// target of a wrapper such as env, timeout or xargs.
	argvs [][]word
	// opaque is set when the command line names code that cannot be resolved,
	// for example a dynamic option before a wrapper's target.
	opaque string
	// bareShell marks a shell without -c: it runs a script file or stdin.
	// readsStdin reports the stdin case, where a heredoc on the statement is
	// the script.
	bareShell  bool
	readsStdin bool
	// transparent marks commands judged only by what they launch, so their own
	// risk is not added: shells, trap and command -v lookups.
	transparent bool
	// env holds the parsed options when the command is env.
	env *envArgs
}

// resolveLaunch reports what words launch. Callers check dynamicName first.
func resolveLaunch(words []word) launch {
	if len(words) == 0 || words[0].dynamic || words[0].text == "" {
		return launch{dynamicName: true}
	}
	l := launch{name: baseCommand(words[0].text)}
	args := words[1:]

	switch {
	case isShellCommand(l.name):
		l.transparent = true
		invocation := parseShellInvocation(args)
		if invocation.hasScript {
			l.scripts = []word{invocation.script}
		} else {
			l.bareShell = true
			l.readsStdin = invocation.readsStdin
		}
	case l.name == "trap":
		l.transparent = true
		action, found, dynamic := trapAction(args)
		switch {
		case dynamic:
			l.scripts = []word{{dynamic: true}}
		case found:
			l.scripts = []word{action}
		}
	case l.name == "eval":
		// eval joins its literal arguments and runs them as shell input. A
		// dynamic argument leaves nothing to read; eval's own risk still applies.
		if script, ok := joinLiteralWords(args); ok && script != "" {
			l.scripts = []word{{text: script}}
		}
	case l.name == "find":
		l.argvs = findExecCommands(args)
	case l.name == "exec":
		if nested, ok := execCommand(args); ok {
			l.argvs = [][]word{nested}
		}
	default:
		if spec, ok := wrapperSpecs[l.name]; ok {
			resolveWrapper(&l, args, spec)
		}
	}
	return l
}

func resolveWrapper(l *launch, args []word, spec wrapperSpec) {
	if l.name == "command" && hasCommandLookupFlag(args) {
		// command -v only looks the name up; nothing runs.
		l.transparent = true
		return
	}

	if spec.envArgs {
		opts := parseEnvArgs(args)
		l.env = &opts
		switch {
		case opts.splitString:
			// The split string is read by canonical forms, but the pass that
			// judges the command treats it as opaque: env splits it into argv
			// words instead of parsing it as a script.
			l.opaque = "env -S executes a command string that cannot be inspected"
			l.scripts = []word{opts.splitValue}
		case opts.dynamic:
			l.opaque = "dynamic command name in env"
		case len(opts.command) > 0:
			l.argvs = [][]word{opts.command}
		}
		return
	}

	// capsh runs /bin/bash for everything after `--`, so its arguments are a
	// shell invocation rather than an executable name.
	if spec.shellAfterDoubleDash {
		if rest, ok := argsAfterDoubleDash(args); ok {
			bash := append([]word{{text: "bash"}}, rest...)
			l.argvs = [][]word{bash}
			return
		}
	}

	// Wrappers such as runuser -c and flock -c take a shell command string.
	// It is a script, not an argv[0].
	script, found, dynamic := commandFlagValue(args, spec.commandFlags)
	if dynamic {
		l.opaque = "dynamic command string in " + l.name
		return
	}
	if found {
		l.scripts = []word{script}
		return
	}

	nested, found, dynamic := wrapperNested(args, spec)
	switch {
	case dynamic:
		l.opaque = "dynamic command name in " + l.name
	case found && len(nested) > 0:
		l.argvs = [][]word{nested}
	}
}

// canNest reports whether code launched at depth may itself be analyzed.
// maxShellNesting is the deepest level analyzed.
func canNest(depth int) bool {
	return depth < maxShellNesting
}

// findExecCommands returns the argument vectors launched by find -exec,
// -execdir, -ok and -okdir.
func findExecCommands(words []word) [][]word {
	var commands [][]word
	for i := 0; i < len(words); i++ {
		switch words[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(words) && words[end].text != ";" && words[end].text != "+" {
				end++
			}
			if end > i+1 {
				commands = append(commands, words[i+1:end])
			}
			i = end
		}
	}
	return commands
}

// trapAction extracts the literal action from a trap argument vector. found is
// false for listing and reset forms; dynamic reports that the action or an
// option could not be resolved.
func trapAction(args []word) (action word, found, dynamic bool) {
	for _, w := range args {
		if w.dynamic {
			return word{}, false, true
		}
		switch w.text {
		case "-l", "-p":
			return word{}, false, false
		case "--":
			continue
		case "", "-":
			return word{}, false, false
		}
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return w, true, false
	}
	return word{}, false, false
}

// execCommand returns the argv that exec replaces the shell with, skipping its
// own options.
func execCommand(args []word) ([]word, bool) {
	i := 0
	for i < len(args) {
		text := args[i].text
		switch {
		case text == "--":
			i++
		case text == "-a":
			i += 2
			continue
		case strings.HasPrefix(text, "-a") && len(text) > 2:
			i++
			continue
		case text == "-c" || text == "-l":
			i++
			continue
		case strings.HasPrefix(text, "-") && text != "-":
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil, false
	}
	return args[i:], true
}

func wrapperNested(words []word, spec wrapperSpec) ([]word, bool, bool) {
	positional := spec.skipPositional
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.dynamic {
			return nil, false, true
		}

		text := w.text
		if text == "--" {
			// For wrappers that consume positional arguments (timeout, flock,
			// lxc exec), "--" only ends option parsing: the command still comes
			// after those positionals. Returning here would treat the next
			// positional (a duration, lock file or container) as the command.
			if positional > 0 {
				continue
			}
			if i+1 < len(words) {
				return words[i+1:], true, false
			}
			return nil, false, false
		}
		if strings.HasPrefix(text, "-") && text != "-" {
			if spec.valueFlags[text] {
				i++
				continue
			}
			if name, _, ok := strings.Cut(text, "="); ok && spec.valueFlags[name] {
				continue
			}
			if len(text) > 2 && text[1] != '-' && spec.valueFlags[text[:2]] {
				continue
			}
			continue
		}
		if positional > 0 {
			positional--
			continue
		}
		return words[i:], true, false
	}
	return nil, false, false
}

// argsAfterDoubleDash returns the arguments after the first literal "--".
func argsAfterDoubleDash(words []word) ([]word, bool) {
	for i, w := range words {
		if !w.dynamic && w.text == "--" {
			return words[i+1:], true
		}
	}
	return nil, false
}

// commandFlagValue finds a flag whose value is a shell command string (-c for
// runuser, flock and similar wrappers). The third result reports a dynamic word
// before the flag: the effective flag cannot be resolved, so the caller must
// stay fail-closed. After "--" the value is a positional argument, not a flag.
func commandFlagValue(words []word, flags map[string]bool) (word, bool, bool) {
	if len(flags) == 0 {
		return word{}, false, false
	}
	for i := 0; i < len(words); i++ {
		w := words[i]
		if w.text == "--" {
			return word{}, false, false
		}
		if w.dynamic {
			return word{}, false, true
		}
		text := w.text
		if flags[text] {
			if i+1 < len(words) {
				return words[i+1], true, false
			}
			return word{}, false, false
		}
		if name, value, ok := strings.Cut(text, "="); ok && flags[name] {
			return word{text: value}, true, false
		}
		if strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && len(text) > 2 {
			for flag := range flags {
				if len(flag) == 2 && strings.HasPrefix(text, flag) {
					return word{text: text[2:]}, true, false
				}
			}
		}
	}
	return word{}, false, false
}

// hasCommandLookupFlag reports whether command(1) was invoked with -v/-V,
// including combined forms such as -pv. Lookup mode does not execute the
// argument, so it is not a guard concern.
//
// Only the options that precede command's own argument count: a -v after the
// command name belongs to the nested command (`command rm -v`), not to
// command(1).
func hasCommandLookupFlag(words []word) bool {
	for _, w := range words {
		if w.dynamic {
			return false
		}
		text := w.text
		if text == "--" || text == "-" || !strings.HasPrefix(text, "-") {
			return false
		}
		if !strings.HasPrefix(text, "--") && strings.ContainsAny(text[1:], "vV") {
			return true
		}
	}
	return false
}

func joinLiteralWords(words []word) (string, bool) {
	texts := make([]string, 0, len(words))
	for _, w := range words {
		if w.dynamic {
			return "", false
		}
		texts = append(texts, w.text)
	}
	return strings.Join(texts, " "), true
}
