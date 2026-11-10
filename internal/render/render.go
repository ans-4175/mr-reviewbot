// Package render turns a Review into the MR comment (with the sha marker).
package render

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"mr-reviewbot/internal/review"
	"mr-reviewbot/internal/state"
)

const maxBody = 60000

type Meta struct{ From, To, Mode, Language string }

// legend explains only the levels that appear in this comment.
func legend(t Text, findings []review.Finding) string {
	used := map[string]bool{}
	for _, f := range findings {
		used[f.Severity] = true
	}
	var parts []string
	for _, sev := range review.Severities {
		if used[sev] {
			parts = append(parts, fmt.Sprintf("%s = %s", t.Severity[sev], t.Meaning[sev]))
		}
	}
	return "_" + t.Legend + ": " + strings.Join(parts, " · ") + "_"
}

// GuardVerdict stops the verdict from being rosier than the findings it carries.
func GuardVerdict(r *review.Review) {
	hasCritical, hasRequired := false, false
	for _, f := range r.Findings {
		hasCritical = hasCritical || f.Severity == "critical"
		hasRequired = hasRequired || f.Severity == "required"
	}
	if hasCritical {
		r.Verdict = "unsafe"
	} else if hasRequired && r.Verdict == "safe_to_merge" {
		r.Verdict = "needs_changes"
	}
}

var (
	quickActionRe = regexp.MustCompile(`(?m)^([ \t]*)/(\w)`)
	backtickRunRe = regexp.MustCompile("`+")
)

// NeutralizeQuickActions: GitLab runs lines starting with "/" as quick actions (e.g. /merge).
func NeutralizeQuickActions(text string) string {
	return quickActionRe.ReplaceAllString(text, "${1}​/${2}")
}

func fence(text string) string {
	longest := 0
	for _, m := range backtickRunRe.FindAllString(text, -1) {
		longest = max(longest, len(m))
	}
	return strings.Repeat("`", max(4, longest+1))
}

func location(f review.Finding) string {
	switch {
	case f.File == "":
		return "-"
	case f.Line != nil:
		return fmt.Sprintf("`%s:%d`", f.File, *f.Line)
	}
	return fmt.Sprintf("`%s`", f.File)
}

func rank(sev string) int {
	for i, s := range review.Severities {
		if s == sev {
			return i
		}
	}
	return len(review.Severities)
}

func short(s string) string { return s[:min(8, len(s))] }

func Comment(r *review.Review, m Meta) string {
	t := TextFor(m.Language)
	findings := append([]review.Finding(nil), r.Findings...)
	sort.SliceStable(findings, func(i, j int) bool { return rank(findings[i].Severity) < rank(findings[j].Severity) })

	var b strings.Builder
	fmt.Fprintf(&b, "## 🤖 %s · %s\n\n> %s\n\n", t.Title, t.Verdict[r.Verdict], strings.ReplaceAll(strings.TrimSpace(r.VerdictReason), "\n", " "))
	mode := t.Mode[m.Mode]
	if mode == "" {
		mode = m.Mode
	}
	fmt.Fprintf(&b, "**%s:** `%s..%s` (%s) · %s\n\n", t.Range, short(m.From), short(m.To), mode, t.ReadOnly)
	fmt.Fprintf(&b, "### %s\n\n%s\n\n", t.Summary, strings.TrimSpace(r.Summary))

	if len(findings) == 0 {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", t.Findings, t.NoFindings)
	} else {
		fmt.Fprintf(&b, "### %s (%d)\n\n| # | %s | %s | %s |\n|---|---|---|---|\n", t.Findings, len(findings), t.Level, t.Location, t.Finding)
		for i, f := range findings {
			fmt.Fprintf(&b, "| %d | %s | %s | %s |\n", i+1, t.Severity[f.Severity], location(f), strings.ReplaceAll(f.Title, "|", `\|`))
		}
		fmt.Fprintf(&b, "\n%s\n\n", legend(t, findings))
		for i, f := range findings {
			body := strings.TrimSpace(f.Detail)
			if f.Suggestion != "" {
				body += "\n\n**" + t.Suggestion + ":** " + strings.TrimSpace(f.Suggestion)
			}
			fmt.Fprintf(&b, "<details><summary>%d. %s: %s</summary>\n\n%s\n\n</details>\n\n", i+1, t.Severity[f.Severity], f.Title, body)
		}
	}
	if len(r.ResolvedSinceLast) > 0 {
		fmt.Fprintf(&b, "### %s\n\n", t.Resolved)
		for _, x := range r.ResolvedSinceLast {
			fmt.Fprintf(&b, "- %s\n", x)
		}
		b.WriteString("\n")
	}
	if r.CoverageNotes != "" {
		fmt.Fprintf(&b, "### %s\n\n%s\n\n", t.Coverage, strings.TrimSpace(r.CoverageNotes))
	}
	steer := strings.TrimSpace(r.SteeringPrompt)
	if steer == "" {
		steer = t.NoFollowUp
	}
	f := fence(steer)
	fmt.Fprintf(&b, "### %s\n\n<details><summary>%s</summary>\n\n%stext\n%s\n%s\n\n</details>\n\n", t.NextPrompt, t.CopyHint, f, steer, f)
	b.WriteString(t.Footer + "\n\n")
	b.WriteString(state.Marker(m.To))

	body := NeutralizeQuickActions(b.String())
	if len(body) > maxBody {
		keep := body[:maxBody-200]
		for !utf8.ValidString(keep) {
			keep = keep[:len(keep)-1]
		}
		if i := strings.LastIndex(keep, "\n"); i > 0 {
			keep = keep[:i]
		}
		body = keep + "\n\n" + t.Truncated + "\n\n" + state.Marker(m.To)
	}
	return body
}
