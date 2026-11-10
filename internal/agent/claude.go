package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Claude runs `claude -p` with only Read/Grep/Glob and without the repo's own settings, hooks or CLAUDE.md.
type Claude struct{ cfg Config }

func NewClaude(c Config) *Claude {
	c.Command = orDefault(c.Command, "claude")
	if c.MaxTurns == 0 {
		c.MaxTurns = 40
	}
	return &Claude{cfg: c}
}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Version(ctx context.Context) (string, error) { return version(ctx, c.cfg.Command) }

func (c *Claude) Args(req Request) []string {
	args := []string{
		"-p", // the prompt comes on stdin: as one argv string it would hit Linux's 128 KiB per-argument limit
		"--output-format", "json",
		"--json-schema", req.Schema,
		"--append-system-prompt-file", req.SystemFile,
		"--tools", "Read,Grep,Glob",
		// Pinned so a user's defaultMode (auto, bypass) can't approve reads outside the checkout.
		"--permission-mode", "dontAsk",
		"--max-turns", fmt.Sprint(c.cfg.MaxTurns),
		"--no-session-persistence",
		"--strict-mcp-config",
		"--disable-slash-commands",
		"--setting-sources", "user", // skip the repo's .claude/settings (hooks etc.)
	}
	if c.cfg.Model != "" {
		args = append(args, "--model", c.cfg.Model)
	}
	return args
}

func (c *Claude) Review(ctx context.Context, req Request) (*Output, error) {
	out, err := run(ctx, c.cfg.Timeout, req.Dir, []string{"CLAUDE_CODE_DISABLE_CLAUDE_MDS=1"}, strings.NewReader(req.Task), c.cfg.Command, c.Args(req)...)
	if err != nil {
		return nil, err
	}
	return ParseClaude(out)
}

// ParseClaude unwraps the `--output-format json` envelope; falls back to JSON in the text result.
func ParseClaude(raw []byte) (*Output, error) {
	var env struct {
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Structured json.RawMessage `json:"structured_output"`
		Cost       float64         `json:"total_cost_usd"`
		Turns      int             `json:"num_turns"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("claude output is not JSON: %.200s", raw)
	}
	if env.IsError {
		return nil, fmt.Errorf("claude reported an error: %.300s", env.Result)
	}
	payload := []byte(env.Structured)
	if len(payload) == 0 || string(payload) == "null" {
		payload = ExtractJSON(env.Result)
	}
	return &Output{JSON: payload, CostUSD: env.Cost, Turns: env.Turns}, nil
}
