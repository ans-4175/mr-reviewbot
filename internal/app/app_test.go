package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ans-4175/mr-reviewbot/internal/agent"
	"github.com/ans-4175/mr-reviewbot/internal/config"
	"github.com/ans-4175/mr-reviewbot/internal/forge"
	"github.com/ans-4175/mr-reviewbot/internal/queue"
	"github.com/ans-4175/mr-reviewbot/internal/state"
)

// fakeForge serves one MR from a local bare repo and records posted notes.
type fakeForge struct {
	remote string
	mr     forge.MR
	notes  []forge.Note
}

func (f *fakeForge) Name() string                            { return "fake" }
func (f *fakeForge) User() (*forge.User, error)              { return &forge.User{ID: 42, Username: "bot"}, nil }
func (f *fakeForge) OpenMRs(string) ([]forge.MR, error)      { return []forge.MR{f.mr}, nil }
func (f *fakeForge) MR(string, int) (forge.MR, error)        { return f.mr, nil }
func (f *fakeForge) Notes(string, int) ([]forge.Note, error) { return f.notes, nil }
func (f *fakeForge) CloneURL(string) string                  { return f.remote }
func (f *fakeForge) CredentialHelper() string                { return "" }
func (f *fakeForge) HeadRef(iid int) string                  { return fmt.Sprintf("refs/merge-requests/%d/head", iid) }
func (f *fakeForge) ParseURL(string) (string, int, bool)     { return "", 0, false }
func (f *fakeForge) PostNote(_ string, _ int, b string) error {
	f.notes = append(f.notes, forge.Note{ID: int64(len(f.notes) + 1), Body: b, Author: forge.User{ID: 42}})
	return nil
}

type fakeAgent struct {
	answer string
	tasks  []string
	dirs   []string
	leaky  bool // reads outside its checkout during the probe
	probes int
}

func (a *fakeAgent) Name() string                            { return "fake" }
func (a *fakeAgent) Version(context.Context) (string, error) { return "fake 1.0", nil }
func (a *fakeAgent) Review(_ context.Context, r agent.Request) (*agent.Output, error) {
	if in, err := os.ReadFile(filepath.Join(r.Dir, "inside.txt")); err == nil { // the isolation probe
		a.probes++
		text := string(in)
		if a.leaky {
			out, _ := os.ReadFile(filepath.Join(r.Dir, "..", "outside.txt"))
			text += " " + string(out)
		}
		return &agent.Output{JSON: []byte(`{"summary":"` + text + `"}`)}, nil
	}
	a.tasks, a.dirs = append(a.tasks, r.Task), append(a.dirs, r.Dir)
	if _, err := os.Stat(filepath.Join(r.Dir, "feature.txt")); err != nil {
		return nil, fmt.Errorf("worktree missing the MR head: %v", err)
	}
	return &agent.Output{JSON: []byte(a.answer), CostUSD: 0.1}, nil
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, text string) string {
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "edit "+name)
	return git(t, dir, "rev-parse", "HEAD")
}

const answer = `{"verdict":"safe_to_merge","verdict_reason":"r","summary":"s","findings":[{"severity":"required","axis":"correctness","file":"feature.txt","line":1,"title":"T","detail":"D","suggestion":"S"}],"resolved_since_last":[],"coverage_notes":"","steering_prompt":"fix it"}`

// setup builds a server repo with an MR head ref, plus an env wired to fakes.
func setup(t *testing.T, lang string) (*env, *fakeForge, *fakeAgent, string, *bytes.Buffer) {
	work := t.TempDir()
	git(t, work, "init", "-q", "-b", "main")
	commit(t, work, "base.txt", "base\n")
	git(t, work, "checkout", "-q", "-b", "feat")
	head := commit(t, work, "feature.txt", "new\n")
	remote := filepath.Join(t.TempDir(), "server.git")
	git(t, work, "clone", "-q", "--bare", work, remote)
	git(t, remote, "update-ref", "refs/merge-requests/7/head", head)

	cfg, err := config.Parse([]byte(`{"repos":[{"path":"acme/app","language":"` + lang + `"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	st := t.TempDir()
	q, err := queue.New(st)
	if err != nil {
		t.Fatal(err)
	}
	ff := &fakeForge{remote: remote, mr: forge.MR{IID: 7, Title: "Add feature", State: "opened", SHA: head,
		SourceBranch: "feat", TargetBranch: "main", Author: forge.User{ID: 1, Username: "dev"}, UpdatedAt: time.Now()}}
	fa := &fakeAgent{answer: answer}
	var out, errb bytes.Buffer
	return &env{state: st, cfg: cfg, forge: ff, agent: fa, q: q, out: &out, err: &errb}, ff, fa, work, &errb
}

func TestPollReviewsPostsThenSkipsUntilNewPush(t *testing.T) {
	e, ff, fa, work, logs := setup(t, "en")

	if code := e.poll(false, 5); code != 0 {
		t.Fatalf("poll exit %d\n%s", code, logs)
	}
	if len(ff.notes) != 1 {
		t.Fatalf("want 1 posted note, got %d\n%s", len(ff.notes), logs)
	}
	body := ff.notes[0].Body
	for _, want := range []string{"🟡 Needs changes", "🟠 Fix", state.Marker(ff.mr.SHA), "fix it"} {
		if !strings.Contains(body, want) {
			t.Errorf("comment missing %q", want)
		}
	}
	if !strings.Contains(fa.tasks[0], "Review mode: full") {
		t.Error("first review must be full")
	}
	if _, err := os.Stat(fa.dirs[0]); !os.IsNotExist(err) {
		t.Error("worktree must be removed after the review")
	}

	// Same sha: nothing to do.
	e.poll(false, 5)
	if len(ff.notes) != 1 || len(fa.tasks) != 1 {
		t.Fatalf("up-to-date MR was reviewed again")
	}

	// New push: incremental review of only the new commit.
	git(t, work, "checkout", "-q", "feat")
	head2 := commit(t, work, "more.txt", "more\n")
	git(t, work, "push", "-q", ff.remote, "feat")
	git(t, ff.remote, "update-ref", "refs/merge-requests/7/head", head2)
	ff.mr.SHA = head2
	if code := e.poll(false, 5); code != 0 {
		t.Fatalf("second poll exit %d\n%s", code, logs)
	}
	if len(ff.notes) != 2 || !strings.Contains(fa.tasks[1], "Review mode: incremental") || !strings.Contains(fa.tasks[1], "The bot's previous review") {
		t.Fatalf("want an incremental second review that sees the previous one, got %d notes\n%s", len(ff.notes), logs)
	}
	if strings.Contains(fa.tasks[1], "feature.txt\n") || !strings.Contains(fa.tasks[1], "more.txt") {
		t.Error("incremental range must cover only the new commit")
	}
	pending, _ := e.q.Pending()
	if len(pending) != 0 {
		t.Errorf("queue not drained: %d", len(pending))
	}
}

func TestIndonesianComment(t *testing.T) {
	e, ff, fa, _, logs := setup(t, "id")
	if code := e.poll(false, 5); code != 0 {
		t.Fatalf("poll exit %d\n%s", code, logs)
	}
	if !strings.Contains(ff.notes[0].Body, "🟠 Perbaiki") || !strings.Contains(fa.tasks[0], "Bahasa Indonesia") {
		t.Error("language id not applied")
	}
}

func TestDryRunNeverPostsOrQueues(t *testing.T) {
	e, ff, _, _, logs := setup(t, "en")
	if code := e.poll(true, 5); code != 0 {
		t.Fatalf("exit %d\n%s", code, logs)
	}
	pending, _ := e.q.Pending()
	if len(ff.notes) != 0 || len(pending) != 0 {
		t.Error("dry run posted or queued")
	}
}

func TestBadAgentAnswerFailsAndStaysQueued(t *testing.T) {
	e, ff, fa, _, _ := setup(t, "en")
	fa.answer = `{"verdict":"maybe"}`
	if code := e.poll(false, 5); code != 1 {
		t.Fatalf("want exit 1, got %d", code)
	}
	pending, _ := e.q.Pending()
	if len(ff.notes) != 0 || len(pending) != 1 || pending[0].Attempts != 1 {
		t.Errorf("failed job must stay queued with one attempt: %+v", pending)
	}
}

func TestIsolationProbeRunsOnceThenStamps(t *testing.T) {
	e, ff, fa, _, logs := setup(t, "en")
	if code := e.poll(false, 5); code != 0 || len(ff.notes) != 1 {
		t.Fatalf("exit %d\n%s", code, logs)
	}
	if fa.probes != 1 {
		t.Fatalf("want one probe before the first review, got %d", fa.probes)
	}
	e.isolated = false // a new process: the stamp must spare a second probe
	if err := e.ensureIsolated(context.Background()); err != nil || fa.probes != 1 {
		t.Errorf("stamp ignored: probes=%d err=%v", fa.probes, err)
	}
}

func TestLeakyAgentIsRefusedAndJobStaysQueued(t *testing.T) {
	e, ff, fa, _, _ := setup(t, "en")
	fa.leaky = true
	for run := 0; run < 4; run++ { // more runs than MaxAttempts: a host problem must never park an MR
		e.isolated, e.isoErr = false, nil // each run is a new process
		if code := e.poll(false, 5); code != 1 {
			t.Fatalf("run %d: want exit 1, got %d", run, code)
		}
	}
	pending, _ := e.q.Pending()
	if len(ff.notes) != 0 || len(fa.tasks) != 0 || len(pending) != 1 || pending[0].Attempts != 0 || len(e.q.FailedJobs()) != 0 {
		t.Errorf("leaky agent must never review nor charge the job: notes=%d tasks=%d pending=%+v failed=%d",
			len(ff.notes), len(fa.tasks), pending, len(e.q.FailedJobs()))
	}
	if fa.probes != 4 {
		t.Errorf("want one probe per run, got %d", fa.probes)
	}
}
