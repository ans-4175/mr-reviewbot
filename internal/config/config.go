// Package config loads and validates config.json: which forge and agent to use, and the repo whitelist.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"mr-reviewbot/internal/agent"
	"mr-reviewbot/internal/forge"
)

var pathRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)+$`)

var Languages = []string{"en", "id"}

type Repo struct {
	Path           string
	Enabled        bool
	LookbackDays   int
	MaxDiffLines   int
	Language       string
	SkipLabels     []string
	SkipAuthors    []string
	TargetBranches []string // empty = any target branch
}

type Config struct {
	Forge     forge.Config
	Agent     agent.Config
	Repos     []Repo
	MaxPerRun int
}

func (c *Config) Enabled() []Repo {
	var out []Repo
	for _, r := range c.Repos {
		if r.Enabled {
			out = append(out, r)
		}
	}
	return out
}

func (c *Config) Get(path string) *Repo {
	for _, r := range c.Repos {
		if r.Enabled && r.Path == path {
			r := r
			return &r
		}
	}
	return nil
}

type Overrides struct {
	LookbackDays   *int      `json:"lookback_days,omitempty"`
	MaxDiffLines   *int      `json:"max_diff_lines,omitempty"`
	Language       *string   `json:"language,omitempty"`
	SkipLabels     *[]string `json:"skip_labels,omitempty"`
	SkipAuthors    *[]string `json:"skip_authors,omitempty"`
	TargetBranches *[]string `json:"target_branches,omitempty"`
}

type RawForge struct {
	Type    string `json:"type"`
	Host    string `json:"host,omitempty"`
	Command string `json:"command,omitempty"`
}

type RawAgent struct {
	Type           string `json:"type"`
	Command        string `json:"command,omitempty"`
	Model          string `json:"model,omitempty"`
	MaxTurns       int    `json:"max_turns,omitempty"`
	TimeoutMinutes int    `json:"timeout_minutes,omitempty"`
}

type RawRepo struct {
	Path    string `json:"path"`
	Enabled *bool  `json:"enabled,omitempty"`
	Overrides
}

// Raw is the on-disk shape; init writes it, Parse reads it.
type Raw struct {
	Forge     RawForge  `json:"forge"`
	Agent     RawAgent  `json:"agent"`
	MaxPerRun *int      `json:"max_per_run,omitempty"`
	Defaults  Overrides `json:"defaults"`
	Repos     []RawRepo `json:"repos"`
}

func apply(dst *Repo, o Overrides) {
	if o.LookbackDays != nil {
		dst.LookbackDays = *o.LookbackDays
	}
	if o.MaxDiffLines != nil {
		dst.MaxDiffLines = *o.MaxDiffLines
	}
	if o.Language != nil {
		dst.Language = *o.Language
	}
	if o.SkipLabels != nil {
		dst.SkipLabels = *o.SkipLabels
	}
	if o.SkipAuthors != nil {
		dst.SkipAuthors = *o.SkipAuthors
	}
	if o.TargetBranches != nil {
		dst.TargetBranches = *o.TargetBranches
	}
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Parse rejects unknown keys so a typo in the whitelist can't silently disable a rule.
func Parse(data []byte) (*Config, error) {
	var r Raw
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	cfg := &Config{MaxPerRun: 5}
	if r.MaxPerRun != nil {
		cfg.MaxPerRun = *r.MaxPerRun
	}
	if cfg.MaxPerRun < 1 {
		return nil, fmt.Errorf("max_per_run must be >= 1")
	}

	cfg.Forge = forge.Config{Type: r.Forge.Type, Host: r.Forge.Host, Command: r.Forge.Command}
	if cfg.Forge.Type == "" {
		cfg.Forge.Type = "gitlab"
	}
	cfg.Agent = agent.Config{Type: r.Agent.Type, Command: r.Agent.Command, Model: r.Agent.Model,
		MaxTurns: r.Agent.MaxTurns, Timeout: 20 * time.Minute}
	if cfg.Agent.Type == "" {
		cfg.Agent.Type = "claude"
	}
	if r.Agent.TimeoutMinutes != 0 {
		cfg.Agent.Timeout = time.Duration(r.Agent.TimeoutMinutes) * time.Minute
	}
	if r.Agent.MaxTurns < 0 || r.Agent.TimeoutMinutes < 0 {
		return nil, fmt.Errorf("agent.max_turns and agent.timeout_minutes must be >= 0")
	}

	base := Repo{LookbackDays: 7, MaxDiffLines: 3000, Language: "en", SkipLabels: []string{"no-ai-review"}}
	apply(&base, r.Defaults)
	if !has(Languages, base.Language) {
		return nil, fmt.Errorf("defaults.language must be one of %v", Languages)
	}

	seen := map[string]bool{}
	for i, rr := range r.Repos {
		if !pathRe.MatchString(rr.Path) {
			return nil, fmt.Errorf("repos[%d]: invalid path %q (want group/.../project)", i, rr.Path)
		}
		if seen[rr.Path] {
			return nil, fmt.Errorf("duplicate repo %s", rr.Path)
		}
		seen[rr.Path] = true
		repo := base
		repo.Path, repo.Enabled = rr.Path, true
		if rr.Enabled != nil {
			repo.Enabled = *rr.Enabled
		}
		apply(&repo, rr.Overrides)
		if repo.LookbackDays < 1 || repo.MaxDiffLines < 1 {
			return nil, fmt.Errorf("%s: lookback_days and max_diff_lines must be >= 1", rr.Path)
		}
		if !has(Languages, repo.Language) {
			return nil, fmt.Errorf("%s: language must be one of %v", rr.Path, Languages)
		}
		cfg.Repos = append(cfg.Repos, repo)
	}
	return cfg, nil
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func xdg(envVar, fallback string) string {
	if d := os.Getenv(envVar); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, fallback)
}

// DefaultPath: $REVIEWBOT_CONFIG, else $XDG_CONFIG_HOME/mr-reviewbot/config.json.
func DefaultPath() string {
	if p := os.Getenv("REVIEWBOT_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(xdg("XDG_CONFIG_HOME", ".config"), "mr-reviewbot", "config.json")
}

// DefaultStateDir holds the queue, mirrors, logs and rendered comments: $REVIEWBOT_STATE, else $XDG_STATE_HOME/mr-reviewbot.
func DefaultStateDir() string {
	if p := os.Getenv("REVIEWBOT_STATE"); p != "" {
		return p
	}
	return filepath.Join(xdg("XDG_STATE_HOME", filepath.Join(".local", "state")), "mr-reviewbot")
}
