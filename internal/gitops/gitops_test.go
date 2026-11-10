package gitops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type repo struct {
	t   *testing.T
	dir string
}

func (r *repo) sh(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "HOME="+r.dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) commit(name, text string) string {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(text), 0o644); err != nil {
		r.t.Fatal(err)
	}
	r.sh("add", "-A")
	r.sh("commit", "-q", "-m", "edit "+name)
	return r.sh("rev-parse", "HEAD")
}

func newRepo(t *testing.T) (*repo, string, string) {
	r := &repo{t: t, dir: t.TempDir()}
	r.sh("init", "-q", "-b", "main")
	base := r.commit("base.txt", "base\n")
	r.sh("checkout", "-q", "-b", "feat")
	return r, base, filepath.Join(r.dir, ".git")
}

func TestFirstReviewIsFull(t *testing.T) {
	r, _, gd := newRepo(t)
	h := r.commit("a.txt", "a\n")
	p, err := PlanRange(gd, "main", h, "", 1000)
	if err != nil || p.Mode != "full" || len(p.Files) != 1 || p.Files[0] != "a.txt" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestIncrementalOnlyCoversNewCommits(t *testing.T) {
	r, _, gd := newRepo(t)
	h1 := r.commit("a.txt", "a\n")
	h2 := r.commit("b.txt", "b\n")
	p, err := PlanRange(gd, "main", h2, h1, 1000)
	if err != nil || p.Mode != "incremental" || len(p.Files) != 1 || p.Files[0] != "b.txt" || strings.Contains(p.Diff, "a.txt") {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestForcePushFallsBackToFull(t *testing.T) {
	r, base, gd := newRepo(t)
	h1 := r.commit("a.txt", "a\n")
	r.sh("reset", "-q", "--hard", base)
	h2 := r.commit("c.txt", "c\n")
	p, err := PlanRange(gd, "main", h2, h1, 1000)
	if err != nil || p.Mode != "full-forcepush" || p.FromSHA != base {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestUnknownLastSHAIsForcePush(t *testing.T) {
	r, _, gd := newRepo(t)
	h := r.commit("a.txt", "a\n")
	p, err := PlanRange(gd, "main", h, strings.Repeat("f", 40), 1000)
	if err != nil || p.Mode != "full-forcepush" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestMergedTargetChangesExcluded(t *testing.T) {
	r, _, gd := newRepo(t)
	h1 := r.commit("a.txt", "a\n")
	r.sh("checkout", "-q", "main")
	r.commit("main_only.txt", "m\n")
	r.sh("checkout", "-q", "feat")
	r.sh("merge", "-q", "--no-edit", "main")
	h2 := r.commit("b.txt", "b\n")
	p, err := PlanRange(gd, "main", h2, h1, 1000)
	if err != nil || len(p.Files) != 1 || p.Files[0] != "b.txt" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestDiffCappedPerFile(t *testing.T) {
	r, _, gd := newRepo(t)
	var a, b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&a, "line%d\n", i)
		fmt.Fprintf(&b, "row%d\n", i)
	}
	r.commit("a.txt", a.String())
	h := r.commit("b.txt", b.String())
	p, err := PlanRange(gd, "main", h, "", 60)
	if err != nil || !p.Truncated || len(p.Omitted) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestWorktreeChecksOutSymlinksAsPlainFiles(t *testing.T) {
	r, _, gd := newRepo(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.MkdirAll(secret, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(r.dir, "evil")); err != nil {
		t.Fatal(err)
	}
	r.sh("add", "-A")
	r.sh("commit", "-q", "-m", "add symlink")
	head := r.sh("rev-parse", "HEAD")
	dest := filepath.Join(t.TempDir(), "wt")
	cleanup, err := Worktree(gd, head, dest)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	fi, err := os.Lstat(filepath.Join(dest, "evil"))
	if err != nil || fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		t.Fatalf("want a plain file, got %v %v", fi.Mode(), err)
	}
}
