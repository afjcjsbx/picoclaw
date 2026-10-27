package toolshared

import (
	"strings"
	"testing"
)

func TestSanitizeIdentifierComponent(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"read_file", "read_file"},
		{"My Server/Tool!", "my_server_tool"},
		{"__a__b__", "a_b"},
		{"", "unnamed"},
		{"!!!", "unnamed"},
		{strings.Repeat("a", 100), strings.Repeat("a", 64)},
	}
	for _, tt := range tests {
		if got := SanitizeIdentifierComponent(tt.in); got != tt.want {
			t.Errorf("SanitizeIdentifierComponent(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
