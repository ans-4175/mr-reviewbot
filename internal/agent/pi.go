package agent

import (
	"context"
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

// piGuard blocks pi tool calls that reach outside the checkout; pi itself has no path restriction.
//
//go:embed pi_guard.ts
var piGuard []byte

// Pi runs `pi -p` with only its read/search tools, the path guard, and no other extensions, skills, MCP or context files.
type Pi struct{ cfg Config }

func NewPi(c Config) *Pi {
	c.Command = orDefault(c.Command, "pi")
	return &Pi{cfg: c}
}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) Version(ctx context.Context) (string, error) { return version(ctx, p.cfg.Command) }

func (p *Pi) Args(req Request, guard string) []string {
	args := []string{"-p",
		"--no-session",
		"--tools", "read,grep,find,ls",
		"--no-extensions", "-e", guard, // explicit -e still loads with --no-extensions
		"--no-skills", "--no-prompt-templates", "--no-themes", "--no-mcp",
		"--no-context-files", // the MR's AGENTS.md/CLAUDE.md is data, not instructions
		"--no-approve",
		"--offline",
	}
	if p.cfg.Model != "" {
		args = append(args, "--model", p.cfg.Model)
	}
	return args // the prompt comes on stdin (argv would hit Linux's 128 KiB per-argument limit)
}

func (p *Pi) Review(ctx context.Context, req Request) (*Output, error) {
	prompt, err := inlineSystem(req)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "reviewbot-pi-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	guard := filepath.Join(tmp, "reviewbot-guard.ts")
	if err := os.WriteFile(guard, piGuard, 0o644); err != nil {
		return nil, err
	}
	out, err := run(ctx, p.cfg.Timeout, req.Dir, []string{"PI_OFFLINE=1"}, strings.NewReader(prompt), p.cfg.Command, p.Args(req, guard)...)
	if err != nil {
		return nil, err
	}
	return &Output{JSON: ExtractJSON(string(out))}, nil
}
