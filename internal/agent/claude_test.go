package agent

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const answer = `{"verdict":"safe_to_merge"}`

func TestParseStructuredOutput(t *testing.T) {
	env, _ := json.Marshal(map[string]any{"structured_output": json.RawMessage(answer), "total_cost_usd": 0.42, "num_turns": 9})
	o, err := ParseClaude(env)
	if err != nil || string(o.JSON) != answer || o.CostUSD != 0.42 || o.Turns != 9 {
		t.Fatalf("got %+v, %v", o, err)
	}
}

func TestParseFencedResultFallback(t *testing.T) {
	env, _ := json.Marshal(map[string]any{"result": "```json\n" + answer + "\n```"})
	o, err := ParseClaude(env)
	if err != nil || string(o.JSON) != answer {
		t.Fatalf("got %+v, %v", o, err)
	}
}

func TestParseErrors(t *testing.T) {
	for _, b := range []string{"not json", `{"is_error":true,"result":"boom"}`} {
		if _, err := ParseClaude([]byte(b)); err == nil {
			t.Errorf("expected error for %s", b)
		}
	}
}

// The read-only guarantee lives in these flags; a refactor must not drop them.
func TestArgsAreReadOnly(t *testing.T) {
	args := NewClaude(Config{Model: "m"}).Args(Request{Task: "t", SystemFile: "s", Schema: "{}"})
	joined := strings.Join(args, " ")
	for _, want := range []string{"--tools Read,Grep,Glob", "--permission-mode dontAsk", "--strict-mcp-config", "--setting-sources user", "--no-session-persistence", "--model m", "--max-turns 40"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %v", want, args)
		}
	}
	if slices.Contains(args, "--dangerously-skip-permissions") {
		t.Error("must never skip permissions")
	}
}
