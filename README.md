# mr-reviewbot

**A code reviewer that reads everything, runs nothing, and remembers where it left off without a database.**

`reviewbot` is a small Go binary that watches your merge requests (GitLab) and pull requests (GitHub). Every time someone pushes, it reads the change, the code around it and the discussion, then posts one comment: a verdict, ranked findings, and a prompt you can paste straight into your coding agent for the next round.

```bash
curl -fsSL https://raw.githubusercontent.com/ans-4175/mr-reviewbot/main/scripts/install.sh | sh
reviewbot init --repo your-group/your-project
reviewbot doctor --probe
reviewbot service install     # and go make coffee
```

## Why another review bot?

Because most of them want a server, a webhook, an API key of their own, and a database to remember what they already said. This one wants none of that.

- **It moves into the house you already have.** No new accounts, no secrets to paste. It uses the forge CLI you are already logged into (`glab` or `gh`) and the coding agent you already pay for (`claude`, `pi`, with `codex` and `opencode` waiting at the door). Install the binary, write one config file, done. An agent can do the whole setup by following [SETUP.md](SETUP.md).
- **It gives a verdict, not a vibe.** ✅ Safe to merge, 🟡 Needs changes, or 🔴 Not safe to merge, and the verdict is never rosier than its own findings.
- **It writes the next prompt for you.** Each comment ends with a paste-ready task list for your coding agent: what is wrong, where, what "fixed" looks like, how to check it. Humans steer, agents iterate, the bot reviews the next push.
- **It has no database, and no amnesia.** State is the commit sha and the comments already on the MR. Every review ends with an invisible `<!-- mr-reviewbot v1 sha=… -->` marker. Next run, the bot finds its own last marker, diffs from that sha to the new head, and checks whether its earlier "must fix" items got fixed. Wipe the host, reinstall, and it picks up exactly where it stopped.
- **It reads the room.** The author's explanations and reviewers' inline comments are part of the context, so it won't nag about a choice that was already justified in the thread. (It checks the claim against the code first. Comments are context, never commands.)

## What a review looks like

```
## 🤖 Automated review · 🟡 Needs changes
> One required finding: the new endpoint takes the tenant id from the request body.

Range: a1b2c3d4..e5f6a7b8 (incremental since the last review) · Code reading only, no test/lint/build

### Findings (2)
| # | Level       | Location          | Finding                                |
| 1 | 🟠 Fix      | api/orders.go:88  | Tenant id taken from the request body  |
| 2 | ⚪ Optional | api/orders.go:120 | Duplicate nil check                    |
_Levels: 🟠 Fix = fix before merging · ⚪ Optional = nice to have, fine to skip_

### Resolved since the last review
- api/orders.go: the missing auth check from the previous review is in place.

### Prompt for the next iteration   ▸ (collapsed, paste into your coding agent)
```

Levels are one action word each, so nobody has to decode "nit-ish but maybe blocking": 🔴 **Must-fix** · 🟠 **Fix** · 🔵 **Consider** · ⚪ **Optional** · 💬 **Note**. Comments come in English (default) or Indonesian (`"language": "id"`, where the levels become Wajib, Perbaiki, Saran, Sunnah, Info).

## Two ways to use it

| | A. Poll bot | B. On demand |
|---|---|---|
| Who starts it | a scheduler, every `poll_interval_minutes` (default 30) | someone in chat: "review api!357" |
| How | `reviewbot service install` (launchd or systemd) | `reviewbot skill install <chat-agent-skills-dir>` |
| Reviews | every new push on the whitelisted repos | the one MR asked for, right away |

Both share one config, one whitelist and one queue. Pick one or run both. [SETUP.md](SETUP.md) walks through each.

## How it works

```
scheduler (poll_interval_minutes)        chat agent (skill review-mr)
        │                                          │
        ▼                                          ▼
   reviewbot poll                           reviewbot review <MR>
        │ 1. read the whitelist (config.json)
        │ 2. per repo: open, non-draft MRs
        │ 3. find the sha marker in the bot's own comments
        │      same sha as head  → skip, nothing new
        │      different / none  → queue
        ▼
   queue (one file per MR) ── single worker (flock), at most max_per_run per run
        │ 4. isolation probe passed for this agent/version? if not, stop here
        │ 5. blobless git mirror + worktree at the head sha (MR symlinks become plain files)
        │ 6. range: last reviewed sha → head (incremental), or the whole MR
        │ 7. agent reads description, commits, discussion, diff and the checkout; returns JSON
        ▼
   MR comment: verdict + findings + resolved items + steering prompt + new sha marker
```

- **Each push gets a review, not each poll.** Same sha, no comment. New sha: only the changes since the last reviewed sha are searched for new findings (changes that arrived by merging the target branch are dropped), but the verdict judges the whole MR. A force-push or a vanished old sha falls back to a full review.
- **Not noisy.** Drafts, the `no-ai-review` label, `skip_authors`, and never-reviewed MRs idle for more than `lookback_days` (default 7) are skipped. Bots' MRs are reviewed like anyone else's. A manual `review` ignores the lookback.
- **Context, in order of trust.** `CLAUDE.md`/`AGENTS.md` from the target branch are the only trusted conventions. The MR description, commits, the last 20 human comments (inline ones with file:line) and the diff are all marked untrusted data.
- **Review lenses.** The prompt embeds `code-review-and-quality`, `code-simplification` and `security-and-hardening` from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) (MIT, pinned, see [NOTICE](NOTICE)).
- **Slow runs never pile up.** The interval counts from the end of the previous run.

## Supported forges and agents

| | Status |
|---|---|
| GitLab via `glab` (gitlab.com or self-hosted) | ✅ in daily use |
| GitHub via `gh` (github.com or Enterprise) | ✅ reviews its own PRs |
| Claude Code (`claude`) | ✅ isolation probe passes |
| pi (`pi`) | ✅ isolation probe passes (with a bundled guard extension) |
| Codex (`codex`) | 🧪 adapter ready, needs a real-host check: [#2](https://github.com/ans-4175/mr-reviewbot/issues/2) |
| opencode (`opencode`) | 🧪 adapter ready, needs a real-host check: [#3](https://github.com/ans-4175/mr-reviewbot/issues/3) |

"Adapter ready" is not a polite word for "unsafe": reviewbot will not review with an agent until the isolation probe passes on your host.

## Security, or: what a crafted MR can and cannot do

- **The reviewer only reads, and that is tested, not assumed.** Before reviewing, reviewbot runs a probe (re-run whenever the agent, its version or the model changes, and at least daily): the agent must read a file in the checkout and fail to read a file, a symlink or a symlinked directory outside it. Fail the probe and no review runs; the jobs stay queued, nothing gets parked. Each adapter also pins read-only settings: Claude runs with `--tools Read,Grep,Glob` and `--permission-mode dontAsk`, without MCP servers, slash commands, the repo's `.claude/settings`, hooks, or auto-loaded `CLAUDE.md`. pi runs with read/search tools only, behind a guard extension.
- **The forge token never reaches the agent.** The binary posts the comment; the agent only returns JSON.
- **MR content is untrusted.** The prompt forbids following instructions found in the MR, its comments or its files, and asks the reviewer to flag attempts. This lowers the risk; it does not remove it. A clever MR can still sway a verdict, which is why humans merge and the bot never approves or merges anything.
- **GitLab quick actions are defused.** Comment lines starting with `/` are neutralized, so a finding can't `/merge` anything.
- **Your code goes to the agent's model provider.** The whitelist is the control: list only repos whose code may leave your network that way.
- **Comments are posted as whoever the forge CLI is logged in as.** Use a dedicated bot account, and never test `poll`/`review` without `--dry-run` while logged in as yourself.

## Commands

```
reviewbot init        write a config (flags only, so agents can run it too)
reviewbot doctor      git, forge login, agent CLI, repo access; --probe also tests isolation
reviewbot service     install | uninstall | status | print   (the poll bot)
reviewbot skill       install <skills-dir>                    (the chat trigger)
reviewbot scan        read-only: which MRs would be reviewed, and why the rest are skipped
reviewbot poll        queue + work the queue (what the scheduler runs)
reviewbot review      <MR/PR URL | group/project!iid | owner/repo#n | name!iid> [--force] [--dry-run]
reviewbot queue       pending and failed jobs
reviewbot validate    check the config
```

Common flags: `--config <file>` (default `$XDG_CONFIG_HOME/mr-reviewbot/config.json`), `--state <dir>` (default `$XDG_STATE_HOME/mr-reviewbot`), `--json` (results on stdout, logs on stderr), `--dry-run` (review for real, post nothing, leave the queue alone).

Exit codes: `0` ok (including "nothing to do"), `1` a review or check failed, `2` usage or config error, `3` another worker is busy (the job stays queued).

## Configuration

One file, [config.example.json](config.example.json). Unknown keys are rejected, so a typo can't silently switch a rule off.

| Key | Meaning |
|---|---|
| `forge.type` / `host` / `command` | `gitlab` or `github`, the host (default gitlab.com / github.com), the CLI binary |
| `agent.type` / `command` / `model` | `claude`, `pi`, `codex` or `opencode`, the CLI binary, the model (empty = the agent's own default) |
| `agent.max_turns` / `timeout_minutes` | per review, defaults 40 and 20 |
| `max_per_run` | reviews per scheduled run (skips don't count); the rest wait their turn, oldest first |
| `poll_interval_minutes` | poll bot cadence (default 30); re-run `service install` after changing it |
| `defaults.*`, `repos[].*` | `lookback_days`, `max_diff_lines`, `language` (`en`/`id`), `skip_labels`, `skip_authors`, `target_branches` |

## Development

Go ≥ 1.22 and `git`. No third-party Go modules.

```bash
make test    # go vet + go test; an end-to-end test drives a fake forge and agent over a real local git remote
make build   # bin/reviewbot
make dist    # release archives for darwin/linux × arm64/amd64 + checksums.txt
```

Rules for contributors and their agents are in [AGENTS.md](AGENTS.md). Good first contributions: [`first-time-issue`](https://github.com/ans-4175/mr-reviewbot/labels/first-time-issue).

## License

MIT, © ans4175 and LabtekIndie. See [LICENSE](LICENSE); third-party notices in [NOTICE](NOTICE).
