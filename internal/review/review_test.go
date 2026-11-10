package review

import (
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/ans-4175/mr-reviewbot/internal/forge"
	"github.com/ans-4175/mr-reviewbot/internal/gitops"
)

const good = `{"verdict":"safe_to_merge","verdict_reason":"r","summary":"s","findings":[],"resolved_since_last":[],"coverage_notes":"","steering_prompt":"p"}`

func TestParseValid(t *testing.T) {
	r, err := Parse([]byte(good))
	if err != nil || r.Verdict != "safe_to_merge" {
		t.Fatalf("got %+v, %v", r, err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, b := range []string{
		"not json",
		`{"verdict":"ok-lah","verdict_reason":"r","summary":"s"}`,
		`{"verdict":"safe_to_merge","verdict_reason":"r","summary":"s","findings":[{"severity":"huge","title":"t","detail":"d"}]}`,
		`{"verdict":"safe_to_merge","verdict_reason":"","summary":"s"}`,
	} {
		if _, err := Parse([]byte(b)); err == nil {
			t.Errorf("expected error for %s", b)
		}
	}
}

func TestSteeringPromptDoubleEscapingIsRepaired(t *testing.T) {
	got := unescapeIfDoubleEncoded(`Tasks:\n1. pattern \\s+ and \"x\"`)
	if got != "Tasks:\n1. pattern \\s+ and \"x\"" {
		t.Errorf("got %q", got)
	}
	real := "line1\nline2 with regex \\s+"
	if unescapeIfDoubleEncoded(real) != real {
		t.Error("a prompt with real newlines must be left alone")
	}
}

func TestClipNeverSplitsARune(t *testing.T) {
	got := clip("aé", 2) // 'é' is two bytes; cutting at byte 2 would leave half of it
	if !utf8.ValidString(got) || !strings.HasPrefix(got, "a\n") {
		t.Errorf("got %q", got)
	}
	if clip("abc", 10) != "abc" {
		t.Error("short input must be returned as is")
	}
}

func TestSystemPromptEmbedsSkillsWithoutFrontmatter(t *testing.T) {
	fsys := fstest.MapFS{"prompts/system.md": {Data: []byte("BASE")}}
	for _, s := range skillFiles {
		fsys[s.path] = &fstest.MapFile{Data: []byte("---\nname: x\n---\nBODY-" + s.title)}
	}
	p, err := BuildSystemPrompt(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(p, "BASE") || !strings.Contains(p, "BODY-code-simplification") || strings.Contains(p, "name: x") {
		t.Errorf("bad system prompt: %.200s", p)
	}
}

func TestTaskPrompt(t *testing.T) {
	mr := forge.MR{Title: "T", Author: forge.User{Username: "dev"}, SourceBranch: "feat", TargetBranch: "main"}
	plan := &gitops.Plan{Mode: "incremental", FromSHA: strings.Repeat("a", 40), ToSHA: strings.Repeat("b", 40),
		Files: []string{"x.go"}, Diff: "+hi", Truncated: true, Omitted: []string{"big.go"}}
	for lang, name := range map[string]string{"en": "English", "id": "Bahasa Indonesia"} {
		p := BuildTaskPrompt(mr, plan, "", "OLD-REVIEW", "- @dev:\n  WHY-NOTE\n", lang)
		for _, want := range []string{"untrusted data", "big.go", "incremental", name, "OLD-REVIEW", "against the code at head", "Recent discussion", "WHY-NOTE"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt missing %q", lang, want)
			}
		}
	}
}

func TestFormatDiscussion(t *testing.T) {
	line := 12
	day := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	notes := []forge.Note{
		{Body: "merged main", System: true, Author: forge.User{ID: 1}},
		{Body: "old bot review", Author: forge.User{ID: 42, Username: "bot"}},
		{Body: "kept the loop on purpose:\nfaster for small n", Author: forge.User{ID: 1, Username: "dev"}, CreatedAt: day,
			Position: &forge.Position{NewPath: "a.go", NewLine: &line}},
		{Body: "  ", Author: forge.User{ID: 2, Username: "rev"}},
	}
	got := FormatDiscussion(notes, 42)
	want := "- @dev 2026-11-10 on a.go:12:\n  kept the loop on purpose:\n  faster for small n\n"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	var many []forge.Note
	for i := 0; i < 30; i++ {
		many = append(many, forge.Note{Body: fmt.Sprintf("c%02d", i), Author: forge.User{ID: 1, Username: "dev"}})
	}
	got = FormatDiscussion(many, 42)
	if strings.Contains(got, "c09") || !strings.Contains(got, "c10") || !strings.Contains(got, "c29") {
		t.Errorf("want the newest 20 only: %s", got)
	}
	if FormatDiscussion(nil, 42) != "" {
		t.Error("no notes, no discussion")
	}
}
