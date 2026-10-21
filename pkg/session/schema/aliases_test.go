package schema

import (
	"slices"
	"testing"
)

func TestNormalizeAliases(t *testing.T) {
	tests := []struct {
		name         string
		canonicalKey string
		aliases      []string
		want         []string
	}{
		{
			name:         "nil input returns nil",
			canonicalKey: "agent:main:main",
			aliases:      nil,
			want:         nil,
		},
		{
			name:         "empty input returns nil",
			canonicalKey: "agent:main:main",
			aliases:      []string{},
			want:         nil,
		},
		{
			name:         "trims whitespace",
			canonicalKey: "canonical",
			aliases:      []string{" a ", "\tb\t", "\n c\n"},
			want:         []string{"a", "b", "c"},
		},
		{
			name:         "drops empty entries",
			canonicalKey: "canonical",
			aliases:      []string{"", "  ", "a"},
			want:         []string{"a"},
		},
		{
			name:         "drops duplicates keeping first occurrence",
			canonicalKey: "canonical",
			aliases:      []string{"a", "b", "a", " b "},
			want:         []string{"a", "b"},
		},
		{
			name:         "preserves case so case variants stay distinct",
			canonicalKey: "canonical",
			aliases:      []string{"Alpha", "alpha"},
			want:         []string{"Alpha", "alpha"},
		},
		{
			name:         "removes the canonical key",
			canonicalKey: "agent:main:main",
			aliases:      []string{"agent:main:main", "telegram:123"},
			want:         []string{"telegram:123"},
		},
		{
			name:         "removes the canonical key after trimming both sides",
			canonicalKey: " canonical ",
			aliases:      []string{"canonical", "other"},
			want:         []string{"other"},
		},
		{
			name:         "preserves order",
			canonicalKey: "canonical",
			aliases:      []string{"c", "a", "b"},
			want:         []string{"c", "a", "b"},
		},
		{
			name:         "all entries dropped returns nil",
			canonicalKey: "canonical",
			aliases:      []string{"canonical", "", "   "},
			want:         nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAliases(tt.canonicalKey, tt.aliases)
			if !slices.Equal(got, tt.want) || (got == nil) != (tt.want == nil) {
				t.Errorf("NormalizeAliases(%q, %q) = %q, want %q",
					tt.canonicalKey, tt.aliases, got, tt.want)
			}
		})
	}
}
