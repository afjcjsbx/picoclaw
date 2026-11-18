package shellguard

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// HardlineFinding is one deterministic, non-overridable command rejection.
// The rule name is stable so tests and audit logs do not depend on user-facing
// wording.
type HardlineFinding struct {
	Rule   string
	Reason string
}

// HardlineFile evaluates the hardline floor against a parsed command. Unlike
// the configurable deny patterns, these rules cannot be disabled, exempted or
// delegated to a model. The AST is used so quoting, wrappers and path
// normalization cannot hide the operation from a regex.
//
// A nil file is not a finding: the caller decides how an unparseable command is
// handled (the strict policy reviews it and blocks it without an approver).
func HardlineFile(file *syntax.File, cwd, home string) (HardlineFinding, bool) {
	if file == nil {
		return HardlineFinding{}, false
	}
	finder := hardlineFinder{cwd: cwd, home: home}
	syntax.Walk(file, func(node syntax.Node) bool {
		if finder.found {
			return false
		}
		switch n := node.(type) {
		case *syntax.FuncDecl:
			finder.checkForkBomb(n)
		case *syntax.CallExpr:
			finder.checkCall(n)
		case *syntax.Redirect:
			finder.checkRedirect(n)
		}
		return true
	})
	return finder.result, finder.found
}

type hardlineFinder struct {
	cwd    string
	home   string
	found  bool
	result HardlineFinding
}

func (f *hardlineFinder) reject(rule, reason string) {
	if f.found {
		return
	}
	f.found = true
	f.result = HardlineFinding{Rule: rule, Reason: reason}
}

func (f *hardlineFinder) checkCall(call *syntax.CallExpr) {
	if call == nil || len(call.Args) == 0 {
		return
	}
	words := make([]word, len(call.Args))
	for i, arg := range call.Args {
		words[i] = literalWord(arg)
	}
	f.checkArgv(words, 0)
}

func (f *hardlineFinder) checkArgv(words []word, depth int) {
	if f.found || len(words) == 0 || depth > maxShellNesting {
		return
	}
	if words[0].dynamic || words[0].text == "" {
		return
	}

	name := baseCommand(words[0].text)
	if isShellCommand(name) {
		invocation := parseShellInvocation(words[1:])
		if invocation.hasScript && !invocation.script.dynamic {
			f.checkScript(invocation.script.text, depth+1)
		}
		return
	}
	switch name {
	case "rm":
		f.checkRecursiveDelete(words[1:])
	case "find":
		f.checkFind(words[1:], depth)
	case "chmod", "chown", "chgrp":
		f.checkRecursivePermission(name, words[1:])
	case "dd":
		f.checkDD(words[1:])
	case "kill":
		f.checkKill(words[1:])
	case "exec":
		if nested, ok := execCommand(words[1:]); ok {
			f.checkArgv(nested, depth+1)
		}
	case "eval":
		if script, ok := joinLiteralWords(words[1:]); ok {
			f.checkScript(script, depth+1)
		}
	case "trap":
		if action, found, dynamic := trapAction(words[1:]); found && !dynamic {
			f.checkScript(action.text, depth+1)
		}
	}

	if name == "env" {
		opts := parseEnvArgs(words[1:])
		if opts.splitString && !opts.splitValue.dynamic {
			f.checkScript(opts.splitValue.text, depth+1)
			return
		}
	}
	if spec, ok := wrapperSpecs[name]; ok {
		if nested, found, dynamic := wrapperNested(words[1:], spec); found && !dynamic {
			f.checkArgv(nested, depth+1)
		}
	}
}

func (f *hardlineFinder) checkScript(script string, depth int) {
	if f.found || depth > maxShellNesting || script == "" {
		return
	}
	file, err := Parse(script)
	if err != nil {
		return
	}
	nested := hardlineFinder{cwd: f.cwd, home: f.home}
	syntax.Walk(file, func(node syntax.Node) bool {
		if nested.found {
			return false
		}
		switch n := node.(type) {
		case *syntax.FuncDecl:
			nested.checkForkBomb(n)
		case *syntax.CallExpr:
			nested.checkCall(n)
		case *syntax.Redirect:
			nested.checkRedirect(n)
		}
		return true
	})
	if nested.found {
		f.reject(nested.result.Rule, nested.result.Reason)
	}
}

// checkRecursiveDelete catches rm -r/-R/--recursive when an operand resolves to
// the filesystem root, a top-level system directory or the home directory.
// Subdirectories below those roots stay governed by the configurable risk
// classifier, so rm -rf /home/alice/build is not a hardline rejection.
func (f *hardlineFinder) checkRecursiveDelete(args []word) {
	recursive := false
	var operands []word
	afterDoubleDash := false
	for _, arg := range args {
		if arg.dynamic {
			// A dynamic operand for rm is rejected by the risk classifier as a
			// destructive dynamic operand. It is not a hardline finding here
			// because the classifier has a more precise message.
			continue
		}
		text := arg.text
		if !afterDoubleDash && text == "--" {
			afterDoubleDash = true
			continue
		}
		if !afterDoubleDash && strings.HasPrefix(text, "-") {
			if text == "--recursive" || text == "-R" ||
				(strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--") &&
					(strings.Contains(text, "r") || strings.Contains(text, "R"))) {
				recursive = true
			}
			continue
		}
		operands = append(operands, arg)
	}
	if !recursive {
		return
	}
	for _, operand := range operands {
		path, ok := f.resolvePath(operand.text)
		if !ok {
			continue
		}
		if isHardlineProtectedPath(path) {
			f.reject(
				"hardline.rm-protected-root",
				"recursive deletion of protected path "+path+" is not allowed",
			)
			return
		}
	}
}

// checkFind catches find -delete or find -exec on a root/system path. The
// nested -exec argv is evaluated with the same recursive rules.
func (f *hardlineFinder) checkFind(args []word, depth int) {
	if len(args) == 0 {
		return
	}
	paths := findPathOperands(args)
	for _, pathWord := range paths {
		path, ok := f.resolvePath(pathWord.text)
		if !ok || !isHardlineProtectedPath(path) {
			continue
		}
		if findHasDelete(args) {
			f.reject(
				"hardline.find-delete-protected-root",
				"find -delete on protected path "+path+" is not allowed",
			)
			return
		}
	}
	for _, nested := range findExecCommands(args) {
		if f.findExecDestroysRoot(nested, paths) {
			return
		}
		f.checkArgv(nested, depth+1)
		if f.found {
			return
		}
	}
}

// findExecDestroysRoot rejects a destructive -exec action when find itself
// walks a protected root. The nested argv contains a placeholder ("{}" or a
// relative operand), so resolving it alone cannot prove the target is safe.
func (f *hardlineFinder) findExecDestroysRoot(nested []word, paths []word) bool {
	if len(nested) == 0 {
		return false
	}
	protected := false
	for _, pathWord := range paths {
		path, ok := f.resolvePath(pathWord.text)
		if ok && isHardlineProtectedPath(path) {
			protected = true
			break
		}
	}
	if !protected || nested[0].dynamic {
		return false
	}
	switch baseCommand(nested[0].text) {
	case "rm", "shred", "truncate", "dd":
		f.reject(
			"hardline.find-exec-protected-root",
			"find -exec runs a destructive command on a protected path",
		)
		return true
	case "chmod", "chown", "chgrp":
		for _, arg := range nested[1:] {
			if !arg.dynamic && (arg.text == "-R" || arg.text == "--recursive" ||
				(strings.HasPrefix(arg.text, "-") && !strings.HasPrefix(arg.text, "--") &&
					strings.ContainsAny(arg.text[1:], "rR"))) {
				f.reject(
					"hardline.find-exec-protected-root",
					"find -exec runs a recursive permission change on a protected path",
				)
				return true
			}
		}
	}
	return false
}

func findPathOperands(args []word) []word {
	var paths []word
	skipNext := false
	for i, arg := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if arg.dynamic || arg.text == "" {
			continue
		}
		text := arg.text
		switch text {
		case "--":
			continue
		case "-L", "-P", "-H":
			continue
		case "-D":
			if i+1 < len(args) {
				skipNext = true
			}
			continue
		}
		if strings.HasPrefix(text, "-") {
			break
		}
		paths = append(paths, arg)
	}
	return paths
}

func findHasDelete(args []word) bool {
	for _, arg := range args {
		if arg.dynamic {
			continue
		}
		if arg.text == "-delete" {
			return true
		}
	}
	return false
}

func (f *hardlineFinder) checkRecursivePermission(name string, args []word) {
	recursive := false
	var operands []string
	afterDoubleDash := false
	for _, arg := range args {
		if arg.dynamic {
			continue
		}
		text := arg.text
		if !afterDoubleDash && text == "--" {
			afterDoubleDash = true
			continue
		}
		if !afterDoubleDash && strings.HasPrefix(text, "-") {
			switch {
			case text == "--recursive" || text == "-R":
				recursive = true
			case strings.HasPrefix(text, "--"):
				// Other long flags do not make the operation recursive unless
				// they are explicitly --recursive.
			case len(text) > 1 && strings.ContainsAny(text[1:], "rR"):
				recursive = true
			}
			continue
		}
		operands = append(operands, text)
	}
	if !recursive || len(operands) < 2 {
		return
	}
	// chmod/chown take a mode or owner operand before the paths. The last
	// operand is the most reliable target for both two- and multi-path forms;
	// scanning all trailing operands is safe because a protected path is
	// always dangerous for a recursive operation.
	for _, operand := range operands {
		path, ok := f.resolvePath(operand)
		if !ok || !isHardlineProtectedPath(path) {
			continue
		}
		f.reject(
			"hardline."+name+"-recursive-protected-root",
			"recursive "+name+" on protected path "+path+" is not allowed",
		)
		return
	}
}

func (f *hardlineFinder) checkDD(args []word) {
	for _, arg := range args {
		if arg.dynamic || arg.text == "" {
			continue
		}
		if strings.HasPrefix(arg.text, "if=") {
			f.reject("hardline.dd-read", "dd reads raw disks or devices")
			return
		}
		if !strings.HasPrefix(arg.text, "of=") {
			continue
		}
		target := strings.TrimPrefix(arg.text, "of=")
		if isBlockDevicePath(target) {
			f.reject(
				"hardline.dd-block-device",
				"dd writes to block device "+target,
			)
			return
		}
	}
}

func (f *hardlineFinder) checkKill(args []word) {
	signal := ""
	targets := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg.dynamic {
			continue
		}
		text := arg.text
		switch {
		case text == "--":
			targets = append(targets, wordTexts(args[i+1:])...)
			i = len(args)
		case text == "-l" || text == "--list":
			return
		case text == "-s" || text == "--signal":
			if i+1 < len(args) && !args[i+1].dynamic {
				signal = args[i+1].text
				i++
			}
		case strings.HasPrefix(text, "--signal="):
			signal = strings.TrimPrefix(text, "--signal=")
		case strings.HasPrefix(text, "-") && len(text) > 1:
			signal = text[1:]
		default:
			targets = append(targets, text)
		}
	}

	if isHangupEveryProcess(signal) {
		f.reject("hardline.kill-every-process", "sending SIGHUP to every process is not allowed")
		return
	}
	if !isKillSignal(signal) {
		return
	}
	for _, target := range targets {
		if target == "1" || target == "-1" {
			f.reject("hardline.kill-init", "killing PID 1 with SIGKILL is not allowed")
			return
		}
	}
}

func isHangupEveryProcess(signal string) bool {
	switch strings.ToUpper(strings.TrimPrefix(signal, "-")) {
	case "1", "HUP", "SIGHUP":
		return true
	default:
		return false
	}
}

func isKillSignal(signal string) bool {
	switch strings.ToUpper(strings.TrimPrefix(signal, "-")) {
	case "9", "KILL", "SIGKILL":
		return true
	default:
		return false
	}
}

func (f *hardlineFinder) checkRedirect(redir *syntax.Redirect) {
	if redir == nil || redir.Word == nil {
		return
	}
	switch redir.Op {
	case syntax.RdrOut, syntax.AppOut, syntax.RdrAll, syntax.AppAll:
	default:
		return
	}
	target := literalWord(redir.Word)
	if target.dynamic || target.text == "" {
		return
	}
	if isBlockDevicePath(target.text) {
		f.reject(
			"hardline.redirect-block-device",
			"redirecting output to block device "+target.text+" is not allowed",
		)
	}
}

func (f *hardlineFinder) checkForkBomb(decl *syntax.FuncDecl) {
	if decl == nil || decl.Body == nil || decl.Name == nil {
		return
	}
	name := baseCommand(decl.Name.Value)
	if name == "" {
		return
	}
	selfCallInBackground := func(node syntax.Node) bool {
		found := false
		syntax.Walk(node, func(n syntax.Node) bool {
			if found {
				return false
			}
			switch x := n.(type) {
			case *syntax.Stmt:
				if x.Background && nodeCallsCommand(x.Cmd, name) {
					found = true
				}
			case *syntax.BinaryCmd:
				if (x.Op == syntax.Pipe || x.Op == syntax.PipeAll) &&
					(nodeCallsCommand(x.X, name) || nodeCallsCommand(x.Y, name)) {
					found = true
				}
			}
			return !found
		})
		return found
	}
	if selfCallInBackground(decl.Body) {
		f.reject(
			"hardline.fork-bomb",
			"recursive background function "+name+" is a fork bomb",
		)
	}
}

func nodeCallsCommand(node syntax.Node, name string) bool {
	found := false
	syntax.Walk(node, func(n syntax.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		first := literalWord(call.Args[0])
		if !first.dynamic && baseCommand(first.text) == name {
			found = true
			return false
		}
		return true
	})
	return found
}

func (f *hardlineFinder) resolvePath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		if f.home == "" {
			return "", false
		}
		raw = f.home + strings.TrimPrefix(raw, "~")
	}
	if !filepath.IsAbs(raw) {
		if f.cwd == "" {
			return "", false
		}
		raw = filepath.Join(f.cwd, raw)
	}
	raw = filepath.Clean(raw)
	// A glob that targets the root or a protected top-level directory is just
	// as destructive as the directory itself.
	if raw == "/*" || raw == "/**" {
		return "/", true
	}
	if strings.HasSuffix(raw, string(filepath.Separator)+"*") {
		raw = strings.TrimSuffix(raw, string(filepath.Separator)+"*")
	}
	return raw, true
}

var hardlineProtectedRoots = map[string]bool{
	"/":      true,
	"/bin":   true,
	"/boot":  true,
	"/dev":   true,
	"/etc":   true,
	"/home":  true,
	"/lib":   true,
	"/lib64": true,
	"/opt":   true,
	"/proc":  true,
	"/root":  true,
	"/sbin":  true,
	"/srv":   true,
	"/sys":   true,
	"/usr":   true,
	"/var":   true,
}

func isHardlineProtectedPath(path string) bool {
	if path == "" {
		return false
	}
	return hardlineProtectedRoots[filepath.Clean(path)]
}

// isBlockDevicePath reports whether path names a raw block device rather than a
// pseudo-device such as /dev/null. It is intentionally conservative: a name
// with a partition suffix is still a device.
func isBlockDevicePath(path string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	if !strings.HasPrefix(path, "/dev/") {
		return false
	}
	name := strings.TrimPrefix(path, "/dev/")
	prefixes := []string{
		"sd", "hd", "vd", "xvd", "nvme", "mmcblk", "loop", "dm-", "md", "sr", "nbd", "disk",
	}
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			// Reject a bare prefix such as /dev/sda; allow /dev/sda1 too.
			if len(name) > len(prefix) || prefix == "disk" {
				return true
			}
		}
	}
	return false
}
