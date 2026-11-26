package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/Hinikaa/depbrief/internal/lockfile"
)

const (
	defaultNPMBaseURL     = "https://registry.npmjs.org"
	defaultCratesBaseURL  = "https://crates.io"
	defaultPyPIBaseURL    = "https://pypi.org"
	defaultGoProxyBaseURL = "https://proxy.golang.org"
	defaultTimeout        = 15 * time.Second
	maxResponseBytes      = 8 << 20
	userAgent             = "depbrief/1.0 (+https://github.com/Hinikaa/depbrief)"
)

// Endpoints overrides default upstream registry URLs, primarily for testing.
type Endpoints struct {
	NPM     string
	Crates  string
	PyPI    string
	GoProxy string
}

// PackageInfo holds resolved upstream version and repository metadata.
type PackageInfo struct {
	LatestVersion string
	RepoURL       string
}

// Client queries package registries for latest versions and source repositories.
type Client struct {
	httpClient *http.Client
	endpoints  Endpoints
}

// NewClient creates a registry Client with optional HTTP client and endpoint overrides.
func NewClient(httpClient *http.Client, endpoints Endpoints) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}
	if endpoints.NPM == "" {
		endpoints.NPM = defaultNPMBaseURL
	}
	if endpoints.Crates == "" {
		endpoints.Crates = defaultCratesBaseURL
	}
	if endpoints.PyPI == "" {
		endpoints.PyPI = defaultPyPIBaseURL
	}
	if endpoints.GoProxy == "" {
		endpoints.GoProxy = defaultGoProxyBaseURL
	}
	endpoints.NPM = strings.TrimRight(endpoints.NPM, "/")
	endpoints.Crates = strings.TrimRight(endpoints.Crates, "/")
	endpoints.PyPI = strings.TrimRight(endpoints.PyPI, "/")
	endpoints.GoProxy = strings.TrimRight(endpoints.GoProxy, "/")

	return &Client{
		httpClient: httpClient,
		endpoints:  endpoints,
	}
}

// Resolve queries the appropriate upstream registry for dep and returns its latest version and GitHub repo URL.
func (c *Client) Resolve(ctx context.Context, dep lockfile.Dependency) (PackageInfo, error) {
	switch dep.Ecosystem {
	case lockfile.EcosystemNPM, lockfile.EcosystemPNPM:
		return c.resolveNPM(ctx, dep.Name)
	case lockfile.EcosystemCargo:
		return c.resolveCrates(ctx, dep.Name)
	case lockfile.EcosystemUV:
		return c.resolvePyPI(ctx, dep.Name)
	case lockfile.EcosystemGo:
		return c.resolveGo(ctx, dep.Name)
	default:
		return PackageInfo{}, fmt.Errorf("unsupported ecosystem %q", dep.Ecosystem)
	}
}

// ResolveRepoOnly resolves only the upstream GitHub repository URL if not already present.
func (c *Client) ResolveRepoOnly(ctx context.Context, dep lockfile.Dependency) (string, error) {
	if dep.RepoURL != "" {
		if norm := NormalizeGitHubURL(dep.RepoURL); norm != "" {
			return norm, nil
		}
	}
	if dep.Ecosystem == lockfile.EcosystemGo {
		if norm := NormalizeGitHubURL(dep.Name); norm != "" {
			return norm, nil
		}
	}
	info, err := c.Resolve(ctx, dep)
	if err != nil {
		return "", err
	}
	return info.RepoURL, nil
}

func (c *Client) resolveNPM(ctx context.Context, pkg string) (PackageInfo, error) {
	// Abbreviated install metadata misses repository/homepage on some registries; fetch full packument.
	escaped := url.PathEscape(pkg)
	reqURL := fmt.Sprintf("%s/%s", c.endpoints.NPM, escaped)
	body, err := c.getJSON(ctx, reqURL)
	if err != nil {
		return PackageInfo{}, err
	}

	var doc struct {
		DistTags   map[string]string `json:"dist-tags"`
		Repository json.RawMessage   `json:"repository"`
		Homepage   string            `json:"homepage"`
		Bugs       struct {
			URL string `json:"url"`
		} `json:"bugs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return PackageInfo{}, fmt.Errorf("decode npm metadata for %s: %w", pkg, err)
	}

	latest := doc.DistTags["latest"]
	repoURL := extractNPMRepo(doc.Repository)
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(doc.Homepage)
	}
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(doc.Bugs.URL)
	}
	return PackageInfo{
		LatestVersion: strings.TrimSpace(latest),
		RepoURL:       repoURL,
	}, nil
}

func extractNPMRepo(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return NormalizeGitHubURL(asString)
	}
	var asObj struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(raw, &asObj); err == nil {
		return NormalizeGitHubURL(asObj.URL)
	}
	return ""
}

func (c *Client) resolveCrates(ctx context.Context, crate string) (PackageInfo, error) {
	reqURL := fmt.Sprintf("%s/api/v1/crates/%s", c.endpoints.Crates, url.PathEscape(crate))
	body, err := c.getJSON(ctx, reqURL)
	if err != nil {
		return PackageInfo{}, err
	}

	var doc struct {
		Crate struct {
			MaxStableVersion string `json:"max_stable_version"`
			MaxVersion       string `json:"max_version"`
			NewestVersion    string `json:"newest_version"`
			Repository       string `json:"repository"`
			Homepage         string `json:"homepage"`
		} `json:"crate"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return PackageInfo{}, fmt.Errorf("decode crates.io metadata for %s: %w", crate, err)
	}

	latest := doc.Crate.MaxStableVersion
	if latest == "" {
		latest = doc.Crate.MaxVersion
	}
	if latest == "" {
		latest = doc.Crate.NewestVersion
	}
	repoURL := NormalizeGitHubURL(doc.Crate.Repository)
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(doc.Crate.Homepage)
	}
	return PackageInfo{
		LatestVersion: strings.TrimSpace(latest),
		RepoURL:       repoURL,
	}, nil
}

func (c *Client) resolvePyPI(ctx context.Context, pkg string) (PackageInfo, error) {
	reqURL := fmt.Sprintf("%s/pypi/%s/json", c.endpoints.PyPI, url.PathEscape(pkg))
	body, err := c.getJSON(ctx, reqURL)
	if err != nil {
		return PackageInfo{}, err
	}

	var doc struct {
		Info struct {
			Version     string            `json:"version"`
			HomePage    string            `json:"home_page"`
			ProjectURL  string            `json:"project_url"`
			ProjectURLs map[string]string `json:"project_urls"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return PackageInfo{}, fmt.Errorf("decode pypi metadata for %s: %w", pkg, err)
	}

	repoURL := ""
	preferredKeys := []string{"Source", "Source Code", "Repository", "GitHub", "Code", "Homepage", "Home"}
	for _, k := range preferredKeys {
		for actualK, v := range doc.Info.ProjectURLs {
			if strings.EqualFold(actualK, k) {
				if norm := NormalizeGitHubURL(v); norm != "" {
					repoURL = norm
					break
				}
			}
		}
		if repoURL != "" {
			break
		}
	}
	if repoURL == "" {
		for _, v := range doc.Info.ProjectURLs {
			if norm := NormalizeGitHubURL(v); norm != "" {
				repoURL = norm
				break
			}
		}
	}
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(doc.Info.HomePage)
	}
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(doc.Info.ProjectURL)
	}

	return PackageInfo{
		LatestVersion: strings.TrimSpace(doc.Info.Version),
		RepoURL:       repoURL,
	}, nil
}

func (c *Client) resolveGo(ctx context.Context, modPath string) (PackageInfo, error) {
	escaped := EscapeGoModulePath(modPath)
	reqURL := fmt.Sprintf("%s/%s/@latest", c.endpoints.GoProxy, escaped)
	body, err := c.getJSON(ctx, reqURL)
	if err != nil {
		return PackageInfo{}, err
	}

	var doc struct {
		Version string `json:"Version"`
		Origin  struct {
			URL string `json:"URL"`
		} `json:"Origin"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return PackageInfo{}, fmt.Errorf("decode go proxy metadata for %s: %w", modPath, err)
	}

	repoURL := NormalizeGitHubURL(doc.Origin.URL)
	if repoURL == "" {
		repoURL = NormalizeGitHubURL(modPath)
	}
	return PackageInfo{
		LatestVersion: strings.TrimSpace(doc.Version),
		RepoURL:       repoURL,
	}, nil
}

func (c *Client) getJSON(ctx context.Context, reqURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request %s: %w", reqURL, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", reqURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s returned HTTP %d", reqURL, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", reqURL, err)
	}
	return data, nil
}

// EscapeGoModulePath applies Go module proxy uppercase escaping ('A' -> '!a').
func EscapeGoModulePath(modPath string) string {
	var b strings.Builder
	b.Grow(len(modPath))
	for _, r := range modPath {
		if unicode.IsUpper(r) {
			b.WriteByte('!')
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// NormalizeGitHubURL extracts a canonical "https://github.com/owner/repo" URL from common VCS/homepage formats.
func NormalizeGitHubURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "github:") {
		s = "https://github.com/" + strings.TrimPrefix(s, "github:")
	}
	s = strings.TrimPrefix(s, "git+")
	s = strings.TrimPrefix(s, "scm:git:")
	if strings.HasPrefix(s, "git://github.com/") {
		s = "https://github.com/" + strings.TrimPrefix(s, "git://github.com/")
	}
	if strings.HasPrefix(s, "ssh://git@github.com/") {
		s = "https://github.com/" + strings.TrimPrefix(s, "ssh://git@github.com/")
	}
	if strings.HasPrefix(s, "git@github.com:") {
		s = "https://github.com/" + strings.TrimPrefix(s, "git@github.com:")
	}
	if strings.HasPrefix(s, "github.com/") {
		s = "https://" + s
	}

	parsed, err := url.Parse(s)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	owner := parts[0]
	repo := strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" || strings.ContainsAny(owner, " \t\n\r") || strings.ContainsAny(repo, " \t\n\r") {
		return ""
	}
	return "https://github.com/" + owner + "/" + repo
}
