package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/Hinikaa/depbrief/internal/changelog"
	"github.com/Hinikaa/depbrief/internal/lockfile"
	"github.com/Hinikaa/depbrief/internal/registry"
	"github.com/Hinikaa/depbrief/internal/render"
	"github.com/Hinikaa/depbrief/internal/semver"
)

const (
	exitClean   = 0
	exitFailOn  = 1
	exitErr     = 2
	defaultConc = 6
)

var version = "dev"

type cliConfig struct {
	format      string
	directOnly  bool
	directSet   bool
	majorOnly   bool
	concurrency int
	failOn      string
	noChangelog bool
	ecosystem   string
	positional  []string
}

func main() {
	code := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, registry.Endpoints{}, changelog.Options{})
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, regEndpoints registry.Endpoints, clOpts changelog.Options) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitErr
	}

	sub := args[0]
	if sub == "-h" || sub == "--help" || sub == "help" {
		printUsage(stdout)
		return exitClean
	}
	if sub == "-v" || sub == "--version" || sub == "version" {
		fmt.Fprintf(stdout, "depbrief %s\n", version)
		return exitClean
	}

	cfg, err := parseFlags(sub, args[1:])
	if err != nil {
		if err == flag.ErrHelp {
			printUsage(stdout)
			return exitClean
		}
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitErr
	}

	var report render.Report
	switch sub {
	case "scan":
		dir := "."
		if len(cfg.positional) > 1 {
			fmt.Fprintf(stderr, "error: scan accepts at most one directory argument\n")
			return exitErr
		}
		if len(cfg.positional) == 1 {
			dir = cfg.positional[0]
		}
		report, err = executeScan(ctx, dir, cfg, regEndpoints, clOpts)
	case "diff":
		if len(cfg.positional) != 2 {
			fmt.Fprintf(stderr, "error: diff requires <old-lock> <new-lock>\n")
			return exitErr
		}
		report, err = executeDiff(ctx, cfg.positional[0], cfg.positional[1], cfg, regEndpoints, clOpts)
	default:
		fmt.Fprintf(stderr, "error: unknown subcommand %q\n", sub)
		printUsage(stderr)
		return exitErr
	}

	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return exitErr
	}

	switch cfg.format {
	case "text":
		err = render.Text(stdout, report)
	case "markdown":
		err = render.Markdown(stdout, report)
	case "json":
		err = render.JSON(stdout, report)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error rendering output: %v\n", err)
		return exitErr
	}

	if shouldFail(cfg.failOn, report) {
		return exitFailOn
	}
	return exitClean
}

func parseFlags(sub string, rawArgs []string) (cliConfig, error) {
	cfg := cliConfig{
		format:      "text",
		directOnly:  true,
		concurrency: defaultConc,
		failOn:      "none",
		ecosystem:   "auto",
	}

	boolFlags := map[string]bool{
		"direct-only":  true,
		"major-only":   true,
		"no-changelog": true,
		"h":            true,
		"help":         true,
	}
	reordered := reorderFlags(rawArgs, boolFlags)

	fs := flag.NewFlagSet(sub, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.format, "format", "text", "Output format: text|markdown|json")
	fs.BoolVar(&cfg.directOnly, "direct-only", true, "Filter to direct dependencies when manifest exists")
	fs.BoolVar(&cfg.majorOnly, "major-only", false, "Include major version upgrades only")
	fs.IntVar(&cfg.concurrency, "concurrency", defaultConc, "Maximum concurrent upstream requests")
	fs.StringVar(&cfg.failOn, "fail-on", "none", "Exit 1 condition: breaking|security|none")
	fs.BoolVar(&cfg.noChangelog, "no-changelog", false, "Skip GitHub release/changelog fetching")
	fs.StringVar(&cfg.ecosystem, "ecosystem", "auto", "Ecosystem override: auto|npm|pnpm|cargo|uv|go")

	if err := fs.Parse(reordered); err != nil {
		return cfg, err
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "direct-only" {
			cfg.directSet = true
		}
	})

	cfg.format = strings.ToLower(strings.TrimSpace(cfg.format))
	switch cfg.format {
	case "text", "markdown", "json":
	default:
		return cfg, fmt.Errorf("invalid --format %q (expected text, markdown, or json)", cfg.format)
	}

	cfg.failOn = strings.ToLower(strings.TrimSpace(cfg.failOn))
	switch cfg.failOn {
	case "none", "breaking", "security":
	default:
		return cfg, fmt.Errorf("invalid --fail-on %q (expected breaking, security, or none)", cfg.failOn)
	}

	if cfg.concurrency < 1 {
		return cfg, fmt.Errorf("--concurrency must be >= 1")
	}
	if !lockfile.ValidEcosystem(cfg.ecosystem) {
		return cfg, fmt.Errorf("invalid --ecosystem %q (expected auto, npm, pnpm, cargo, uv, or go)", cfg.ecosystem)
	}

	cfg.positional = fs.Args()
	return cfg, nil
}

// reorderFlags moves flag tokens before positional arguments so Go's flag package parses flags placed after paths.
func reorderFlags(args []string, boolFlags map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			flags = append(flags, arg)
			name := strings.TrimLeft(arg, "-")
			if !strings.Contains(name, "=") && !boolFlags[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		pos = append(pos, arg)
	}
	return append(flags, pos...)
}

func executeScan(ctx context.Context, dir string, cfg cliConfig, regEndpoints registry.Endpoints, clOpts changelog.Options) (render.Report, error) {
	detected, err := lockfile.Detect(dir, cfg.ecosystem)
	if err != nil {
		return render.Report{}, err
	}

	regClient := registry.NewClient(nil, regEndpoints)
	fetcher := changelog.NewFetcher(clOpts)

	var candidates []lockfile.Dependency
	var warnings []string

	for _, d := range detected {
		res, err := lockfile.ParseFile(d.Path, string(d.Ecosystem))
		if err != nil {
			return render.Report{}, err
		}
		filtered, warn := filterDirect(res, cfg)
		if warn != "" {
			warnings = append(warnings, warn)
		}
		candidates = append(candidates, filtered...)
	}

	type scanOutcome struct {
		upgrade *render.UpgradeBrief
		warning string
	}

	outcomes := runConcurrent(ctx, len(candidates), cfg.concurrency, func(idx int) scanOutcome {
		dep := candidates[idx]
		fromVer, err := semver.Parse(dep.Version)
		if err != nil {
			return scanOutcome{warning: fmt.Sprintf("%s: unparseable locked version %q", dep.Name, dep.Version)}
		}

		info, err := regClient.Resolve(ctx, dep)
		if err != nil {
			return scanOutcome{warning: fmt.Sprintf("%s: registry lookup failed: %v", dep.Name, err)}
		}
		if info.LatestVersion == "" {
			return scanOutcome{}
		}
		toVer, err := semver.Parse(info.LatestVersion)
		if err != nil {
			return scanOutcome{warning: fmt.Sprintf("%s: unparseable latest version %q", dep.Name, info.LatestVersion)}
		}
		if semver.Compare(fromVer, toVer) >= 0 {
			return scanOutcome{}
		}
		isMajor := semver.IsMajorUpgrade(fromVer, toVer)
		if cfg.majorOnly && !isMajor {
			return scanOutcome{}
		}

		repoURL := info.RepoURL
		if repoURL == "" {
			repoURL = dep.RepoURL
		}

		brief := render.UpgradeBrief{
			Name:      dep.Name,
			Ecosystem: dep.Ecosystem,
			From:      dep.Version,
			To:        info.LatestVersion,
			Direct:    dep.Direct,
			Major:     isMajor,
			RepoURL:   repoURL,
		}

		if !cfg.noChangelog && repoURL != "" {
			res, err := fetcher.Fetch(ctx, repoURL, dep.Name, fromVer, toVer)
			if err != nil {
				brief.Warning = err.Error()
			} else {
				brief.ChangelogSource = res.Source
				brief.Notes = changelog.Classify(res.Entries)
			}
		}

		return scanOutcome{upgrade: &brief}
	})

	var upgrades []render.UpgradeBrief
	for _, out := range outcomes {
		if out.warning != "" {
			warnings = append(warnings, out.warning)
		}
		if out.upgrade != nil {
			upgrades = append(upgrades, *out.upgrade)
		}
	}
	sortUpgrades(upgrades)

	report := render.Report{
		Mode:     "scan",
		Target:   filepath.Clean(dir),
		Upgrades: upgrades,
		Warnings: warnings,
	}
	report.ComputeSummary(len(candidates))
	return report, nil
}

func executeDiff(ctx context.Context, oldPath, newPath string, cfg cliConfig, regEndpoints registry.Endpoints, clOpts changelog.Options) (render.Report, error) {
	oldRes, err := lockfile.ParseFile(oldPath, cfg.ecosystem)
	if err != nil {
		return render.Report{}, err
	}
	newRes, err := lockfile.ParseFile(newPath, cfg.ecosystem)
	if err != nil {
		return render.Report{}, err
	}
	if oldRes.Ecosystem != newRes.Ecosystem {
		return render.Report{}, fmt.Errorf("ecosystem mismatch between %s (%s) and %s (%s)", oldPath, oldRes.Ecosystem, newPath, newRes.Ecosystem)
	}

	var warnings []string
	newFiltered, warn := filterDirect(newRes, cfg)
	if warn != "" {
		warnings = append(warnings, warn)
	}

	// Pick highest locked version per package name in old lockfile for comparison.
	oldByName := make(map[string]lockfile.Dependency)
	for _, d := range oldRes.Dependencies {
		prev, exists := oldByName[d.Name]
		if !exists {
			oldByName[d.Name] = d
			continue
		}
		prevV, err1 := semver.Parse(prev.Version)
		curV, err2 := semver.Parse(d.Version)
		if err1 == nil && err2 == nil && semver.Compare(curV, prevV) > 0 {
			oldByName[d.Name] = d
		}
	}

	type diffPair struct {
		oldDep  lockfile.Dependency
		newDep  lockfile.Dependency
		fromVer semver.Version
		toVer   semver.Version
		isMajor bool
	}

	var changed []diffPair
	checked := 0
	for _, nd := range newFiltered {
		checked++
		od, exists := oldByName[nd.Name]
		if !exists || od.Version == nd.Version {
			continue
		}
		fromVer, err1 := semver.Parse(od.Version)
		toVer, err2 := semver.Parse(nd.Version)
		if err1 != nil || err2 != nil {
			warnings = append(warnings, fmt.Sprintf("%s: cannot compare %q -> %q", nd.Name, od.Version, nd.Version))
			continue
		}
		if semver.Compare(fromVer, toVer) >= 0 {
			continue
		}
		isMajor := semver.IsMajorUpgrade(fromVer, toVer)
		if cfg.majorOnly && !isMajor {
			continue
		}
		changed = append(changed, diffPair{
			oldDep:  od,
			newDep:  nd,
			fromVer: fromVer,
			toVer:   toVer,
			isMajor: isMajor,
		})
	}

	regClient := registry.NewClient(nil, regEndpoints)
	fetcher := changelog.NewFetcher(clOpts)

	upgrades := runConcurrent(ctx, len(changed), cfg.concurrency, func(idx int) render.UpgradeBrief {
		p := changed[idx]
		repoURL := p.newDep.RepoURL
		if repoURL == "" {
			repoURL = p.oldDep.RepoURL
		}

		brief := render.UpgradeBrief{
			Name:      p.newDep.Name,
			Ecosystem: p.newDep.Ecosystem,
			From:      p.oldDep.Version,
			To:        p.newDep.Version,
			Direct:    p.newDep.Direct,
			Major:     p.isMajor,
			RepoURL:   repoURL,
		}

		if cfg.noChangelog {
			return brief
		}

		if repoURL == "" {
			resolved, err := regClient.ResolveRepoOnly(ctx, p.newDep)
			if err != nil {
				brief.Warning = fmt.Sprintf("repo lookup failed: %v", err)
				return brief
			}
			repoURL = resolved
			brief.RepoURL = repoURL
		}
		if repoURL == "" {
			brief.ChangelogSource = "none"
			return brief
		}

		res, err := fetcher.Fetch(ctx, repoURL, p.newDep.Name, p.fromVer, p.toVer)
		if err != nil {
			brief.Warning = err.Error()
			return brief
		}
		brief.ChangelogSource = res.Source
		brief.Notes = changelog.Classify(res.Entries)
		return brief
	})

	sortUpgrades(upgrades)

	report := render.Report{
		Mode:     "diff",
		Target:   fmt.Sprintf("%s -> %s", filepath.Clean(oldPath), filepath.Clean(newPath)),
		Upgrades: upgrades,
		Warnings: warnings,
	}
	report.ComputeSummary(checked)
	return report, nil
}

func filterDirect(res lockfile.Result, cfg cliConfig) ([]lockfile.Dependency, string) {
	if !cfg.directOnly {
		return res.Dependencies, ""
	}
	if !res.HasDirectInfo {
		if cfg.directSet {
			return nil, fmt.Sprintf("%s: no manifest found to determine direct dependencies", res.LockfilePath)
		}
		// Default to all dependencies when no manifest is available.
		return res.Dependencies, ""
	}
	var out []lockfile.Dependency
	for _, d := range res.Dependencies {
		if d.Direct {
			out = append(out, d)
		}
	}
	return out, ""
}

func runConcurrent[T any](ctx context.Context, count, workers int, fn func(idx int) T) []T {
	results := make([]T, count)
	if count == 0 {
		return results
	}
	if workers > count {
		workers = count
	}
	jobs := make(chan int, count)
	for i := 0; i < count; i++ {
		jobs <- i
	}
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for idx := range jobs {
				if ctx.Err() != nil {
					return
				}
				results[idx] = fn(idx)
			}
		}()
	}
	wg.Wait()
	return results
}

func sortUpgrades(upgrades []render.UpgradeBrief) {
	sort.Slice(upgrades, func(i, j int) bool {
		if upgrades[i].Name != upgrades[j].Name {
			return upgrades[i].Name < upgrades[j].Name
		}
		return upgrades[i].From < upgrades[j].From
	})
}

func shouldFail(failOn string, report render.Report) bool {
	switch failOn {
	case "security":
		return report.Summary.SecurityFindings > 0
	case "breaking":
		return report.Summary.BreakingFindings > 0 || report.Summary.MajorUpgrades > 0
	default:
		return false
	}
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `depbrief - dependency upgrade breaking-change & security brief

Usage:
  depbrief scan [dir] [flags]
  depbrief diff <old-lock> <new-lock> [flags]
  depbrief version

Flags:
  --format text|markdown|json       Output format (default: text)
  --direct-only                     Filter to direct dependencies when manifest exists (default: true)
  --major-only                      Only include major version upgrades
  --concurrency N                   Concurrent upstream requests (default: 6)
  --fail-on breaking|security|none  Exit 1 when matched findings occur (default: none)
  --no-changelog                    Version diff only, skip GitHub release/changelog fetches
  --ecosystem auto|npm|pnpm|cargo|uv|go
                                    Override lockfile ecosystem detection (default: auto)

Exit codes:
  0  Clean
  1  --fail-on condition triggered
  2  Error
`)
}
