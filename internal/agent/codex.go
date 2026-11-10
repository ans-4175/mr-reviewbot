package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// Codex runs `codex exec` in the read-only sandbox, without the repo's AGENTS.md, MCP servers, web search or exec rules.
type Codex struct{ cfg Config }

func NewCodex(c Config) *Codex {
	c.Command = orDefault(c.Command, "codex")
	return &Codex{cfg: c}
}

func (c *Codex) Name() string { return "codex" }

func (c *Codex) Version(ctx context.Context) (string, error) { return version(ctx, c.cfg.Command) }

func (c *Codex) Args(req Request, schemaFile, lastFile string) []string {
	args := []string{"exec",
		"--sandbox", "read-only",
		"--ephemeral",
		"--skip-git-repo-check",
		"--ignore-rules",
		"--color", "never",
		"-c", `approval_policy="never"`,
		"-c", "project_doc_max_bytes=0", // the MR's AGENTS.md is data, not instructions
		"-c", "mcp_servers={}",
		"-c", `web_search="disabled"`,
		"-C", req.Dir,
		"--output-schema", schemaFile,
		"-o", lastFile,
	}
	if c.cfg.Model != "" {
		args = append(args, "-m", c.cfg.Model)
	}
	return append(args, "-") // prompt on stdin: it can be large
}

func (c *Codex) Review(ctx context.Context, req Request) (*Output, error) {
	tmp, err := os.MkdirTemp("", "reviewbot-codex-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	schemaFile, lastFile := filepath.Join(tmp, "schema.json"), filepath.Join(tmp, "last.txt")
	if err := os.WriteFile(schemaFile, []byte(req.Schema), 0o644); err != nil {
		return nil, err
	}
	prompt, err := inlineSystem(req)
	if err != nil {
		return nil, err
	}
	if _, err := run(ctx, c.cfg.Timeout, req.Dir, nil, strings.NewReader(prompt), c.cfg.Command, c.Args(req, schemaFile, lastFile)...); err != nil {
		return nil, err
	}
	last, err := os.ReadFile(lastFile)
	if err != nil {
		return nil, err
	}
	return &Output{JSON: ExtractJSON(string(last))}, nil
}
