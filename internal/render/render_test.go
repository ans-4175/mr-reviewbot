package render

import (
	"regexp"
	"strings"
	"testing"

	"mr-reviewbot/internal/review"
)

var markerRe = regexp.MustCompile(`<!-- mr-reviewbot v1 sha=([0-9a-f]{40}) -->`)

func meta(lang string) Meta {
	return Meta{From: strings.Repeat("1", 40), To: strings.Repeat("2", 40), Mode: "incremental", Language: lang}
}

func rv(verdict string, findings ...review.Finding) *review.Review {
	return &review.Review{Verdict: verdict, VerdictReason: "reason", Summary: "summary", Findings: findings, SteeringPrompt: "Do X"}
}

func fd(sev, title string) review.Finding {
	line := 3
	return review.Finding{Severity: sev, Axis: "correctness", File: "a.go", Line: &line, Title: title, Detail: "d", Suggestion: "s"}
}

func TestEnglishIsDefault(t *testing.T) {
	for _, lang := range []string{"en", "", "fr"} {
		body := Comment(rv("needs_changes", fd("required", "A"), fd("nit", "B")), meta(lang))
		for _, want := range []string{"🟡 Needs changes", "🟠 Fix", "⚪ Optional", "_Levels: 🟠 Fix = fix before merging · ⚪ Optional = nice to have, fine to skip_", "incremental since the last review"} {
			if !strings.Contains(body, want) {
				t.Errorf("lang %q: missing %q", lang, want)
			}
		}
	}
}

func TestIndonesianLabels(t *testing.T) {
	body := Comment(rv("needs_changes", fd("required", "A"), fd("nit", "B")), meta("id"))
	for _, want := range []string{"🟡 Perlu penyesuaian", "🟠 Perbaiki", "⚪ Sunnah", "_Level: 🟠 Perbaiki = perbaiki sebelum merge · ⚪ Sunnah = baik dilakukan, boleh dilewati_", "### Ringkasan"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(body, "Needs changes") {
		t.Error("english leaked into id comment")
	}
}

func TestEveryLanguageCoversEveryKey(t *testing.T) {
	for lang, tx := range texts {
		for _, sev := range review.Severities {
			if tx.Severity[sev] == "" || tx.Meaning[sev] == "" {
				t.Errorf("%s: severity %s missing", lang, sev)
			}
		}
		for _, v := range review.Verdicts {
			if tx.Verdict[v] == "" {
				t.Errorf("%s: verdict %s missing", lang, v)
			}
		}
	}
}

func TestNoLegendWithoutFindings(t *testing.T) {
	if strings.Contains(Comment(rv("safe_to_merge"), meta("en")), "_Levels:") {
		t.Error("no findings, no legend")
	}
}

func TestEmptySteeringPromptGetsPlaceholder(t *testing.T) {
	r := rv("safe_to_merge")
	r.SteeringPrompt = ""
	if !strings.Contains(Comment(r, meta("en")), "(no follow-up)") {
		t.Error("placeholder missing")
	}
}

func TestGuardVerdict(t *testing.T) {
	for _, c := range []struct{ in, sev, want string }{
		{"safe_to_merge", "critical", "unsafe"},
		{"safe_to_merge", "required", "needs_changes"},
		{"safe_to_merge", "nit", "safe_to_merge"},
	} {
		r := rv(c.in, fd(c.sev, "t"))
		GuardVerdict(r)
		if r.Verdict != c.want {
			t.Errorf("%s+%s: got %s want %s", c.in, c.sev, r.Verdict, c.want)
		}
	}
}

func TestMarkerMatchesAndSurvivesTruncation(t *testing.T) {
	body := Comment(rv("safe_to_merge"), meta("en"))
	if m := markerRe.FindStringSubmatch(body); m == nil || m[1] != meta("en").To {
		t.Fatal("marker missing or wrong sha")
	}
	r := rv("safe_to_merge")
	r.SteeringPrompt = strings.Repeat("x\n", 40000)
	body = Comment(r, meta("en"))
	if m := markerRe.FindStringSubmatch(body); m == nil || m[1] != meta("en").To {
		t.Error("marker lost after truncation")
	}
	if len(body) > maxBody {
		t.Errorf("body too long: %d", len(body))
	}
}

func TestQuickActionsNeutralized(t *testing.T) {
	got := NeutralizeQuickActions("a\n/merge\n  /approve")
	if strings.Contains(got, "\n/merge") || strings.Contains(got, "  /approve") {
		t.Errorf("quick action survived: %q", got)
	}
}

func TestFenceSurvivesBackticks(t *testing.T) {
	r := rv("safe_to_merge")
	r.SteeringPrompt = "use ```js\nx\n``` ok"
	body := Comment(r, meta("en"))
	if !strings.Contains(body, "````text") || strings.Contains(body, "`````") {
		t.Error("expected a 4-backtick fence")
	}
}

func TestFindingsSortedBySeverity(t *testing.T) {
	body := Comment(rv("needs_changes", fd("nit", "NITT"), fd("required", "REQQ")), meta("en"))
	if strings.Index(body, "REQQ") > strings.Index(body, "NITT") {
		t.Error("required must come before nit")
	}
}
