package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func has(t *testing.T, name string, args []string, want ...string) {
	t.Helper()
	joined := " " + strings.Join(args, " ") + " "
	for _, w := range want {
		if !strings.Contains(joined, " "+w+" ") {
			t.Errorf("%s: missing %q in %v", name, w, args)
		}
	}
	for _, bad := range []string{"--dangerously-bypass-approvals-and-sandbox", "danger-full-access", "workspace-write", "--auto", "--approve"} {
		if slices.Contains(args, bad) {
			t.Errorf("%s: must never pass %s", name, bad)
		}
	}
}

func TestCodexArgsAreReadOnly(t *testing.T) {
	args := NewCodex(Config{Model: "m"}).Args(Request{Dir: "/w"}, "/s.json", "/l.txt")
	has(t, "codex", args, "exec", "--sandbox read-only", "--ephemeral", "--ignore-rules", `approval_policy="never"`,
		"project_doc_max_bytes=0", "mcp_servers={}", "-C /w", "--output-schema /s.json", "-o /l.txt", "-m m")
	if args[len(args)-1] != "-" {
		t.Error("prompt must come from stdin")
	}
}

func TestPiArgsAreReadOnly(t *testing.T) {
	args := NewPi(Config{Model: "m"}).Args(Request{}, "/g.ts")
	has(t, "pi", args, "-p", "--no-session", "-e /g.ts", "--tools read,grep,find,ls", "--no-extensions", "--no-skills", "--no-mcp", "--no-context-files", "--no-approve", "--model m")
	if slices.Contains(args, "--") {
		t.Error("prompt must come on stdin, not argv")
	}
}

func TestOpenCodePermissionsDenyEverythingButReads(t *testing.T) {
	var perm map[string]string
	if err := json.Unmarshal([]byte(openCodePermission), &perm); err != nil {
		t.Fatal(err)
	}
	for k, v := range perm {
		allowed := k == "read" || k == "grep" || k == "glob" || k == "list"
		if allowed != (v == "allow") || (!allowed && v != "deny") {
			t.Errorf("%s=%s", k, v)
		}
	}
	for _, k := range []string{"edit", "bash", "external_directory", "webfetch", "websearch", "task"} {
		if perm[k] != "deny" {
			t.Errorf("%s must be denied", k)
		}
	}
	o := NewOpenCode(Config{Model: "p/m"})
	has(t, "opencode", o.Args(Request{Dir: "/w"}, "/t/task.md"), "run", "--format json", "--dir /w", "--file /t/task.md", "--model p/m")
	env := strings.Join(o.Env(), " ")
	for _, w := range []string{"OPENCODE_DISABLE_PROJECT_CONFIG=1", "OPENCODE_PURE=1", "OPENCODE_DISABLE_CLAUDE_CODE=1"} {
		if !strings.Contains(env, w) {
			t.Errorf("env missing %s", w)
		}
	}
}

func TestOpenCodeEventText(t *testing.T) {
	raw := `{"type":"step_start","part":{}}
{"type":"text","part":{"type":"text","text":"{\"verdict\":"}}
{"type":"text","part":{"type":"text","text":"\"safe_to_merge\"}"}}
{"type":"step_finish","part":{}}`
	if got := string(ExtractJSON(openCodeText([]byte(raw)))); got != `{"verdict":"safe_to_merge"}` {
		t.Errorf("got %q", got)
	}
}

func TestExtractJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:                               `{"a":1}`,
		"Here you go:\n```json\n{\"a\":1}\n```": `{"a":1}`,
		"banner\n{\"a\":{\"b\":2}}\nbye":        `{"a":{"b":2}}`,
		"no json":                               "no json",
	} {
		if got := string(ExtractJSON(in)); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestNewKnowsEveryType(t *testing.T) {
	for _, typ := range Types {
		a, err := New(Config{Type: typ})
		if err != nil || a.Name() != typ {
			t.Errorf("%s: %v", typ, err)
		}
	}
	if _, err := New(Config{Type: "copilot"}); err == nil {
		t.Error("unknown type must fail")
	}
}
