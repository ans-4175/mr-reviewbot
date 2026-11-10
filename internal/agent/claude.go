package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// Claude runs `claude -p` with only Read/Grep/Glob and without the repo's own settings, hooks or CLAUDE.md.
type Claude struct{ cfg Config }

func NewClaude(c Config) *Claude {
	if c.Command == "" {
		c.Command = "claude"
	}
	if c.MaxTurns == 0 {
		c.MaxTurns = 40
	}
	return &Claude{cfg: c}
}

func (c *Claude) Name() string { return "claude" }

func (c *Claude) Version(ctx context.Context) (string, error) { return version(ctx, c.cfg.Command) }

func (c *Claude) Args(req Request) []string {
	args := []string{
		"-p", req.Task,
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
	if c.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.cfg.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, c.cfg.Command, c.Args(req)...)
	cmd.Dir = req.Dir
	cmd.Env = append(os.Environ(), "CLAUDE_CODE_DISABLE_CLAUDE_MDS=1")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return nil, fmt.Errorf("claude failed: %v: %.400s", err, errb.String())
	}
	return ParseClaude(out.Bytes())
}

var fenceRe = regexp.MustCompile("(?s)^```(?:json)?\\s*(.*?)\\s*```$")

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
		text := strings.TrimSpace(env.Result)
		if m := fenceRe.FindStringSubmatch(text); m != nil {
			text = m[1]
		}
		payload = []byte(text)
	}
	return &Output{JSON: payload, CostUSD: env.Cost, Turns: env.Turns}, nil
}
