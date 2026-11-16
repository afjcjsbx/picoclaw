package shellguard

import (
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// WorkspacePolicy describes the static context used to decide whether a
// command can reach outside the workspace.
type WorkspacePolicy struct {
	// Cwd is the absolute, symlink-resolved directory the command starts in.
	Cwd string
	// Home is the absolute home directory used for ~ and $HOME expansion.
	// An empty value makes those expansions unresolvable and therefore
	// blocked.
	Home string
	// CDPath reports that the child shell inherits a non-empty CDPATH, so a
	// relative cd target may resolve outside the current directory.
	CDPath bool
	// PWDUnset reports that $PWD is no longer trusted to equal Cwd.
	PWDUnset bool
	// CwdChanged reports that the shell has already changed directory before
	// the script is inspected, so bare relative operands can be symlinks under
	// a directory other than Cwd.
	CwdChanged bool
	// AllowPath reports whether an absolute path is inside the workspace or
	// covered by an operator allowlist. It is responsible for resolving
	// symlinks; CheckWorkspace only joins and cleans paths.
	AllowPath func(string) bool
}

// WorkspaceError reports a command that cannot be proven to stay inside
// the workspace. It is deliberately separate from BlockedError: workspace
// restriction is an operator policy, not a risk threshold.
type WorkspaceError struct {
	Reason string
}

func (v *WorkspaceError) Error() string { return v.Reason }

func newWorkspaceError(reason string) *WorkspaceError {
	return &WorkspaceError{Reason: reason}
}

// normalizeWorkspace applies only the fixed rewrites a shell performs before
// parsing: backslash-newline continuations and ANSI-C quoting. It deliberately
// leaves $IFS alone, because replacing it inside a longer variable name such as
// $IFSPATH would corrupt the word and could hide a real path.
func normalizeWorkspace(command string) string {
	command = stripShellLineContinuations(command)
	return decodeANSICQuotes(command)
}

// CheckWorkspace parses command and rejects constructs that the byte-level
// absolute-path check cannot see: tilde and $HOME expansion in path position,
// parent-directory traversal, dynamic redirection targets, and directory
// changes through cd, pushd, popd and env -C.
//
// It is a static, best-effort guard, not an OS sandbox. Commands whose paths
// cannot be resolved statically are rejected only in path position; arbitrary
// child processes and file contents are outside its reach. OS-level isolation
// remains the only hard boundary.
func CheckWorkspace(command string, policy WorkspacePolicy) error {
	return checkWorkspaceScript(command, policy, 0)
}

// CheckWorkspaceFile validates an already parsed command. The exec guard uses
// it to share one parse with the risk classifier. Commands that contain fixed
// shell rewrites are re-parsed after normalizeWorkspace, because those rewrites
// can change word boundaries and the shared file reflects the written form.
func CheckWorkspaceFile(command string, file *syntax.File, policy WorkspacePolicy) error {
	if file == nil {
		return newWorkspaceError("command could not be parsed for workspace validation")
	}
	if normalized := normalizeWorkspace(command); normalized != command {
		return checkWorkspaceText(normalized, policy, 0)
	}
	return checkWorkspaceFile(file, policy, 0)
}

func checkWorkspaceScript(command string, policy WorkspacePolicy, depth int) error {
	// Apply the fixed shell rewrites before parsing. This also applies to
	// scripts reached through sh -c, eval and heredocs.
	return checkWorkspaceText(normalizeWorkspace(command), policy, depth)
}

func checkWorkspaceText(command string, policy WorkspacePolicy, depth int) error {
	if depth > maxShellNesting {
		return newWorkspaceError("shell nesting limit exceeded")
	}

	file, err := Parse(command)
	if err != nil {
		// Block scripts this parser cannot inspect rather than leaving a
		// parser-differential gap. The exec shell does not run syntax it
		// rejects either, so legitimate commands are unaffected.
		return newWorkspaceError("command could not be parsed for workspace validation")
	}
	return checkWorkspaceFile(file, policy, depth)
}

func checkWorkspaceFile(file *syntax.File, policy WorkspacePolicy, depth int) error {
	state := workspaceState{
		policy:        policy,
		cdpathSet:     policy.CDPath,
		pwdOverridden: policy.PWDUnset,
		cwdChanged:    policy.CwdChanged,
	}
	if policy.Cwd != "" {
		state.cwds = []string{policy.Cwd}
	}
	var violation error
	syntax.Walk(file, func(node syntax.Node) bool {
		if violation != nil {
			return false
		}
		stmt, ok := node.(*syntax.Stmt)
		if !ok {
			return true
		}
		violation = state.checkStmt(stmt, depth)
		return violation == nil
	})
	return violation
}

type workspaceState struct {
	policy WorkspacePolicy
	// cwds lists every working directory the command may have entered, in
	// source order. Every cd/pushd target is validated before it is added, so
	// the list only contains directories inside the workspace boundary.
	// Relative path operands are checked against all of them: the shell's
	// effective cwd is one of these, and a path that escapes from any of them
	// cannot be trusted.
	cwds []string
	// cwdChanged is set when the shell has changed directory, including a
	// change carried in from an enclosing script.
	cwdChanged bool
	// homeChanged marks HOME as assigned or unset. Tilde/$HOME then follows a
	// value this pass cannot trust across subshells, functions and branches,
	// so expansion is rejected instead of risking a scope leak.
	homeChanged bool
	// pwdOverridden marks a dynamic or static PWD assignment. $PWD is then
	// no longer trusted to match the real current directory.
	pwdOverridden bool
	// cdpathSet marks a CDPATH assignment. Relative cd targets may then
	// resolve through CDPATH instead of the current directory.
	cdpathSet bool
}

func (s *workspaceState) effectiveHome() (string, bool) {
	if s.homeChanged {
		return "", false
	}
	return s.policy.Home, s.policy.Home != ""
}

// nestedPolicy carries HOME/PWD/CDPATH invalidation and the effective working
// directory into scripts that run in the same environment (eval, sh -c,
// heredocs and exec wrappers).
func (s *workspaceState) nestedPolicy() WorkspacePolicy {
	policy := s.policy
	home, ok := s.effectiveHome()
	if !ok {
		policy.Home = ""
	} else {
		policy.Home = home
	}
	policy.Cwd = s.currentCwd()
	policy.CwdChanged = s.hasCwdCandidates()
	policy.CDPath = s.cdpathSet
	policy.PWDUnset = s.pwdOverridden
	return policy
}

func (s *workspaceState) hasCwdCandidates() bool {
	return s.cwdChanged || len(s.cwds) > 1
}

func (s *workspaceState) currentCwd() string {
	if len(s.cwds) == 0 {
		return s.policy.Cwd
	}
	return s.cwds[len(s.cwds)-1]
}

// addCwd records a directory the shell may have changed into. Deduplication
// keeps the candidate list small for commands that cd into the same place
// repeatedly.
func (s *workspaceState) addCwd(cwd string) {
	if cwd == "" {
		return
	}
	s.cwdChanged = true
	for _, existing := range s.cwds {
		if existing == cwd {
			return
		}
	}
	s.cwds = append(s.cwds, cwd)
}

// resolveCwd applies a validated, literal directory target to the current
// effective cwd. Symlinks are not resolved here: the target already passed the
// AllowPath check, and keeping the lexical path preserves relative access
// through a workspace-internal symlink.
func (s *workspaceState) resolveCwd(target string) string {
	if filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	base := s.currentCwd()
	if base == "" {
		return filepath.Clean(target)
	}
	return filepath.Clean(filepath.Join(base, target))
}

func (s *workspaceState) checkStmt(stmt *syntax.Stmt, depth int) error {
	if err := s.checkRedirects(stmt.Redirs); err != nil {
		return err
	}

	switch cmd := stmt.Cmd.(type) {
	case *syntax.CallExpr:
		return s.checkCall(cmd, stmt.Redirs, depth)
	case *syntax.DeclClause:
		for _, assign := range cmd.Args {
			if err := s.checkAssignment(assign); err != nil {
				return err
			}
		}
	case *syntax.TestClause:
		return s.checkTestExpr(cmd.X)
	case *syntax.ForClause:
		if iter, ok := cmd.Loop.(*syntax.WordIter); ok {
			for _, item := range iter.Items {
				if err := s.checkWord(s.expandWord(item, false), false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *workspaceState) checkTestExpr(expr syntax.TestExpr) error {
	switch x := expr.(type) {
	case *syntax.Word:
		return s.checkWord(s.expandWord(x, false), false)
	case *syntax.BinaryTest:
		if err := s.checkTestExpr(x.X); err != nil {
			return err
		}
		return s.checkTestExpr(x.Y)
	case *syntax.UnaryTest:
		return s.checkTestExpr(x.X)
	case *syntax.ParenTest:
		return s.checkTestExpr(x.X)
	}
	return nil
}

func (s *workspaceState) checkCall(
	call *syntax.CallExpr,
	redirs []*syntax.Redirect,
	depth int,
) error {
	for _, assign := range call.Assigns {
		if err := s.checkAssignment(assign); err != nil {
			return err
		}
	}

	args := make([]expandedWord, len(call.Args))
	for i, arg := range call.Args {
		args[i] = s.expandWord(arg, false)
	}

	// env -C changes the cwd only for the command it launches. Add the target
	// as a temporary candidate before checking the rest of the argv, so its
	// relative operands are not mistakenly resolved against the outer cwd.
	if len(args) > 0 && baseCommand(args[0].text) == "env" {
		if target, ok := envChdirTarget(args[1:]); ok &&
			!target.dynamic && target.text != "" && target.text != "-" {
			if err := s.checkDirTarget(target, false); err != nil {
				return err
			}
			saved := append([]string(nil), s.cwds...)
			s.addCwd(s.resolveCwd(target.text))
			defer func() { s.cwds = saved }()
		}
	}

	for _, arg := range args {
		if err := s.checkWord(arg, false); err != nil {
			return err
		}
	}
	s.noteUnset(args)
	return s.checkSimple(args, redirs, depth, true)
}

// checkSimple validates the surface of one simple command exactly once and then
// inspects the scripts or commands it launches. Keeping the surface checks here
// avoids running them twice when checkNested recurses through wrappers.
func (s *workspaceState) checkSimple(
	args []expandedWord,
	redirs []*syntax.Redirect,
	depth int,
	track bool,
) error {
	if depth > maxShellNesting {
		return newWorkspaceError("shell nesting limit exceeded")
	}
	if len(args) == 0 {
		return nil
	}
	// Surface flags are checked even when the command name is dynamic: a
	// command such as `$CMD -C ..` must still not point outside the workspace.
	if err := s.checkPathFlags(args); err != nil {
		return err
	}
	if err := s.checkSymlinkSource(args); err != nil {
		return err
	}
	if err := s.checkDirChange(args, track); err != nil {
		return err
	}
	return s.checkNested(args, redirs, depth)
}

func (s *workspaceState) noteUnset(args []expandedWord) {
	if len(args) == 0 || baseCommand(args[0].text) != "unset" {
		return
	}
	for _, arg := range args[1:] {
		if arg.dynamic {
			// The variable name is unknown; it could be HOME or PWD.
			s.homeChanged = true
			s.pwdOverridden = true
			continue
		}
		switch arg.text {
		case "HOME":
			s.homeChanged = true
		case "PWD":
			s.pwdOverridden = true
		}
	}
}

func (s *workspaceState) checkAssignment(assign *syntax.Assign) error {
	if assign == nil {
		return nil
	}
	if assign.Array != nil {
		if assign.Name != nil {
			switch assign.Name.Value {
			case "HOME":
				s.homeChanged = true
			case "PWD", "OLDPWD", "CDPATH":
				return newWorkspaceError(
					"path array assignment cannot be validated under workspace restriction",
				)
			}
		}
		for _, elem := range assign.Array.Elems {
			if elem == nil || elem.Value == nil {
				continue
			}
			if err := s.checkWord(s.expandWord(elem.Value, true), false); err != nil {
				return err
			}
		}
	}
	if assign.Value == nil {
		return nil
	}

	value := s.expandWord(assign.Value, true)
	if assign.Name != nil {
		switch assign.Name.Value {
		case "HOME":
			// The value itself is not a file access. Mark HOME as changed so
			// later ~/$HOME expansion is rejected without trusting the
			// assignment's scope.
			s.homeChanged = true
			return nil
		case "PWD":
			if value.dynamic {
				return newWorkspaceError(
					"dynamic path assignment cannot be validated under workspace restriction",
				)
			}
			s.pwdOverridden = true
			if value.text == "" {
				return nil
			}
			return s.checkPath(value.text)
		case "OLDPWD":
			if value.dynamic {
				return newWorkspaceError(
					"dynamic path assignment cannot be validated under workspace restriction",
				)
			}
			if value.text == "" {
				return nil
			}
			return s.checkPath(value.text)
		case "CDPATH":
			if value.dynamic {
				return newWorkspaceError(
					"dynamic path assignment cannot be validated under workspace restriction",
				)
			}
			s.cdpathSet = true
			for _, entry := range strings.Split(value.text, ":") {
				if entry == "" {
					continue
				}
				if err := s.checkPath(entry); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if value.homePath {
		return s.checkWord(value, false)
	}
	return nil
}

func (s *workspaceState) checkRedirects(redirs []*syntax.Redirect) error {
	for _, redir := range redirs {
		if redir == nil || redir.Word == nil {
			continue
		}
		switch redir.Op {
		case syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc, syntax.DplIn, syntax.DplOut:
			continue
		}
		// Process substitution is a command, not a path; the inner command is
		// visited by syntax.Walk.
		if len(redir.Word.Parts) == 1 {
			if _, ok := redir.Word.Parts[0].(*syntax.ProcSubst); ok {
				continue
			}
		}
		if err := s.checkWord(s.expandWord(redir.Word, false), true); err != nil {
			return err
		}
	}
	return nil
}

func (s *workspaceState) checkWord(w expandedWord, pathPosition bool) error {
	if w.homePath {
		if w.dynamic || w.text == "" {
			return newWorkspaceError("path outside working dir")
		}
		return s.checkPath(w.text)
	}
	if w.dynamic {
		if pathPosition {
			return newWorkspaceError("dynamic path cannot be validated under workspace restriction")
		}
		return nil
	}
	if w.text == "" {
		return nil
	}
	if pathPosition {
		if !isWorkspaceURL(w.text) && hasParentComponent(w.text) {
			return newWorkspaceError("path traversal detected")
		}
		return s.checkPath(w.text)
	}
	if filepath.IsAbs(w.text) {
		return s.checkPath(w.text)
	}
	// Relative operands are only visible to the byte-level guard when they
	// contain a slash. After a cd they must also be resolved against the new
	// directory, not only against the workspace root.
	if value, ok := relativePathCandidate(w.text); ok {
		return s.checkPath(value)
	}
	// A bare operand can still be a symlink under the new directory, for
	// example `cd sub; cat secret` with sub/secret pointing outside. Once the
	// shell has changed directory, treat plain non-flag operands as candidate
	// paths. Nonexistent names resolve to their existing inside ancestor and
	// remain harmless.
	if s.hasCwdCandidates() && plainPathOperand(w.text) {
		return s.checkPath(w.text)
	}
	return nil
}

func plainPathOperand(text string) bool {
	if text == "" || strings.HasPrefix(text, "-") || isAssignment(text) {
		return false
	}
	return !strings.ContainsAny(text, "*?[")
}

// relativePathCandidate extracts the path-shaped value of an operand. It
// ignores bare words, flags and assignments, which keeps grep patterns and
// option values from being treated as filesystem paths.
func relativePathCandidate(text string) (string, bool) {
	if text == "" || isWorkspaceURL(text) {
		return "", false
	}
	value := text
	if strings.HasPrefix(value, "-") {
		eq := strings.IndexByte(value, '=')
		if eq < 0 {
			// A single-dash option can carry an attached path, as in
			// cc -I/etc or echo -isystem/usr/include.
			slash := strings.IndexAny(value, `/\`)
			if slash <= 0 {
				return "", false
			}
			value = value[slash:]
		} else {
			value = value[eq+1:]
			if value == "" || isWorkspaceURL(value) {
				return "", false
			}
		}
	}
	if strings.ContainsAny(value, `/\`) {
		return value, true
	}
	return "", false
}

// checkPath validates a path against every directory the command may have
// changed into. A path that escapes from any candidate cwd is rejected.
func (s *workspaceState) checkPath(path string) error {
	if filePath, ok := fileURLPath(path); ok {
		path = filePath
	}
	if isWorkspaceURL(path) {
		return nil
	}
	// Shell syntax such as `2>/dev/null;` or `>/dev/null]` can leave trailing
	// punctuation attached to the path token. The byte-level guard already
	// trims it; keep the same behavior here so harmless pseudo-devices stay
	// usable.
	path = strings.TrimRight(path, ",)]}:")
	if path == "" {
		return newWorkspaceError("path outside working dir")
	}
	if filepath.IsAbs(path) {
		return s.checkPathFrom(path, "")
	}
	if len(s.cwds) == 0 {
		return s.checkPathFrom(path, s.policy.Cwd)
	}
	for _, cwd := range s.cwds {
		if err := s.checkPathFrom(path, cwd); err != nil {
			return err
		}
	}
	return nil
}

func (s *workspaceState) checkPathFrom(path, cwd string) error {
	if isWorkspaceURL(path) {
		return nil
	}
	path = strings.TrimRight(path, ",)]}:")
	if path == "" {
		return newWorkspaceError("path outside working dir")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(cwd, path)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return newWorkspaceError("path outside working dir")
	}
	if s.policy.AllowPath == nil || !s.policy.AllowPath(absolute) {
		return newWorkspaceError("path outside working dir")
	}
	return nil
}

// expandedWord is a shell word with the statically known expansions applied.
// dynamic reports that part of the word could not be resolved without running
// the shell.
type expandedWord struct {
	text     string
	dynamic  bool
	homePath bool
}

func (s *workspaceState) expandWord(w *syntax.Word, assignment bool) expandedWord {
	var out expandedWord
	if w == nil || len(w.Parts) == 0 {
		return out
	}

	if lit, ok := w.Parts[0].(*syntax.Lit); ok {
		if assignment {
			if prefix, rest, ok := assignmentTilde(lit.Value); ok {
				out.text = prefix
				out.homePath = true
				s.appendTilde(&out, rest)
				s.addParts(&out, w.Parts[1:])
				return out
			}
		}
		if strings.HasPrefix(lit.Value, "~") {
			out.homePath = true
			s.appendTilde(&out, lit.Value[1:])
			s.addParts(&out, w.Parts[1:])
			return out
		}
	}

	s.addParts(&out, w.Parts)
	return out
}

func (s *workspaceState) addParts(out *expandedWord, parts []syntax.WordPart) {
	for _, part := range parts {
		switch p := part.(type) {
		case *syntax.Lit:
			out.text += p.Value
		case *syntax.SglQuoted:
			out.text += p.Value
			if p.Dollar {
				out.dynamic = true
			}
		case *syntax.DblQuoted:
			s.addParts(out, p.Parts)
		case *syntax.ParamExp:
			name, ok := simpleParamName(p)
			if !ok {
				// ${HOME:-...}, ${#HOME} and similar forms still start from
				// the home directory. Treat a leading one as an unresolvable
				// home path instead of letting the literal suffix through.
				if p.Param != nil && p.Param.Value == "HOME" && atPathStart(out.text) {
					out.homePath = true
				}
				out.dynamic = true
				continue
			}
			switch name {
			case "HOME":
				if atPathStart(out.text) {
					out.homePath = true
				}
				home, ok := s.effectiveHome()
				if !ok {
					out.dynamic = true
					continue
				}
				out.text += home
			case "PWD":
				if atPathStart(out.text) {
					out.homePath = true
				}
				cwd := s.currentCwd()
				if cwd == "" || s.pwdOverridden {
					out.dynamic = true
					continue
				}
				out.text += cwd
			case "OLDPWD":
				if atPathStart(out.text) {
					out.homePath = true
				}
				out.dynamic = true
			default:
				out.dynamic = true
			}
		default:
			out.dynamic = true
		}
	}
}

func (s *workspaceState) appendTilde(out *expandedWord, rest string) {
	home, ok := s.effectiveHome()
	if !ok {
		out.dynamic = true
		return
	}
	switch {
	case rest == "":
		out.text += home
	case strings.HasPrefix(rest, "/"):
		out.text += home + rest
	default:
		// ~user, ~+, ~- need the passwd database or shell state.
		out.dynamic = true
	}
}

// assignmentTilde splits an assignment word such as X=~/path into its literal
// prefix and the part after the tilde. Bash expands ~ after = in assignment
// context, including arguments to declare/export/local/readonly/typeset.
func assignmentTilde(value string) (prefix, rest string, ok bool) {
	eq := strings.IndexByte(value, '=')
	if eq <= 0 || !isAssignment(value[:eq+1]) {
		return "", "", false
	}
	after := value[eq+1:]
	if !strings.HasPrefix(after, "~") {
		return "", "", false
	}
	return value[:eq+1], after[1:], true
}

func atPathStart(text string) bool {
	return text == "" || strings.HasSuffix(text, "=")
}

func simpleParamName(p *syntax.ParamExp) (string, bool) {
	if p.Param == nil || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet ||
		p.Split != syntax.OptUnset || p.GlobSubst != syntax.OptUnset ||
		p.RcExpand != syntax.OptUnset || p.NestedParam != nil || p.Index != nil ||
		len(p.Modifiers) != 0 || p.Slice != nil || p.Repl != nil ||
		p.Names != 0 || p.Exp != nil {
		return "", false
	}
	return p.Param.Value, true
}

// checkPathFlags validates directory-valued options such as tar -C, git -C,
// make -C and env --chdir. A bare `..` there reaches the parent without a
// slash, so the byte-level traversal regex misses it.
func (s *workspaceState) checkPathFlags(args []expandedWord) error {
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
			if err := s.checkPathFlagValue(expandedWord{
				text:    text[2:],
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		case strings.HasPrefix(text, "--directory="):
			if err := s.checkPathFlagValue(expandedWord{
				text:    strings.TrimPrefix(text, "--directory="),
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		case strings.HasPrefix(text, "--chdir="):
			if err := s.checkPathFlagValue(expandedWord{
				text:    strings.TrimPrefix(text, "--chdir="),
				dynamic: args[i].dynamic,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *workspaceState) checkPathFlagValue(value expandedWord) error {
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
func (s *workspaceState) checkSymlinkSource(args []expandedWord) error {
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
func (s *workspaceState) checkDirChange(args []expandedWord, track bool) error {
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
func unwrapExecPrefix(args []expandedWord) []expandedWord {
	for len(args) > 0 {
		name := baseCommand(args[0].text)
		if name != "command" && name != "builtin" {
			return args
		}
		if name == "command" && hasCommandLookupFlag(wordsFromExpanded(args[1:])) {
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

func (s *workspaceState) checkCD(args []expandedWord, track bool) error {
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

func cdTarget(args []expandedWord) (expandedWord, bool) {
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
		return expandedWord{}, false
	}
	return args[i], true
}

func (s *workspaceState) checkPushd(args []expandedWord, track bool) error {
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

func (s *workspaceState) checkEnvChdir(args []expandedWord) error {
	if hasEnvSplitString(wordsFromExpanded(args)) {
		return newWorkspaceError("env -S cannot be validated under workspace restriction")
	}
	for _, arg := range args {
		if arg.dynamic || !isAssignment(arg.text) {
			continue
		}
		name, _, _ := strings.Cut(arg.text, "=")
		switch name {
		case "HOME", "PWD", "OLDPWD", "CDPATH":
			return newWorkspaceError(
				"path environment override cannot be validated under workspace restriction",
			)
		}
	}
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "-i" || text == "--ignore-environment":
			s.homeChanged = true
		case text == "-u" || text == "--unset":
			if i+1 < len(args) && args[i+1].text == "HOME" {
				s.homeChanged = true
			}
			i++
		case strings.HasPrefix(text, "--unset=") &&
			strings.TrimPrefix(text, "--unset=") == "HOME":
			s.homeChanged = true
		case strings.HasPrefix(text, "-u") && strings.TrimPrefix(text, "-u") == "HOME":
			s.homeChanged = true
		}
	}
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "--":
			return nil
		case text == "-C" || text == "--chdir":
			if i+1 >= len(args) {
				return newWorkspaceError("directory change cannot be validated under workspace restriction")
			}
			return s.checkDirTarget(args[i+1], false)
		case strings.HasPrefix(text, "--chdir="):
			return s.checkDirTarget(expandedWord{
				text:    strings.TrimPrefix(text, "--chdir="),
				dynamic: args[i].dynamic,
			}, false)
		case strings.HasPrefix(text, "-C") && len(text) > 2:
			return s.checkDirTarget(expandedWord{
				text:    text[2:],
				dynamic: args[i].dynamic,
			}, false)
		}
	}
	return nil
}

func (s *workspaceState) checkDirTarget(target expandedWord, track bool) error {
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

// shellReadsStdin reports whether the shell would read its script from stdin.
// A script file positional or -c/--command means stdin is data instead.
func shellReadsStdin(args []expandedWord) bool {
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "--":
			return i+1 >= len(args)
		case text == "-" || text == "-s":
			return true
		case text == "-c" || text == "--command":
			return false
		case strings.HasPrefix(text, "--command="):
			return false
		case shellValueFlags[text]:
			i++
		case strings.HasPrefix(text, "-") && !strings.HasPrefix(text, "--"):
			if strings.Contains(text[1:], "c") {
				return false
			}
			if strings.Contains(text[1:], "s") {
				return true
			}
		case !strings.HasPrefix(text, "-"):
			return false
		}
	}
	return true
}

func (s *workspaceState) checkNested(
	args []expandedWord,
	redirs []*syntax.Redirect,
	depth int,
) error {
	if len(args) == 0 || args[0].dynamic {
		return nil
	}
	name := baseCommand(args[0].text)

	if isShellCommand(name) {
		words := wordsFromExpanded(args[1:])
		if script, ok := shellScriptArg(words); ok {
			if !script.literal {
				return newWorkspaceError("dynamic shell script cannot be validated under workspace restriction")
			}
			return checkWorkspaceScript(script.text, s.nestedPolicy(), depth+1)
		}
		// `bash <<EOF` executes the heredoc body as a script. Quoted and
		// unquoted bodies are both inspected; expansion is applied first when
		// the outer shell would have expanded it. A heredoc on a shell that
		// already has a script file or -c argument is stdin data, not a
		// script, and is left alone.
		if shellReadsStdin(args[1:]) {
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

	if name == "env" && hasEnvSplitString(wordsFromExpanded(args[1:])) {
		return newWorkspaceError("env -S cannot be validated under workspace restriction")
	}

	switch name {
	case "eval":
		script, ok := joinExpanded(args[1:])
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
	case "busybox":
		return s.checkBusybox(args[1:], depth)
	}

	if spec, ok := wrapperSpecs[name]; ok {
		words := wordsFromExpanded(args[1:])
		if name == "command" && hasCommandLookupFlag(words) {
			return nil
		}
		nested, found, dynamic := wrapperNested(words, spec)
		if dynamic {
			return newWorkspaceError("dynamic command cannot be validated under workspace restriction")
		}
		if found && len(nested) > 0 {
			// env -C changes the cwd only for the command it launches, so the
			// target is a temporary candidate while that command is checked.
			if name == "env" {
				if target, ok := envChdirTarget(args[1:]); ok &&
					!target.dynamic && target.text != "" && target.text != "-" {
					saved := append([]string(nil), s.cwds...)
					s.addCwd(s.resolveCwd(target.text))
					err := s.checkSimple(expandedFromWords(nested), nil, depth+1, false)
					s.cwds = saved
					return err
				}
			}
			return s.checkSimple(expandedFromWords(nested), nil, depth+1, false)
		}
	}
	return nil
}

// envChdirTarget returns the literal directory passed to env -C/--chdir.
func envChdirTarget(args []expandedWord) (expandedWord, bool) {
	for i := 0; i < len(args); i++ {
		text := args[i].text
		switch {
		case text == "--":
			return expandedWord{}, false
		case text == "-C" || text == "--chdir":
			if i+1 < len(args) {
				return args[i+1], true
			}
			return expandedWord{}, false
		case strings.HasPrefix(text, "--chdir="):
			return expandedWord{
				text:    strings.TrimPrefix(text, "--chdir="),
				dynamic: args[i].dynamic,
			}, true
		case strings.HasPrefix(text, "-C") && len(text) > 2:
			return expandedWord{
				text:    text[2:],
				dynamic: args[i].dynamic,
			}, true
		}
	}
	return expandedWord{}, false
}

// checkBusybox treats the first non-flag argument as the applet, so
// `busybox sh -c ...` is inspected like `sh -c ...`. BusyBox is common on the
// low-resource targets PicoClaw runs on.
func (s *workspaceState) checkBusybox(args []expandedWord, depth int) error {
	i := 0
	for i < len(args) {
		text := args[i].text
		if text == "--" {
			i++
			break
		}
		if strings.HasPrefix(text, "-") && text != "-" {
			i++
			continue
		}
		break
	}
	if i >= len(args) {
		return nil
	}
	return s.checkSimple(args[i:], nil, depth+1, false)
}

func (s *workspaceState) checkTrap(args []expandedWord, depth int) error {
	for _, arg := range args {
		if arg.dynamic {
			return newWorkspaceError("dynamic trap action cannot be validated under workspace restriction")
		}
		switch arg.text {
		case "-l", "-p":
			return nil
		case "--":
			continue
		case "", "-":
			return nil
		}
		if strings.HasPrefix(arg.text, "-") {
			continue
		}
		return checkWorkspaceScript(arg.text, s.nestedPolicy(), depth+1)
	}
	return nil
}

// checkExec inspects the command that exec replaces the shell with. exec is
// both a shell builtin and an execve wrapper, so a nested shell script would
// otherwise escape the AST pass.
func (s *workspaceState) checkExec(args []expandedWord, depth int) error {
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
		return nil
	}
	return s.checkSimple(args[i:], nil, depth+1, false)
}

// checkFindExec inspects commands launched by find -exec/-execdir/-ok/-okdir.
// find runs them directly, so a nested sh -c script is just as reachable as it
// is from a top-level command.
func (s *workspaceState) checkFindExec(args []expandedWord, depth int) error {
	for i := 0; i < len(args); i++ {
		switch args[i].text {
		case "-exec", "-execdir", "-ok", "-okdir":
			end := i + 1
			for end < len(args) && args[end].text != ";" && args[end].text != "+" {
				end++
			}
			if end > i+1 {
				if err := s.checkSimple(args[i+1:end], nil, depth+1, false); err != nil {
					return err
				}
			}
			i = end
		}
	}
	return nil
}

func joinExpanded(args []expandedWord) (string, bool) {
	var b strings.Builder
	for i, arg := range args {
		if arg.dynamic {
			return "", false
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(arg.text)
	}
	return b.String(), true
}

func wordsFromExpanded(args []expandedWord) []word {
	words := make([]word, len(args))
	for i, arg := range args {
		words[i] = word{text: arg.text, literal: !arg.dynamic}
	}
	return words
}

func expandedFromWords(words []word) []expandedWord {
	args := make([]expandedWord, len(words))
	for i, w := range words {
		args[i] = expandedWord{text: w.text, dynamic: !w.literal}
	}
	return args
}

func hasParentComponent(path string) bool {
	for _, component := range strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '\\'
	}) {
		if component == ".." {
			return true
		}
	}
	return false
}

func isWorkspaceURL(text string) bool {
	lower := strings.ToLower(text)
	for _, scheme := range []string{
		"http://", "https://", "ftp://", "ftps://", "sftp://", "ssh://", "git://",
	} {
		if strings.HasPrefix(lower, scheme) {
			return true
		}
	}
	return false
}

// fileURLPath extracts the local path from a file: URI so it is checked like a
// plain absolute path. Web schemes are exempt from workspace checks, but file:
// URIs point at the local filesystem and must stay inside the boundary.
func fileURLPath(text string) (string, bool) {
	if !strings.HasPrefix(strings.ToLower(text), "file:") {
		return "", false
	}
	rest := text[len("file:"):]
	rest = strings.TrimPrefix(rest, "//")
	if !strings.HasPrefix(rest, "/") {
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return "", false
		}
		rest = rest[slash:]
	}
	return rest, true
}
