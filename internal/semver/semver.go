// Package semver parses the release versions dropsite is tagged with
// (MAJOR.MINOR.PATCH with an optional pre-release, an optional leading "v")
// and compares them. Development builds ("dev", "dev+<sha>") are not
// release versions and never parse.
package semver

import (
	"strconv"
	"strings"
)

// Version is a parsed release version. Build metadata is dropped.
type Version struct {
	Major, Minor, Patch int
	Pre                 string // pre-release without the leading "-", e.g. "rc.1"
}

// Parse reads "1.2.3", "v1.2.3", "1.2.3-rc.1" or "1.2.3+meta".
func Parse(s string) (Version, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v Version
	if i := strings.IndexByte(s, '-'); i >= 0 {
		v.Pre = s[i+1:]
		s = s[:i]
		if v.Pre == "" {
			return Version{}, false
		}
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return Version{}, false
	}
	nums := [3]int{}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || p == "" || (len(p) > 1 && p[0] == '0') || p[0] == '+' {
			return Version{}, false
		}
		nums[i] = n
	}
	v.Major, v.Minor, v.Patch = nums[0], nums[1], nums[2]
	return v, true
}

// String formats the version without a leading "v".
func (v Version) String() string {
	s := strconv.Itoa(v.Major) + "." + strconv.Itoa(v.Minor) + "." + strconv.Itoa(v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare returns -1, 0 or 1. A pre-release sorts before its release;
// pre-releases of the same version compare as plain strings.
func Compare(a, b Version) int {
	for _, d := range [3]int{a.Major - b.Major, a.Minor - b.Minor, a.Patch - b.Patch} {
		if d != 0 {
			return sign(d)
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1
	case b.Pre == "":
		return -1
	}
	return strings.Compare(a.Pre, b.Pre)
}

// MinorApart reports whether a and b differ in the major or minor number.
func MinorApart(a, b Version) bool {
	return a.Major != b.Major || a.Minor != b.Minor
}

func sign(d int) int {
	if d < 0 {
		return -1
	}
	return 1
}
