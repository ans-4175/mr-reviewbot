package app

import (
	"strings"
	"testing"

	"mr-reviewbot/internal/config"
	"mr-reviewbot/internal/forge"
)

func cfgOf(t *testing.T, paths ...string) *config.Config {
	var repos []string
	for _, p := range paths {
		repos = append(repos, `{"path":"`+p+`"}`)
	}
	c, err := config.Parse([]byte(`{"repos":[` + strings.Join(repos, ",") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolveRef(t *testing.T) {
	two := cfgOf(t, "acme/clients/shop/billing", "acme/platform/api-mono")
	one := cfgOf(t, "acme/clients/shop/billing")
	gitlab := forge.NewGitLab("", "").ParseURL
	selfHosted := forge.NewGitLab("git.example.org", "").ParseURL
	cases := []struct {
		name    string
		cfg     *config.Config
		parse   urlParser
		ref     string
		project string
		iid     int
		errHas  string
	}{
		{"url", two, gitlab, "https://gitlab.com/acme/clients/shop/billing/-/merge_requests/56", "acme/clients/shop/billing", 56, ""},
		{"url with tab suffix", two, gitlab, "https://gitlab.com/acme/platform/api-mono/-/merge_requests/357/diffs", "acme/platform/api-mono", 357, ""},
		{"self-hosted url", two, selfHosted, "https://git.example.org/acme/platform/api-mono/-/merge_requests/9", "acme/platform/api-mono", 9, ""},
		{"other host url is not parsed", two, selfHosted, "https://gitlab.com/acme/platform/api-mono/-/merge_requests/9", "", 0, "not an MR reference"},
		{"full path ref", two, gitlab, "acme/clients/shop/billing!56", "acme/clients/shop/billing", 56, ""},
		{"short name", two, gitlab, "api-mono!357", "acme/platform/api-mono", 357, ""},
		{"bare id with one repo", one, gitlab, "56", "acme/clients/shop/billing", 56, ""},
		{"bare id with bang", one, gitlab, "!56", "acme/clients/shop/billing", 56, ""},
		{"bare id is ambiguous", two, gitlab, "56", "", 0, "ambiguous"},
		{"not whitelisted", two, gitlab, "other/repo!1", "", 0, "not a whitelisted"},
		{"junk", two, gitlab, "hello", "", 0, "not an MR reference"},
		{"partial name must not match mid-word", two, gitlab, "lling!56", "", 0, "not a whitelisted"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, iid, err := resolveRef(c.cfg, c.parse, c.ref)
			if c.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), c.errHas) {
					t.Fatalf("want error containing %q, got %v", c.errHas, err)
				}
				return
			}
			if err != nil || p != c.project || iid != c.iid {
				t.Fatalf("got %q %d %v", p, iid, err)
			}
		})
	}
}

func TestShortNameAmbiguousAcrossRepos(t *testing.T) {
	c := cfgOf(t, "a/x/api", "b/y/api")
	if _, _, err := resolveRef(c, forge.NewGitLab("", "").ParseURL, "api!1"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("got %v", err)
	}
}
