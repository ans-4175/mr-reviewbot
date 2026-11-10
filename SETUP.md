# Setup

Every step is a plain command, so a person or a coding agent can follow this file top to bottom. Each step says how to check it worked.

There are two ways to use the bot. Both start with the same **base setup**:

- **A. Poll bot:** a scheduler reviews every new push on the whitelisted repos on its own.
- **B. On-demand review:** a chat agent (Hermes, Claude Code, …) runs a review when someone asks. No scheduler needed.

You can set up either one, or both on the same host.

## Supported forges and agents

| | Status |
|---|---|
| **GitLab** via `glab` (gitlab.com or self-hosted) | supported, in production use |
| **GitHub** via `gh` (github.com or Enterprise) | supported; API and git access verified |
| **Claude Code** (`claude`) | supported; isolation probe passes |
| **Codex** (`codex`) | adapter ready (read-only sandbox, output schema). Not yet verified: on the test host Codex's own exec tool timed out |
| **opencode** (`opencode`) | adapter ready (permissions deny everything but reads, `external_directory` denied). Not yet verified on a real host |
| **pi** (`pi`) | adapter ready, but pi has no path restriction: the probe shows it can read outside the checkout, so reviewbot refuses to review with it |

Whatever agent you pick, reviewbot runs the isolation probe before the first review and again whenever the agent's version or model changes (and at least daily). If the agent can read outside the MR checkout, no review runs and the job stays queued. "Adapter ready" means it will work as soon as the probe passes on your host.

---

# Base setup

## 1. Prerequisites on the host

| Tool | Why | Check |
|---|---|---|
| `git` | blobless mirror + worktree per MR | `git --version` |
| `glab` or `gh` | forge API and git credentials | `glab auth status` / `gh auth status` |
| an agent CLI | the reviewer | e.g. `claude --version` |

**Pick the forge account first.** Reviews are posted as whoever the forge CLI is logged in as. Use a dedicated bot user with read access to each repo, and log in as it:

```bash
glab auth login                              # gitlab.com
glab auth login --hostname git.example.org   # self-hosted GitLab
gh auth login                                # github.com (or --hostname for Enterprise)
```

**The agent must already work non-interactively** for this user, e.g. `claude -p "say ok"` prints `ok`. Whatever model and provider it uses by default (API key, subscription, a router) is what the reviewer uses, unless you set `agent.model`.

## 2. Install the binary

From a release (recommended):

```bash
curl -fsSL https://raw.githubusercontent.com/ans-4175/mr-reviewbot/main/scripts/install.sh | sh
```

This downloads the archive for your OS/CPU from [GitHub Releases](https://github.com/ans-4175/mr-reviewbot/releases), verifies its SHA-256 against `checksums.txt`, and installs `reviewbot` to `~/.local/bin` (`REVIEWBOT_BIN_DIR` to change it, `REVIEWBOT_VERSION=v0.2.0` to pin).

By hand: download `mr-reviewbot_<os>_<arch>.tar.gz` and `checksums.txt` from the Releases page, check with `shasum -a 256 -c checksums.txt --ignore-missing`, extract, and put `reviewbot` on your `PATH`.

With Go ≥ 1.22: `go install github.com/ans-4175/mr-reviewbot/cmd/reviewbot@latest`.

Check: `reviewbot version`

## 3. Write the config

```bash
reviewbot init --repo your-group/your-project --repo your-group/another-project
```

Useful flags:

| Flag | Default | Meaning |
|---|---|---|
| `--forge gitlab\|github` | `gitlab` | code host |
| `--host` | `gitlab.com` / `github.com` | self-hosted GitLab or GitHub Enterprise |
| `--agent claude\|codex\|opencode\|pi` | `claude` | reviewer |
| `--model` | the agent's own default | reviewer model |
| `--language en\|id` | `en` | comment language |
| `--max-per-run` | `5` | reviews per scheduled run (poll bot) |
| `--poll-interval-minutes` | `30` | minutes between the end of one scheduled run and the next (poll bot) |

The config lands in `~/.config/mr-reviewbot/config.json`; edit it directly afterwards (format in [config.example.json](config.example.json)). The whitelist in `repos` applies to both the poll bot and on-demand reviews: a repo that is not listed is refused.

Check: `reviewbot validate`

## 4. Check the host

```bash
reviewbot doctor --probe
```

Every line must say `ok`: git, the forge login (it prints the account reviews will be posted as), the agent CLI, the state dir, API + git access for each repo, and the agent's isolation (one agent call: it must read the checkout but not a file or symlink beside it).

## 5. Try one review without posting

```bash
reviewbot scan                                   # which MRs would be reviewed, and why the rest are skipped
reviewbot review your-project!12 --dry-run       # GitHub: your-repo#12 or the PR URL
```

The rendered comment is in `~/.local/state/mr-reviewbot/out/<project>!<iid>-<sha>.md`. Read one before going live. Then post one for real (`reviewbot review your-project!12`); running it again must answer `skipped up-to-date`.

---

# A. Poll bot (scheduled)

```bash
reviewbot service install
reviewbot service status
```

- The interval is `poll_interval_minutes` from the config (default 30), counted from the **end** of the previous run, so slow runs never overlap. After changing it, run `reviewbot service install` again. `--interval 15m` overrides the config for one install.
- **macOS:** a LaunchAgent in `~/Library/LaunchAgents/mr-reviewbot.plist`. LaunchAgents only run while this user is logged in to the GUI; keep the machine awake (`sudo pmset -a sleep 0` on an always-on Mac).
- **Linux:** `~/.config/systemd/user/mr-reviewbot.{service,timer}`. To keep it running after logout: `sudo loginctl enable-linger $USER`.
- The scheduler gets the `PATH` you installed it with, so it finds the same forge and agent CLIs. Re-run `service install` if they move.
- `reviewbot service print` shows the files without installing; `reviewbot service uninstall` stops it.

Each run reviews at most `max_per_run` MRs (skips don't count); the rest wait for the next run, oldest first. Logs: `~/.local/state/mr-reviewbot/log/poll.{out,err}.log`, and one line per review with cost and verdict in `log/reviews.jsonl`.

---

# B. On-demand review (chat agent)

Give your chat agent the `review-mr` skill, so "review api!357" in chat runs `reviewbot review`:

```bash
reviewbot skill install <your-agent-skills-dir>     # e.g. ~/.hermes/profiles/<profile>/skills
```

The installed copy calls the binary by absolute path, because agent runtimes often start with a minimal `PATH`. From a source checkout you can symlink instead: `ln -s "$PWD/skills/review-mr" <your-agent-skills-dir>/review-mr`. Re-run `skill install` after upgrading reviewbot.

On-demand reviews skip the `lookback_days` limit and are processed right away (only the requested MR, even when the poll queue has others). They share the queue lock with the poll bot: if a scheduled run is busy, the answer is exit code `3` and the MR stays queued.

Check from the chat agent: ask it to review an MR with `--dry-run`. It should report status, verdict and number of findings.

---

## Operations

| Need | Command |
|---|---|
| What is queued or failing | `reviewbot queue` (shows the last error; after 3 failures on the same sha a job is parked until a new push or a manual `review`) |
| Re-review the same sha | `reviewbot review <MR> --force` |
| Stop reviewing one MR | add the `no-ai-review` label |
| Stop one repo | `"enabled": false` in the config |
| Stop the poll bot | `reviewbot service uninstall` |
| Clear the git cache | `rm -rf ~/.local/state/mr-reviewbot/{mirrors,work}` (rebuilt on demand) |
| Upgrade | re-run the install script; the scheduler picks up the new binary on its next run; re-run `skill install` for chat agents |

## Troubleshooting

- `… login: … 401` → the token expired or belongs to another host: `glab auth login` / `gh auth login` (with `--hostname` if self-hosted).
- `repo …: git ls-remote … Authentication failed` → the account can use the API but not git; check its role on the repo.
- `refusing to review: … isolation check failed` → the agent could read outside the checkout (or could not read at all). Run `reviewbot doctor --probe` for details; check the agent's own settings and version.
- `<agent> failed` / `no valid review JSON` → run the agent by hand as the same user (e.g. `claude -p "say ok"`); check the model or router behind it. The job retries on the next run.
- Exit `3` → another run holds the worker lock; the MR stays queued.
