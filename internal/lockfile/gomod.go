package lockfile

import (
	"fmt"
	"os"
	"strings"
)

type goModParser struct{}

func (goModParser) Ecosystem() Ecosystem { return EcosystemGo }

func (goModParser) Parse(lockPath string) (Result, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", lockPath, err)
	}

	seen := make(map[string]Dependency)
	inRequireBlock := false

	lines := strings.Split(string(data), "\n")
	for _, raw := range lines {
		comment := ""
		code := raw
		if idx := strings.Index(raw, "//"); idx >= 0 {
			comment = raw[idx+2:]
			code = raw[:idx]
		}
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}

		if inRequireBlock {
			if code == ")" {
				inRequireBlock = false
				continue
			}
			if mod, ver, ok := parseGoRequireFields(code); ok {
				addGoDep(seen, mod, ver, comment)
			}
			continue
		}

		if strings.HasPrefix(code, "require") {
			rest := strings.TrimSpace(strings.TrimPrefix(code, "require"))
			if rest == "(" {
				inRequireBlock = true
				continue
			}
			if strings.HasPrefix(rest, "(") && strings.HasSuffix(rest, ")") {
				inner := strings.TrimSpace(rest[1 : len(rest)-1])
				if mod, ver, ok := parseGoRequireFields(inner); ok {
					addGoDep(seen, mod, ver, comment)
				}
				continue
			}
			if mod, ver, ok := parseGoRequireFields(rest); ok {
				addGoDep(seen, mod, ver, comment)
			}
		}
	}

	deps := make([]Dependency, 0, len(seen))
	for _, d := range seen {
		deps = append(deps, d)
	}
	sortDeps(deps)

	return Result{
		Ecosystem:     EcosystemGo,
		LockfilePath:  lockPath,
		Dependencies:  deps,
		HasDirectInfo: true,
	}, nil
}

func parseGoRequireFields(s string) (modulePath, version string, ok bool) {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return "", "", false
	}
	return fields[0], fields[1], true
}

func addGoDep(seen map[string]Dependency, mod, ver, comment string) {
	indirect := false
	for _, tok := range strings.Fields(comment) {
		if tok == "indirect" {
			indirect = true
			break
		}
	}
	repoURL := ""
	if strings.HasPrefix(mod, "github.com/") {
		repoURL = normalizeRepoURL("https://" + mod)
	}
	key := mod + "@" + ver
	if prev, exists := seen[key]; exists {
		if !indirect && !prev.Direct {
			prev.Direct = true
			seen[key] = prev
		}
		return
	}
	seen[key] = Dependency{
		Name:      mod,
		Ecosystem: EcosystemGo,
		Version:   ver,
		Direct:    !indirect,
		RepoURL:   repoURL,
	}
}
