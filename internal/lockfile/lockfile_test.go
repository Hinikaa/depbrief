package lockfile

import (
	"path/filepath"
	"testing"
)

func byName(deps []Dependency) map[string]Dependency {
	m := make(map[string]Dependency, len(deps))
	for _, d := range deps {
		m[d.Name] = d
	}
	return m
}

func TestNPMParser(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "npm", "package-lock.json"), "auto")
	if err != nil {
		t.Fatalf("ParseFile npm: %v", err)
	}
	if res.Ecosystem != EcosystemNPM {
		t.Fatalf("ecosystem = %s, want npm", res.Ecosystem)
	}
	if !res.HasDirectInfo {
		t.Fatalf("expected HasDirectInfo=true")
	}
	got := byName(res.Dependencies)
	if len(got) != 5 {
		t.Fatalf("got %d deps, want 5: %+v", len(got), got)
	}
	if !got["express"].Direct || got["express"].Version != "4.18.2" {
		t.Errorf("express = %+v", got["express"])
	}
	if !got["@tanstack/react-query"].Direct || got["@tanstack/react-query"].Version != "4.36.1" {
		t.Errorf("@tanstack/react-query = %+v", got["@tanstack/react-query"])
	}
	if got["accepts"].Direct {
		t.Errorf("accepts should be indirect")
	}
	if got["qs"].Direct {
		t.Errorf("nested qs should be indirect")
	}
}

func TestPNPMParser(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "pnpm", "pnpm-lock.yaml"), "auto")
	if err != nil {
		t.Fatalf("ParseFile pnpm: %v", err)
	}
	if res.Ecosystem != EcosystemPNPM {
		t.Fatalf("ecosystem = %s, want pnpm", res.Ecosystem)
	}
	if !res.HasDirectInfo {
		t.Fatalf("expected HasDirectInfo=true")
	}
	got := byName(res.Dependencies)
	if len(got) != 4 {
		t.Fatalf("got %d deps, want 4: %+v", len(got), got)
	}
	if !got["@trpc/server"].Direct || got["@trpc/server"].Version != "10.45.0" {
		t.Errorf("@trpc/server = %+v", got["@trpc/server"])
	}
	if !got["zod"].Direct || !got["typescript"].Direct {
		t.Errorf("expected zod and typescript to be direct")
	}
	if got["undici-types"].Direct {
		t.Errorf("undici-types should be indirect")
	}
}

func TestCargoParser(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "cargo", "Cargo.lock"), "auto")
	if err != nil {
		t.Fatalf("ParseFile cargo: %v", err)
	}
	if res.Ecosystem != EcosystemCargo {
		t.Fatalf("ecosystem = %s, want cargo", res.Ecosystem)
	}
	if !res.HasDirectInfo {
		t.Fatalf("expected HasDirectInfo=true")
	}
	got := byName(res.Dependencies)
	if _, hasRoot := got["svc-cli"]; hasRoot {
		t.Errorf("root workspace package svc-cli should be excluded")
	}
	if !got["serde"].Direct || got["serde"].Version != "1.0.197" {
		t.Errorf("serde = %+v", got["serde"])
	}
	if !got["reqwest"].Direct {
		t.Errorf("renamed dep reqwest should be direct")
	}
	if !got["insta"].Direct {
		t.Errorf("dev dep insta should be direct")
	}
	if got["mio"].Direct {
		t.Errorf("mio should be indirect")
	}
}

func TestUVParser(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "uv", "uv.lock"), "auto")
	if err != nil {
		t.Fatalf("ParseFile uv: %v", err)
	}
	if res.Ecosystem != EcosystemUV {
		t.Fatalf("ecosystem = %s, want uv", res.Ecosystem)
	}
	if !res.HasDirectInfo {
		t.Fatalf("expected HasDirectInfo=true")
	}
	got := byName(res.Dependencies)
	if _, hasRoot := got["ml-worker"]; hasRoot {
		t.Errorf("virtual root ml-worker should be excluded")
	}
	if !got["httpx"].Direct || !got["pydantic"].Direct || !got["pytest"].Direct {
		t.Errorf("expected httpx, pydantic, pytest to be direct: %+v", got)
	}
	if got["anyio"].Direct {
		t.Errorf("anyio should be indirect")
	}
}

func TestGoModParser(t *testing.T) {
	res, err := ParseFile(filepath.Join("testdata", "gomod", "go.mod"), "auto")
	if err != nil {
		t.Fatalf("ParseFile go.mod: %v", err)
	}
	if res.Ecosystem != EcosystemGo {
		t.Fatalf("ecosystem = %s, want go", res.Ecosystem)
	}
	got := byName(res.Dependencies)
	if len(got) != 3 {
		t.Fatalf("got %d deps, want 3: %+v", len(got), got)
	}
	if !got["github.com/spf13/cobra"].Direct || got["github.com/spf13/cobra"].RepoURL != "https://github.com/spf13/cobra" {
		t.Errorf("cobra = %+v", got["github.com/spf13/cobra"])
	}
	if !got["github.com/stretchr/testify"].Direct {
		t.Errorf("testify should be direct")
	}
	if got["golang.org/x/sync"].Direct {
		t.Errorf("x/sync should be indirect")
	}
}

func TestDetect(t *testing.T) {
	detected, err := Detect(filepath.Join("testdata", "cargo"), "auto")
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(detected) != 1 || detected[0].Ecosystem != EcosystemCargo {
		t.Fatalf("detected = %+v, want single cargo", detected)
	}

	if _, err := Detect(filepath.Join("testdata", "cargo"), "npm"); err == nil {
		t.Fatalf("expected error when filtering for missing npm lockfile")
	}
}
