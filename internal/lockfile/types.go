package lockfile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ecosystem identifies a package manager ecosystem.
type Ecosystem string

const (
	EcosystemNPM   Ecosystem = "npm"
	EcosystemPNPM  Ecosystem = "pnpm"
	EcosystemCargo Ecosystem = "cargo"
	EcosystemUV    Ecosystem = "uv"
	EcosystemGo    Ecosystem = "go"
)

// Dependency represents a single locked dependency entry.
type Dependency struct {
	Name      string    `json:"name"`
	Ecosystem Ecosystem `json:"ecosystem"`
	Version   string    `json:"version"`
	Direct    bool      `json:"direct"`
	RepoURL   string    `json:"repo_url,omitempty"`
}

// Result holds parsed dependencies and metadata about direct dependency detection.
type Result struct {
	Ecosystem     Ecosystem
	LockfilePath  string
	Dependencies  []Dependency
	HasDirectInfo bool
}

// Parser reads a lockfile and extracts its locked dependencies.
type Parser interface {
	Ecosystem() Ecosystem
	Parse(lockPath string) (Result, error)
}

// DetectedLockfile pairs a lockfile path on disk with its parser.
type DetectedLockfile struct {
	Ecosystem Ecosystem
	Path      string
}

var defaultOrder = []struct {
	ecosystem Ecosystem
	filename  string
}{
	{EcosystemNPM, "package-lock.json"},
	{EcosystemPNPM, "pnpm-lock.yaml"},
	{EcosystemCargo, "Cargo.lock"},
	{EcosystemUV, "uv.lock"},
	{EcosystemGo, "go.mod"},
}

// ParserFor returns the Parser for the given ecosystem.
func ParserFor(eco Ecosystem) (Parser, error) {
	switch eco {
	case EcosystemNPM:
		return npmParser{}, nil
	case EcosystemPNPM:
		return pnpmParser{}, nil
	case EcosystemCargo:
		return cargoParser{}, nil
	case EcosystemUV:
		return uvParser{}, nil
	case EcosystemGo:
		return goModParser{}, nil
	default:
		return nil, fmt.Errorf("unsupported ecosystem %q", eco)
	}
}

// ValidEcosystem reports whether name is a recognized ecosystem or "auto".
func ValidEcosystem(name string) bool {
	switch Ecosystem(strings.ToLower(strings.TrimSpace(name))) {
	case "", "auto", EcosystemNPM, EcosystemPNPM, EcosystemCargo, EcosystemUV, EcosystemGo:
		return true
	default:
		return false
	}
}

// Detect finds lockfiles inside dir. When ecosystem is empty or "auto", all supported lockfiles are checked.
func Detect(dir string, ecosystem string) ([]DetectedLockfile, error) {
	cleanDir := filepath.Clean(dir)
	info, err := os.Stat(cleanDir)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", cleanDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", cleanDir)
	}

	ecoFilter := strings.ToLower(strings.TrimSpace(ecosystem))
	if !ValidEcosystem(ecoFilter) {
		return nil, fmt.Errorf("unknown ecosystem %q", ecosystem)
	}

	var found []DetectedLockfile
	for _, entry := range defaultOrder {
		if ecoFilter != "" && ecoFilter != "auto" && string(entry.ecosystem) != ecoFilter {
			continue
		}
		candidate := filepath.Join(cleanDir, entry.filename)
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			found = append(found, DetectedLockfile{
				Ecosystem: entry.ecosystem,
				Path:      candidate,
			})
		}
	}
	if len(found) == 0 {
		if ecoFilter != "" && ecoFilter != "auto" {
			return nil, fmt.Errorf("no %s lockfile found in %s", ecoFilter, cleanDir)
		}
		return nil, fmt.Errorf("no supported lockfiles found in %s", cleanDir)
	}
	return found, nil
}

// InferEcosystem determines the ecosystem from a lockfile path when ecosystem is empty or "auto".
func InferEcosystem(lockPath string, ecosystem string) (Ecosystem, error) {
	ecoFilter := strings.ToLower(strings.TrimSpace(ecosystem))
	if ecoFilter != "" && ecoFilter != "auto" {
		eco := Ecosystem(ecoFilter)
		if _, err := ParserFor(eco); err != nil {
			return "", err
		}
		return eco, nil
	}

	base := filepath.Base(lockPath)
	for _, entry := range defaultOrder {
		if base == entry.filename {
			return entry.ecosystem, nil
		}
	}
	lower := strings.ToLower(base)
	switch {
	case strings.Contains(lower, "package-lock") && strings.HasSuffix(lower, ".json"):
		return EcosystemNPM, nil
	case strings.Contains(lower, "pnpm-lock") && (strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml")):
		return EcosystemPNPM, nil
	case strings.Contains(lower, "cargo") && strings.HasSuffix(lower, ".lock"):
		return EcosystemCargo, nil
	case strings.Contains(lower, "uv") && strings.HasSuffix(lower, ".lock"):
		return EcosystemUV, nil
	case strings.HasSuffix(lower, "go.mod") || strings.Contains(lower, "go.mod"):
		return EcosystemGo, nil
	default:
		return "", fmt.Errorf("cannot infer ecosystem from %q; pass --ecosystem", lockPath)
	}
}

// ParseFile parses a single lockfile path using an explicit or inferred ecosystem.
func ParseFile(lockPath string, ecosystem string) (Result, error) {
	cleanPath := filepath.Clean(lockPath)
	eco, err := InferEcosystem(cleanPath, ecosystem)
	if err != nil {
		return Result{}, err
	}
	parser, err := ParserFor(eco)
	if err != nil {
		return Result{}, err
	}
	return parser.Parse(cleanPath)
}

func sortDeps(deps []Dependency) {
	sort.Slice(deps, func(i, j int) bool {
		if deps[i].Name != deps[j].Name {
			return deps[i].Name < deps[j].Name
		}
		return deps[i].Version < deps[j].Version
	})
}

func normalizeRepoURL(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.TrimPrefix(s, "git+")
	s = strings.TrimSuffix(s, ".git")
	if strings.HasPrefix(s, "git://github.com/") {
		s = "https://github.com/" + strings.TrimPrefix(s, "git://github.com/")
	}
	if strings.HasPrefix(s, "ssh://git@github.com/") {
		s = "https://github.com/" + strings.TrimPrefix(s, "ssh://git@github.com/")
	}
	if strings.HasPrefix(s, "git@github.com:") {
		s = "https://github.com/" + strings.TrimPrefix(s, "git@github.com:")
	}
	if idx := strings.Index(s, "#"); idx >= 0 {
		s = s[:idx]
	}
	s = strings.TrimRight(s, "/")
	if strings.Contains(s, "github.com/") {
		idx := strings.Index(s, "github.com/")
		rest := strings.TrimPrefix(s[idx:], "github.com/")
		parts := strings.Split(rest, "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			return "https://github.com/" + parts[0] + "/" + parts[1]
		}
	}
	return ""
}
