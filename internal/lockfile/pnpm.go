package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type pnpmParser struct{}

func (pnpmParser) Ecosystem() Ecosystem { return EcosystemPNPM }

func (pnpmParser) Parse(lockPath string) (Result, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", lockPath, err)
	}

	directSet := make(map[string]bool)
	hasDirectInfo := false

	manifestPath := filepath.Join(filepath.Dir(lockPath), "package.json")
	if mData, err := os.ReadFile(manifestPath); err == nil {
		var pkg packageJSON
		if err := json.Unmarshal(mData, &pkg); err == nil {
			collectMapKeys(directSet, pkg.Dependencies)
			collectMapKeys(directSet, pkg.DevDependencies)
			collectMapKeys(directSet, pkg.OptionalDependencies)
			collectMapKeys(directSet, pkg.PeerDependencies)
			hasDirectInfo = true
		}
	}

	lines := strings.Split(string(data), "\n")
	section := ""
	importerIndent := -1
	depGroupIndent := -1
	inDepGroup := false

	seen := make(map[string]Dependency)

	for _, rawLine := range lines {
		trimmed := strings.TrimSpace(rawLine)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		indent := countLeadingSpaces(rawLine)
		if indent == 0 && strings.HasSuffix(trimmed, ":") {
			section = strings.TrimSuffix(trimmed, ":")
			importerIndent = -1
			depGroupIndent = -1
			inDepGroup = false
			continue
		}

		switch section {
		case "importers":
			if !strings.HasSuffix(trimmed, ":") {
				continue
			}
			key := unquoteYAML(strings.TrimSuffix(trimmed, ":"))
			if importerIndent == -1 || indent <= importerIndent {
				importerIndent = indent
				inDepGroup = false
				hasDirectInfo = true
				continue
			}
			if key == "dependencies" || key == "devDependencies" || key == "optionalDependencies" || key == "peerDependencies" {
				depGroupIndent = indent
				inDepGroup = true
				hasDirectInfo = true
				continue
			}
			if inDepGroup && indent == depGroupIndent+2 {
				directSet[key] = true
			} else if indent <= depGroupIndent {
				inDepGroup = false
			}

		case "packages":
			// Top-level keys directly under "packages:" sit at indent 2.
			if indent != 2 || !strings.HasSuffix(trimmed, ":") {
				continue
			}
			rawKey := unquoteYAML(strings.TrimSuffix(trimmed, ":"))
			name, ver, ok := parsePNPMPackageKey(rawKey)
			if !ok {
				continue
			}
			key := name + "@" + ver
			if _, exists := seen[key]; !exists {
				seen[key] = Dependency{
					Name:      name,
					Ecosystem: EcosystemPNPM,
					Version:   ver,
					Direct:    directSet[name],
				}
			}
		}
	}

	// If importers appeared after packages or package.json was read separately, re-apply directSet.
	deps := make([]Dependency, 0, len(seen))
	for _, d := range seen {
		d.Direct = hasDirectInfo && directSet[d.Name]
		deps = append(deps, d)
	}
	sortDeps(deps)

	return Result{
		Ecosystem:     EcosystemPNPM,
		LockfilePath:  lockPath,
		Dependencies:  deps,
		HasDirectInfo: hasDirectInfo,
	}, nil
}

func parsePNPMPackageKey(raw string) (name, version string, ok bool) {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "/")
	if s == "" {
		return "", "", false
	}

	// Strip peer dependency qualifiers: "pkg@1.2.3(react@18.0.0)" or "pkg@1.2.3_react@18.0.0"
	if idx := strings.IndexByte(s, '('); idx > 0 {
		s = s[:idx]
	}
	if idx := strings.IndexByte(s, '_'); idx > 0 {
		s = s[:idx]
	}

	// pnpm v6 supports both "@scope/name@1.2.3" and older "/@scope/name/1.2.3".
	atIdx := strings.LastIndexByte(s, '@')
	if atIdx > 0 {
		name = s[:atIdx]
		ver := s[atIdx+1:]
		if !isLikelySemver(ver) {
			return "", "", false
		}
		return name, ver, true
	}

	slashIdx := strings.LastIndexByte(s, '/')
	if slashIdx > 0 {
		name = s[:slashIdx]
		ver := s[slashIdx+1:]
		if !isLikelySemver(ver) {
			return "", "", false
		}
		return name, ver, true
	}

	return "", "", false
}

func isLikelySemver(v string) bool {
	if v == "" {
		return false
	}
	c := v[0]
	return (c >= '0' && c <= '9') || c == 'v'
}

func countLeadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

func unquoteYAML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
