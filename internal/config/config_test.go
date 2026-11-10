package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultsAndOverride(t *testing.T) {
	c, err := Parse([]byte(`{"forge":{"type":"gitlab"},"agent":{"type":"claude"},"defaults":{"lookback_days":3},"repos":[{"path":"a/b"},{"path":"a/c","lookback_days":9,"enabled":false,"language":"id"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Repos[0].LookbackDays != 3 || c.Repos[1].LookbackDays != 9 {
		t.Errorf("lookback: %+v", c.Repos)
	}
	if c.Repos[0].Language != "en" || c.Repos[1].Language != "id" {
		t.Errorf("language: default must be en, override id: %+v", c.Repos)
	}
	if len(c.Enabled()) != 1 || c.Get("a/c") != nil || c.Get("a/b") == nil {
		t.Error("enabled/get mismatch")
	}
	if c.MaxPerRun != 5 || c.Agent.Timeout != 20*time.Minute {
		t.Errorf("defaults: %+v", c)
	}
}

func TestMinimalConfigDefaultsToGitLabAndClaude(t *testing.T) {
	c, err := Parse([]byte(`{"repos":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Forge.Type != "gitlab" || c.Agent.Type != "claude" {
		t.Errorf("got %+v %+v", c.Forge, c.Agent)
	}
}

func TestAgentSettings(t *testing.T) {
	c, err := Parse([]byte(`{"agent":{"type":"claude","model":"opus","max_turns":10,"timeout_minutes":5},"repos":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if c.Agent.Model != "opus" || c.Agent.MaxTurns != 10 || c.Agent.Timeout != 5*time.Minute {
		t.Errorf("got %+v", c.Agent)
	}
}

func TestRejectsBadInput(t *testing.T) {
	for _, bad := range []string{
		`{"repos":[{"path":"noslash"}]}`,
		`{"repos":[{"path":"a/b"},{"path":"a/b"}]}`,
		`{"repos":[{"path":"a/b","typo":1}]}`,
		`{"repos":[{"path":"a/b","language":"fr"}]}`,
		`{"defaults":{"language":"fr"},"repos":[]}`,
		`{"repos":[{"path":"a/b","lookback_days":0}]}`,
		`{"agent":{"type":"claude","timeout_minutes":-1}}`,
		`{"forge":{"type":"gitlab","token":"x"}}`,
		`{"nope":1}`,
		`{"max_per_run":0}`,
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("expected error for %s", bad)
		}
	}
}

func TestExampleConfigIsValid(t *testing.T) {
	if _, err := Load(filepath.Join("..", "..", "config.example.json")); err != nil {
		t.Fatal(err)
	}
}

func TestPathsFollowEnv(t *testing.T) {
	t.Setenv("REVIEWBOT_CONFIG", "")
	t.Setenv("REVIEWBOT_STATE", "")
	t.Setenv("XDG_CONFIG_HOME", "/x/cfg")
	t.Setenv("XDG_STATE_HOME", "/x/state")
	if DefaultPath() != "/x/cfg/mr-reviewbot/config.json" || DefaultStateDir() != "/x/state/mr-reviewbot" {
		t.Errorf("got %s %s", DefaultPath(), DefaultStateDir())
	}
	t.Setenv("REVIEWBOT_CONFIG", "/a.json")
	t.Setenv("REVIEWBOT_STATE", "/s")
	if DefaultPath() != "/a.json" || DefaultStateDir() != "/s" {
		t.Errorf("env override ignored")
	}
}
