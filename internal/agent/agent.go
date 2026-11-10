// Package agent runs a coding-agent CLI as a read-only reviewer and returns its JSON answer.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

type Request struct {
	Task       string // user prompt
	SystemFile string // reviewer instructions
	Schema     string // JSON schema the answer must follow
	Dir        string // checkout of the MR head; the only tree the agent may read
}

type Output struct {
	JSON    []byte
	CostUSD float64
	Turns   int
}

// Agent must keep the run read-only: no shell, no writes, no network tools, nothing outside Dir.
// The app re-checks that with a probe whenever the agent's version changes.
type Agent interface {
	Name() string
	// Version proves the CLI is installed and runnable.
	Version(ctx context.Context) (string, error)
	Review(ctx context.Context, req Request) (*Output, error)
}

type Config struct {
	Type     string
	Command  string
	Model    string
	MaxTurns int
	Timeout  time.Duration
}

var Types = []string{"claude", "codex", "pi", "opencode"}

func New(c Config) (Agent, error) {
	switch c.Type {
	case "claude":
		return NewClaude(c), nil
	case "codex":
		return NewCodex(c), nil
	case "pi":
		return NewPi(c), nil
	case "opencode":
		return NewOpenCode(c), nil
	}
	return nil, fmt.Errorf("unsupported agent %q (supported: %s)", c.Type, strings.Join(Types, ", "))
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func version(ctx context.Context, command string) (string, error) {
	out, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --version: %v: %.200s", command, err, strings.TrimSpace(string(out)))
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1]), nil // some CLIs print banners before the version
}

// run executes an agent CLI in dir with the timeout; stdout is returned even when the exit code is non-zero.
func run(ctx context.Context, timeout time.Duration, dir string, env []string, stdin io.Reader, name string, args ...string) ([]byte, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return nil, fmt.Errorf("%s failed: %v: %.400s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.Bytes(), nil
}

var fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*\\})\\s*```")

// ExtractJSON pulls the answer object out of free text: a fenced block, else the outermost braces.
func ExtractJSON(text string) []byte {
	text = strings.TrimSpace(text)
	if m := fenceRe.FindAllStringSubmatch(text, -1); m != nil {
		return []byte(m[len(m)-1][1])
	}
	if i, j := strings.Index(text, "{"), strings.LastIndex(text, "}"); i >= 0 && j > i {
		return []byte(text[i : j+1])
	}
	return []byte(text)
}

// inlineSystem is for CLIs without a system-prompt flag.
func inlineSystem(req Request) (string, error) {
	sys, err := os.ReadFile(req.SystemFile)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("<reviewer_instructions>\n%s\n</reviewer_instructions>\n\n%s\n\nReturn only one JSON object that matches this JSON schema, with no prose around it:\n%s\n",
		sys, req.Task, req.Schema), nil
}
