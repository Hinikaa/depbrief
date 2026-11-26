package changelog

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hinikaa/depbrief/internal/semver"
)

const sampleChangelog = `# Changelog

All notable changes to this project will be documented in this file.

## [Unreleased]
- Work in progress

## [3.0.0] - 2026-03-01
### Security
- Fix prototype pollution in query parser (CVE-2026-12345)

### Breaking Changes
- Remove legacy sync() API; use async() instead
- Minimum Node.js version is now Node 20

### Bug Fixes
- chore(deps): bump lodash from 4.17.20 to 4.17.21
- Fix connection pool leak on timeout

## v2.1.0 - 2026-01-15
- Deprecated: oldClient() will be removed in v4
- Dependabot: bump actions/checkout from 3 to 4
- Added streaming response support

## [2.0.0] - 2025-11-01
- Initial 2.0 release
`

func TestSliceMarkdownAndClassify(t *testing.T) {
	from, _ := semver.Parse("2.0.0")
	to, _ := semver.Parse("3.0.0")

	entries := SliceMarkdown(sampleChangelog, from, to)
	if len(entries) != 2 {
		t.Fatalf("SliceMarkdown got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].Version.String() != "3.0.0" || entries[1].Version.String() != "2.1.0" {
		t.Fatalf("unexpected entry order/versions: %+v", entries)
	}

	notes := Classify(entries)
	if len(notes.Security) != 1 || !strings.Contains(notes.Security[0].Text, "CVE-2026-12345") {
		t.Errorf("Security = %+v", notes.Security)
	}
	if len(notes.Breaking) != 2 {
		t.Errorf("Breaking = %+v, want 2 items", notes.Breaking)
	}
	if len(notes.Deprecated) != 1 {
		t.Errorf("Deprecated = %+v, want 1 item", notes.Deprecated)
	}
	if len(notes.Notable) != 2 {
		t.Errorf("Notable = %+v, want 2 non-noise items", notes.Notable)
	}
	for _, item := range notes.Notable {
		if strings.Contains(strings.ToLower(item.Text), "dependabot") || strings.Contains(strings.ToLower(item.Text), "chore(deps)") {
			t.Errorf("noise line was not stripped: %+v", item)
		}
	}
}

func TestRuntimeBumpClassification(t *testing.T) {
	v1, _ := semver.Parse("1.0.0")
	v2, _ := semver.Parse("1.1.0")
	entries := []ReleaseEntry{
		{
			Version: v2,
			Body:    "- Drop support for Python 3.9; minimum Python 3.10 required\n- Thanks to @alice for their first contribution!\n- @alice made their first contribution in https://github.com/org/repo/pull/1\n- Full Changelog: https://github.com/org/repo/compare/v1.0.0...v1.1.0",
		},
	}
	_ = v1
	notes := Classify(entries)
	if len(notes.RuntimeBump) != 1 {
		t.Fatalf("RuntimeBump = %+v, want 1", notes.RuntimeBump)
	}
	if len(notes.Notable) != 0 {
		t.Fatalf("expected boilerplate to be stripped, got Notable = %+v", notes.Notable)
	}
}

func TestFetcherGitHubReleasesPaginationAndAuth(t *testing.T) {
	var sawAuth bool
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		switch r.URL.Path {
		case "/repos/org/pkg/releases":
			page := r.URL.Query().Get("page")
			if page == "" {
				w.Header().Set("Link", fmt.Sprintf(`<%s/repos/org/pkg/releases?page=2>; rel="next"`, srvURL))
				w.Write([]byte(`[
					{"tag_name": "v2.2.0", "body": "- BREAKING: drop legacy flag", "html_url": "https://github.com/org/pkg/releases/tag/v2.2.0"},
					{"tag_name": "v2.3.0-rc.1", "draft": true, "body": "- Draft"}
				]`))
				return
			}
			if page == "2" {
				w.Write([]byte(`[
					{"tag_name": "v2.1.0", "body": "- Fix XSS in template rendering (GHSA-1234-5678-9abc)", "html_url": "https://github.com/org/pkg/releases/tag/v2.1.0"},
					{"tag_name": "v2.0.0", "body": "- Base version"}
				]`))
				return
			}
		default:
			http.NotFound(w, r)
		}
	}))
	srvURL = srv.URL
	defer srv.Close()

	fetcher := NewFetcher(Options{
		APIBaseURL: srv.URL,
		RawBaseURL: srv.URL,
		Token:      "test-token",
		HTTPClient: srv.Client(),
	})

	from, _ := semver.Parse("2.0.0")
	to, _ := semver.Parse("2.2.0")
	res, err := fetcher.Fetch(context.Background(), "https://github.com/org/pkg", "pkg", from, to)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !sawAuth {
		t.Errorf("expected Bearer token header")
	}
	if res.Source != "github-releases" {
		t.Errorf("Source = %q, want github-releases", res.Source)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(res.Entries), res.Entries)
	}
	if res.Entries[0].Version.String() != "2.2.0" || res.Entries[1].Version.String() != "2.1.0" {
		t.Errorf("unexpected versions: %+v", res.Entries)
	}
}

func TestFetcherFallbackToChangelogMD(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/pkg/releases":
			w.Write([]byte(`[]`))
		case "/org/pkg/HEAD/CHANGELOG.md":
			w.Write([]byte(sampleChangelog))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	fetcher := NewFetcher(Options{
		APIBaseURL: srv.URL,
		RawBaseURL: srv.URL,
		HTTPClient: srv.Client(),
	})

	from, _ := semver.Parse("2.0.0")
	to, _ := semver.Parse("3.0.0")
	res, err := fetcher.Fetch(context.Background(), "https://github.com/org/pkg", "pkg", from, to)
	if err != nil {
		t.Fatalf("Fetch fallback: %v", err)
	}
	if res.Source != "changelog:CHANGELOG.md" {
		t.Errorf("Source = %q, want changelog:CHANGELOG.md", res.Source)
	}
	if len(res.Entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(res.Entries))
	}
}

func TestFetcherRateLimitError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	fetcher := NewFetcher(Options{
		APIBaseURL: srv.URL,
		RawBaseURL: srv.URL,
		HTTPClient: srv.Client(),
	})
	from, _ := semver.Parse("1.0.0")
	to, _ := semver.Parse("2.0.0")
	_, err := fetcher.Fetch(context.Background(), "https://github.com/org/pkg", "pkg", from, to)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}
