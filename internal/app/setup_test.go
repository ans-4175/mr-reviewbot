package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ans-4175/mr-reviewbot/internal/config"
)

func TestInitWritesValidConfigAndRefusesOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	var out, errb bytes.Buffer
	code := runInit([]string{"--config", path, "--repo", "acme/app", "--repo", "acme/web/", "--language", "id", "--model", "opus"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Repos) != 2 || c.Repos[1].Path != "acme/web" || c.Repos[0].Language != "id" || c.Agent.Model != "opus" || c.Forge.Host != "gitlab.com" {
		t.Errorf("got %+v", c)
	}
	if runInit([]string{"--config", path}, &out, &errb) != 2 {
		t.Error("must refuse to overwrite without --force")
	}
	if runInit([]string{"--config", path, "--force"}, &out, &errb) != 0 {
		t.Error("--force must overwrite")
	}
}

func TestInitRejectsBadInput(t *testing.T) {
	var out, errb bytes.Buffer
	dir := t.TempDir()
	for _, args := range [][]string{{"--repo", "noslash"}, {"--language", "fr"}, {"--forge", "bitbucket"}, {"--agent", "copilot"}} {
		path := filepath.Join(dir, strings.Join(args, "_")+".json")
		if runInit(append([]string{"--config", path}, args...), &out, &errb) != 2 {
			t.Errorf("expected rejection for %v", args)
		}
		if _, err := os.Stat(path); err == nil {
			t.Errorf("wrote a config for bad input %v", args)
		}
	}
}

func TestServiceFiles(t *testing.T) {
	s := service{Label: "mr-reviewbot", Binary: "/opt/my bin/reviewbot", Config: "/c/config.json", State: "/s", Path: "/usr/bin:/x&y", Interval: 30 * time.Minute}
	p := s.plist()
	for _, want := range []string{"<string>/opt/my bin/reviewbot</string>", "<string>poll</string>", "<string>/c/config.json</string>",
		"<integer>1800</integer>", "/usr/bin:/x&amp;y", "/s/log/poll.err.log", "<false/>"} {
		if !strings.Contains(p, want) {
			t.Errorf("plist missing %q", want)
		}
	}
	svc, timer := s.systemdService(), s.systemdTimer()
	if !strings.Contains(svc, `ExecStart="/opt/my bin/reviewbot" poll --config /c/config.json --state /s`) || !strings.Contains(svc, "Type=oneshot") {
		t.Errorf("service:\n%s", svc)
	}
	if !strings.Contains(timer, "OnUnitInactiveSec=1800s") || !strings.Contains(timer, "Unit=mr-reviewbot.service") {
		t.Errorf("timer:\n%s", timer)
	}
}

func TestServicePrintNeedsNoConfig(t *testing.T) {
	var out, errb bytes.Buffer
	if code := runService([]string{"print", "--config", "/nope.json", "--state", "/s", "--binary", "/b"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "/b") {
		t.Error(out.String())
	}
	if runService([]string{"install", "--config", "/nope.json", "--binary", "/b"}, &out, &errb) != 2 {
		t.Error("install must refuse a missing config")
	}
}

func TestSkillInstallUsesAbsoluteBinary(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	if code := runSkill([]string{"install", dir}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	b, err := os.ReadFile(filepath.Join(dir, "review-mr", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if !strings.Contains(string(b), "name: review-mr") || !strings.Contains(string(b), exe+" review <MR> --json") {
		t.Errorf("skill not rewritten:\n%s", b)
	}
}

func TestSkillInstallLeavesSymlinkAlone(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	if err := os.Symlink(target, filepath.Join(dir, "review-mr")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if runSkill([]string{"install", dir}, &out, &errb) != 2 {
		t.Error("must not write through a symlinked skill dir")
	}
}
