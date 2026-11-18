package tools

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPatternSetPrefilterSkipsNonMatchingForms(t *testing.T) {
	set := newPatternSetWithPrefilter(
		[]*regexp.Regexp{regexp.MustCompile(`\brm\s+-rf\b`)},
		"rm",
	)
	require.False(t, set.matchAny([]string{"echo a long benign command"}))
	require.True(t, set.matchAny([]string{"rm -rf build"}))
}

func TestPatternSetCombinesLargeSets(t *testing.T) {
	set := newPatternSetWithPrefilter(
		[]*regexp.Regexp{
			regexp.MustCompile(`\brm\b`),
			regexp.MustCompile(`\bmkfs\b`),
			regexp.MustCompile(`\bdd\b`),
			regexp.MustCompile(`\bshutdown\b`),
		},
		"rm", "mkfs", "dd", "shutdown",
	)
	require.NotNil(t, set.combined, "large sets should compile one alternation")
	require.True(t, set.matchAny([]string{"sudo rm -rf /"}))
	require.False(t, set.matchAny([]string{"echo hello"}))
}

func TestPatternSetFallsBackToIndividualPatterns(t *testing.T) {
	set := newPatternSetWithPrefilter(
		[]*regexp.Regexp{
			regexp.MustCompile(`(?i)\brm\b`),
			regexp.MustCompile(`\bmkfs\b`),
			regexp.MustCompile(`\bdd\b`),
			regexp.MustCompile(`\bshutdown\b`),
		},
		"rm", "mkfs", "dd", "shutdown",
	)
	// Force the fallback path; the individual pattern is case-insensitive.
	set.combined = nil
	require.True(t, set.matchAny([]string{"rm -RF build"}))
}
