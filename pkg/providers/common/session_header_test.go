package common

import (
	"net/http"
	"testing"
)

func TestSessionHeaderApply(t *testing.T) {
	plain := SessionHeader{Name: "X-S"}
	withFallback := SessionHeader{Name: "X-S", Fallback: "fb"}
	key := func(v any) map[string]any { return map[string]any{"session_key": v} }

	tests := []struct {
		name    string
		preset  string
		sh      SessionHeader
		options map[string]any
		want    string
	}{
		{"sets trimmed key", "", plain, key("  a:b  "), "a:b"},
		{"existing value wins", "pinned", plain, key("a"), "pinned"},
		{"non-printable replaced", "", plain, key("a\nbé"), "a_b_"},
		{"blank key uses fallback", "", withFallback, key(" "), "fb"},
		{"non-string key uses fallback", "", withFallback, key(7), "fb"},
		{"nil options use fallback", "", withFallback, nil, "fb"},
		{"key beats fallback", "", withFallback, key("k"), "k"},
		{"no key, no fallback", "", plain, nil, ""},
		{"zero value disabled", "", SessionHeader{}, key("a"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.preset != "" {
				h.Set("X-S", tt.preset)
			}
			tt.sh.Apply(h, tt.options)
			if got := h.Get("X-S"); got != tt.want {
				t.Fatalf("X-S = %q, want %q", got, tt.want)
			}
		})
	}
}
