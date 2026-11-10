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

// GitHub wraps `gh api`; auth is whatever gh is logged in as for Host. PRs map onto MR, issue comments onto Note.
type GitHub struct {
	Host    string
	Command string
	user    *User
}

func NewGitHub(host, command string) *GitHub {
	if host == "" {
		host = "github.com"
	}
	if command == "" {
		command = "gh"
	}
	return &GitHub{Host: host, Command: command}
}

func (g *GitHub) Name() string { return "github" }

func (g *GitHub) run(stdin []byte, args ...string) ([]byte, error) {
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

func ghGet[T any](g *GitHub, endpoint string) (T, error) {
	var v T
	b, err := g.run(nil, endpoint)
	if err != nil {
		return v, err
	}
	return v, json.Unmarshal(b, &v)
}

func ghGetAll[T any](g *GitHub, endpoint string) ([]T, error) {
	sep := "?"
	if strings.Contains(endpoint, "?") {
		sep = "&"
	}
	var all []T
	for page := 1; ; page++ {
		chunk, err := ghGet[[]T](g, fmt.Sprintf("%s%sper_page=%d&page=%d", endpoint, sep, pageSize, page))
		if err != nil {
			return nil, err
		}
		all = append(all, chunk...)
		if len(chunk) < pageSize {
			return all, nil
		}
	}
}

type ghUser struct {
	ID    int64  `json:"id"`
	Login string `json:"login"`
}

type ghPull struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Draft     bool      `json:"draft"`
	User      ghUser    `json:"user"`
	UpdatedAt time.Time `json:"updated_at"`
	Labels    []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Head struct {
		SHA string `json:"sha"`
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
}

func (p ghPull) toMR() MR {
	state := p.State
	if state == "open" {
		state = "opened"
	}
	m := MR{IID: p.Number, Title: p.Title, Description: p.Body, State: state, Draft: p.Draft, SHA: p.Head.SHA,
		SourceBranch: p.Head.Ref, TargetBranch: p.Base.Ref, Author: User{ID: p.User.ID, Username: p.User.Login}, UpdatedAt: p.UpdatedAt}
	for _, l := range p.Labels {
		m.Labels = append(m.Labels, l.Name)
	}
	return m
}

func (g *GitHub) User() (*User, error) {
	if g.user == nil {
		u, err := ghGet[ghUser](g, "user")
		if err != nil {
			return nil, err
		}
		g.user = &User{ID: u.ID, Username: u.Login}
	}
	return g.user, nil
}

func (g *GitHub) OpenMRs(project string) ([]MR, error) {
	pulls, err := ghGetAll[ghPull](g, fmt.Sprintf("repos/%s/pulls?state=open&sort=updated&direction=desc", project))
	if err != nil {
		return nil, err
	}
	var out []MR
	for _, p := range pulls {
		if !p.Draft {
			out = append(out, p.toMR())
		}
	}
	return out, nil
}

func (g *GitHub) MR(project string, iid int) (MR, error) {
	p, err := ghGet[ghPull](g, fmt.Sprintf("repos/%s/pulls/%d", project, iid))
	return p.toMR(), err
}

func (g *GitHub) Notes(project string, iid int) ([]Note, error) {
	comments, err := ghGetAll[struct {
		ID   int64  `json:"id"`
		Body string `json:"body"`
		User ghUser `json:"user"`
	}](g, fmt.Sprintf("repos/%s/issues/%d/comments", project, iid))
	if err != nil {
		return nil, err
	}
	notes := make([]Note, 0, len(comments))
	for _, c := range comments {
		notes = append(notes, Note{ID: c.ID, Body: c.Body, Author: User{ID: c.User.ID, Username: c.User.Login}})
	}
	return notes, nil
}

func (g *GitHub) PostNote(project string, iid int, body string) error {
	payload, _ := json.Marshal(map[string]string{"body": body})
	_, err := g.run(payload, "-X", "POST", fmt.Sprintf("repos/%s/issues/%d/comments", project, iid), "--input", "-")
	return err
}

func (g *GitHub) CloneURL(project string) string {
	return fmt.Sprintf("https://%s/%s.git", g.Host, project)
}

func (g *GitHub) CredentialHelper() string { return "!" + g.Command + " auth git-credential" }

func (g *GitHub) HeadRef(iid int) string { return fmt.Sprintf("refs/pull/%d/head", iid) }

func (g *GitHub) ParseURL(url string) (string, int, bool) {
	re := regexp.MustCompile(`^https?://` + regexp.QuoteMeta(g.Host) + `/([^/]+/[^/]+)/pull/(\d+)`)
	m := re.FindStringSubmatch(strings.TrimSpace(url))
	if m == nil {
		return "", 0, false
	}
	iid, _ := strconv.Atoi(m[2])
	return m[1], iid, true
}
