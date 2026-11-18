package tools

import (
	"regexp"
	"strings"
)

// patternSet matches a group of regular expressions and skips the whole group
// when none of its literal markers occurs in the candidate forms. A long
// benign command no longer pays patterns*length regex work, while the common
// short command still short-circuits on the first matching pattern.
type patternSet struct {
	patterns  []*regexp.Regexp
	prefilter []string
}

func newPatternSet(patterns []*regexp.Regexp) patternSet {
	return newPatternSetWithPrefilter(patterns)
}

func newPatternSetWithPrefilter(patterns []*regexp.Regexp, markers ...string) patternSet {
	return patternSet{patterns: patterns, prefilter: markers}
}

func (s patternSet) matchAny(forms []string) bool {
	if len(s.prefilter) > 0 && !formsContainAny(forms, s.prefilter) {
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
