package updater

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		name    string
		current string
		latest  string
		want    int
		ok      bool
	}{
		{name: "same release", current: "v0.5.6", latest: "v0.5.6", want: 0, ok: true},
		{name: "current older", current: "v0.5.4", latest: "v0.5.6", want: -1, ok: true},
		{name: "current newer", current: "v0.5.7", latest: "v0.5.6", want: 1, ok: true},
		{name: "missing v prefix", current: "0.5.4", latest: "v0.5.6", want: -1, ok: true},
		{name: "minor and patch ordering", current: "v0.10.0", latest: "v0.9.9", want: 1, ok: true},
		{
			name:    "git describe is ahead of its base tag",
			current: "v0.5.4-128-ge5915850",
			latest:  "v0.5.4",
			want:    1,
			ok:      true,
		},
		{
			name:    "git describe is behind a newer release",
			current: "v0.5.4-128-ge5915850",
			latest:  "v0.5.6",
			want:    -1,
			ok:      true,
		},
		{
			name:    "dirty git describe is ahead of its base tag",
			current: "v0.5.4-3-gabc1234-dirty",
			latest:  "v0.5.4",
			want:    1,
			ok:      true,
		},
		{
			name:    "git describe of a prerelease tag",
			current: "v1.0.0-rc.1-2-gabc1234",
			latest:  "v1.0.0-rc.1",
			want:    1,
			ok:      true,
		},
		{name: "release beats prerelease", current: "v1.0.0-rc.1", latest: "v1.0.0", want: -1, ok: true},
		{name: "prerelease numeric ordering", current: "v1.0.0-rc.2", latest: "v1.0.0-rc.10", want: -1, ok: true},
		{
			name:    "longer prerelease wins equal prefix",
			current: "v1.0.0-alpha",
			latest:  "v1.0.0-alpha.1",
			want:    -1,
			ok:      true,
		},
		{
			name:    "numeric prerelease sorts before alphanumeric",
			current: "v1.0.0-2",
			latest:  "v1.0.0-alpha",
			want:    -1,
			ok:      true,
		},
		{
			name:    "formatted version with git commit",
			current: "v0.5.4-128-ge5915850 (git: e5915850)",
			latest:  "v0.5.6",
			want:    -1,
			ok:      true,
		},
		{name: "dev is not comparable", current: "dev", latest: "v0.5.6", want: 0, ok: false},
		{name: "nightly tag is not comparable", current: "v0.5.6", latest: "nightly", want: 0, ok: false},
		{name: "malformed current", current: "v0.5", latest: "v0.5.6", want: 0, ok: false},
		{name: "malformed latest", current: "v0.5.6", latest: "latest", want: 0, ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := compareVersions(tc.current, tc.latest)
			assert.Equal(t, tc.ok, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseVersion(t *testing.T) {
	tests := []struct {
		name           string
		input          string
		wantOK         bool
		wantCore       [3]uint64
		wantPrerelease []string
		wantCommits    uint64
	}{
		{
			name:     "release tag",
			input:    "v1.2.3",
			wantOK:   true,
			wantCore: [3]uint64{1, 2, 3},
		},
		{
			name:           "prerelease tag",
			input:          "v0.0.0-nightly.20260310.b89f6445",
			wantOK:         true,
			wantPrerelease: []string{"nightly", "20260310", "b89f6445"},
		},
		{
			name:        "git describe",
			input:       "v0.5.4-128-ge5915850",
			wantOK:      true,
			wantCore:    [3]uint64{0, 5, 4},
			wantCommits: 128,
		},
		{
			name:           "dirty git describe with prerelease base",
			input:          "v1.0.0-rc.1-4-gabc1234-dirty",
			wantOK:         true,
			wantCore:       [3]uint64{1, 0, 0},
			wantPrerelease: []string{"rc", "1"},
			wantCommits:    4,
		},
		{name: "dev", input: "dev", wantOK: false},
		{name: "nightly", input: "nightly", wantOK: false},
		{name: "empty", input: "", wantOK: false},
		{name: "missing patch", input: "v1.2", wantOK: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseVersion(tc.input)
			assert.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			assert.Equal(t, tc.wantCore, [3]uint64{got.major, got.minor, got.patch})
			assert.Equal(t, tc.wantPrerelease, got.prerelease)
			assert.Equal(t, tc.wantCommits, got.commitsAhead)
		})
	}
}
