package tools

import (
	"regexp"
	"strings"
)

// patternSet compiles a group of regular expressions into one alternation when
// the group is large enough. A long command such as a 500-argument build line
// used to be scanned once per pattern; one combined pass keeps the guard cost
// proportional to the command length instead of patterns*length.
type patternSet struct {
	patterns  []*regexp.Regexp
	combined  *regexp.Regexp
	prefilter []string
}

// minCombinedPatterns avoids the larger alternation for one-off operator
// patterns, where a direct scan is cheaper to compile and run.
const minCombinedPatterns = 4

func newPatternSet(patterns []*regexp.Regexp) patternSet {
	return newPatternSetWithPrefilter(patterns)
}

// newPatternSetWithPrefilter adds a set of literal markers. When none of the
// markers occurs in any candidate form, the whole regex set is skipped. This
// keeps a long benign command from paying patterns*length regex work.
func newPatternSetWithPrefilter(patterns []*regexp.Regexp, markers ...string) patternSet {
	set := patternSet{patterns: patterns, prefilter: markers}
	if len(patterns) >= minCombinedPatterns {
		set.combined = combinePatterns(patterns)
	}
	return set
}

func combinePatterns(patterns []*regexp.Regexp) *regexp.Regexp {
	var expression strings.Builder
	for _, pattern := range patterns {
		if pattern == nil || pattern.String() == "" {
			continue
		}
		if expression.Len() > 0 {
			expression.WriteByte('|')
		}
		expression.WriteString("(?:")
		expression.WriteString(pattern.String())
		expression.WriteByte(')')
	}
	if expression.Len() == 0 {
		return nil
	}
	combined, err := regexp.Compile(expression.String())
	if err != nil {
		// An operator pattern with an inline flag or an unusual group may not
		// compose into one alternation. Fall back to the individual scans.
		return nil
	}
	return combined
}

func (s patternSet) matchAny(forms []string) bool {
	if len(s.prefilter) > 0 && !formsContainAny(forms, s.prefilter) {
		return false
	}
	if s.combined != nil {
		for _, form := range forms {
			if s.combined.MatchString(form) {
				return true
			}
		}
		return false
	}
	for _, pattern := range s.patterns {
		for _, form := range forms {
			if pattern.MatchString(form) {
				return true
			}
		}
	}
	return false
}

func formsContainAny(forms, markers []string) bool {
	for _, form := range forms {
		for _, marker := range markers {
			if strings.Contains(form, marker) {
				return true
			}
		}
	}
	return false
}
