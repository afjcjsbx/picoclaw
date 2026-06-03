package skills

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/utils"
)

func ValidateSkillName(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("skill name is required")
	}
	if filepath.IsAbs(trimmed) {
		return fmt.Errorf("skill name must not be an absolute path")
	}
	if err := utils.ValidateSkillIdentifier(trimmed); err != nil {
		return fmt.Errorf("skill name is invalid: %w", err)
	}
	if len(trimmed) > MaxNameLength {
		return fmt.Errorf("skill name exceeds %d characters", MaxNameLength)
	}
	if !namePattern.MatchString(trimmed) {
		return fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	return nil
}

func ValidateSkillReference(name string) error {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return fmt.Errorf("skill name is required")
	}
	parts := strings.Split(trimmed, ":")
	if len(parts) == 1 {
		return ValidateSkillName(trimmed)
	}
	if len(parts) != 2 {
		return fmt.Errorf("skill reference must be <skill> or <namespace>:<skill>")
	}
	if err := ValidateSkillName(parts[0]); err != nil {
		return fmt.Errorf("skill namespace is invalid: %w", err)
	}
	if err := ValidateSkillName(parts[1]); err != nil {
		return err
	}
	if len(trimmed) > MaxNameLength*2+1 {
		return fmt.Errorf("skill reference exceeds %d characters", MaxNameLength*2+1)
	}
	return nil
}
