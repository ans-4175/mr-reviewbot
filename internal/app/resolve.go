package app

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/ans-4175/mr-reviewbot/internal/config"
)

var (
	mrRefRe  = regexp.MustCompile(`^([\w.\-/]+)!(\d+)$`)
	bareIDRe = regexp.MustCompile(`^!?(\d+)$`)
)

// resolveProject maps a full path or a unique trailing name ("api") to a whitelisted repo.
func resolveProject(cfg *config.Config, name string) (string, error) {
	var hits []string
	for _, r := range cfg.Enabled() {
		if r.Path == name || strings.HasSuffix(r.Path, "/"+name) {
			hits = append(hits, r.Path)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return "", fmt.Errorf("%q is not a whitelisted repo", name)
	}
	return "", fmt.Errorf("%q is ambiguous: %s", name, strings.Join(hits, ", "))
}

type urlParser func(string) (string, int, bool)

// resolveRef accepts an MR URL, "group/project!iid", "name!iid", or a bare iid when only one repo is whitelisted.
func resolveRef(cfg *config.Config, parseURL urlParser, ref string) (string, int, error) {
	ref = strings.TrimSpace(ref)
	var name string
	var iid int
	if p, n, ok := parseURL(ref); ok {
		name, iid = p, n
	} else if m := mrRefRe.FindStringSubmatch(ref); m != nil {
		name = m[1]
		iid, _ = strconv.Atoi(m[2])
	} else if m := bareIDRe.FindStringSubmatch(ref); m != nil {
		enabled := cfg.Enabled()
		if len(enabled) != 1 {
			return "", 0, fmt.Errorf("an MR id alone is ambiguous with %d whitelisted repos; use <repo-name>!<iid> or the MR URL", len(enabled))
		}
		name = enabled[0].Path
		iid, _ = strconv.Atoi(m[1])
	} else {
		return "", 0, fmt.Errorf("not an MR reference: %q (use the MR URL, group/project!iid, or repo-name!iid)", ref)
	}
	project, err := resolveProject(cfg, name)
	if err != nil {
		return "", 0, err
	}
	return project, iid, nil
}
