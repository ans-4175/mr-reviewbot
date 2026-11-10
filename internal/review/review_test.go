package review

import (
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"

	"mr-reviewbot/internal/forge"
	"mr-reviewbot/internal/gitops"
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
		p := BuildTaskPrompt(mr, plan, "", "OLD-REVIEW", lang)
		for _, want := range []string{"untrusted data", "big.go", "incremental", name, "OLD-REVIEW", "against the code at head"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s prompt missing %q", lang, want)
			}
		}
	}
}
