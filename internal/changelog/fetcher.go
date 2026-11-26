package changelog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Hinikaa/depbrief/internal/semver"
)

const (
	defaultGitHubAPIBase = "https://api.github.com"
	defaultGitHubRawBase = "https://raw.githubusercontent.com"
	defaultTimeout       = 15 * time.Second
	maxBodyBytes         = 8 << 20
	maxReleasePages      = 10
	userAgent            = "depbrief/1.0 (+https://github.com/Hinikaa/depbrief)"
)

var changelogCandidates = []string{"CHANGELOG.md", "CHANGES.md", "HISTORY.md"}

// Options configures a Fetcher.
type Options struct {
	APIBaseURL string
	RawBaseURL string
	Token      string
	HTTPClient *http.Client
}

// Fetcher retrieves intermediate release notes from GitHub Releases or CHANGELOG files.
type Fetcher struct {
	httpClient *http.Client
	apiBaseURL string
	rawBaseURL string
	token      string
}

// NewFetcher constructs a Fetcher using optional overrides; defaults read GITHUB_TOKEN from env.
func NewFetcher(opts Options) *Fetcher {
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	apiBase := strings.TrimRight(opts.APIBaseURL, "/")
	if apiBase == "" {
		apiBase = defaultGitHubAPIBase
	}
	rawBase := strings.TrimRight(opts.RawBaseURL, "/")
	if rawBase == "" {
		rawBase = defaultGitHubRawBase
	}
	token := opts.Token
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	return &Fetcher{
		httpClient: client,
		apiBaseURL: apiBase,
		rawBaseURL: rawBase,
		token:      token,
	}
}

// FetchResult contains the fetched entries and source metadata.
type FetchResult struct {
	Entries []ReleaseEntry
	Source  string // "github-releases", "changelog:<filename>", or "none"
}

// Fetch retrieves release notes for repoURL between fromExclusive and toInclusive.
// pkgName is used to match monorepo tag prefixes such as "pkg@1.2.3".
func (f *Fetcher) Fetch(ctx context.Context, repoURL, pkgName string, fromExclusive, toInclusive semver.Version) (FetchResult, error) {
	owner, repo, ok := ParseGitHubOwnerRepo(repoURL)
	if !ok {
		return FetchResult{Source: "none"}, fmt.Errorf("invalid or non-GitHub repository URL %q", repoURL)
	}

	entries, err := f.fetchGitHubReleases(ctx, owner, repo, pkgName, fromExclusive, toInclusive)
	if err != nil {
		return FetchResult{}, err
	}
	if len(entries) > 0 {
		return FetchResult{Entries: entries, Source: "github-releases"}, nil
	}

	for _, filename := range changelogCandidates {
		rawURL := fmt.Sprintf("%s/%s/%s/HEAD/%s", f.rawBaseURL, url.PathEscape(owner), url.PathEscape(repo), filename)
		body, found, err := f.getOptionalText(ctx, rawURL)
		if err != nil {
			return FetchResult{}, err
		}
		if !found {
			continue
		}
		sliced := SliceMarkdown(body, fromExclusive, toInclusive)
		if len(sliced) > 0 {
			for i := range sliced {
				sliced[i].URL = fmt.Sprintf("https://github.com/%s/%s/blob/HEAD/%s", owner, repo, filename)
			}
			return FetchResult{Entries: sliced, Source: "changelog:" + filename}, nil
		}
	}

	return FetchResult{Source: "none"}, nil
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
}

func (f *Fetcher) fetchGitHubReleases(ctx context.Context, owner, repo, pkgName string, fromExclusive, toInclusive semver.Version) ([]ReleaseEntry, error) {
	nextURL := fmt.Sprintf("%s/repos/%s/%s/releases?per_page=100", f.apiBaseURL, url.PathEscape(owner), url.PathEscape(repo))
	var matched []ReleaseEntry

	for page := 0; page < maxReleasePages && nextURL != ""; page++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, nextURL, nil)
		if err != nil {
			return nil, fmt.Errorf("build GitHub request: %w", err)
		}
		req.Header.Set("User-Agent", userAgent)
		req.Header.Set("Accept", "application/vnd.github+json")
		if f.token != "" {
			req.Header.Set("Authorization", "Bearer "+f.token)
		}

		resp, err := f.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GitHub releases request: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		resp.Body.Close()

		if resp.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
			if resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.StatusCode == http.StatusTooManyRequests {
				return nil, fmt.Errorf("GitHub API rate limit exceeded (HTTP %d); set GITHUB_TOKEN to increase quota", resp.StatusCode)
			}
			return nil, fmt.Errorf("GitHub API returned HTTP %d for %s/%s", resp.StatusCode, owner, repo)
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("GitHub API returned HTTP %d for %s/%s", resp.StatusCode, owner, repo)
		}
		if readErr != nil {
			return nil, fmt.Errorf("read GitHub releases: %w", readErr)
		}

		var releases []ghRelease
		if err := json.Unmarshal(body, &releases); err != nil {
			return nil, fmt.Errorf("decode GitHub releases: %w", err)
		}
		if len(releases) == 0 {
			break
		}

		for _, rel := range releases {
			if rel.Draft {
				continue
			}
			ver, ok := extractTagVersion(rel.TagName, pkgName)
			if !ok {
				continue
			}
			if semver.InRange(ver, fromExclusive, toInclusive) {
				matched = append(matched, ReleaseEntry{
					Version: ver,
					Tag:     rel.TagName,
					URL:     rel.HTMLURL,
					Body:    rel.Body,
				})
			}
		}

		nextURL = parseNextLink(resp.Header.Get("Link"))
	}

	sort.SliceStable(matched, func(i, j int) bool {
		return semver.Compare(matched[i].Version, matched[j].Version) > 0
	})
	return matched, nil
}

func (f *Fetcher) getOptionalText(ctx context.Context, rawURL string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", false, fmt.Errorf("build raw request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	if f.token != "" {
		req.Header.Set("Authorization", "Bearer "+f.token)
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", false, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("GET %s returned HTTP %d", rawURL, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", rawURL, err)
	}
	return string(data), true, nil
}

func parseNextLink(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}
	for _, part := range strings.Split(linkHeader, ",") {
		part = strings.TrimSpace(part)
		if !strings.Contains(part, `rel="next"`) {
			continue
		}
		start := strings.IndexByte(part, '<')
		end := strings.IndexByte(part, '>')
		if start >= 0 && end > start+1 {
			return part[start+1 : end]
		}
	}
	return ""
}

func extractTagVersion(tagName, pkgName string) (semver.Version, bool) {
	tag := strings.TrimSpace(tagName)
	if tag == "" {
		return semver.Version{}, false
	}
	if v, err := semver.Parse(tag); err == nil {
		return v, true
	}

	// Handle monorepo tags such as "pkg@1.2.3", "@scope/pkg@1.2.3", "pkg-v1.2.3", or "pkg/v1.2.3".
	if idx := strings.LastIndexByte(tag, '@'); idx > 0 {
		prefix := tag[:idx]
		verPart := tag[idx+1:]
		if pkgName == "" || prefix == pkgName || strings.HasSuffix(pkgName, "/"+prefix) {
			if v, err := semver.Parse(verPart); err == nil {
				return v, true
			}
		}
		return semver.Version{}, false
	}

	if pkgName != "" {
		shortPkg := pkgName
		if slash := strings.LastIndexByte(pkgName, '/'); slash >= 0 {
			shortPkg = pkgName[slash+1:]
		}
		for _, sep := range []string{"-", "/"} {
			for _, candidatePrefix := range []string{pkgName + sep, shortPkg + sep} {
				if strings.HasPrefix(tag, candidatePrefix) {
					if v, err := semver.Parse(strings.TrimPrefix(tag, candidatePrefix)); err == nil {
						return v, true
					}
				}
			}
		}
	}

	return semver.Version{}, false
}

// ParseGitHubOwnerRepo extracts owner and repo from a GitHub URL.
func ParseGitHubOwnerRepo(repoURL string) (owner, repo string, ok bool) {
	s := strings.TrimSpace(repoURL)
	if s == "" {
		return "", "", false
	}
	if strings.HasPrefix(s, "github.com/") {
		s = "https://" + s
	}
	parsed, err := url.Parse(s)
	if err != nil {
		return "", "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host != "github.com" && host != "www.github.com" {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	if len(parts) < 2 {
		return "", "", false
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	if owner == "" || repo == "" {
		return "", "", false
	}
	return owner, repo, true
}
