// Package agent runs a coding-agent CLI as a read-only reviewer and returns its JSON answer.
package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

type Request struct {
	Task       string // user prompt
	SystemFile string // appended system prompt
	Schema     string // JSON schema the answer must follow
	Dir        string // checkout of the MR head; the only tree the agent may read
}

type Output struct {
	JSON    []byte
	CostUSD float64
	Turns   int
}

// Agent must keep the run read-only: no shell, no writes, no network tools.
type Agent interface {
	Name() string
	// Version proves the CLI is installed and runnable.
	Version(ctx context.Context) (string, error)
	Review(ctx context.Context, req Request) (*Output, error)
}

func version(ctx context.Context, command string) (string, error) {
	out, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s --version: %v: %.200s", command, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

type Config struct {
	Type     string
	Command  string
	Model    string
	MaxTurns int
	Timeout  time.Duration
}

func New(c Config) (Agent, error) {
	switch c.Type {
	case "claude":
		return NewClaude(c), nil
	}
	return nil, fmt.Errorf("unsupported agent %q (supported: claude)", c.Type)
}
