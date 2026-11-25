//go:build windows

package tools

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSplitShellPathMatch exercises the Windows path splitting helper.
func TestSplitShellPathMatch(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"/dev/null", []string{"/dev/null"}},
		{"/dev/null;", []string{"/dev/null"}},
		{"/dev/null;echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null&&echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null||echo b", []string{"/dev/null", "echo b"}},
		{"/dev/null|cat", []string{"/dev/null", "cat"}},
		{"/dev/null)", []string{"/dev/null"}},
		{"/dev/null]", []string{"/dev/null"}},
		{"/dev/null}", []string{"/dev/null"}},
		{"/dev/null,", []string{"/dev/null"}},
		{"/dev/null:", []string{"/dev/null"}},
		// Backticks are covered here only: a full command containing them is
		// rejected earlier by the dangerous-pattern check.
		{"/dev/null`", []string{"/dev/null"}},
		{"/dev/null`echo", []string{"/dev/null", "echo"}},
		{"/etc/passwd;echo", []string{"/etc/passwd", "echo"}},
		{"/etc/passwd", []string{"/etc/passwd"}},
		// Every segment after a separator is kept so it can be validated.
		{"/dev/null;cat</etc/shadow", []string{"/dev/null", "cat", "/etc/shadow"}},
		// Known trade-off: trailing punctuation is stripped even from real names.
		{"/workspace/backup,", []string{"/workspace/backup"}},
		// Windows matches are intentionally left untouched.
		{`C:\dev\null;x`, []string{`C:\dev\null;x`}},
	}

	for _, tc := range cases {
		require.Equal(t, tc.want, splitShellPathMatch(tc.in), "splitShellPathMatch(%q)", tc.in)
	}
}
