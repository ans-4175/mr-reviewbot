# mr-reviewbot

A small Go CLI that polls your merge requests and posts a read-only AI code review on each new push: a verdict, ranked findings, and a paste-ready prompt to steer the next agent iteration.

It does not ship its own credentials or model client. It drives the tools already on the host:

- **Forge CLI** for API and git access: `glab` (GitLab, incl. self-hosted). GitHub via `gh` is planned.
- **Coding agent CLI** as the reviewer, locked to read-only tools: `claude` (Claude Code). Codex and pi are planned.
- **Scheduler**: launchd on macOS, a systemd user timer on Linux.

Setup is in [SETUP.md](SETUP.md). The short version:

```bash
curl -fsSL https://raw.githubusercontent.com/<owner>/mr-reviewbot/main/scripts/install.sh | REVIEWBOT_REPO=<owner>/mr-reviewbot sh
reviewbot init --repo your-group/your-project
reviewbot doctor --probe
reviewbot service install
```

## What a review looks like

```
## 🤖 Automated review · 🟡 Needs changes
> One required finding: the new endpoint skips the tenant check.

Range: a1b2c3d4..e5f6a7b8 (incremental since the last review) · Code reading only, no test/lint/build

### Findings (2)
| # | Level      | Location           | Finding                                |
| 1 | 🟠 Fix     | api/orders.go:88   | Tenant id taken from the request body  |
| 2 | ⚪ Optional | api/orders.go:120 | Duplicate nil check                    |
_Levels: 🟠 Fix = fix before merging · ⚪ Optional = nice to have, fine to skip_

### Prompt for the next iteration   (collapsed, paste into your coding agent)
```

Verdicts: ✅ Safe to merge · 🟡 Needs changes · 🔴 Not safe to merge. Levels are one action word each: 🔴 Must-fix, 🟠 Fix, 🔵 Consider, ⚪ Optional, 💬 Note. Comments can be written in English (default) or Indonesian (`"language": "id"`).

## How it works

```
scheduler (every 30 min)                 chat agent (skill review-mr)
        │                                          │
        ▼                                          ▼
   reviewbot poll                           reviewbot review <MR>
        │ 1. read the whitelist (config.json)
        │ 2. per repo: open, non-draft MRs
        │ 3. find the sha marker in the bot's own comments
        │      same sha as head  → skip
        │      different / none  → queue
        ▼
   queue (one file per MR) ── single worker (flock), at most max_per_run per run
        │ 4. blobless git mirror + worktree at the head sha
        │ 5. range: last reviewed sha → head (incremental), or the whole MR
        │ 6. agent with Read/Grep/Glob only, structured JSON answer
        ▼
   MR comment: verdict + findings + steering prompt + new sha marker
```

- **State lives on the forge.** Each comment ends with `<!-- mr-reviewbot v1 sha=… -->`. Only comments written by the account the forge CLI is logged in as count, so nobody else can fake a "reviewed" mark. Losing the host loses nothing but the cache.
- **Each push gets a review.** A new head sha is reviewed incrementally (only changes since the last reviewed sha, files that arrived by merging the target are dropped). A force-push or a missing old sha falls back to a full review. The verdict always judges the whole MR, and open required findings from the previous review are carried forward.
- **Not noisy.** Drafts, the `no-ai-review` label, `skip_authors`, and never-reviewed MRs idle for more than `lookback_days` (default 7) are skipped. Authors of any kind are reviewed, bots included. A manual `review` ignores the lookback.
- **The verdict can't be rosier than the findings.** Any must-fix → not safe; any fix → at least needs changes.
- **Context first.** The reviewer gets the MR description, commits, `CLAUDE.md`/`AGENTS.md` from the target branch (as the only trusted conventions), and a full checkout to read around the diff.
- **Review lenses.** The reviewer prompt embeds `code-review-and-quality`, `code-simplification` and `security-and-hardening` from [addyosmani/agent-skills](https://github.com/addyosmani/agent-skills) (MIT, pinned, see [NOTICE](NOTICE)).
- **The 30 minutes count from the end of the previous run**, so slow rounds never overlap.

## Commands

```
reviewbot init        write a config (non-interactive flags, so agents can run it too)
reviewbot doctor      check git, forge login, agent CLI, repo access; --probe tests agent isolation
reviewbot service     install | uninstall | status | print
reviewbot skill       install <skills-dir>
reviewbot scan        read-only: which MRs would be reviewed and why
reviewbot poll        queue + work the queue (what the scheduler runs)
reviewbot review      <MR URL | group/project!iid | name!iid> [--force] [--dry-run]
reviewbot queue       pending and failed jobs
reviewbot validate    check the config
```

Common flags: `--config <file>` (default `$XDG_CONFIG_HOME/mr-reviewbot/config.json`), `--state <dir>` (default `$XDG_STATE_HOME/mr-reviewbot`), `--json` (results on stdout, logs always on stderr), `--dry-run` (run the reviewer but never post or touch the queue).

Exit codes: `0` ok (incl. nothing to do), `1` a review or check failed, `2` usage/config error, `3` another worker is running (the job stays queued).

## Configuration

See [config.example.json](config.example.json). Unknown keys are rejected so a typo cannot silently disable a rule.

| Key | Meaning |
|---|---|
| `forge.type` / `host` / `command` | `gitlab`, `gitlab.com` or your host, the CLI binary |
| `agent.type` / `command` / `model` | `claude`, the CLI binary, model (empty = the agent's own default) |
| `agent.max_turns` / `timeout_minutes` | per review, defaults 40 and 20 |
| `max_per_run` | reviews per run (skips don't count); the rest wait for the next run, oldest first |
| `defaults.*`, `repos[].*` | `lookback_days`, `max_diff_lines`, `language` (`en`/`id`), `skip_labels`, `skip_authors`, `target_branches` |

## Chat trigger (agent skill)

`skills/review-mr/SKILL.md` lets a chat agent (Hermes, Claude Code, …) run `reviewbot review <MR>` when someone asks for a review. Symlink it from a checkout, or write a copy with the absolute binary path baked in:

```bash
reviewbot skill install ~/.hermes/profiles/<profile>/skills
```

## Security

- **The reviewer only reads.** Claude runs with `--tools Read,Grep,Glob`, no MCP servers, no slash commands, only user-level settings (the repo's own `.claude/settings` and hooks are ignored), and `CLAUDE.md` auto-loading disabled. `reviewbot doctor --probe` checks on your host that it can read the checkout but not a file (or symlink) outside it.
- **The forge token never reaches the agent.** The binary posts the comment; the agent only returns JSON.
- **MR content is untrusted.** The prompt forbids following instructions found in it and asks the reviewer to flag such attempts. This lowers the risk, it does not remove it: a crafted MR can still sway a verdict. Humans make the merge decision; the bot never approves or merges.
- Comment lines starting with `/` are neutralized so GitLab does not run them as quick actions.
- **Your code goes to the agent's model provider.** The whitelist is the control. Review only repos whose code may leave your network that way.
- **Comments are posted as whoever the forge CLI is logged in as.** Use a dedicated bot account, and never test `poll`/`review` without `--dry-run` while logged in as yourself.

## Development

Go ≥ 1.22, `git`. No third-party Go modules.

```bash
make test    # go vet + go test (git tests use throwaway repos; an end-to-end test uses a fake forge and agent)
make build   # bin/reviewbot
make dist    # release archives for darwin/linux × arm64/amd64 + checksums.txt
```

Contributor and agent rules are in [AGENTS.md](AGENTS.md).

## License

MIT, see [LICENSE](LICENSE). Third-party notices in [NOTICE](NOTICE).
