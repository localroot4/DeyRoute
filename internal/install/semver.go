package install

import (
	"strconv"
	"strings"
)

// semver is a parsed Semantic Version (build metadata dropped).
type semver struct {
	nums [3]uint64
	pre  []string
}

// parseSemver accepts "1.2.3", "v1.2.3", "1.2" (patch 0), "1.2.3-rc.1+meta"
// and tag forms with a path prefix such as "app/v2.12.3".
func parseSemver(s string) (semver, bool) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimPrefix(s, "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	var v semver
	core := s
	if i := strings.IndexByte(s, '-'); i >= 0 {
		core = s[:i]
		pre := s[i+1:]
		if pre == "" {
			return semver{}, false
		}
		v.pre = strings.Split(pre, ".")
		for _, p := range v.pre {
			if p == "" {
				return semver{}, false
			}
		}
	}
	parts := strings.Split(core, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return semver{}, false
	}
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return semver{}, false
		}
		v.nums[i] = n
	}
	return v, true
}

func cmpUint(a, b uint64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// comparePre orders pre-release identifiers per SemVer 2.0 §11.
func comparePre(a, b []string) int {
	switch {
	case len(a) == 0 && len(b) == 0:
		return 0
	case len(a) == 0:
		return 1 // a release is newer than any of its pre-releases
	case len(b) == 0:
		return -1
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		an, aerr := strconv.ParseUint(a[i], 10, 64)
		bn, berr := strconv.ParseUint(b[i], 10, 64)
		switch {
		case aerr == nil && berr == nil:
			if c := cmpUint(an, bn); c != 0 {
				return c
			}
		case aerr == nil:
			return -1 // numeric identifiers sort before alphanumeric ones
		case berr == nil:
			return 1
		default:
			if c := strings.Compare(a[i], b[i]); c != 0 {
				return c
			}
		}
	}
	return cmpUint(uint64(len(a)), uint64(len(b)))
}

// CompareVersions compares two SemVer strings: -1, 0 or 1, and ok=false when
// either does not parse (e.g. "dev").
func CompareVersions(a, b string) (int, bool) {
	va, ok1 := parseSemver(a)
	vb, ok2 := parseSemver(b)
	if !ok1 || !ok2 {
		return 0, false
	}
	for i := 0; i < 3; i++ {
		if c := cmpUint(va.nums[i], vb.nums[i]); c != 0 {
			return c, true
		}
	}
	return comparePre(va.pre, vb.pre), true
}

// NewerThan reports whether version a is strictly newer than b by SemVer.
// Unparseable versions ("dev") are never newer; a parseable release is newer
// than an unparseable current version so dev builds can update.
func NewerThan(a, b string) bool {
	if c, ok := CompareVersions(a, b); ok {
		return c > 0
	}
	_, okA := parseSemver(a)
	_, okB := parseSemver(b)
	return okA && !okB
}
