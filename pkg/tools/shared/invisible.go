package toolshared

import "strings"

// isInvisibleUnicode reports whether r is one of the invisible characters that
// can be embedded in model-facing text without changing how it looks:
//
//   - U+E0000-U+E007F: the Unicode TAG block, whose characters are commonly
//     used to encode a second, hidden message next to visible text.
//   - U+202A-U+202E: bidi embedding and override controls (LRE, RLE, PDF, LRO,
//     RLO).
//   - U+2066-U+2069: bidi isolate controls (LRI, RLI, FSI, PDI).
//
// Zero-width joiners and non-joiners (U+200C/U+200D) are deliberately kept:
// emoji sequences and several scripts rely on them. The same applies to other
// zero-width characters that have legitimate uses.
func isInvisibleUnicode(r rune) bool {
	return (r >= 0xE0000 && r <= 0xE007F) ||
		(r >= 0x202A && r <= 0x202E) ||
		(r >= 0x2066 && r <= 0x2069)
}

// HasInvisibleUnicode reports whether s contains a TAG character or a bidi
// embedding, override, or isolate control.
func HasInvisibleUnicode(s string) bool {
	return strings.IndexFunc(s, isInvisibleUnicode) >= 0
}

// StripInvisibleUnicode removes TAG characters and bidi embedding, override,
// and isolate controls from s. It returns s unchanged (without allocating) when
// there is nothing to remove.
func StripInvisibleUnicode(s string) string {
	if !HasInvisibleUnicode(s) {
		return s
	}

	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if !isInvisibleUnicode(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// StripInvisibleUnicodeDeep applies StripInvisibleUnicode to every string
// reachable from value through map[string]any, []any, and []string values. It
// returns value itself when nothing changed, so a clean structure keeps its
// identity (and therefore its wire bytes and prompt-cache prefix).
func StripInvisibleUnicodeDeep(value any) any {
	cleaned, _ := stripInvisibleUnicodeDeep(value)
	return cleaned
}

func stripInvisibleUnicodeDeep(value any) (any, bool) {
	switch v := value.(type) {
	case string:
		cleaned := StripInvisibleUnicode(v)
		return cleaned, cleaned != v
	case map[string]any:
		changed := false
		out := make(map[string]any, len(v))
		for key, item := range v {
			cleaned, itemChanged := stripInvisibleUnicodeDeep(item)
			out[key] = cleaned
			changed = changed || itemChanged
		}
		if !changed {
			return v, false
		}
		return out, true
	case []any:
		changed := false
		out := make([]any, len(v))
		for i, item := range v {
			cleaned, itemChanged := stripInvisibleUnicodeDeep(item)
			out[i] = cleaned
			changed = changed || itemChanged
		}
		if !changed {
			return v, false
		}
		return out, true
	case []string:
		changed := false
		out := make([]string, len(v))
		for i, item := range v {
			cleaned := StripInvisibleUnicode(item)
			out[i] = cleaned
			changed = changed || cleaned != item
		}
		if !changed {
			return v, false
		}
		return out, true
	default:
		return value, false
	}
}
