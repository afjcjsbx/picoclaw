package shellguard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const (
	// maxShellNesting bounds recursive analysis of sh -c scripts and wrapper
	// commands. Real commands nest one or two levels; the limit prevents hostile
	// input from turning the guard into an unbounded parser recursion.
	maxShellNesting = 8

	// maxScriptFileBytes bounds a single inspected script file. The file is read
	// once into memory for the parser, so a hard cap keeps the guard from turning
	// a large script into an unbounded allocation on low-memory hardware.
	maxScriptFileBytes = 1 << 20 // 1 MiB

	// maxScriptInspectBytes bounds the total bytes read for nested script files
	// during one Check call. Scripts stay live on the stack while their nested
	// scripts are checked, so this caps peak memory independently of nesting.
	maxScriptInspectBytes = 2 << 20 // 2 MiB
)

// CheckOptions supplies the execution context needed to inspect shell script
// files. Without a Root, a script file stays opaque and is rejected, which is
// the historical fail-closed behavior.
type CheckOptions struct {
	// Dir resolves relative script paths. It is the directory the command runs
	// in, not necessarily the workspace root.
	Dir string
	// Root is the workspace root. A script whose resolved path is outside it is
	// rejected instead of read, so the guard never reads arbitrary paths.
	Root string
}

// checkEnv is the per-Check mutable inspection context. Dir and Root are
// immutable; remaining tracks the script-byte budget shared by nested calls.
type checkEnv struct {
	dir       string
	root      string
	remaining int
}

// Guard classifies commands against a configured risk threshold.
type Guard struct {
	threshold RiskLevel
	overrides map[string]string
	modifiers map[string][]ArgModifier
}

// NewGuard builds a Guard. The threshold defaults to "medium" when empty.
// Overrides and user modifiers are validated so a typo fails configuration
// loading instead of silently disabling the guard.
func NewGuard(
	threshold string,
	overrides map[string]string,
	modifiers map[string][]ArgModifier,
) (*Guard, error) {
	level, err := ParseRiskLevel(threshold)
	if err != nil {
		return nil, err
	}

	normalizedOverrides := NormalizeCommandKeys(overrides)
	for cmd, raw := range normalizedOverrides {
		if _, err := ParseRiskLevel(raw); err != nil {
			return nil, fmt.Errorf("invalid risk override for %q: %w", cmd, err)
		}
	}

	normalizedModifiers := make(map[string][]ArgModifier, len(modifiers))
	for cmd, entries := range modifiers {
		key := baseCommand(cmd)
		for _, entry := range entries {
			if entry.Level < RiskLow || entry.Level > RiskCritical {
				return nil, fmt.Errorf("invalid risk modifier level for %q", cmd)
			}
			if len(entry.Args) == 0 {
				return nil, fmt.Errorf("risk modifier for %q must list at least one argument", cmd)
			}
			normalizedModifiers[key] = append(normalizedModifiers[key], entry)
		}
	}

	return &Guard{
		threshold: level,
		overrides: normalizedOverrides,
		modifiers: normalizedModifiers,
	}, nil
}

// Check parses command and returns a BlockedError when any simple command in it
// exceeds the configured threshold. A nil result means the command may run.
// It is CheckWithOptions without a script inspection context, so `bash file.sh`
// stays opaque and is rejected.
func (g *Guard) Check(command string) *BlockedError {
	return g.CheckWithOptions(command, CheckOptions{})
}

// CheckWithOptions is Check with the directory and workspace root used to
// resolve and inspect script files named on the command line.
func (g *Guard) CheckWithOptions(command string, opts CheckOptions) *BlockedError {
	if g == nil {
		return nil
	}
	env := &checkEnv{dir: opts.Dir, root: opts.Root, remaining: maxScriptInspectBytes}
	return g.checkScript(command, env, 0)
}

func (g *Guard) checkScript(command string, env *checkEnv, depth int) *BlockedError {
	return g.checkScriptReader(strings.NewReader(command), summarize(command), env, depth)
}

// checkScriptReader parses and walks a script from reader. summary names the
// source in a parse error; for a file it is the path, so file contents are not
// copied just to build an error message.
func (g *Guard) checkScriptReader(
	reader io.Reader,
	summary string,
	env *checkEnv,
	depth int,
) *BlockedError {
	if depth > maxShellNesting {
		return NewBlockedError(
			[]string{"<nested shell>"}, RiskCritical, g.threshold, "shell nesting limit exceeded",
		)
	}

	parser := syntax.NewParser(syntax.Variant(syntax.LangBash))
	file, err := parser.Parse(reader, "")
	if err != nil {
		return NewBlockedError(
			[]string{summary},
			RiskCritical,
			g.threshold,
			"command could not be parsed for risk classification",
		)
	}

	var blocked *BlockedError
	syntax.Walk(file, func(node syntax.Node) bool {
		if blocked != nil {
			return false
		}
		if node == nil {
			return true
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		blocked = g.checkCall(call, env, depth)
		return blocked == nil
	})
	return blocked
}

func (g *Guard) checkCall(call *syntax.CallExpr, env *checkEnv, depth int) *BlockedError {
	if len(call.Args) == 0 {
		return nil
	}

	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}

	first := words[0]
	if !first.literal || first.text == "" {
		return NewBlockedError(
			[]string{"<dynamic>"}, RiskCritical, g.threshold,
			"dynamic command name cannot be classified",
		)
	}

	name := baseCommand(first.text)
	if isShellCommand(name) {
		return g.checkShell(name, words, env, depth)
	}
	if name == "trap" {
		return g.checkTrap(words, env, depth)
	}
	if spec, ok := wrapperSpecs[name]; ok {
		return g.checkWrapper(name, words, spec, env, depth)
	}
	return g.classify(words)
}

func (g *Guard) classify(words []word) *BlockedError {
	args := wordTexts(words)
	level := ClassifyCommand(args, g.overrides, g.modifiers)
	if IsAllowed(level, g.threshold) {
		return nil
	}
	return NewBlockedError(args, level, g.threshold, "command risk exceeds configured threshold")
}

func (g *Guard) checkShell(name string, words []word, env *checkEnv, depth int) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "shell nesting limit exceeded",
		)
	}

	script, input := shellScriptArg(words[1:])
	switch input {
	case shellInputCommand:
		if !script.literal {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold, "shell script argument is dynamic",
			)
		}
		return g.checkScript(script.text, env, depth+1)
	case shellInputFile:
		if !script.literal {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold, "shell script path is dynamic",
			)
		}
		return g.checkScriptFile(script.text, words, env, depth+1)
	default:
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold,
			"shell reads commands from stdin or a script file that cannot be inspected",
		)
	}
}

func (g *Guard) checkTrap(words []word, env *checkEnv, depth int) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "command nesting limit exceeded",
		)
	}

	for i := 1; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold, "trap action is dynamic",
			)
		}
		switch w.text {
		case "-l", "-p":
			return nil
		case "--":
			continue
		case "", "-":
			return nil
		}
		if strings.HasPrefix(w.text, "-") {
			continue
		}
		return g.checkScript(w.text, env, depth+1)
	}
	return nil
}

func (g *Guard) checkWrapper(
	name string,
	words []word,
	spec wrapperSpec,
	env *checkEnv,
	depth int,
) *BlockedError {
	if depth >= maxShellNesting {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "command nesting limit exceeded",
		)
	}
	if name == "command" && hasCommandLookupFlag(words[1:]) {
		return nil
	}
	if name == "env" && hasEnvSplitString(words[1:]) {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold,
			"env -S executes a command string that cannot be inspected",
		)
	}
	if blocked := g.classify(words); blocked != nil {
		return blocked
	}

	// capsh runs /bin/bash for everything after `--`, so its arguments are a
	// shell invocation rather than an executable name.
	if spec.shellAfterDoubleDash {
		if rest, ok := argsAfterDoubleDash(words[1:]); ok {
			bash := make([]word, 0, len(rest)+1)
			bash = append(bash, word{text: "bash", literal: true})
			bash = append(bash, rest...)
			return g.checkShell("bash", bash, env, depth+1)
		}
	}

	// Wrappers such as runuser -c and flock -c take a shell command string.
	// Inspect it as a script instead of treating its first token as argv[0].
	if script, found, dynamic := commandFlagValue(words[1:], spec.commandFlags); dynamic {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "dynamic command string in "+name,
		)
	} else if found {
		if !script.literal {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold, "dynamic command string in "+name,
			)
		}
		return g.checkScript(script.text, env, depth+1)
	}

	nested, found, dynamic := wrapperNested(words[1:], spec)
	if dynamic {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "dynamic command name in "+name,
		)
	}
	if !found || len(nested) == 0 {
		return nil
	}

	nestedName := ""
	if nested[0].literal {
		nestedName = baseCommand(nested[0].text)
	}
	if nestedName == "" {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold, "dynamic command name in "+name,
		)
	}
	if isShellCommand(nestedName) {
		if name == "xargs" {
			return NewBlockedError(
				wordTexts(words), RiskCritical, g.threshold,
				"xargs launching a shell cannot be inspected",
			)
		}
		return g.checkShell(nestedName, nested, env, depth+1)
	}
	if nestedName == "trap" {
		return g.checkTrap(nested, env, depth+1)
	}
	if next, ok := wrapperSpecs[nestedName]; ok {
		return g.checkWrapper(nestedName, nested, next, env, depth+1)
	}
	return g.classify(nested)
}

// word is a shell word reduced to its literal parts. literal is false when the
// word contains an expansion, substitution or glob, which the static guard
// cannot resolve.
type word struct {
	text    string
	literal bool
}

func literalWord(w *syntax.Word) word {
	var b strings.Builder
	literal := true

	var addParts func([]syntax.WordPart)
	addParts = func(parts []syntax.WordPart) {
		for _, part := range parts {
			switch p := part.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
				// $'...' is an ANSI-C quoted string: the parser keeps escapes
				// such as \x72 undecoded, so treating it as literal would let
				// $'\x72m' -rf / masquerade as an unknown command.
				if p.Dollar {
					literal = false
				}
			case *syntax.DblQuoted:
				addParts(p.Parts)
			default:
				literal = false
			}
		}
	}
	addParts(w.Parts)
	return word{text: b.String(), literal: literal}
}

func wordTexts(words []word) []string {
	args := make([]string, 0, len(words))
	for _, w := range words {
		if w.text != "" {
			args = append(args, w.text)
		}
	}
	return args
}

func summarize(s string) string {
	const maxLen = 120
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

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

// shellInput describes how a shell invocation supplies its commands.
type shellInput int

const (
	// shellInputOpaque is stdin or a form the guard cannot inspect.
	shellInputOpaque shellInput = iota
	// shellInputCommand is a literal -c / --command script string.
	shellInputCommand
	// shellInputFile is a script path passed as a positional argument.
	shellInputFile
)

// shellScriptArg extracts the literal -c script or the script file path from a
// shell invocation. It returns shellInputOpaque when the shell would read
// commands from stdin, because those commands cannot be inspected statically.
func shellScriptArg(words []word) (word, shellInput) {
	for i := 0; i < len(words); i++ {
		w := words[i]
		if !w.literal {
			return word{}, shellInputOpaque
		}
		text := w.text
		switch {
		case text == "--":
			if i+1 < len(words) {
				return words[i+1], shellInputFile
			}
			return word{}, shellInputOpaque
		case text == "--command":
			if i+1 < len(words) {
				return words[i+1], shellInputCommand
			}
			return word{}, shellInputOpaque
		case strings.HasPrefix(text, "--command="):
			return word{text: strings.TrimPrefix(text, "--command="), literal: w.literal}, shellInputCommand
		case shellValueFlags[text]:
			i++
		case text == "-c":
			if i+1 < len(words) {
				return words[i+1], shellInputCommand
			}
			return word{}, shellInputOpaque
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--"):
			if strings.Contains(text[1:], "c") {
				if i+1 < len(words) {
					return words[i+1], shellInputCommand
				}
				return word{}, shellInputOpaque
			}
			if strings.ContainsAny(text[1:], "si") {
				return word{}, shellInputOpaque
			}
		case !strings.HasPrefix(text, "-"):
			return w, shellInputFile
		}
	}
	return word{}, shellInputOpaque
}

var errScriptOutsideWorkspace = errors.New("script file is outside the workspace")

// checkScriptFile reads a script named on the command line and classifies it.
// The file must resolve inside the workspace root; anything else stays opaque
// and is rejected, so a command cannot point the guard at /etc or a symlink
// that escapes the workspace.
func (g *Guard) checkScriptFile(
	path string,
	words []word,
	env *checkEnv,
	depth int,
) *BlockedError {
	script, err := readWorkspaceScript(path, env)
	if err != nil {
		return NewBlockedError(
			wordTexts(words), RiskCritical, g.threshold,
			"shell script cannot be inspected: "+err.Error(),
		)
	}
	return g.checkScriptReader(bytes.NewReader(script), summarize(path), env, depth)
}

// readWorkspaceScript resolves path against Dir, verifies it stays inside Root
// and reads it with a bounded allocation. The returned bytes are the only copy;
// the parser reads them in place, so no string copy is made.
func readWorkspaceScript(path string, env *checkEnv) ([]byte, error) {
	if path == "" {
		return nil, errors.New("empty script path")
	}
	if env.root == "" {
		return nil, errors.New("workspace root is not configured")
	}

	root, err := filepath.Abs(env.root)
	if err != nil {
		return nil, err
	}
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}

	dir := env.dir
	if dir == "" {
		dir = root
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	candidate, err = filepath.Abs(candidate)
	if err != nil {
		return nil, err
	}

	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", path, err)
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, errScriptOutsideWorkspace
	}

	return readScriptFile(root, rel, env)
}

// readScriptFile reads a regular file up to maxScriptFileBytes. The stat size
// seeds the buffer capacity so a typical script needs one allocation; the
// shared remaining budget bounds peak memory across nested scripts.
//
// The file is opened through os.Root, so a symlink swapped in after the
// EvalSymlinks check above cannot redirect the read outside the workspace.
func readScriptFile(root, rel string, env *checkEnv) ([]byte, error) {
	if env.remaining <= 0 {
		return nil, errors.New("script inspection budget exhausted")
	}

	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer rootFS.Close()

	// Stat before opening so a FIFO or device node named like a script is
	// rejected without an open that could block waiting for a writer.
	info, err := rootFS.Stat(rel)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > maxScriptFileBytes {
		return nil, fmt.Errorf("script exceeds %d bytes", maxScriptFileBytes)
	}

	f, err := rootFS.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Re-check the opened descriptor: the path could be swapped between Stat
	// and Open, and os.Root permits device files inside the tree.
	info, err = f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if info.Size() > maxScriptFileBytes {
		return nil, fmt.Errorf("script exceeds %d bytes", maxScriptFileBytes)
	}

	// Read at most one byte past the per-file and per-check limits so an
	// oversized file is detected without ever loading it in full.
	limit := int64(maxScriptFileBytes) + 1
	if budget := int64(env.remaining) + 1; budget < limit {
		limit = budget
	}
	buf := bytes.NewBuffer(make([]byte, 0, int(info.Size())))
	if _, err := buf.ReadFrom(io.LimitReader(f, limit)); err != nil {
		return nil, err
	}
	if buf.Len() > maxScriptFileBytes {
		return nil, fmt.Errorf("script exceeds %d bytes", maxScriptFileBytes)
	}
	if buf.Len() > env.remaining {
		return nil, errors.New("script inspection budget exhausted")
	}
	env.remaining -= buf.Len()

	return buf.Bytes(), nil
}

type wrapperSpec struct {
	valueFlags     map[string]bool
	commandFlags   map[string]bool
	skipPositional int
	envAssignments bool
	// shellAfterDoubleDash marks wrappers that execute /bin/bash with the
	// arguments after `--` (capsh). Those arguments are parsed as a script.
	shellAfterDoubleDash bool
}

// containerCLISpec is shared by lxc and incus: both take a subcommand, a
// container name and then the command to run inside the container.
var containerCLISpec = wrapperSpec{
	valueFlags: map[string]bool{
		"--project": true, "-P": true, "--remote": true,
		"--env": true, "--cwd": true, "--user": true,
		"--group": true, "--mode": true, "--width": true,
		"--height": true,
	},
	skipPositional: 2,
}

var wrapperSpecs = map[string]wrapperSpec{
	"env": {
		valueFlags: map[string]bool{
			"-u": true, "-C": true, "-S": true,
			"--unset": true, "--chdir": true, "--split-string": true,
		},
		envAssignments: true,
	},
	"command": {},
	"builtin": {},
	"nohup":   {},
	"time": {
		valueFlags: map[string]bool{
			"-o": true, "--output": true, "-f": true, "--format": true,
		},
	},
	"nice": {
		valueFlags: map[string]bool{"-n": true, "--adjustment": true},
	},
	"ionice": {
		valueFlags: map[string]bool{
			"-c": true, "-n": true, "-p": true,
			"--class": true, "--classdata": true, "--pid": true,
		},
	},
	"stdbuf": {
		valueFlags: map[string]bool{
			"-i": true, "-o": true, "-e": true,
			"--input": true, "--output": true, "--error": true,
		},
	},
	"timeout": {
		valueFlags: map[string]bool{
			"-s": true, "-k": true, "--signal": true, "--kill-after": true,
		},
		skipPositional: 1,
	},
	"watch": {
		valueFlags: map[string]bool{"-n": true, "--interval": true},
	},
	"setsid": {},
	"xvfb-run": {
		valueFlags: map[string]bool{
			"-e": true, "-n": true, "-s": true,
			"--error-file": true, "--server-num": true, "--server-args": true,
		},
	},
	"xargs": {
		valueFlags: map[string]bool{
			"-a": true, "-d": true, "-E": true, "-e": true, "-I": true,
			"-i": true, "-L": true, "-l": true, "-n": true, "-P": true,
			"-s": true, "--arg-file": true, "--delimiter": true,
			"--max-args": true, "--max-procs": true, "--max-chars": true,
			"--replace": true,
		},
	},
	"runuser": {
		valueFlags: map[string]bool{
			"-u": true, "--user": true, "-g": true, "--group": true,
			"-G": true, "--supp-group": true, "-s": true, "--shell": true,
			"-w": true, "--whitelist-environment": true,
		},
		commandFlags: map[string]bool{
			"-c": true, "--command": true, "--session-command": true,
		},
	},
	"setpriv": {
		valueFlags: map[string]bool{
			"--reuid": true, "--regid": true, "--groups": true,
			"--inh-caps": true, "--ambient-caps": true,
			"--bounding-set": true, "--securebits": true,
			"--selinux-label": true, "--apparmor-profile": true,
			"--landlock-access": true, "--landlock-rule": true,
		},
	},
	"capsh": {
		valueFlags: map[string]bool{
			"--user": true, "--group": true, "--uid": true, "--gid": true,
			"--shell": true, "--keep": true, "--caps": true, "--inh": true,
			"--addamb": true, "--delamb": true, "--drop": true,
			"--chroot": true, "--securebits": true,
		},
		shellAfterDoubleDash: true,
	},
	"machinectl": {
		valueFlags: map[string]bool{
			"-M": true, "--machine": true, "--uid": true, "--host": true,
			"-H": true, "-i": true, "--identity": true, "-p": true,
			"--property": true, "-n": true, "--lines": true, "-o": true,
			"--output": true, "-s": true, "--signal": true,
			"--kill-whom": true,
		},
		skipPositional: 2,
	},
	"lxc":     containerCLISpec,
	"incus":   containerCLISpec,
	"busybox": {},
	"flock": {
		valueFlags: map[string]bool{
			"-w": true, "--wait": true, "--timeout": true,
			"-E": true, "--conflict-exit-code": true,
		},
		commandFlags:   map[string]bool{"-c": true, "--command": true},
		skipPositional: 1,
	},
	"chrt": {
		valueFlags: map[string]bool{
			"-T": true, "--sched-runtime": true,
			"-P": true, "--sched-period": true,
			"-D": true, "--sched-deadline": true,
		},
		skipPositional: 1,
	},
	"taskset": {skipPositional: 1},
}

func wrapperNested(words []word, spec wrapperSpec) ([]word, bool, bool) {
	positional := spec.skipPositional

	for i := 0; i < len(words); i++ {
		w := words[i]
		if spec.envAssignments && isAssignment(w.text) {
			continue
		}
		if !w.literal {
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

func isAssignment(s string) bool {
	eq := strings.IndexByte(s, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := s[i]
		switch {
		case c == '_':
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// argsAfterDoubleDash returns the arguments after the first literal "--".
func argsAfterDoubleDash(words []word) ([]word, bool) {
	for i, w := range words {
		if w.literal && w.text == "--" {
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
		if !w.literal {
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
			return word{text: value, literal: true}, true, false
		}
		if strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") && len(text) > 2 {
			for flag := range flags {
				if len(flag) == 2 && strings.HasPrefix(text, flag) {
					return word{text: text[2:], literal: true}, true, false
				}
			}
		}
	}
	return word{}, false, false
}

// hasCommandLookupFlag reports whether command(1) was invoked with -v/-V,
// including combined forms such as -pv. Lookup mode does not execute the
// argument, so it is not a guard concern.
func hasCommandLookupFlag(words []word) bool {
	for _, w := range words {
		if !w.literal {
			continue
		}
		text := w.text
		if strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") &&
			strings.ContainsAny(text[1:], "vV") {
			return true
		}
	}
	return false
}

// hasEnvSplitString reports whether an env invocation uses -S/--split-string.
// The attached form (-S"cmd") does not match the exact -S argument modifier,
// so it is checked here before the argv reaches the classifier.
func hasEnvSplitString(words []word) bool {
	for _, w := range words {
		if isAssignment(w.text) {
			continue
		}
		if !w.literal {
			return false
		}
		text := w.text
		if text == "--" || text == "-" {
			return false
		}
		if !strings.HasPrefix(text, "-") {
			return false
		}
		if text == "--split-string" || strings.HasPrefix(text, "--split-string=") {
			return true
		}
		if !strings.HasPrefix(text, "--") && shortEnvSplitFlag(text) {
			return true
		}
	}
	return false
}

// shortEnvSplitFlag scans an env short-option cluster. -u and -C consume the
// rest of the word as their value, so -uS unsets S rather than enabling -S.
func shortEnvSplitFlag(text string) bool {
	for i := 1; i < len(text); i++ {
		switch text[i] {
		case 'u', 'C':
			return false
		case 'S':
			return true
		}
	}
	return false
}
