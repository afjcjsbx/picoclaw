package skills

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// ValidateSkillName validates a name for skills being created or installed:
// valid format and at most MaxNameLength bytes.
func ValidateSkillName(name string) error {
	if err := validateSkillNameFormat(name); err != nil {
		return err
	}
	if len(strings.TrimSpace(name)) > MaxNameLength {
		return fmt.Errorf("skill name exceeds %d characters", MaxNameLength)
	}
	return nil
}

// validateSkillNameFormat is ValidateSkillName without the length cap. Listing
// and loading use it so already-installed skills with long names stay usable;
// the catalog truncates them instead.
func validateSkillNameFormat(name string) error {
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
	if !namePattern.MatchString(trimmed) {
		return fmt.Errorf("skill name must be alphanumeric with hyphens")
	}
	return nil
}
