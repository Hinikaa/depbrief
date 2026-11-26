package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hinikaa/depbrief/internal/lockfile"
)

func TestEscapeGoModulePath(t *testing.T) {
	got := EscapeGoModulePath("github.com/Azure/azure-sdk-for-go/sdk/azcore")
	want := "github.com/!azure/azure-sdk-for-go/sdk/azcore"
	if got != want {
		t.Errorf("EscapeGoModulePath = %q, want %q", got, want)
	}
}

func TestNormalizeGitHubURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"git+https://github.com/expressjs/express.git", "https://github.com/expressjs/express"},
		{"git@github.com:serde-rs/serde.git", "https://github.com/serde-rs/serde"},
		{"ssh://git@github.com/tokio-rs/tokio.git#main", "https://github.com/tokio-rs/tokio"},
		{"github:TanStack/query", "https://github.com/TanStack/query"},
		{"https://github.com/pydantic/pydantic/issues", "https://github.com/pydantic/pydantic"},
		{"github.com/spf13/cobra/v2", "https://github.com/spf13/cobra"},
		{"https://example.com/not-github", ""},
	}
	for _, tc := range tests {
		if got := NormalizeGitHubURL(tc.in); got != tc.want {
			t.Errorf("NormalizeGitHubURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRegistryResolvers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/express":
			w.Write([]byte(`{
				"dist-tags": {"latest": "5.0.1"},
				"repository": {"type": "git", "url": "git+https://github.com/expressjs/express.git"}
			}`))
		case r.URL.RawPath == "/@tanstack%2Freact-query" || r.URL.Path == "/@tanstack/react-query":
			w.Write([]byte(`{
				"dist-tags": {"latest": "5.50.0"},
				"repository": "github:TanStack/query"
			}`))
		case r.URL.Path == "/api/v1/crates/serde":
			if !strings.Contains(r.Header.Get("User-Agent"), "depbrief") {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Write([]byte(`{
				"crate": {
					"max_stable_version": "1.0.210",
					"repository": "https://github.com/serde-rs/serde"
				}
			}`))
		case r.URL.Path == "/pypi/pydantic/json":
			w.Write([]byte(`{
				"info": {
					"version": "2.9.2",
					"home_page": "https://docs.pydantic.dev/",
					"project_urls": {
						"Source": "https://github.com/pydantic/pydantic"
					}
				}
			}`))
		case r.URL.Path == "/github.com/!burnt!sushi/toml/@latest":
			w.Write([]byte(`{
				"Version": "v1.4.0",
				"Origin": {"VCS": "git", "URL": "https://github.com/BurntSushi/toml"}
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := NewClient(srv.Client(), Endpoints{
		NPM:     srv.URL,
		Crates:  srv.URL,
		PyPI:    srv.URL,
		GoProxy: srv.URL,
	})
	ctx := context.Background()

	cases := []struct {
		dep        lockfile.Dependency
		wantLatest string
		wantRepo   string
	}{
		{
			dep:        lockfile.Dependency{Name: "express", Ecosystem: lockfile.EcosystemNPM},
			wantLatest: "5.0.1",
			wantRepo:   "https://github.com/expressjs/express",
		},
		{
			dep:        lockfile.Dependency{Name: "@tanstack/react-query", Ecosystem: lockfile.EcosystemPNPM},
			wantLatest: "5.50.0",
			wantRepo:   "https://github.com/TanStack/query",
		},
		{
			dep:        lockfile.Dependency{Name: "serde", Ecosystem: lockfile.EcosystemCargo},
			wantLatest: "1.0.210",
			wantRepo:   "https://github.com/serde-rs/serde",
		},
		{
			dep:        lockfile.Dependency{Name: "pydantic", Ecosystem: lockfile.EcosystemUV},
			wantLatest: "2.9.2",
			wantRepo:   "https://github.com/pydantic/pydantic",
		},
		{
			dep:        lockfile.Dependency{Name: "github.com/BurntSushi/toml", Ecosystem: lockfile.EcosystemGo},
			wantLatest: "v1.4.0",
			wantRepo:   "https://github.com/BurntSushi/toml",
		},
	}

	for _, tc := range cases {
		info, err := client.Resolve(ctx, tc.dep)
		if err != nil {
			t.Fatalf("Resolve(%s/%s): %v", tc.dep.Ecosystem, tc.dep.Name, err)
		}
		if info.LatestVersion != tc.wantLatest || info.RepoURL != tc.wantRepo {
			t.Errorf("Resolve(%s) = %+v, want latest=%q repo=%q", tc.dep.Name, info, tc.wantLatest, tc.wantRepo)
		}
	}
}
