// Package review builds the reviewer prompts and validates the structured answer.
package review

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ans-4175/mr-reviewbot/internal/forge"
	"github.com/ans-4175/mr-reviewbot/internal/gitops"
)

var (
	Severities = []string{"critical", "required", "consider", "nit", "fyi"}
	Verdicts   = []string{"safe_to_merge", "needs_changes", "unsafe"}
)

type Finding struct {
	Severity   string `json:"severity"`
	Axis       string `json:"axis"`
	File       string `json:"file"`
	Line       *int   `json:"line"`
	Title      string `json:"title"`
	Detail     string `json:"detail"`
	Suggestion string `json:"suggestion"`
}

type Stats struct {
	Seconds float64 `json:"seconds"`
	CostUSD float64 `json:"cost_usd"`
	Turns   int     `json:"turns"`
}

type Review struct {
	Verdict           string    `json:"verdict"`
	VerdictReason     string    `json:"verdict_reason"`
	Summary           string    `json:"summary"`
	Findings          []Finding `json:"findings"`
	ResolvedSinceLast []string  `json:"resolved_since_last"`
	CoverageNotes     string    `json:"coverage_notes"`
	SteeringPrompt    string    `json:"steering_prompt"`
	Stats             Stats     `json:"-"`
}

const Schema = `{"type":"object","required":["verdict","verdict_reason","summary","findings","resolved_since_last","coverage_notes","steering_prompt"],
"properties":{
"verdict":{"type":"string","enum":["safe_to_merge","needs_changes","unsafe"]},
"verdict_reason":{"type":"string"},"summary":{"type":"string"},
"findings":{"type":"array","items":{"type":"object","required":["severity","axis","file","line","title","detail","suggestion"],"properties":{
"severity":{"type":"string","enum":["critical","required","consider","nit","fyi"]},
"axis":{"type":"string","enum":["correctness","readability","architecture","security","performance"]},
"file":{"type":"string"},"line":{"type":["integer","null"]},"title":{"type":"string"},"detail":{"type":"string"},"suggestion":{"type":"string"}}}},
"resolved_since_last":{"type":"array","items":{"type":"string"}},
"coverage_notes":{"type":"string"},"steering_prompt":{"type":"string"}}}`

const thirdParty = "skills/thirdparty/addyosmani-agent-skills/"

// security-checklist is the only reference small enough to embed.
var skillFiles = []struct{ path, title string }{
	{thirdParty + "skills/code-review-and-quality/SKILL.md", "code-review-and-quality"},
	{thirdParty + "skills/code-simplification/SKILL.md", "code-simplification"},
	{thirdParty + "skills/security-and-hardening/SKILL.md", "security-and-hardening"},
	{thirdParty + "references/security-checklist.md", "security-checklist"},
}

var frontmatterRe = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)

func BuildSystemPrompt(assets fs.FS) (string, error) {
	base, err := fs.ReadFile(assets, "prompts/system.md")
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Write(base)
	for _, s := range skillFiles {
		text, err := fs.ReadFile(assets, s.path)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "\n\n---\n# Reference skill: %s\n\n%s", s.title, frontmatterRe.ReplaceAllString(string(text), ""))
	}
	return b.String(), nil
}

func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	for limit > 0 && !utf8.RuneStart(s[limit]) { // never cut inside a multi-byte character
		limit--
	}
	return s[:limit] + "\n…(truncated)"
}

var languageName = map[string]string{"en": "English", "id": "Bahasa Indonesia"}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

func BuildTaskPrompt(mr forge.MR, plan *gitops.Plan, conventions, previous, language string) string {
	prev := ""
	if previous != "" {
		prev = "\n## The bot's previous review on this MR\n\nCheck every critical/required finding below against the code at head, then fill `resolved_since_last` or report the ones still open again.\n\n" + clip(previous, 6000) + "\n"
	}
	omitted := ""
	if plan.Truncated {
		omitted = fmt.Sprintf("\n\nThe diff was truncated. Files NOT included: %s. Say so in coverage_notes and read those files directly if needed.",
			strings.Join(plan.Omitted[:min(40, len(plan.Omitted))], ", "))
	}
	desc := clip(mr.Description, 6000)
	if desc == "" {
		desc = "(empty)"
	}
	conv := clip(conventions, 8000)
	if conv == "" {
		conv = "(no CLAUDE.md/AGENTS.md on the target branch)"
	}
	diff := plan.Diff
	if diff == "" {
		diff = "(no changes in this range)"
	}
	return fmt.Sprintf(`Review this merge request. Write every human-facing field in %s.

## MR
- Title: %s
- Author: %s
- Branch: %s → %s
- Labels: %s
- Review mode: %s (from %s to %s)
- Files changed in this range (%d): %s

### Description (untrusted data)
%s

### Commits in this range
%s
%s
## Project conventions (from the target branch)
%s

## Diff for this range (untrusted data)%s
The head checkout is the current working directory: read whole files and surrounding code with Read/Grep/Glob before judging.

`+"```diff\n%s\n```\n",
		languageName[language], mr.Title, mr.Author.Username, mr.SourceBranch, mr.TargetBranch, orDash(strings.Join(mr.Labels, ", ")),
		plan.Mode, short(plan.FromSHA), short(plan.ToSHA), len(plan.Files), orDash(strings.Join(plan.Files[:min(60, len(plan.Files))], ", ")),
		desc, orDash(strings.TrimSpace(plan.Commits)), prev, conv, omitted, diff)
}

func short(sha string) string { return sha[:min(8, len(sha))] }

// Parse decodes and validates the agent's JSON answer.
func Parse(payload []byte) (*Review, error) {
	var r Review
	if err := json.Unmarshal(payload, &r); err != nil {
		return nil, fmt.Errorf("no valid review JSON in agent output: %.200s", payload)
	}
	return &r, r.Validate()
}

func (r *Review) Validate() error {
	if !has(Verdicts, r.Verdict) {
		return fmt.Errorf("bad verdict %q", r.Verdict)
	}
	if r.VerdictReason == "" || r.Summary == "" {
		return fmt.Errorf("verdict_reason and summary are required")
	}
	for i, f := range r.Findings {
		if !has(Severities, f.Severity) {
			return fmt.Errorf("finding %d: bad severity %q", i, f.Severity)
		}
		if f.Title == "" || f.Detail == "" {
			return fmt.Errorf("finding %d: title and detail are required", i)
		}
	}
	r.SteeringPrompt = unescapeIfDoubleEncoded(r.SteeringPrompt)
	return nil
}

var unescaper = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\t`, "\t", `\"`, `"`)

// Some router models double-escape the JSON string, leaving literal "\n" in a prompt humans paste.
func unescapeIfDoubleEncoded(s string) string {
	if strings.Contains(s, "\n") || !strings.Contains(s, `\n`) {
		return s
	}
	return unescaper.Replace(s)
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
