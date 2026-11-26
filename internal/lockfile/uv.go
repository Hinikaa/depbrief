package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type uvParser struct{}

func (uvParser) Ecosystem() Ecosystem { return EcosystemUV }

func (uvParser) Parse(lockPath string) (Result, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", lockPath, err)
	}

	directSet := make(map[string]bool)
	hasDirectInfo := false

	pyprojectPath := filepath.Join(filepath.Dir(lockPath), "pyproject.toml")
	if mData, err := os.ReadFile(pyprojectPath); err == nil {
		for k := range parsePyprojectDirectDeps(string(mData)) {
			directSet[k] = true
		}
		hasDirectInfo = true
	}

	type uvPkg struct {
		name    string
		version string
		source  string
		deps    []string
	}

	var pkgs []uvPkg
	var cur *uvPkg
	inDepsArray := false

	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if line == "[[package]]" {
			if cur != nil {
				pkgs = append(pkgs, *cur)
			}
			cur = &uvPkg{}
			inDepsArray = false
			continue
		}
		if strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "[[") {
			if cur != nil && line == "[package.metadata]" {
				// Keep current package context if metadata table follows, or close on new section.
				continue
			}
			inDepsArray = false
			continue
		}
		if cur == nil {
			continue
		}
		if inDepsArray {
			if name := extractInlineTableField(strings.TrimSuffix(line, ","), "name"); name != "" {
				cur.deps = append(cur.deps, name)
			}
			if strings.Contains(line, "]") {
				inDepsArray = false
			}
			continue
		}

		k, v, ok := parseTOMLKeyValue(line)
		if !ok {
			continue
		}
		switch k {
		case "name":
			cur.name = unquoteTOMLString(v)
		case "version":
			cur.version = unquoteTOMLString(v)
		case "source":
			cur.source = v
		case "dependencies":
			if strings.HasPrefix(v, "[") {
				if strings.Contains(v, "]") {
					cur.deps = append(cur.deps, extractNamesFromInlineArray(v)...)
				} else {
					inDepsArray = true
				}
			}
		}
	}
	if cur != nil {
		pkgs = append(pkgs, *cur)
	}

	for _, p := range pkgs {
		if isUVLocalProject(p.source) && len(p.deps) > 0 {
			for _, depName := range p.deps {
				directSet[normalizePyName(depName)] = true
			}
			hasDirectInfo = true
		}
	}

	seen := make(map[string]Dependency)
	for _, p := range pkgs {
		if p.name == "" || p.version == "" || isUVLocalProject(p.source) {
			continue
		}
		key := p.name + "@" + p.version
		if _, exists := seen[key]; !exists {
			seen[key] = Dependency{
				Name:      p.name,
				Ecosystem: EcosystemUV,
				Version:   p.version,
				Direct:    hasDirectInfo && directSet[normalizePyName(p.name)],
			}
		}
	}

	deps := make([]Dependency, 0, len(seen))
	for _, d := range seen {
		deps = append(deps, d)
	}
	sortDeps(deps)

	return Result{
		Ecosystem:     EcosystemUV,
		LockfilePath:  lockPath,
		Dependencies:  deps,
		HasDirectInfo: hasDirectInfo,
	}, nil
}

func isUVLocalProject(source string) bool {
	return strings.Contains(source, "virtual") || strings.Contains(source, "editable")
}

func extractNamesFromInlineArray(v string) []string {
	var names []string
	for _, item := range strings.Split(v, "}") {
		if idx := strings.IndexByte(item, '{'); idx >= 0 {
			item = item[idx:] + "}"
			if name := extractInlineTableField(item, "name"); name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func parsePyprojectDirectDeps(content string) map[string]bool {
	direct := make(map[string]bool)
	section := ""
	inArray := false

	collectStrings := func(chunk string) {
		inDouble := false
		inSingle := false
		start := -1
		for i := 0; i < len(chunk); i++ {
			c := chunk[i]
			if c == '"' && !inSingle {
				if inDouble {
					req := chunk[start:i]
					if name := parsePEP508Name(req); name != "" {
						direct[normalizePyName(name)] = true
					}
					inDouble = false
				} else {
					inDouble = true
					start = i + 1
				}
			} else if c == '\'' && !inDouble {
				if inSingle {
					req := chunk[start:i]
					if name := parsePEP508Name(req); name != "" {
						direct[normalizePyName(name)] = true
					}
					inSingle = false
				} else {
					inSingle = true
					start = i + 1
				}
			}
		}
	}

	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			inArray = false
			continue
		}
		if inArray {
			collectStrings(line)
			if strings.Contains(line, "]") {
				inArray = false
			}
			continue
		}
		k, v, ok := parseTOMLKeyValue(line)
		if !ok {
			continue
		}
		isTarget := (section == "project" && k == "dependencies") ||
			section == "project.optional-dependencies" ||
			section == "dependency-groups" ||
			(section == "tool.uv" && k == "dev-dependencies")
		if !isTarget {
			continue
		}
		if strings.HasPrefix(v, "[") {
			collectStrings(v)
			if !strings.Contains(v, "]") {
				inArray = true
			}
		}
	}
	return direct
}

func parsePEP508Name(spec string) string {
	s := strings.TrimSpace(spec)
	if s == "" {
		return ""
	}
	end := len(s)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[', '=', '<', '>', '!', '~', ';', ' ', '@':
			end = i
			return s[:end]
		}
	}
	return s[:end]
}

// PEP 503 package name normalization: lowercase and collapse runs of [-_.] to "-".
func normalizePyName(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	prevDash := false
	for i := 0; i < len(lower); i++ {
		c := lower[i]
		if c == '-' || c == '_' || c == '.' {
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
			continue
		}
		prevDash = false
		b.WriteByte(c)
	}
	return b.String()
}
