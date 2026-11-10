package state

import (
	"testing"
	"time"

	"mr-reviewbot/internal/config"
	"mr-reviewbot/internal/forge"
)

const bot = 7

var (
	shaA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	now  = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	repo = config.Repo{Path: "g/p", Enabled: true, LookbackDays: 7, SkipLabels: []string{"no-ai-review"}}
)

func mr(mod func(*forge.MR)) forge.MR {
	m := forge.MR{State: "opened", Author: forge.User{ID: 1, Username: "dev"}, TargetBranch: "main", SHA: shaB,
		UpdatedAt: time.Date(2026, 11, 9, 22, 4, 0, 0, time.UTC)}
	if mod != nil {
		mod(&m)
	}
	return m
}

func note(sha string, author int64, system bool) forge.Note {
	return forge.Note{Author: forge.User{ID: author}, System: system, Body: "review\n" + Marker(sha)}
}

func TestLastReview(t *testing.T) {
	if LastReview([]forge.Note{{Author: forge.User{ID: bot}, Body: "hi"}}, bot) != nil {
		t.Error("no marker should give nil")
	}
	if LastReview([]forge.Note{note(shaA, 99, false)}, bot) != nil {
		t.Error("marker from another user must be ignored")
	}
	if LastReview([]forge.Note{note(shaA, bot, true)}, bot) != nil {
		t.Error("system notes must be ignored")
	}
	if got := LastReview([]forge.Note{note(shaA, bot, false), note(shaB, bot, false)}, bot); got == nil || got.SHA != shaB {
		t.Errorf("want newest marker, got %+v", got)
	}
}

func TestDecide(t *testing.T) {
	cases := []struct {
		name   string
		mr     forge.MR
		last   string
		force  bool
		review bool
		reason string
	}{
		{"new", mr(nil), "", false, true, "new"},
		{"same sha is skipped", mr(nil), shaB, false, false, "up-to-date"},
		{"new push triggers review", mr(nil), shaA, false, true, "updated"},
		{"update beats lookback", mr(func(m *forge.MR) { m.UpdatedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }), shaA, false, true, "updated"},
		{"old unreviewed is skipped", mr(func(m *forge.MR) { m.UpdatedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) }), "", false, false, "stale-first-review"},
		{"draft", mr(func(m *forge.MR) { m.Draft = true }), "", false, false, "draft"},
		{"merged", mr(func(m *forge.MR) { m.State = "merged" }), "", false, false, "not-open"},
		{"skip label", mr(func(m *forge.MR) { m.Labels = []string{"no-ai-review"} }), "", false, false, "skip-label:no-ai-review"},
		{"bot-authored MR is reviewed like any other", mr(func(m *forge.MR) { m.Author.ID = bot }), "", false, true, "new"},
		{"force bypasses up-to-date", mr(nil), shaB, true, true, "forced"},
		{"force never bypasses draft", mr(func(m *forge.MR) { m.Draft = true }), "", true, false, "draft"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(c.mr, c.last, repo, now, c.force, false)
			if d.Review != c.review || d.Reason != c.reason {
				t.Errorf("got %+v, want review=%v reason=%s", d, c.review, c.reason)
			}
		})
	}
}

func TestManualBypassesLookbackOnly(t *testing.T) {
	old := mr(func(m *forge.MR) { m.UpdatedAt = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) })
	if d := Decide(old, "", repo, now, false, true); !d.Review {
		t.Errorf("manual must review a stale MR, got %+v", d)
	}
	if d := Decide(old, shaB, repo, now, false, true); d.Review || d.Reason != "up-to-date" {
		t.Errorf("manual still skips an already-reviewed sha without --force, got %+v", d)
	}
	draft := mr(func(m *forge.MR) { m.Draft = true })
	if d := Decide(draft, "", repo, now, false, true); d.Review {
		t.Errorf("manual must not review a draft, got %+v", d)
	}
}
