package forge

import (
	"encoding/json"
	"testing"
)

func TestGitHubPullMapsToMR(t *testing.T) {
	var p ghPull
	raw := `{"number":12,"title":"T","body":"B","state":"open","draft":false,"user":{"id":5,"login":"dev"},
	"updated_at":"2026-11-10T10:00:00Z","labels":[{"name":"no-ai-review"}],"head":{"sha":"abc","ref":"feat"},"base":{"ref":"main"}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	m := p.toMR()
	if m.IID != 12 || m.State != "opened" || m.SHA != "abc" || m.SourceBranch != "feat" || m.TargetBranch != "main" ||
		m.Author.Username != "dev" || m.Author.ID != 5 || len(m.Labels) != 1 || m.Labels[0] != "no-ai-review" || m.UpdatedAt.IsZero() {
		t.Errorf("got %+v", m)
	}
	p.State = "closed"
	if p.toMR().State != "closed" {
		t.Error("closed must stay closed")
	}
}

func TestURLsAndRefs(t *testing.T) {
	gh, gl := NewGitHub("", ""), NewGitLab("git.example.org", "")
	if p, n, ok := gh.ParseURL("https://github.com/acme/app/pull/7/files"); !ok || p != "acme/app" || n != 7 {
		t.Errorf("github url: %q %d %v", p, n, ok)
	}
	if _, _, ok := gh.ParseURL("https://gitlab.com/acme/app/-/merge_requests/7"); ok {
		t.Error("github parser accepted a gitlab url")
	}
	if p, n, ok := gl.ParseURL("https://git.example.org/a/b/c/-/merge_requests/3"); !ok || p != "a/b/c" || n != 3 {
		t.Errorf("gitlab url: %q %d %v", p, n, ok)
	}
	for _, c := range []struct{ got, want string }{
		{gh.HeadRef(7), "refs/pull/7/head"},
		{gl.HeadRef(7), "refs/merge-requests/7/head"},
		{gh.CloneURL("acme/app"), "https://github.com/acme/app.git"},
		{gl.CloneURL("a/b"), "https://git.example.org/a/b.git"},
		{gh.CredentialHelper(), "!gh auth git-credential"},
		{gl.CredentialHelper(), "!glab auth git-credential"},
	} {
		if c.got != c.want {
			t.Errorf("got %q want %q", c.got, c.want)
		}
	}
}

func TestNewRejectsUnknown(t *testing.T) {
	if _, err := New(Config{Type: "bitbucket"}); err == nil {
		t.Error("expected error")
	}
}
