package semver

import (
	"fmt"
	"strconv"
	"strings"
)

// Version represents a parsed semantic version.
type Version struct {
	Major      int
	Minor      int
	Patch      int
	Prerelease string
	Build      string
	Raw        string
}

// Parse parses a strict or tolerant semantic version string (e.g. "v1.2.3", "1.2", "1.2.3-rc.1+build").
func Parse(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	if raw == "" {
		return Version{}, fmt.Errorf("empty version string")
	}

	v := raw
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		v = v[1:]
	}

	var build string
	if idx := strings.IndexByte(v, '+'); idx >= 0 {
		build = v[idx+1:]
		v = v[:idx]
		if build == "" {
			return Version{}, fmt.Errorf("invalid version %q: empty build metadata", raw)
		}
	}

	var prerelease string
	if idx := strings.IndexByte(v, '-'); idx >= 0 {
		prerelease = v[idx+1:]
		v = v[:idx]
		if prerelease == "" {
			return Version{}, fmt.Errorf("invalid version %q: empty prerelease", raw)
		}
	}

	parts := strings.Split(v, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return Version{}, fmt.Errorf("invalid version %q: expected 1 to 3 numeric components", raw)
	}

	nums := make([]int, 3)
	for i, part := range parts {
		if part == "" || !isDigits(part) {
			return Version{}, fmt.Errorf("invalid version %q: non-numeric component %q", raw, part)
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return Version{}, fmt.Errorf("invalid version %q: %w", raw, err)
		}
		nums[i] = n
	}

	if prerelease != "" {
		for _, id := range strings.Split(prerelease, ".") {
			if id == "" {
				return Version{}, fmt.Errorf("invalid version %q: empty prerelease identifier", raw)
			}
		}
	}

	return Version{
		Major:      nums[0],
		Minor:      nums[1],
		Patch:      nums[2],
		Prerelease: prerelease,
		Build:      build,
		Raw:        raw,
	}, nil
}

// ParseStrict parses a strict X.Y.Z (with optional v prefix, prerelease, build) version.
func ParseStrict(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	core := raw
	if strings.HasPrefix(core, "v") || strings.HasPrefix(core, "V") {
		core = core[1:]
	}
	if idx := strings.IndexAny(core, "-+"); idx >= 0 {
		core = core[:idx]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("invalid strict semver %q: must have major.minor.patch", raw)
	}
	for _, p := range parts {
		if len(p) > 1 && p[0] == '0' {
			return Version{}, fmt.Errorf("invalid strict semver %q: leading zero in %q", raw, p)
		}
	}
	return Parse(s)
}

// String returns the canonical normalized version string without build metadata if empty.
func (v Version) String() string {
	base := fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Prerelease != "" {
		base += "-" + v.Prerelease
	}
	if v.Build != "" {
		base += "+" + v.Build
	}
	return base
}

// Compare compares two semantic versions per SemVer 2.0.0 precedence rules.
// Returns -1 if a < b, 0 if a == b, and 1 if a > b. Build metadata is ignored.
func Compare(a, b Version) int {
	if a.Major != b.Major {
		return cmpInt(a.Major, b.Major)
	}
	if a.Minor != b.Minor {
		return cmpInt(a.Minor, b.Minor)
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}

	if a.Prerelease == b.Prerelease {
		return 0
	}
	if a.Prerelease == "" {
		return 1
	}
	if b.Prerelease == "" {
		return -1
	}

	aParts := strings.Split(a.Prerelease, ".")
	bParts := strings.Split(b.Prerelease, ".")
	limit := len(aParts)
	if len(bParts) < limit {
		limit = len(bParts)
	}
	for i := 0; i < limit; i++ {
		if c := comparePrereleaseIdent(aParts[i], bParts[i]); c != 0 {
			return c
		}
	}
	return cmpInt(len(aParts), len(bParts))
}

// IsMajorUpgrade reports whether upgrading from `from` to `to` crosses a major version boundary.
func IsMajorUpgrade(from, to Version) bool {
	return Compare(from, to) < 0 && to.Major > from.Major
}

// InRange reports whether v lies strictly after fromExclusive and at or before toInclusive.
func InRange(v, fromExclusive, toInclusive Version) bool {
	return Compare(v, fromExclusive) > 0 && Compare(v, toInclusive) <= 0
}

func comparePrereleaseIdent(a, b string) int {
	aNum, aErr := strconv.Atoi(a)
	bNum, bErr := strconv.Atoi(b)
	aIsNum := aErr == nil && isDigits(a)
	bIsNum := bErr == nil && isDigits(b)

	switch {
	case aIsNum && bIsNum:
		return cmpInt(aNum, bNum)
	case aIsNum && !bIsNum:
		return -1
	case !aIsNum && bIsNum:
		return 1
	default:
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	}
}

func isDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
