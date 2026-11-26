package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Hinikaa/depbrief/internal/changelog"
	"github.com/Hinikaa/depbrief/internal/lockfile"
)

func sampleReport() Report {
	r := Report{
		Mode:   "diff",
		Target: "old.lock -> new.lock",
		Upgrades: []UpgradeBrief{
			{
				Name:            "express",
				Ecosystem:       lockfile.EcosystemNPM,
				From:            "4.18.2",
				To:              "5.0.1",
				Direct:          true,
				Major:           true,
				RepoURL:         "https://github.com/expressjs/express",
				ChangelogSource: "github-releases",
				Notes: changelog.ClassifiedNotes{
					Security: []changelog.Item{{Category: changelog.CategorySecurity, Version: "5.0.1", Text: "Fix open redirect (CVE-2024-29041)"}},
					Breaking: []changelog.Item{{Category: changelog.CategoryBreaking, Version: "5.0.0", Text: "Drop Node 16 support"}},
				},
			},
			{
				Name:      "vitest",
				Ecosystem: lockfile.EcosystemNPM,
				From:      "1.2.0",
				To:        "2.1.0",
				Direct:    true,
				Major:     true,
				Notes: changelog.ClassifiedNotes{
					Deprecated: []changelog.Item{{Category: changelog.CategoryDeprecated, Version: "2.0.0", Text: "Deprecated old pool option"}},
				},
			},
		},
	}
	r.ComputeSummary(5)
	return r
}

func TestRenderFormats(t *testing.T) {
	rep := sampleReport()

	var textBuf bytes.Buffer
	if err := Text(&textBuf, rep); err != nil {
		t.Fatalf("Text: %v", err)
	}
	txt := textBuf.String()
	if !strings.Contains(txt, "=== SECURITY (1) ===") || !strings.Contains(txt, "=== DEPRECATIONS / RUNTIME (1) ===") {
		t.Fatalf("unexpected Text output:\n%s", txt)
	}
	if !strings.Contains(txt, "CVE-2024-29041") {
		t.Errorf("missing CVE in Text output")
	}

	var mdBuf bytes.Buffer
	if err := Markdown(&mdBuf, rep); err != nil {
		t.Fatalf("Markdown: %v", err)
	}
	md := mdBuf.String()
	if !strings.Contains(md, "<details>") || !strings.Contains(md, "express") {
		t.Fatalf("unexpected Markdown output:\n%s", md)
	}

	var jsonBuf bytes.Buffer
	if err := JSON(&jsonBuf, rep); err != nil {
		t.Fatalf("JSON: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(jsonBuf.Bytes(), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if decoded.Summary.SecurityFindings != 1 || decoded.Summary.Upgrades != 2 {
		t.Errorf("unexpected decoded summary: %+v", decoded.Summary)
	}
}
