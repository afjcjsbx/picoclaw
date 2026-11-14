package tools

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// newRiskGuard builds the AST-based command classifier. It is part of the
// optional built-in guard set, so enable_deny_patterns=false disables it just
// like the regex deny list; hardline and custom deny patterns still apply.
//
// Windows is skipped because the exec tool runs PowerShell there and the
// classifier's command table describes POSIX tools.
func newRiskGuard(cfg *config.Config, enabled bool) (*shellguard.Guard, error) {
	if !enabled || runtime.GOOS == "windows" {
		return nil, nil
	}

	threshold := "medium"
	var overrides map[string]string
	var modifiers map[string][]shellguard.ArgModifier
	if cfg != nil {
		if cfg.Tools.Exec.RiskThreshold != "" {
			threshold = cfg.Tools.Exec.RiskThreshold
		}
		overrides = cfg.Tools.Exec.RiskOverrides
		var err error
		modifiers, err = argModifiersFromConfig(cfg.Tools.Exec.ArgModifiers)
		if err != nil {
			return nil, err
		}
	}

	return shellguard.NewGuard(threshold, overrides, modifiers)
}

func argModifiersFromConfig(
	cfg map[string][]config.ArgModifierConfig,
) (map[string][]shellguard.ArgModifier, error) {
	if len(cfg) == 0 {
		return nil, nil
	}

	modifiers := make(map[string][]shellguard.ArgModifier, len(cfg))
	for command, entries := range cfg {
		converted := make([]shellguard.ArgModifier, 0, len(entries))
		for _, entry := range entries {
			if strings.TrimSpace(entry.Level) == "" {
				return nil, fmt.Errorf("arg modifier for %q is missing a level", command)
			}
			level, err := shellguard.ParseRiskLevel(entry.Level)
			if err != nil {
				return nil, fmt.Errorf("invalid arg modifier for %q: %w", command, err)
			}
			converted = append(converted, shellguard.ArgModifier{Args: entry.Args, Level: level})
		}
		modifiers[command] = converted
	}
	return modifiers, nil
}
