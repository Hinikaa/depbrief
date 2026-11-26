package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type cargoParser struct{}

func (cargoParser) Ecosystem() Ecosystem { return EcosystemCargo }

func (cargoParser) Parse(lockPath string) (Result, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", lockPath, err)
	}

	directSet := make(map[string]bool)
	hasDirectInfo := false
	rootPkgName := ""

	cargoTomlPath := filepath.Join(filepath.Dir(lockPath), "Cargo.toml")
	if mData, err := os.ReadFile(cargoTomlPath); err == nil {
		rootPkgName, directSet = parseCargoToml(string(mData))
		hasDirectInfo = true
	}

	type cargoPkg struct {
		name    string
		version string
		source  string
	}

	var pkgs []cargoPkg
	var cur *cargoPkg

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
			cur = &cargoPkg{}
			continue
		}
		if strings.HasPrefix(line, "[") {
			if cur != nil {
				pkgs = append(pkgs, *cur)
				cur = nil
			}
			continue
		}
		if cur == nil {
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
			cur.source = unquoteTOMLString(v)
		}
	}
	if cur != nil {
		pkgs = append(pkgs, *cur)
	}

	seen := make(map[string]Dependency)
	for _, p := range pkgs {
		if p.name == "" || p.version == "" {
			continue
		}
		if rootPkgName != "" && p.name == rootPkgName && p.source == "" {
			continue
		}
		repoURL := ""
		if strings.HasPrefix(p.source, "git+") {
			repoURL = normalizeRepoURL(p.source)
		}
		key := p.name + "@" + p.version
		if _, exists := seen[key]; !exists {
			seen[key] = Dependency{
				Name:      p.name,
				Ecosystem: EcosystemCargo,
				Version:   p.version,
				Direct:    hasDirectInfo && directSet[p.name],
				RepoURL:   repoURL,
			}
		}
	}

	deps := make([]Dependency, 0, len(seen))
	for _, d := range seen {
		deps = append(deps, d)
	}
	sortDeps(deps)

	return Result{
		Ecosystem:     EcosystemCargo,
		LockfilePath:  lockPath,
		Dependencies:  deps,
		HasDirectInfo: hasDirectInfo,
	}, nil
}

func parseCargoToml(content string) (pkgName string, direct map[string]bool) {
	direct = make(map[string]bool)
	section := ""
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(stripTOMLComment(raw))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		k, v, ok := parseTOMLKeyValue(line)
		if !ok {
			continue
		}
		if section == "package" && k == "name" {
			pkgName = unquoteTOMLString(v)
			continue
		}
		if isCargoDepsSection(section) {
			name := unquoteTOMLString(k)
			if idx := strings.IndexByte(name, '.'); idx > 0 {
				name = name[:idx]
			}
			// Handle renamed dependency `alias = { package = "real_name", ... }`.
			if realPkg := extractInlineTableField(v, "package"); realPkg != "" {
				direct[realPkg] = true
			} else if name != "" {
				direct[name] = true
			}
		}
	}
	return pkgName, direct
}

func isCargoDepsSection(section string) bool {
	return section == "dependencies" ||
		section == "dev-dependencies" ||
		section == "build-dependencies" ||
		section == "workspace.dependencies" ||
		strings.HasSuffix(section, ".dependencies") ||
		strings.HasSuffix(section, ".dev-dependencies") ||
		strings.HasSuffix(section, ".build-dependencies")
}

func extractInlineTableField(val, field string) string {
	if !strings.HasPrefix(val, "{") {
		return ""
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(val, "{"), "}")
	for _, part := range strings.Split(inner, ",") {
		k, v, ok := parseTOMLKeyValue(strings.TrimSpace(part))
		if ok && k == field {
			return unquoteTOMLString(v)
		}
	}
	return ""
}

func stripTOMLComment(line string) string {
	inSingle := false
	inDouble := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' && inDouble {
			escaped = true
			continue
		}
		if c == '\'' && !inDouble {
			inSingle = !inSingle
		} else if c == '"' && !inSingle {
			inDouble = !inDouble
		} else if c == '#' && !inSingle && !inDouble {
			return line[:i]
		}
	}
	return line
}

func parseTOMLKeyValue(line string) (string, string, bool) {
	idx := strings.IndexByte(line, '=')
	if idx <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:idx])
	v := strings.TrimSpace(line[idx+1:])
	return k, v, true
}

func unquoteTOMLString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
