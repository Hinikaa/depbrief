package changelog

import (
	"regexp"
	"strings"
)

// Category identifies the risk/impact classification of a changelog item.
type Category string

const (
	CategorySecurity    Category = "security"
	CategoryBreaking    Category = "breaking"
	CategoryDeprecated  Category = "deprecated"
	CategoryRuntimeBump Category = "runtime_bump"
	CategoryNotable     Category = "notable"
)

// Item represents a single cleaned, classified changelog entry.
type Item struct {
	Category Category `json:"category"`
	Version  string   `json:"version"`
	Text     string   `json:"text"`
}

// ClassifiedNotes holds categorized items from a sequence of releases.
type ClassifiedNotes struct {
	Security    []Item `json:"security,omitempty"`
	Breaking    []Item `json:"breaking,omitempty"`
	Deprecated  []Item `json:"deprecated,omitempty"`
	RuntimeBump []Item `json:"runtime_bump,omitempty"`
	Notable     []Item `json:"notable,omitempty"`
}

var (
	cveGHSARe        = regexp.MustCompile(`(?i)\b(CVE-\d{4}-\d{4,}|GHSA-[0-9a-z]{4}-[0-9a-z]{4}-[0-9a-z]{4})\b`)
	securityTermsRe  = regexp.MustCompile(`(?i)\b(security|vulnerability|vulnerabilities|xss|cross-site scripting|sql injection|command injection|code injection|prototype pollution|auth bypass|authentication bypass|authorization bypass|csrf|ssrf|rce|remote code execution|privilege escalation|path traversal|denial of service|dos|arbitrary code)\b`)
	breakingTermsRe  = regexp.MustCompile(`(?i)(breaking change|breaking:|^\s*feat!:|^\s*fix!:|^\s*refactor!:|removed support|remove support|no longer supported|incompatible with|renamed option|removed deprecated)`)
	deprecatedRe     = regexp.MustCompile(`(?i)\b(deprecate|deprecated|deprecation|will be removed)\b`)
	runtimeBumpRe    = regexp.MustCompile(`(?i)\b((require|requires|required|minimum|min|bump|bumped|drop|dropped|dropping)\s+(support\s+for\s+)?(node(\.js)?|python|go|rust|msrv|jdk|java)\s*v?\d+|msrv\s*(to|=|>=)?\s*\d+\.\d+)\b`)
	noiseDependabot  = regexp.MustCompile(`(?i)\b(dependabot|renovate|chore\(deps(?:-dev)?\)|build\(deps(?:-dev)?\)|ci\(deps\)|bump\s+\S+\s+from\s+\S+\s+to\s+\S+|update\s+dependency\s+\S+)\b`)
	noiseBoilerplate = regexp.MustCompile(`(?i)^(full changelog:|all notable changes to this project|the format is based on|this project adheres to semantic versioning|thanks to @|thank you to @|new contributors|.*made their first contribution in\s+https?://)`)
	mdLinkCleanRe    = regexp.MustCompile(`\[([^\]]+)\]\((https?://[^)]+)\)`)
)

// Classify strips noise and categorizes release entries into risk buckets.
func Classify(entries []ReleaseEntry) ClassifiedNotes {
	var out ClassifiedNotes
	seen := make(map[string]bool)

	for _, entry := range entries {
		verStr := entry.Version.String()
		sectionCat := Category("")
		lines := strings.Split(strings.ReplaceAll(entry.Body, "\r\n", "\n"), "\n")

		for _, rawLine := range lines {
			trimmed := strings.TrimSpace(rawLine)
			if trimmed == "" {
				continue
			}

			if m := atxHeadingRe.FindStringSubmatch(trimmed); m != nil {
				headingText := strings.TrimSpace(m[2])
				sectionCat = categoryFromHeading(headingText)
				continue
			}

			// Strip bullet prefix (*, -, +, or numbered list) for uniform matching.
			clean := stripBulletPrefix(trimmed)
			if clean == "" {
				continue
			}
			// Collapse inline markdown links "[PR #12](url)" -> "PR #12" for readable output.
			clean = mdLinkCleanRe.ReplaceAllString(clean, "$1")
			clean = strings.TrimSpace(clean)

			if isNoiseLine(clean) || sectionCat == "ignore" {
				continue
			}

			cat := classifyLine(clean, sectionCat)
			dedupKey := string(cat) + "|" + verStr + "|" + strings.ToLower(clean)
			if seen[dedupKey] {
				continue
			}
			seen[dedupKey] = true

			item := Item{
				Category: cat,
				Version:  verStr,
				Text:     clean,
			}
			switch cat {
			case CategorySecurity:
				out.Security = append(out.Security, item)
			case CategoryBreaking:
				out.Breaking = append(out.Breaking, item)
			case CategoryDeprecated:
				out.Deprecated = append(out.Deprecated, item)
			case CategoryRuntimeBump:
				out.RuntimeBump = append(out.RuntimeBump, item)
			default:
				out.Notable = append(out.Notable, item)
			}
		}
	}

	return out
}

func categoryFromHeading(heading string) Category {
	lower := strings.ToLower(heading)
	switch {
	case strings.Contains(lower, "new contributor"), strings.Contains(lower, "thank"):
		return "ignore"
	case strings.Contains(lower, "security"):
		return CategorySecurity
	case strings.Contains(lower, "breaking"):
		return CategoryBreaking
	case strings.Contains(lower, "deprecat"):
		return CategoryDeprecated
	default:
		return ""
	}
}

func stripBulletPrefix(s string) string {
	switch {
	case strings.HasPrefix(s, "- "):
		return strings.TrimSpace(s[2:])
	case strings.HasPrefix(s, "* "):
		return strings.TrimSpace(s[2:])
	case strings.HasPrefix(s, "+ "):
		return strings.TrimSpace(s[2:])
	default:
		return s
	}
}

func isNoiseLine(line string) bool {
	if line == "---" || line == "***" || line == "___" {
		return true
	}
	if noiseDependabot.MatchString(line) {
		return true
	}
	if noiseBoilerplate.MatchString(line) {
		return true
	}
	lower := strings.ToLower(line)
	if strings.HasPrefix(lower, "chore:") || strings.HasPrefix(lower, "ci:") || strings.HasPrefix(lower, "docs:") || strings.HasPrefix(lower, "style:") || strings.HasPrefix(lower, "test:") {
		return true
	}
	return false
}

func classifyLine(line string, sectionCat Category) Category {
	if cveGHSARe.MatchString(line) || securityTermsRe.MatchString(line) {
		return CategorySecurity
	}
	if sectionCat == CategorySecurity {
		return CategorySecurity
	}
	if breakingTermsRe.MatchString(line) {
		return CategoryBreaking
	}
	if sectionCat == CategoryBreaking {
		return CategoryBreaking
	}
	if deprecatedRe.MatchString(line) || sectionCat == CategoryDeprecated {
		return CategoryDeprecated
	}
	if runtimeBumpRe.MatchString(line) {
		return CategoryRuntimeBump
	}
	return CategoryNotable
}
