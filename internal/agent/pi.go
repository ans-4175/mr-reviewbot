package agent

import "context"

// Pi runs `pi -p` with only its read/search tools and no extensions, skills, MCP or context files.
type Pi struct{ cfg Config }

func NewPi(c Config) *Pi {
	c.Command = orDefault(c.Command, "pi")
	return &Pi{cfg: c}
}

func (p *Pi) Name() string { return "pi" }

func (p *Pi) Version(ctx context.Context) (string, error) { return version(ctx, p.cfg.Command) }

func (p *Pi) Args(req Request, prompt string) []string {
	args := []string{"-p",
		"--no-session",
		"--tools", "read,grep,find,ls",
		"--no-extensions", "--no-skills", "--no-prompt-templates", "--no-themes", "--no-mcp",
		"--no-context-files", // the MR's AGENTS.md/CLAUDE.md is data, not instructions
		"--no-approve",
		"--offline",
	}
	if p.cfg.Model != "" {
		args = append(args, "--model", p.cfg.Model)
	}
	return append(args, "--", prompt)
}

func (p *Pi) Review(ctx context.Context, req Request) (*Output, error) {
	prompt, err := inlineSystem(req)
	if err != nil {
		return nil, err
	}
	out, err := run(ctx, p.cfg.Timeout, req.Dir, []string{"PI_OFFLINE=1"}, nil, p.cfg.Command, p.Args(req, prompt)...)
	if err != nil {
		return nil, err
	}
	return &Output{JSON: ExtractJSON(string(out))}, nil
}
