package fstools

import (
	"io"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

const maxSearchIgnoreBytes = 256 * 1024

// searchIgnoreRule is one gitignore-style rule. baseOnly rules (patterns
// without "/") match a path's base name at any depth; anchored rules match
// the path relative to the directory that declared them.
type searchIgnoreRule struct {
	re       *regexp.Regexp
	baseOnly bool
	negated  bool
	dirOnly  bool
}

// searchIgnoreSet is the set of rules declared by the .gitignore and .ignore
// files found in one directory.
type searchIgnoreSet struct {
	dir   string
	rules []searchIgnoreRule
}

// searchIgnoreStack holds the rule sets from the walk root down to the
// current directory. Deeper sets and later rules take precedence, and a
// negation ("!pattern") can re-include a path matched earlier.
type searchIgnoreStack []searchIgnoreSet

func (s searchIgnoreStack) ignored(candidate string, isDir bool) bool {
	ignored := false
	for _, set := range s {
		rel, err := filepath.Rel(set.dir, candidate)
		if err != nil || rel == "." || !filepath.IsLocal(rel) {
			continue
		}
		rel = filepath.ToSlash(rel)
		for _, rule := range set.rules {
			if rule.dirOnly && !isDir {
				continue
			}
			target := rel
			if rule.baseOnly {
				target = path.Base(rel)
			}
			if rule.re.MatchString(target) {
				ignored = !rule.negated
			}
		}
	}
	return ignored
}

// loadSearchIgnoreSet reads the .gitignore and .ignore files of dir, when
// present, and returns their combined rules. .ignore is applied after
// .gitignore so it can override it.
func loadSearchIgnoreSet(fsys fileSystem, dir string) (searchIgnoreSet, bool) {
	var rules []searchIgnoreRule
	for _, name := range []string{".gitignore", ".ignore"} {
		content, err := readIgnoreFile(fsys, filepath.Join(dir, name))
		if err != nil || len(content) == 0 {
			continue
		}
		rules = append(rules, parseIgnoreRules(content)...)
	}
	if len(rules) == 0 {
		return searchIgnoreSet{}, false
	}
	return searchIgnoreSet{dir: dir, rules: rules}, true
}

func readIgnoreFile(fsys fileSystem, filePath string) ([]byte, error) {
	info, err := fsys.Stat(filePath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxSearchIgnoreBytes {
		return nil, err
	}
	f, err := fsys.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxSearchIgnoreBytes))
}

// parseIgnoreRules parses gitignore syntax: blank lines and "#" comments are
// skipped, "!" negates, a trailing "/" restricts the rule to directories,
// and a leading "/" anchors it to the declaring directory. Patterns are
// matched with the same glob engine as filename patterns, so "**", "{a,b}"
// and character classes work.
func parseIgnoreRules(content []byte) []searchIgnoreRule {
	var rules []searchIgnoreRule
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSuffix(raw, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// A backslash escapes a leading "#" or "!".
		rule := searchIgnoreRule{}
		if strings.HasPrefix(line, `\#`) || strings.HasPrefix(line, `\!`) {
			line = line[1:]
		} else if strings.HasPrefix(line, "!") {
			rule.negated = true
			line = line[1:]
		}
		if !fillIgnoreRule(&rule, line) {
			continue
		}
		rules = append(rules, rule)
	}
	return rules
}

func fillIgnoreRule(rule *searchIgnoreRule, line string) bool {
	// Trailing unescaped spaces are ignored.
	line = strings.TrimRight(line, " ")
	if strings.HasSuffix(line, "/") {
		rule.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}
	anchored := strings.Contains(line, "/")
	line = strings.TrimPrefix(line, "/")
	if line == "" {
		return false
	}
	re, err := compileSearchGlob(line)
	if err != nil {
		return false
	}
	rule.re = re
	rule.baseOnly = !anchored
	return true
}
