package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/Hinikaa/depbrief/internal/changelog"
	"github.com/Hinikaa/depbrief/internal/lockfile"
)

// UpgradeBrief summarizes a single dependency upgrade and its classified release notes.
type UpgradeBrief struct {
	Name            string                    `json:"name"`
	Ecosystem       lockfile.Ecosystem        `json:"ecosystem"`
	From            string                    `json:"from"`
	To              string                    `json:"to"`
	Direct          bool                      `json:"direct"`
	Major           bool                      `json:"major"`
	RepoURL         string                    `json:"repo_url,omitempty"`
	ChangelogSource string                    `json:"changelog_source,omitempty"`
	Notes           changelog.ClassifiedNotes `json:"notes"`
	Warning         string                    `json:"warning,omitempty"`
}

// Summary holds aggregate counts for a Report.
type Summary struct {
	TotalChecked       int `json:"total_checked"`
	Upgrades           int `json:"upgrades"`
	MajorUpgrades      int `json:"major_upgrades"`
	SecurityFindings   int `json:"security_findings"`
	BreakingFindings   int `json:"breaking_findings"`
	DeprecatedFindings int `json:"deprecated_findings"`
	RuntimeBumps       int `json:"runtime_bumps"`
}

// Report is the top-level output model for scan and diff runs.
type Report struct {
	Mode     string         `json:"mode"`
	Target   string         `json:"target"`
	Summary  Summary        `json:"summary"`
	Upgrades []UpgradeBrief `json:"upgrades"`
	Warnings []string       `json:"warnings,omitempty"`
}

// ComputeSummary populates Report.Summary from Report.Upgrades.
func (r *Report) ComputeSummary(totalChecked int) {
	s := Summary{
		TotalChecked: totalChecked,
		Upgrades:     len(r.Upgrades),
	}
	for _, u := range r.Upgrades {
		if u.Major {
			s.MajorUpgrades++
		}
		s.SecurityFindings += len(u.Notes.Security)
		s.BreakingFindings += len(u.Notes.Breaking)
		s.DeprecatedFindings += len(u.Notes.Deprecated)
		s.RuntimeBumps += len(u.Notes.RuntimeBump)
	}
	r.Summary = s
}

type riskTier int

const (
	tierSecurity riskTier = iota
	tierBreaking
	tierDeprecationsRuntime
	tierOther
)

func classifyTier(u UpgradeBrief) riskTier {
	if len(u.Notes.Security) > 0 {
		return tierSecurity
	}
	if len(u.Notes.Breaking) > 0 {
		return tierBreaking
	}
	if len(u.Notes.Deprecated) > 0 || len(u.Notes.RuntimeBump) > 0 {
		return tierDeprecationsRuntime
	}
	return tierOther
}

// Text writes a plain-ASCII terminal report grouped by risk severity.
func Text(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "depbrief %s: %s\n", report.Mode, report.Target); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Checked: %d | Upgrades: %d | Major: %d | Security: %d | Breaking: %d\n",
		report.Summary.TotalChecked,
		report.Summary.Upgrades,
		report.Summary.MajorUpgrades,
		report.Summary.SecurityFindings,
		report.Summary.BreakingFindings,
	); err != nil {
		return err
	}

	if len(report.Upgrades) == 0 {
		_, err := fmt.Fprintln(w, "\n[+] No upgrades found.")
		return err
	}

	groups := map[riskTier][]UpgradeBrief{}
	for _, u := range report.Upgrades {
		t := classifyTier(u)
		groups[t] = append(groups[t], u)
	}

	orderedTiers := []struct {
		tier  riskTier
		label string
	}{
		{tierSecurity, "SECURITY"},
		{tierBreaking, "BREAKING"},
		{tierDeprecationsRuntime, "DEPRECATIONS / RUNTIME"},
		{tierOther, "OTHER UPGRADES"},
	}

	for _, g := range orderedTiers {
		items := groups[g.tier]
		if len(items) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "\n=== %s (%d) ===\n", g.label, len(items)); err != nil {
			return err
		}

		tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
		for _, u := range items {
			scope := "indirect"
			if u.Direct {
				scope = "direct"
			}
			flags := []string{string(u.Ecosystem), scope}
			if u.Major {
				flags = append(flags, "MAJOR")
			}
			if _, err := fmt.Fprintf(tw, "%s\t%s -> %s\t[%s]\n", u.Name, u.From, u.To, strings.Join(flags, ", ")); err != nil {
				return err
			}
		}
		if err := tw.Flush(); err != nil {
			return err
		}

		for _, u := range items {
			if !hasAnyNotes(u.Notes) && u.Warning == "" {
				continue
			}
			if _, err := fmt.Fprintf(w, "\n  %s (%s -> %s)\n", u.Name, u.From, u.To); err != nil {
				return err
			}
			if u.RepoURL != "" {
				if _, err := fmt.Fprintf(w, "    repo: %s (%s)\n", u.RepoURL, u.ChangelogSource); err != nil {
					return err
				}
			}
			if err := writeTextBucket(w, "SECURITY", u.Notes.Security); err != nil {
				return err
			}
			if err := writeTextBucket(w, "BREAKING", u.Notes.Breaking); err != nil {
				return err
			}
			if err := writeTextBucket(w, "DEPRECATED", u.Notes.Deprecated); err != nil {
				return err
			}
			if err := writeTextBucket(w, "RUNTIME", u.Notes.RuntimeBump); err != nil {
				return err
			}
			if err := writeTextBucket(w, "NOTABLE", u.Notes.Notable); err != nil {
				return err
			}
			if u.Warning != "" {
				if _, err := fmt.Fprintf(w, "    [WARN] %s\n", u.Warning); err != nil {
					return err
				}
			}
		}
	}

	if len(report.Warnings) > 0 {
		if _, err := fmt.Fprintln(w, "\n=== WARNINGS ==="); err != nil {
			return err
		}
		for _, warn := range report.Warnings {
			if _, err := fmt.Fprintf(w, "  [WARN] %s\n", warn); err != nil {
				return err
			}
		}
	}

	return nil
}

func writeTextBucket(w io.Writer, label string, items []changelog.Item) error {
	for _, it := range items {
		if _, err := fmt.Fprintf(w, "    [%s %s] %s\n", label, it.Version, it.Text); err != nil {
			return err
		}
	}
	return nil
}

// Markdown writes a GitHub-flavored Markdown brief with collapsible details per package.
func Markdown(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "# depbrief %s: `%s`\n\n", report.Mode, report.Target); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| Checked | Upgrades | Major | Security | Breaking | Deprecations | Runtime Bumps |\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| ---: | ---: | ---: | ---: | ---: | ---: | ---: |\n"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "| %d | %d | %d | %d | %d | %d | %d |\n\n",
		report.Summary.TotalChecked,
		report.Summary.Upgrades,
		report.Summary.MajorUpgrades,
		report.Summary.SecurityFindings,
		report.Summary.BreakingFindings,
		report.Summary.DeprecatedFindings,
		report.Summary.RuntimeBumps,
	); err != nil {
		return err
	}

	if len(report.Upgrades) == 0 {
		_, err := fmt.Fprintln(w, "No upgrades found.")
		return err
	}

	groups := map[riskTier][]UpgradeBrief{}
	for _, u := range report.Upgrades {
		t := classifyTier(u)
		groups[t] = append(groups[t], u)
	}

	orderedTiers := []struct {
		tier  riskTier
		label string
	}{
		{tierSecurity, "Security"},
		{tierBreaking, "Breaking Changes"},
		{tierDeprecationsRuntime, "Deprecations & Runtime Bumps"},
		{tierOther, "Other Upgrades"},
	}

	for _, g := range orderedTiers {
		items := groups[g.tier]
		if len(items) == 0 {
			continue
		}
		if _, err := fmt.Fprintf(w, "## %s\n\n", g.label); err != nil {
			return err
		}
		for _, u := range items {
			majorTag := ""
			if u.Major {
				majorTag = " `[MAJOR]`"
			}
			secTag := ""
			if len(u.Notes.Security) > 0 {
				secTag = " `[SECURITY]`"
			}
			brkTag := ""
			if len(u.Notes.Breaking) > 0 {
				brkTag = " `[BREAKING]`"
			}
			if _, err := fmt.Fprintf(w, "<details>\n<summary><code>%s</code> (%s): <code>%s</code> -> <code>%s</code>%s%s%s</summary>\n\n",
				u.Name, u.Ecosystem, u.From, u.To, secTag, brkTag, majorTag); err != nil {
				return err
			}
			if u.RepoURL != "" {
				if _, err := fmt.Fprintf(w, "- Repository: %s (source: `%s`)\n", u.RepoURL, u.ChangelogSource); err != nil {
					return err
				}
			}
			if err := writeMarkdownBucket(w, "SECURITY", u.Notes.Security); err != nil {
				return err
			}
			if err := writeMarkdownBucket(w, "BREAKING", u.Notes.Breaking); err != nil {
				return err
			}
			if err := writeMarkdownBucket(w, "DEPRECATED", u.Notes.Deprecated); err != nil {
				return err
			}
			if err := writeMarkdownBucket(w, "RUNTIME", u.Notes.RuntimeBump); err != nil {
				return err
			}
			if err := writeMarkdownBucket(w, "NOTABLE", u.Notes.Notable); err != nil {
				return err
			}
			if u.Warning != "" {
				if _, err := fmt.Fprintf(w, "- WARN: %s\n", u.Warning); err != nil {
					return err
				}
			}
			if !hasAnyNotes(u.Notes) && u.Warning == "" {
				if _, err := fmt.Fprintln(w, "- No notable changelog entries extracted."); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintln(w, "\n</details>"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintln(w); err != nil {
			return err
		}
	}

	return nil
}

func writeMarkdownBucket(w io.Writer, label string, items []changelog.Item) error {
	for _, it := range items {
		if _, err := fmt.Fprintf(w, "- **%s** (`%s`): %s\n", label, it.Version, it.Text); err != nil {
			return err
		}
	}
	return nil
}

// JSON writes the structured Report as indented JSON.
func JSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

func hasAnyNotes(n changelog.ClassifiedNotes) bool {
	return len(n.Security) > 0 ||
		len(n.Breaking) > 0 ||
		len(n.Deprecated) > 0 ||
		len(n.RuntimeBump) > 0 ||
		len(n.Notable) > 0
}
