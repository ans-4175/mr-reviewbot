package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Permissions deny everything but reading inside the worktree; external_directory covers paths outside it.
const openCodePermission = `{"read":"allow","grep":"allow","glob":"allow","list":"allow","external_directory":"deny",` +
	`"edit":"deny","bash":"deny","task":"deny","todowrite":"deny","webfetch":"deny","websearch":"deny",` +
	`"lsp":"deny","skill":"deny","question":"deny","doom_loop":"deny"}`

// OpenCode runs `opencode run` with inline permissions and without project config, plugins or .claude content.
type OpenCode struct{ cfg Config }

func NewOpenCode(c Config) *OpenCode {
	c.Command = orDefault(c.Command, "opencode")
	return &OpenCode{cfg: c}
}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) Version(ctx context.Context) (string, error) { return version(ctx, o.cfg.Command) }

func (o *OpenCode) Env() []string {
	return []string{
		"OPENCODE_PERMISSION=" + openCodePermission,
		"OPENCODE_DISABLE_PROJECT_CONFIG=1", // the MR's opencode.json/AGENTS.md is data
		"OPENCODE_DISABLE_CLAUDE_CODE=1",
		"OPENCODE_DISABLE_EXTERNAL_SKILLS=1",
		"OPENCODE_PURE=1",
		"OPENCODE_DISABLE_AUTOUPDATE=1",
	}
}

// The prompt goes in an attached file: as one argv string it would hit Linux's 128 KiB per-argument limit.
func (o *OpenCode) Args(req Request, promptFile string) []string {
	args := []string{"run", "--format", "json", "--dir", req.Dir, "--file", promptFile}
	if o.cfg.Model != "" {
		args = append(args, "--model", o.cfg.Model)
	}
	return append(args, "Follow the instructions in the attached file and answer with the JSON object only.")
}

func (o *OpenCode) Review(ctx context.Context, req Request) (*Output, error) {
	prompt, err := inlineSystem(req)
	if err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp("", "reviewbot-opencode-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	promptFile := filepath.Join(tmp, "review-task.md")
	if err := os.WriteFile(promptFile, []byte(prompt), 0o644); err != nil {
		return nil, err
	}
	out, err := run(ctx, o.cfg.Timeout, req.Dir, o.Env(), nil, o.cfg.Command, o.Args(req, promptFile)...)
	if err != nil {
		return nil, err
	}
	return &Output{JSON: ExtractJSON(openCodeText(out))}, nil
}

// openCodeText joins the text parts of the JSON event stream; anything unparsed is kept as is.
func openCodeText(raw []byte) string {
	var b strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Part struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"part"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			b.Write(sc.Bytes())
			b.WriteString("\n")
			continue
		}
		if ev.Part.Text != "" && (ev.Type == "text" || ev.Part.Type == "text") {
			b.WriteString(ev.Part.Text)
		}
	}
	return b.String()
}
