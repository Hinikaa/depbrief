package changelog

import (
	"regexp"
	"sort"
	"strings"

	"github.com/Hinikaa/depbrief/internal/semver"
)

// ReleaseEntry represents a raw release or changelog section for a single version.
type ReleaseEntry struct {
	Version semver.Version
	Tag     string
	URL     string
	Body    string
}

var (
	atxHeadingRe = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	versionToken = regexp.MustCompile(`(?i)\bv?(\d+\.\d+(?:\.\d+)?(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)\b`)
)

// SliceMarkdown extracts version sections from a CHANGELOG.md-style document where from < v <= to.
// Results are ordered from newest version to oldest version.
func SliceMarkdown(content string, from, to semver.Version) []ReleaseEntry {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")

	type section struct {
		ver   semver.Version
		tag   string
		level int
		lines []string
	}

	var sections []section
	var current *section

	flush := func() {
		if current != nil {
			sections = append(sections, *current)
			current = nil
		}
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if m := atxHeadingRe.FindStringSubmatch(line); m != nil {
			level := len(m[1])
			title := strings.TrimSpace(m[2])
			if ver, tag, ok := parseHeadingVersion(title); ok {
				flush()
				current = &section{ver: ver, tag: tag, level: level}
				continue
			}
			if current != nil && level <= current.level {
				flush()
				continue
			}
		} else if i+1 < len(lines) && isSetextUnderline(lines[i+1]) {
			title := strings.TrimSpace(line)
			level := 1
			if strings.HasPrefix(strings.TrimSpace(lines[i+1]), "-") {
				level = 2
			}
			if ver, tag, ok := parseHeadingVersion(title); ok {
				flush()
				current = &section{ver: ver, tag: tag, level: level}
				i++
				continue
			}
			if current != nil && level <= current.level {
				flush()
				i++
				continue
			}
		}

		if current != nil {
			current.lines = append(current.lines, line)
		}
	}
	flush()

	var matched []ReleaseEntry
	for _, s := range sections {
		if !semver.InRange(s.ver, from, to) {
			continue
		}
		body := strings.TrimSpace(strings.Join(s.lines, "\n"))
		matched = append(matched, ReleaseEntry{
			Version: s.ver,
			Tag:     s.tag,
			Body:    body,
		})
	}

	sort.SliceStable(matched, func(i, j int) bool {
		return semver.Compare(matched[i].Version, matched[j].Version) > 0
	})
	return matched
}

func isSetextUnderline(line string) bool {
	s := strings.TrimSpace(line)
	if len(s) < 3 {
		return false
	}
	ch := s[0]
	if ch != '=' && ch != '-' {
		return false
	}
	for i := 1; i < len(s); i++ {
		if s[i] != ch {
			return false
		}
	}
	return true
}

func parseHeadingVersion(title string) (semver.Version, string, bool) {
	cleaned := strings.TrimSpace(title)
	// Strip markdown link wrapper: "[2.1.0](https://...)" -> "2.1.0"
	if strings.HasPrefix(cleaned, "[") {
		if closeIdx := strings.IndexByte(cleaned, ']'); closeIdx > 1 {
			cleaned = cleaned[1:closeIdx]
		}
	}
	// Strip trailing markdown anchor/compare links or dates after whitespace.
	cleaned = strings.TrimSpace(cleaned)
	if strings.EqualFold(cleaned, "unreleased") {
		return semver.Version{}, "", false
	}

	// Reject headings that look like regular prose containing a version number unless the version is at the start.
	loc := versionToken.FindStringSubmatchIndex(cleaned)
	if loc == nil {
		return semver.Version{}, "", false
	}
	prefix := strings.TrimSpace(cleaned[:loc[0]])
	if prefix != "" && !strings.EqualFold(prefix, "version") && !strings.EqualFold(prefix, "release") {
		return semver.Version{}, "", false
	}

	rawVer := cleaned[loc[2]:loc[3]]
	ver, err := semver.Parse(rawVer)
	if err != nil {
		return semver.Version{}, "", false
	}
	return ver, cleaned[loc[0]:loc[1]], true
}
