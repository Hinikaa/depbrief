package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hinikaa/depbrief/internal/changelog"
	"github.com/Hinikaa/depbrief/internal/registry"
	"github.com/Hinikaa/depbrief/internal/render"
)

func TestDiffOfflineAndFailOn(t *testing.T) {
	oldLock := filepath.Join("..", "..", "internal", "lockfile", "testdata", "npm", "package-lock.json")
	newLock := filepath.Join("..", "..", "internal", "lockfile", "testdata", "npm", "package-lock.new.json")

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"diff", oldLock, newLock, "--no-changelog", "--format", "json",
	}, &stdout, &stderr, registry.Endpoints{}, changelog.Options{})

	if code != exitClean {
		t.Fatalf("code = %d, want 0; stderr=%s", code, stderr.String())
	}

	var rep render.Report
	if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
		t.Fatalf("decode json: %v\n%s", err, stdout.String())
	}
	if rep.Summary.Upgrades != 3 || rep.Summary.MajorUpgrades != 3 {
		t.Fatalf("unexpected summary for direct-only diff: %+v", rep.Summary)
	}

	stdout.Reset()
	stderr.Reset()
	code = run(context.Background(), []string{
		"diff", oldLock, newLock, "--no-changelog", "--direct-only=false", "--fail-on", "breaking",
	}, &stdout, &stderr, registry.Endpoints{}, changelog.Options{})
	if code != exitFailOn {
		t.Fatalf("expected exit 1 on --fail-on breaking, got %d", code)
	}
	if !strings.Contains(stdout.String(), "accepts") {
		t.Errorf("expected indirect dep accepts when --direct-only=false:\n%s", stdout.String())
	}
}

func TestScanWithMockServersAndFailOnSecurity(t *testing.T) {
	tmpDir := t.TempDir()
	goMod := `module example.com/app

go 1.22

require (
	github.com/org/lib v1.0.0
	github.com/org/indirect v0.1.0 // indirect
)
`
	if err := os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/github.com/org/lib/@latest":
			w.Write([]byte(`{"Version": "v2.0.0"}`))
		case "/repos/org/lib/releases":
			w.Write([]byte(`[
				{"tag_name": "v2.0.0", "body": "- BREAKING: signature changed\n- Fix auth bypass (CVE-2026-9999)"}
			]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	regEp := registry.Endpoints{GoProxy: srv.URL}
	clOpts := changelog.Options{APIBaseURL: srv.URL, RawBaseURL: srv.URL, HTTPClient: srv.Client()}

	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"scan", tmpDir, "--format", "markdown", "--fail-on", "security",
	}, &stdout, &stderr, regEp, clOpts)

	if code != exitFailOn {
		t.Fatalf("code = %d, want 1; stderr=%s\nstdout=%s", code, stderr.String(), stdout.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "CVE-2026-9999") || !strings.Contains(out, "<details>") {
		t.Errorf("unexpected markdown output:\n%s", out)
	}
	if strings.Contains(out, "github.com/org/indirect") {
		t.Errorf("indirect dep should be skipped by default when direct info exists")
	}
}

func TestVersionFlag(t *testing.T) {
	orig := version
	version = "v0.1.0-test"
	defer func() { version = orig }()

	for _, arg := range []string{"-v", "--version", "version"} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{arg}, &stdout, &stderr, registry.Endpoints{}, changelog.Options{})
		if code != exitClean {
			t.Fatalf("%s: code = %d, want 0", arg, code)
		}
		if strings.TrimSpace(stdout.String()) != "depbrief v0.1.0-test" {
			t.Fatalf("%s: stdout = %q, want %q", arg, stdout.String(), "depbrief v0.1.0-test\n")
		}
	}
}
