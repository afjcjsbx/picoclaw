package updater

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
)

// version is a comparable release version. Besides plain release tags such as
// v0.5.6, it understands the git-describe builds produced by the Makefile
// (v0.5.4-128-ge5915850), where the build is ahead of the tag it is based on.
type version struct {
	major, minor, patch uint64
	prerelease          []string
	commitsAhead        uint64
}

var (
	versionTagPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)
	prereleasePattern = regexp.MustCompile(`^[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*$`)
	gitDescribeSuffix = regexp.MustCompile(`-(\d+)-g[0-9a-fA-F]+(?:-dirty)?$`)
)

// parseVersion parses a release tag or git-describe build version. It returns
// false for values that cannot be ordered, such as "dev" or the "nightly" tag.
func parseVersion(s string) (version, bool) {
	var v version

	s = strings.TrimSpace(s)
	// FormatVersion appends " (git: <sha>)" to the version; ignore it.
	if i := strings.Index(s, " (git: "); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimPrefix(s, "v")
	if s == "" || s == "dev" {
		return version{}, false
	}

	if m := gitDescribeSuffix.FindStringSubmatchIndex(s); m != nil {
		commits, err := strconv.ParseUint(s[m[2]:m[3]], 10, 64)
		if err != nil {
			return version{}, false
		}
		v.commitsAhead = commits
		s = s[:m[0]]
	}

	core := s
	prerelease := ""
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core, prerelease = s[:i], s[i+1:]
	}

	m := versionTagPattern.FindStringSubmatch(core)
	if m == nil {
		return version{}, false
	}
	nums := make([]uint64, 0, 3)
	for _, part := range m[1:] {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return version{}, false
		}
		nums = append(nums, n)
	}
	v.major, v.minor, v.patch = nums[0], nums[1], nums[2]

	if prerelease != "" {
		if !prereleasePattern.MatchString(prerelease) {
			return version{}, false
		}
		v.prerelease = strings.Split(prerelease, ".")
	}
	return v, true
}

// compareVersions reports how current relates to latest: -1 when current is
// older, 0 when they are the same release, and 1 when current is newer. The
// bool is false when either value cannot be compared as a version.
func compareVersions(current, latest string) (int, bool) {
	currentV, ok := parseVersion(current)
	if !ok {
		return 0, false
	}
	latestV, ok := parseVersion(latest)
	if !ok {
		return 0, false
	}

	if c := currentV.compare(latestV); c != 0 {
		return c, true
	}

	// Same base release: a git-describe build contains commits made after the
	// tag, so it is newer than the plain tag it is based on.
	switch {
	case currentV.commitsAhead > 0 && latestV.commitsAhead == 0:
		return 1, true
	case currentV.commitsAhead == 0 && latestV.commitsAhead > 0:
		return -1, true
	default:
		return 0, true
	}
}

func (v version) compare(other version) int {
	if c := cmp.Compare(v.major, other.major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.minor, other.minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.patch, other.patch); c != 0 {
		return c
	}
	return comparePrerelease(v.prerelease, other.prerelease)
}

// comparePrerelease follows SemVer precedence: a release sorts after any of
// its prereleases, numeric identifiers sort numerically, numeric identifiers
// sort before alphanumeric ones, and a shorter equal prefix sorts first.
func comparePrerelease(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := comparePrereleaseIdentifier(a[i], b[i]); c != 0 {
			return c
		}
	}
	return cmp.Compare(len(a), len(b))
}

func comparePrereleaseIdentifier(a, b string) int {
	aNum, aErr := strconv.ParseUint(a, 10, 64)
	bNum, bErr := strconv.ParseUint(b, 10, 64)
	switch {
	case aErr == nil && bErr == nil:
		return cmp.Compare(aNum, bNum)
	case aErr == nil:
		return -1
	case bErr == nil:
		return 1
	default:
		return strings.Compare(a, b)
	}
}
