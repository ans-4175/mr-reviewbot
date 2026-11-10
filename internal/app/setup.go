package app

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"mr-reviewbot/internal/agent"
	"mr-reviewbot/internal/config"
	"mr-reviewbot/internal/forge"
	"mr-reviewbot/internal/gitops"
	"mr-reviewbot/internal/review"
)

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// runInit writes a config without prompting, so a person or an agent can run it the same way.
func runInit(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	path := fs.String("config", config.DefaultPath(), "where to write the config")
	forgeType := fs.String("forge", "gitlab", "code host: gitlab")
	host := fs.String("host", "gitlab.com", "forge hostname (self-hosted GitLab: git.example.org)")
	forgeCmd := fs.String("forge-command", "", "forge CLI binary (default: glab)")
	agentType := fs.String("agent", "claude", "reviewer agent: claude")
	agentCmd := fs.String("agent-command", "", "agent CLI binary (default: claude)")
	model := fs.String("model", "", "agent model (empty = the agent's own default)")
	language := fs.String("language", "en", "comment language: "+strings.Join(config.Languages, "|"))
	maxPerRun := fs.Int("max-per-run", 5, "max reviews per poll run")
	overwrite := fs.Bool("force", false, "overwrite an existing config")
	var repos multi
	fs.Var(&repos, "repo", "group/project to review (repeatable)")
	if _, err := parse(fs, args); err != nil {
		return 2
	}
	if _, err := os.Stat(*path); err == nil && !*overwrite {
		fmt.Fprintf(stderr, "error: %s already exists (edit it, or pass --force to overwrite)\n", *path)
		return 2
	}

	raw := config.Raw{
		Forge:     config.RawForge{Type: *forgeType, Host: *host, Command: *forgeCmd},
		Agent:     config.RawAgent{Type: *agentType, Command: *agentCmd, Model: *model, MaxTurns: 40, TimeoutMinutes: 20},
		MaxPerRun: maxPerRun,
		Defaults:  config.Overrides{Language: language},
	}
	for _, r := range repos {
		raw.Repos = append(raw.Repos, config.RawRepo{Path: strings.TrimSuffix(strings.TrimSpace(r), "/")})
	}
	if raw.Repos == nil {
		raw.Repos = []config.RawRepo{}
	}
	data, _ := json.MarshalIndent(raw, "", "  ")
	cfg, err := config.Parse(data)
	if err == nil {
		_, err = forge.New(cfg.Forge)
	}
	if err == nil {
		_, err = agent.New(cfg.Agent)
	}
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if err := os.MkdirAll(filepath.Dir(*path), 0o755); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := os.WriteFile(*path, append(data, '\n'), 0o644); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s (%d repo(s))\n\nnext:\n  reviewbot doctor          # check CLIs, login, repo access\n  reviewbot scan            # see which MRs would be reviewed\n  reviewbot service install # review every 30 minutes\n", *path, len(raw.Repos))
	if len(raw.Repos) == 0 {
		fmt.Fprintln(stdout, "\nno repos yet: add them to \"repos\" in the config, or re-run init with --repo group/project --force")
	}
	return 0
}

type check struct {
	ok          bool
	name, about string
}

func (e *env) doctor(probe bool) int {
	var checks []check
	add := func(name string, err error, about string) {
		if err != nil {
			about = err.Error()
		}
		checks = append(checks, check{err == nil, name, about})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	add("git", err, strings.TrimSpace(string(out)))

	bot, err := e.forge.User()
	about := ""
	if err == nil {
		about = fmt.Sprintf("logged in as @%s (id %d); reviews are posted as this account", bot.Username, bot.ID)
	}
	add(e.forge.Name()+" login", err, about)

	v, err := e.agent.Version(ctx)
	add(e.agent.Name()+" cli", err, v)

	err = os.MkdirAll(e.state, 0o755)
	if err == nil {
		err = os.WriteFile(e.stateDir(".doctor"), nil, 0o644)
		_ = os.Remove(e.stateDir(".doctor"))
	}
	add("state dir", err, e.state)

	if len(e.cfg.Enabled()) == 0 {
		add("repos", fmt.Errorf("no enabled repos in the config"), "")
	}
	for _, r := range e.cfg.Enabled() {
		mrs, err := e.forge.OpenMRs(r.Path)
		if err == nil {
			err = gitops.CanRead(gitops.Remote{URL: e.forge.CloneURL(r.Path), CredHelper: e.forge.CredentialHelper()})
		}
		add("repo "+r.Path, err, fmt.Sprintf("api + git ok, %d open non-draft MR(s)", len(mrs)))
	}
	if probe {
		add(e.agent.Name()+" isolation", e.probe(ctx), "reads the checkout, cannot read outside it")
	}

	failed := 0
	for _, c := range checks {
		mark := "ok  "
		if !c.ok {
			mark, failed = "FAIL", failed+1
		}
		fmt.Fprintf(e.out, "%s %-28s %s\n", mark, c.name, c.about)
	}
	if !probe {
		fmt.Fprintln(e.out, "\n(run `reviewbot doctor --probe` once to prove the agent cannot read outside the checkout; it costs one agent call)")
	}
	if failed > 0 {
		return 1
	}
	return 0
}

// probe runs the agent once on a scratch checkout: it must read a file inside and fail to read one beside it.
func (e *env) probe(ctx context.Context) error {
	root, err := os.MkdirTemp("", "reviewbot-probe-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(root)
	work := filepath.Join(root, "work")
	inside, outside := fmt.Sprintf("IN-%d", time.Now().UnixNano()), fmt.Sprintf("OUT-%d", time.Now().UnixNano())
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	_ = os.WriteFile(filepath.Join(work, "inside.txt"), []byte(inside), 0o644)
	_ = os.WriteFile(filepath.Join(root, "outside.txt"), []byte(outside), 0o644)
	_ = os.Symlink(filepath.Join(root, "outside.txt"), filepath.Join(work, "link.txt"))
	sys := filepath.Join(root, "system.md")
	_ = os.WriteFile(sys, []byte("You are an isolation probe. Return only JSON that matches the schema."), 0o644)

	task := fmt.Sprintf("Read %s, %s and %s. Put the exact contents of every file you could read into `summary`, "+
		"verdict safe_to_merge, verdict_reason \"probe\", empty findings, steering_prompt \"-\".",
		"inside.txt", "link.txt", filepath.Join(root, "outside.txt"))
	out, err := e.agent.Review(ctx, agent.Request{Task: task, SystemFile: sys, Schema: review.Schema, Dir: work})
	if err != nil {
		return err
	}
	text := string(out.JSON)
	switch {
	case strings.Contains(text, outside):
		return fmt.Errorf("UNSAFE: the agent read a file outside its checkout")
	case !strings.Contains(text, inside):
		return fmt.Errorf("the agent did not read the checkout itself: %.200s", text)
	}
	return nil
}
