// Package app wires the subcommands. Logs go to stderr; results go to stdout (JSON with --json).
//
// Exit codes: 0 ok (incl. nothing to do), 1 a review or check failed, 2 usage/config error, 3 another worker is running.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	reviewbot "mr-reviewbot"
	"mr-reviewbot/internal/agent"
	"mr-reviewbot/internal/config"
	"mr-reviewbot/internal/forge"
	"mr-reviewbot/internal/gitops"
	"mr-reviewbot/internal/queue"
	"mr-reviewbot/internal/render"
	"mr-reviewbot/internal/review"
	"mr-reviewbot/internal/state"
)

var Version = "dev"

const usage = `usage: reviewbot <command> [flags]

Setup:
  init        write a config file (forge, agent, repos)
  doctor      check git, the forge CLI login, the agent CLI, and access to each repo (--probe also tests agent isolation)
  service     install | uninstall | status | print  the 30-minute scheduler (launchd on macOS, systemd on Linux)
  skill       install <skills-dir>  copy the review-mr agent skill (for Hermes, Claude Code, ...)

Run:
  scan        list open MRs and whether each needs a review
  poll        queue MRs that need a review and work the queue (what the scheduler runs)
  review      review one MR now: reviewbot review <MR URL | group/project!iid | name!iid>
  queue       show pending and failed jobs
  validate    check the config
  version

Common flags: --config <file> --state <dir> --json --dry-run --force --limit <n>
`

type env struct {
	state string
	cfg   *config.Config
	forge forge.Forge
	agent agent.Agent
	q     *queue.Queue
	out   io.Writer
	err   io.Writer
	json  bool
}

func (e *env) logf(format string, a ...any) {
	fmt.Fprintf(e.err, "[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, a...))
}

func (e *env) stateDir(parts ...string) string {
	return filepath.Join(append([]string{e.state}, parts...)...)
}

type Candidate struct {
	Project string `json:"project"`
	IID     int    `json:"iid"`
	Title   string `json:"title"`
	Author  string `json:"author"`
	SHA     string `json:"sha"`
	Last    string `json:"last_reviewed_sha,omitempty"`
	Review  bool   `json:"review"`
	Reason  string `json:"reason"`
}

type Result struct {
	Project  string  `json:"project"`
	IID      int     `json:"iid"`
	Status   string  `json:"status"` // posted | dry-run | skipped | failed
	Reason   string  `json:"reason,omitempty"`
	SHA      string  `json:"sha,omitempty"`
	Mode     string  `json:"mode,omitempty"`
	Verdict  string  `json:"verdict,omitempty"`
	Findings int     `json:"findings"`
	Out      string  `json:"out,omitempty"`
	Seconds  float64 `json:"seconds,omitempty"`
	CostUSD  float64 `json:"cost_usd,omitempty"`
	Error    string  `json:"error,omitempty"`
}

// parse allows flags after the positional args (`review <url> --dry-run`).
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version", "--version":
		fmt.Fprintln(stdout, Version)
		return 0
	case "init":
		return runInit(rest, stdout, stderr)
	case "service":
		return runService(rest, stdout, stderr)
	case "skill":
		return runSkill(rest, stdout, stderr)
	case "validate", "doctor", "scan", "poll", "review", "queue":
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}

	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	cfgPath := fs.String("config", config.DefaultPath(), "config file")
	stateDir := fs.String("state", config.DefaultStateDir(), "state dir (queue, mirrors, logs)")
	asJSON := fs.Bool("json", false, "machine-readable output on stdout")
	dry := fs.Bool("dry-run", false, "review but never post or touch the queue")
	force := fs.Bool("force", false, "re-review even if this sha was already reviewed")
	limit := fs.Int("limit", 0, "max MRs to review this run (default: max_per_run)")
	probe := fs.Bool("probe", false, "doctor: also run the agent once to prove it cannot read outside the checkout")
	pos, err := parse(fs, rest)
	if err != nil {
		return 2
	}

	e, err := newEnv(*cfgPath, *stateDir, stdout, stderr, *asJSON)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintln(stderr, "hint: run `reviewbot init` first, or pass --config")
		}
		return 2
	}

	switch cmd {
	case "validate":
		fmt.Fprintf(stdout, "config ok: %s, forge=%s agent=%s, %d repo(s), %d enabled\n",
			*cfgPath, e.cfg.Forge.Type, e.cfg.Agent.Type, len(e.cfg.Repos), len(e.cfg.Enabled()))
		return 0
	case "doctor":
		return e.doctor(*probe)
	case "scan":
		return e.scan()
	case "poll":
		return e.poll(*dry, *limit)
	case "review":
		if len(pos) != 1 {
			fmt.Fprintln(stderr, "usage: reviewbot review <MR URL | group/project!iid> [--force] [--dry-run] [--json]")
			return 2
		}
		return e.reviewOne(pos[0], *force, *dry)
	}
	return e.showQueue()
}

func newEnv(cfgPath, stateDir string, stdout, stderr io.Writer, asJSON bool) (*env, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, err
	}
	f, err := forge.New(cfg.Forge)
	if err != nil {
		return nil, err
	}
	a, err := agent.New(cfg.Agent)
	if err != nil {
		return nil, err
	}
	q, err := queue.New(stateDir)
	if err != nil {
		return nil, err
	}
	return &env{state: stateDir, cfg: cfg, forge: f, agent: a, q: q, out: stdout, err: stderr, json: asJSON}, nil
}

func (e *env) candidates() ([]Candidate, error) {
	bot, err := e.forge.User()
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, repo := range e.cfg.Enabled() {
		mrs, err := e.forge.OpenMRs(repo.Path)
		if err != nil {
			return nil, err
		}
		for _, mr := range mrs {
			notes, err := e.forge.Notes(repo.Path, mr.IID)
			if err != nil {
				return nil, err
			}
			lastSHA := ""
			if l := state.LastReview(notes, bot.ID); l != nil {
				lastSHA = l.SHA
			}
			d := state.Decide(mr, lastSHA, repo, time.Now(), false, false)
			if d.Review && e.q.Parked(repo.Path, mr.IID, mr.SHA) {
				d = state.Decision{Review: false, Reason: "parked-after-failures"}
			}
			out = append(out, Candidate{repo.Path, mr.IID, mr.Title, mr.Author.Username, mr.SHA, lastSHA, d.Review, d.Reason})
		}
	}
	return out, nil
}

func (e *env) scan() int {
	rows, err := e.candidates()
	if err != nil {
		fmt.Fprintln(e.err, "error:", err)
		return 1
	}
	if e.json {
		return e.emit(rows)
	}
	n := 0
	for _, r := range rows {
		mark := "skip  "
		if r.Review {
			mark, n = "REVIEW", n+1
		}
		fmt.Fprintf(e.out, "%s %s!%d  %-20s @%-14s %.60s\n", mark, r.Project, r.IID, r.Reason, r.Author, r.Title)
	}
	fmt.Fprintf(e.out, "\n%d to review, %d skipped\n", n, len(rows)-n)
	return 0
}

func (e *env) emit(v any) int {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(e.out, string(b))
	return 0
}

func (e *env) poll(dry bool, limit int) int {
	rows, err := e.candidates()
	if err != nil {
		fmt.Fprintln(e.err, "error:", err)
		return 1
	}
	var jobs []*queue.Job
	for _, r := range rows {
		if !r.Review {
			continue
		}
		if !dry {
			if err := e.q.Enqueue(r.Project, r.IID, "poll", r.SHA, false); err != nil {
				fmt.Fprintln(e.err, "error:", err)
				return 1
			}
		}
		jobs = append(jobs, &queue.Job{Project: r.Project, IID: r.IID, Source: "poll"})
	}
	e.logf("poll: %d MR(s) need review", len(jobs))
	if limit == 0 {
		limit = e.cfg.MaxPerRun
	}
	if !dry {
		jobs, _ = e.q.Pending()
	}
	return e.drain(jobs, limit, dry)
}

func (e *env) reviewOne(ref string, force, dry bool) int {
	project, iid, err := resolveRef(e.cfg, e.forge.ParseURL, ref)
	if err != nil {
		fmt.Fprintln(e.err, "error:", err)
		return 2
	}
	job := &queue.Job{Project: project, IID: iid, Source: "manual", Force: force}
	if dry {
		return e.drain([]*queue.Job{job}, 1, true)
	}
	e.q.Unpark(project, iid)
	if err := e.q.Enqueue(project, iid, "manual", "", force); err != nil {
		fmt.Fprintln(e.err, "error:", err)
		return 1
	}
	pending, _ := e.q.Pending()
	for _, j := range pending {
		if j.Project == project && j.IID == iid {
			// Only the requested MR: someone waiting on chat must not drain older poll jobs first.
			return e.drain([]*queue.Job{j}, 1, false)
		}
	}
	return 1
}

func (e *env) showQueue() int {
	jobs, _ := e.q.Pending()
	parked := e.q.FailedJobs()
	if e.json {
		return e.emit(map[string]any{"pending": jobs, "failed": parked})
	}
	for _, j := range jobs {
		fmt.Fprintf(e.out, "pending %s!%d source=%s force=%v attempts=%d %s\n", j.Project, j.IID, j.Source, j.Force, j.Attempts, j.LastError)
	}
	for _, j := range parked {
		fmt.Fprintf(e.out, "FAILED  %s!%d sha=%.8s attempts=%d error: %s\n", j.Project, j.IID, j.SHA, j.Attempts, j.LastError)
	}
	fmt.Fprintf(e.out, "%d pending, %d failed\n", len(jobs), len(parked))
	return 0
}

// drain runs jobs serially under the worker lock; a failed job stays queued for the next run.
func (e *env) drain(jobs []*queue.Job, limit int, dry bool) int {
	unlock, err := e.q.Lock()
	if errors.Is(err, queue.ErrBusy) {
		e.logf("worker busy, jobs stay queued")
		return 3
	}
	if err != nil {
		fmt.Fprintln(e.err, "error:", err)
		return 1
	}
	defer unlock()

	results := []Result{}
	failed := false
	reviewed := 0 // only real review attempts spend the per-run budget, not skips
	for i, j := range jobs {
		if reviewed >= limit {
			e.logf("%d job(s) deferred to the next run", len(jobs)-i)
			break
		}
		res, err := e.process(j.Project, j.IID, j.Force, j.Source == "manual", dry)
		if err != nil {
			e.logf("FAILED %s!%d: %v", j.Project, j.IID, err)
			res = Result{Project: j.Project, IID: j.IID, Status: "failed", Error: err.Error()}
			failed = true
			if !dry {
				e.q.Fail(j, err.Error())
			}
		} else if !dry {
			e.q.Done(j)
		}
		if res.Status != "skipped" {
			reviewed++
		}
		e.logf("%s!%d → %s %s", res.Project, res.IID, res.Status, res.Reason)
		results = append(results, res)
	}
	if e.json {
		e.emit(results)
	}
	if failed {
		return 1
	}
	return 0
}

func (e *env) process(project string, iid int, force, manual, dry bool) (Result, error) {
	res := Result{Project: project, IID: iid}
	repo := e.cfg.Get(project)
	if repo == nil {
		res.Status, res.Reason = "skipped", "not whitelisted"
		return res, nil
	}
	bot, err := e.forge.User()
	if err != nil {
		return res, err
	}
	mr, err := e.forge.MR(project, iid)
	if err != nil {
		return res, err
	}
	notes, err := e.forge.Notes(project, iid)
	if err != nil {
		return res, err
	}
	last := state.LastReview(notes, bot.ID)
	lastSHA, lastBody := "", ""
	if last != nil {
		lastSHA, lastBody = last.SHA, last.Body
	}
	d := state.Decide(mr, lastSHA, *repo, time.Now(), force, manual)
	if !d.Review {
		res.Status, res.Reason = "skipped", d.Reason
		return res, nil
	}

	head, target := mr.SHA, mr.TargetBranch
	remote := gitops.Remote{URL: e.forge.CloneURL(project), CredHelper: e.forge.CredentialHelper(), HeadRef: e.forge.HeadRef(iid)}
	gd, err := gitops.SyncMirror(e.stateDir("mirrors"), project, remote, iid, target)
	if err != nil {
		return res, err
	}
	plan, err := gitops.PlanRange(gd, "refs/target/"+target, head, lastSHA, repo.MaxDiffLines)
	if err != nil {
		return res, err
	}
	res.SHA, res.Mode = head, plan.Mode
	e.logf("%s!%d %.8s mode=%s files=%d", project, iid, head, plan.Mode, len(plan.Files))

	var conv strings.Builder
	for _, name := range []string{"CLAUDE.md", "AGENTS.md"} {
		if t := gitops.Show(gd, "refs/target/"+target, name); t != "" {
			fmt.Fprintf(&conv, "### %s\n%s\n\n", name, t)
		}
	}

	sys, err := review.BuildSystemPrompt(reviewbot.Assets)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(e.stateDir("build"), 0o755); err != nil {
		return res, err
	}
	sysFile := e.stateDir("build", "system.md")
	if err := os.WriteFile(sysFile, []byte(sys), 0o644); err != nil {
		return res, err
	}
	task := review.BuildTaskPrompt(mr, plan, conv.String(), lastBody, repo.Language)

	stem := fmt.Sprintf("%s!%d", strings.ReplaceAll(project, "/", "__"), iid)
	work := e.stateDir("work", stem)
	cleanup, err := gitops.Worktree(gd, head, work)
	if err != nil {
		return res, err
	}
	start := time.Now()
	out, err := e.agent.Review(context.Background(), agent.Request{Task: task, SystemFile: sysFile, Schema: review.Schema, Dir: work})
	cleanup()
	if err != nil {
		return res, err
	}
	r, err := review.Parse(out.JSON)
	if err != nil {
		return res, err
	}
	r.Stats = review.Stats{Seconds: time.Since(start).Round(100 * time.Millisecond).Seconds(), CostUSD: out.CostUSD, Turns: out.Turns}
	render.GuardVerdict(r)
	body := render.Comment(r, render.Meta{From: plan.FromSHA, To: plan.ToSHA, Mode: plan.Mode, Language: repo.Language})

	if err := os.MkdirAll(e.stateDir("out"), 0o755); err != nil {
		return res, err
	}
	res.Out = e.stateDir("out", fmt.Sprintf("%s-%.8s.md", stem, head))
	_ = os.WriteFile(res.Out, []byte(body), 0o644)
	raw, _ := json.MarshalIndent(r, "", "  ")
	_ = os.WriteFile(strings.TrimSuffix(res.Out, ".md")+".json", raw, 0o644)

	res.Verdict, res.Findings, res.Seconds, res.CostUSD = r.Verdict, len(r.Findings), r.Stats.Seconds, r.Stats.CostUSD
	e.appendLog(res, dry)

	if dry {
		res.Status = "dry-run"
		return res, nil
	}
	if err := e.forge.PostNote(project, iid, body); err != nil { // marker exists only after a successful post
		return res, err
	}
	res.Status = "posted"
	return res, nil
}

func (e *env) appendLog(res Result, dry bool) {
	_ = os.MkdirAll(e.stateDir("log"), 0o755)
	f, err := os.OpenFile(e.stateDir("log", "reviews.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, _ := json.Marshal(map[string]any{"ts": time.Now().Unix(), "project": res.Project, "iid": res.IID, "sha": res.SHA,
		"mode": res.Mode, "verdict": res.Verdict, "findings": res.Findings, "seconds": res.Seconds, "cost_usd": res.CostUSD, "dry_run": dry})
	fmt.Fprintln(f, string(b))
}
