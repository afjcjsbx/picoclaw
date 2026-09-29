package textutil

// TruncateRunes keeps at most maxRunes runes and appends "..." when shortened.
// The suffix is not counted toward maxRunes.
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 {
		if s == "" {
			return s
		}
		return "..."
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[:maxRunes]) + "..."
}
