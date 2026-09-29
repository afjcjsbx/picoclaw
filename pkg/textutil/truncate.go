package textutil

// TruncateRunes keeps at most max runes and appends "..." when shortened.
// The suffix is not counted toward max.
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		if s == "" {
			return s
		}
		return "..."
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}
