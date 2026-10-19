package schema

import "strings"

// NormalizeAliases trims each alias, drops empty entries and duplicates, and
// removes the canonical key itself. Order and case are preserved. It returns
// nil when the input is empty or no alias survives normalization, so callers
// can distinguish "no aliases" from an allocated empty slice.
func NormalizeAliases(canonicalKey string, aliases []string) []string {
	if len(aliases) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	canonicalKey = strings.TrimSpace(canonicalKey)
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" || alias == canonicalKey {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		seen[alias] = struct{}{}
		normalized = append(normalized, alias)
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
