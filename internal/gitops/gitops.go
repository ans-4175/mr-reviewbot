// Package gitops handles the blobless mirror, the review range, and the read-only worktree.
package gitops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// git runs git against a bare dir. Non-zero exits are returned as a code, not an error.
func git(gd string, args ...string) (string, string, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--git-dir", gd}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return out.String(), errb.String(), ee.ExitCode(), nil
	}
	return out.String(), errb.String(), 0, err
}

func mustGit(gd string, args ...string) (string, error) {
	out, stderr, code, err := git(gd, args...)
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git %s failed (%d): %s", strings.Join(args[:min(3, len(args))], " "), code, strings.TrimSpace(stderr))
	}
	return out, nil
}

func MirrorPath(root, project string) string {
	return filepath.Join(root, strings.ReplaceAll(project, "/", "__")+".git")
}

type Remote struct {
	URL        string // clone URL
	CredHelper string // git credential helper backed by the forge CLI; empty = git's own
	HeadRef    string // server ref holding the MR head, e.g. refs/merge-requests/7/head
}

func (r Remote) gitArgs(args ...string) []string {
	if r.CredHelper == "" {
		return args
	}
	return append([]string{"-c", "credential.helper=", "-c", "credential.helper=" + r.CredHelper}, args...)
}

// CanRead proves git (not just the API) can reach the repo with the forge CLI's credentials.
func CanRead(r Remote) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", r.gitArgs("ls-remote", "--quiet", r.URL, "HEAD")...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git ls-remote %s: %v: %.300s", r.URL, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// SyncMirror keeps a blobless bare mirror holding the MR head (refs/mr/<iid>/head) and its target branch.
func SyncMirror(root, project string, remote Remote, iid int, target string) (string, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	gd := MirrorPath(root, project)
	if _, err := os.Stat(gd); os.IsNotExist(err) {
		cmd := exec.Command("git", remote.gitArgs("clone", "--bare", "--filter=blob:none", "--quiet", remote.URL, gd)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("clone %s: %v: %s", project, err, strings.TrimSpace(string(out)))
		}
		// Lazy blob fetches during worktree checkout need the helper in repo config too.
		if remote.CredHelper != "" {
			for _, a := range [][]string{{"--replace-all", "credential.helper", ""}, {"--add", "credential.helper", remote.CredHelper}} {
				if _, err := mustGit(gd, append([]string{"config"}, a...)...); err != nil {
					return "", err
				}
			}
		}
	}
	_, err := mustGit(gd, "fetch", "--quiet", "origin",
		fmt.Sprintf("+%s:refs/mr/%d/head", remote.HeadRef, iid),
		fmt.Sprintf("+refs/heads/%s:refs/target/%s", target, target))
	return gd, err
}

type Plan struct {
	Mode      string // full | incremental | full-forcepush
	FromSHA   string
	ToSHA     string
	Files     []string
	Diff      string
	Commits   string
	Truncated bool
	Omitted   []string
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func names(gd, a, b string) ([]string, error) {
	out, err := mustGit(gd, "diff", "--name-only", a, b)
	return lines(out), err
}

// isAncestor treats an unknown object (e.g. gc'd after a force-push) as "not an ancestor".
func isAncestor(gd, a, b string) bool {
	_, _, code, err := git(gd, "merge-base", "--is-ancestor", a, b)
	return err == nil && code == 0
}

// capDiff keeps whole files until the line budget is spent.
func capDiff(diff string, maxLines int) (string, bool, []string) {
	if strings.Count(diff, "\n") <= maxLines {
		return diff, false, nil
	}
	parts := strings.Split(diff, "\ndiff --git ")
	for i := 1; i < len(parts); i++ {
		parts[i] = "diff --git " + parts[i]
	}
	var kept, omitted []string
	used := 0
	for _, p := range parts {
		n := strings.Count(p, "\n") + 1
		if used+n > maxLines && len(kept) > 0 {
			head := strings.SplitN(p, "\n", 2)[0]
			omitted = append(omitted, head[strings.LastIndex(head, " b/")+3:])
			continue
		}
		kept = append(kept, p)
		used += n
	}
	return strings.Join(kept, "\n"), true, omitted
}

// PlanRange picks what to review: last-reviewed..head when that is a clean fast-forward, else the whole MR.
func PlanRange(gd, targetRef, head, last string, maxLines int) (*Plan, error) {
	baseOut, err := mustGit(gd, "merge-base", targetRef, head)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(baseOut)
	mrFiles, err := names(gd, base, head)
	if err != nil {
		return nil, err
	}
	p := &Plan{ToSHA: head}
	if last != "" && last != head && isAncestor(gd, last, head) {
		p.Mode, p.FromSHA = "incremental", last
		inMR := map[string]bool{}
		for _, f := range mrFiles {
			inMR[f] = true
		}
		changed, err := names(gd, last, head)
		if err != nil {
			return nil, err
		}
		for _, f := range changed { // drop changes that only arrived by merging the target in
			if inMR[f] {
				p.Files = append(p.Files, f)
			}
		}
	} else {
		p.Mode, p.FromSHA = "full", base
		if last != "" && last != head {
			p.Mode = "full-forcepush"
		}
		p.Files = append(p.Files, mrFiles...)
		sort.Strings(p.Files)
	}
	diff := ""
	if len(p.Files) > 0 {
		diff, err = mustGit(gd, append([]string{"diff", "--no-color", "-U3", p.FromSHA, head, "--"}, p.Files...)...)
		if err != nil {
			return nil, err
		}
	}
	p.Commits, err = mustGit(gd, "log", "--no-merges", "--format=%h %an: %s", "-n", "50", p.FromSHA+".."+head)
	if err != nil {
		return nil, err
	}
	p.Diff, p.Truncated, p.Omitted = capDiff(diff, maxLines)
	return p, nil
}

// Show returns a file's content at ref, or "" if it doesn't exist there.
func Show(gd, ref, path string) string {
	out, _, code, err := git(gd, "show", ref+":"+path)
	if err != nil || code != 0 {
		return ""
	}
	return out
}

// Worktree checks out head into dest; call the returned func to remove it.
func Worktree(gd, head, dest string) (func(), error) {
	_ = os.RemoveAll(dest)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}
	if _, err := mustGit(gd, "worktree", "add", "--detach", "--force", dest, head); err != nil {
		return nil, err
	}
	return func() {
		_, _, _, _ = git(gd, "worktree", "remove", "--force", dest)
		_ = os.RemoveAll(dest)
	}, nil
}
