package tools

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sipeed/picoclaw/pkg/config"
)

// execAllowRule is one compiled operator allow rule. The bypass set is
// explicit: a rule only skips the layers it names. Hardline, custom deny rules
// and the workspace boundary are not represented here and can never be
// bypassed.
type execAllowRule struct {
	id             string
	pattern        *regexp.Regexp
	bypassShell    bool
	bypassRisk     bool
	bypassApproval bool
}

func compileExecAllowRules(cfg config.ExecConfig) ([]execAllowRule, error) {
	rules := make([]execAllowRule, 0, len(cfg.CustomAllowPatterns)+len(cfg.CustomRules))

	for i, pattern := range cfg.CustomAllowPatterns {
		re, err := compileFullMatchPattern(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid custom allow pattern: %w", err)
		}
		rules = append(rules, execAllowRule{
			id:          fmt.Sprintf("operator.allow[%d]", i),
			pattern:     re,
			bypassShell: true,
		})
	}
	for i, rule := range cfg.CustomRules {
		pattern := strings.TrimSpace(rule.Pattern)
		if pattern == "" {
			return nil, fmt.Errorf("custom_rules[%d]: pattern is required", i)
		}
		action := strings.ToLower(strings.TrimSpace(rule.Action))
		switch action {
		case "allow":
			re, err := compileFullMatchPattern(pattern)
			if err != nil {
				return nil, fmt.Errorf("custom_rules[%d]: invalid pattern: %w", i, err)
			}
			compiled := execAllowRule{
				id:      fmt.Sprintf("operator.rule[%d]", i),
				pattern: re,
			}
			for _, bypass := range rule.Bypass {
				switch strings.ToLower(strings.TrimSpace(bypass)) {
				case "shell_constructs":
					compiled.bypassShell = true
				case "risk":
					compiled.bypassRisk = true
				case "approval":
					compiled.bypassApproval = true
				default:
					return nil, fmt.Errorf(
						"custom_rules[%d]: unknown bypass %q", i, bypass,
					)
				}
			}
			if !compiled.bypassShell && !compiled.bypassRisk && !compiled.bypassApproval {
				return nil, fmt.Errorf(
					"custom_rules[%d]: allow rule must name at least one bypass", i,
				)
			}
			rules = append(rules, compiled)
		case "deny":
			if len(rule.Bypass) != 0 {
				return nil, fmt.Errorf("custom_rules[%d]: deny rule cannot declare a bypass", i)
			}
			if _, err := regexp.Compile(pattern); err != nil {
				return nil, fmt.Errorf("custom_rules[%d]: invalid deny pattern: %w", i, err)
			}
			// Deny rules are folded into customDenyPatterns by the caller.
		default:
			return nil, fmt.Errorf(
				"custom_rules[%d]: action %q must be allow or deny", i, rule.Action,
			)
		}
	}
	return rules, nil
}

func customDenyPatternsFromRules(cfg config.ExecConfig) []string {
	out := append([]string(nil), cfg.CustomDenyPatterns...)
	for _, rule := range cfg.CustomRules {
		if strings.EqualFold(strings.TrimSpace(rule.Action), "deny") {
			out = append(out, rule.Pattern)
		}
	}
	return out
}

// matchAllowRule returns the first allow rule matching the whole command.
func (t *ExecTool) matchAllowRule(lower string) (execAllowRule, bool) {
	for _, rule := range t.allowRules {
		if rule.pattern.MatchString(lower) {
			return rule, true
		}
	}
	return execAllowRule{}, false
}
