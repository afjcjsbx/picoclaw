package agent

import (
	"reflect"
	"testing"
)

func TestAppendArtifactTags(t *testing.T) {
	tests := []struct {
		name     string
		existing []string
		added    []string
		want     []string
	}{
		{
			name:     "no added tags returns existing",
			existing: []string{"[file:/tmp/spill.txt]"},
			added:    nil,
			want:     []string{"[file:/tmp/spill.txt]"},
		},
		{
			name:     "preserves spill tag and appends media tag",
			existing: []string{"[file:/tmp/spill.txt]"},
			added:    []string{"[image:/tmp/photo.png]"},
			want:     []string{"[file:/tmp/spill.txt]", "[image:/tmp/photo.png]"},
		},
		{
			name:     "drops duplicates",
			existing: []string{"[file:/tmp/a.txt]"},
			added:    []string{"[file:/tmp/a.txt]", "[file:/tmp/b.txt]"},
			want:     []string{"[file:/tmp/a.txt]", "[file:/tmp/b.txt]"},
		},
		{
			name:     "empty existing",
			existing: nil,
			added:    []string{"[file:/tmp/a.txt]"},
			want:     []string{"[file:/tmp/a.txt]"},
		},
		{
			name:     "both empty",
			existing: nil,
			added:    nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := appendArtifactTags(tt.existing, tt.added)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("appendArtifactTags() = %v, want %v", got, tt.want)
			}
		})
	}
}
