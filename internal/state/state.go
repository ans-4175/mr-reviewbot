// Package state decides whether an MR needs a review, from markers in the bot's own notes.
package state

import (
	"fmt"
	"regexp"
	"time"

	"mr-reviewbot/internal/config"
	"mr-reviewbot/internal/forge"
)

// The marker lives in the bot's own MR note, so "last reviewed" survives host loss.
var markerRe = regexp.MustCompile(`<!-- mr-reviewbot v1 sha=([0-9a-f]{40}) -->`)

func Marker(sha string) string { return fmt.Sprintf("<!-- mr-reviewbot v1 sha=%s -->", sha) }

type Last struct {
	SHA  string
	Body string
}

// LastReview returns the newest marker note written by the bot itself (others can't spoof it).
func LastReview(notes []forge.Note, botID int64) *Last {
	var found *Last
	for _, n := range notes {
		if n.System || n.Author.ID != botID {
			continue
		}
		if m := markerRe.FindStringSubmatch(n.Body); m != nil {
			found = &Last{SHA: m[1], Body: n.Body}
		}
	}
	return found
}

type Decision struct {
	Review bool
	Reason string
}

// Decide never filters by author kind: bot-authored MRs are reviewed like any other (use skip_authors to opt out).
// manual (a human asked) bypasses the lookback window but not draft/closed/skip rules.
func Decide(mr forge.MR, lastSHA string, repo config.Repo, now time.Time, force, manual bool) Decision {
	switch {
	case mr.State != "opened":
		return Decision{false, "not-open"}
	case mr.Draft:
		return Decision{false, "draft"}
	}
	for _, l := range mr.Labels {
		for _, s := range repo.SkipLabels {
			if l == s {
				return Decision{false, "skip-label:" + l}
			}
		}
	}
	for _, a := range repo.SkipAuthors {
		if a == mr.Author.Username {
			return Decision{false, "skip-author"}
		}
	}
	if len(repo.TargetBranches) > 0 && !contains(repo.TargetBranches, mr.TargetBranch) {
		return Decision{false, "target-branch-not-listed"}
	}
	switch {
	case mr.SHA == "":
		return Decision{false, "no-head-sha"}
	case force:
		return Decision{true, "forced"}
	case lastSHA == mr.SHA:
		return Decision{false, "up-to-date"}
	case lastSHA != "":
		return Decision{true, "updated"}
	}
	// Never reviewed: ignore old MRs so enabling a repo doesn't flood it.
	if !manual && now.Sub(mr.UpdatedAt) > time.Duration(repo.LookbackDays)*24*time.Hour {
		return Decision{false, "stale-first-review"}
	}
	return Decision{true, "new"}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
