package lockfile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type npmParser struct{}

func (npmParser) Ecosystem() Ecosystem { return EcosystemNPM }

type packageLockJSON struct {
	LockfileVersion int                         `json:"lockfileVersion"`
	Packages        map[string]npmLockPackage   `json:"packages"`
	Dependencies    map[string]npmLegacyLockDep `json:"dependencies"`
}

type npmLockPackage struct {
	Name                 string            `json:"name"`
	Version              string            `json:"version"`
	Link                 bool              `json:"link"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

type npmLegacyLockDep struct {
	Version      string                      `json:"version"`
	Requires     map[string]string           `json:"requires"`
	Dependencies map[string]npmLegacyLockDep `json:"dependencies"`
}

type packageJSON struct {
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	PeerDependencies     map[string]string `json:"peerDependencies"`
}

func (npmParser) Parse(lockPath string) (Result, error) {
	data, err := os.ReadFile(lockPath)
	if err != nil {
		return Result{}, fmt.Errorf("read %s: %w", lockPath, err)
	}

	var lock packageLockJSON
	if err := json.Unmarshal(data, &lock); err != nil {
		return Result{}, fmt.Errorf("parse %s: %w", lockPath, err)
	}
	if lock.LockfileVersion < 2 && len(lock.Packages) == 0 {
		return Result{}, fmt.Errorf("%s: unsupported package-lock.json version %d (require v2 or v3)", lockPath, lock.LockfileVersion)
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

	if root, ok := lock.Packages[""]; ok {
		if len(root.Dependencies)+len(root.DevDependencies)+len(root.OptionalDependencies)+len(root.PeerDependencies) > 0 {
			collectMapKeys(directSet, root.Dependencies)
			collectMapKeys(directSet, root.DevDependencies)
			collectMapKeys(directSet, root.OptionalDependencies)
			collectMapKeys(directSet, root.PeerDependencies)
			hasDirectInfo = true
		}
	}

	seen := make(map[string]Dependency)
	for pkgPath, entry := range lock.Packages {
		if pkgPath == "" || entry.Link || entry.Version == "" {
			continue
		}
		name := entry.Name
		if name == "" {
			name = npmNameFromPath(pkgPath)
		}
		if name == "" {
			continue
		}
		isTopLevel := pkgPath == "node_modules/"+name
		direct := hasDirectInfo && directSet[name] && isTopLevel

		key := name + "@" + entry.Version
		if prev, exists := seen[key]; exists {
			if direct && !prev.Direct {
				prev.Direct = true
				seen[key] = prev
			}
			continue
		}
		seen[key] = Dependency{
			Name:      name,
			Ecosystem: EcosystemNPM,
			Version:   entry.Version,
			Direct:    direct,
		}
	}

	deps := make([]Dependency, 0, len(seen))
	for _, d := range seen {
		deps = append(deps, d)
	}
	sortDeps(deps)

	return Result{
		Ecosystem:     EcosystemNPM,
		LockfilePath:  lockPath,
		Dependencies:  deps,
		HasDirectInfo: hasDirectInfo,
	}, nil
}

func npmNameFromPath(pkgPath string) string {
	const marker = "node_modules/"
	idx := strings.LastIndex(pkgPath, marker)
	if idx < 0 {
		return ""
	}
	return pkgPath[idx+len(marker):]
}

func collectMapKeys(dst map[string]bool, src map[string]string) {
	for k := range src {
		dst[k] = true
	}
}
