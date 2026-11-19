package tools

import (
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newRiskGuard builds the AST-based command classifier. It is part of the
// optional built-in guard set, so enable_deny_patterns=false disables it just
// like the regex deny list. Hardline and custom deny patterns still apply
// independently.
//
// An empty threshold means medium. Windows is skipped because the exec tool
// runs PowerShell there and the classifier's command table describes POSIX
// tools.
func newRiskGuard(threshold string, enabled bool) (*shellguard.Guard, error) {
	if !enabled || !posixShell {
		return nil, nil
	}

	return shellguard.NewGuard(threshold)
}
