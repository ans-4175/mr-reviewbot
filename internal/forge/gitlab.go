package forge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const pageSize = 100

// GitLab wraps `glab api`; auth is whatever glab is logged in as for Host.
type GitLab struct {
	Host    string
	Command string
	user    *User
}

func NewGitLab(host, command string) *GitLab {
	if host == "" {
		host = "gitlab.com"
	}
	if command == "" {
		command = "glab"
	}
	return &GitLab{Host: host, Command: command}
}

func (g *GitLab) Name() string { return "gitlab" }

func enc(project string) string { return strings.ReplaceAll(project, "/", "%2F") }

func (g *GitLab) run(stdin []byte, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, g.Command, append([]string{"api", "--hostname", g.Host}, args...)...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return nil, fmt.Errorf("%s api %s: %v: %.400s", g.Command, args[0], err, msg)
	}
	return out.Bytes(), nil
}

func get[T any](g *GitLab, endpoint string) (T, error) {
	var v T
	b, err := g.run(nil, endpoint)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(b, &v)
}

func getAll[T any](g *GitLab, endpoint string) ([]T, error) {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	var all []T
	for page := 1; ; page++ {
		chunk, err := get[[]T](g, fmt.Sprintf("%s%sper_page=%d&page=%d", endpoint, sep, pageSize, page))
		if err != nil {
			return nil, err
		}
		all = append(all, chunk...)
		if len(chunk) < pageSize {
			return all, nil
		}
	}
}

func (g *GitLab) User() (*User, error) {
	if g.user == nil {
		u, err := get[User](g, "user")
		if err != nil {
			return nil, err
		}
		g.user = &u
	}
	return g.user, nil
}

func (g *GitLab) OpenMRs(project string) ([]MR, error) {
	mrs, err := getAll[MR](g, fmt.Sprintf("projects/%s/merge_requests?state=opened&order_by=updated_at&sort=desc", enc(project)))
	if err != nil {
		return nil, err
	}
	out := mrs[:0]
	for _, m := range mrs {
		if !m.Draft {
			out = append(out, m)
		}
	}
	return out, nil
}

func (g *GitLab) MR(project string, iid int) (MR, error) {
	return get[MR](g, fmt.Sprintf("projects/%s/merge_requests/%d", enc(project), iid))
}

func (g *GitLab) Notes(project string, iid int) ([]Note, error) {
	return getAll[Note](g, fmt.Sprintf("projects/%s/merge_requests/%d/notes?sort=asc&order_by=created_at", enc(project), iid))
}

func (g *GitLab) PostNote(project string, iid int, body string) error {
	payload, _ := json.Marshal(map[string]string{"body": body})
	_, err := g.run(payload, "-X", "POST", fmt.Sprintf("projects/%s/merge_requests/%d/notes", enc(project), iid),
		"--input", "-", "-H", "Content-Type: application/json")
	return err
}

func (g *GitLab) CloneURL(project string) string {
	return fmt.Sprintf("https://%s/%s.git", g.Host, project)
}

func (g *GitLab) CredentialHelper() string { return "!" + g.Command + " auth git-credential" }

func (g *GitLab) HeadRef(iid int) string { return fmt.Sprintf("refs/merge-requests/%d/head", iid) }

func (g *GitLab) ParseURL(url string) (string, int, bool) {
	re := regexp.MustCompile(`^https?://` + regexp.QuoteMeta(g.Host) + `/(.+?)/-/merge_requests/(\d+)`)
	m := re.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", 0, false
	}
	iid, _ := strconv.Atoi(m[2])
	return m[1], iid, true
}
