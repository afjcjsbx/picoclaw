package textutil

import "testing"

func TestTruncateRunes(t *testing.T) {
	for _, tt := range []struct {
		s    string
		max  int
		want string
	}{
		{"", 0, ""},
		{"abc", 3, "abc"},
		{"abc", 2, "ab..."},
		{"é🙂z", 2, "é🙂..."},
		{"abc", 0, "..."},
	} {
		if got := TruncateRunes(tt.s, tt.max); got != tt.want {
			t.Errorf("TruncateRunes(%q, %d) = %q, want %q", tt.s, tt.max, got, tt.want)
		}
	}
}
