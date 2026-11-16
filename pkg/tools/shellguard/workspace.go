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

	args := make([]word, len(call.Args))
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
	args []word,
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

func (s *workspaceState) noteUnset(args []word) {
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

func (s *workspaceState) checkWord(w word, pathPosition bool) error {
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

// checkPathFrom resolves an already cleaned, non-empty path against cwd.
func (s *workspaceState) checkPathFrom(path, cwd string) error {
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
