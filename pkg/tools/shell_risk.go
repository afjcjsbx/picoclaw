package tools

import (
	"runtime"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newRiskGuard builds the AST-based command classifier. It is part of the
// optional built-in guard set, so enable_deny_patterns=false disables it just
// like the regex deny list. Hardline and custom deny patterns still apply
// independently.
//
// Windows is skipped because the exec tool runs PowerShell there and the
// classifier's command table describes POSIX tools.
func newRiskGuard(cfg *config.Config, enabled bool) (*shellguard.Guard, error) {
	if !enabled || runtime.GOOS == "windows" {
		return nil, nil
	}

	threshold := "medium"
	if cfg != nil && cfg.Tools.Exec.RiskThreshold != "" {
		threshold = cfg.Tools.Exec.RiskThreshold
	}
	return shellguard.NewGuard(threshold)
}
