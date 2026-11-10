# Setup

Every step is a plain command, so a person or a coding agent can follow this file top to bottom. Each step says how to check it worked.

Supported today: **GitLab** (gitlab.com or self-hosted) + **Claude Code** on **macOS or Linux**.

## 1. Prerequisites on the host

| Tool | Why | Check |
|---|---|---|
| `git` | blobless mirror + worktree per MR | `git --version` |
| `glab` | GitLab API and git credentials | `glab auth status` |
| `claude` | the reviewer | `claude --version` |

**Pick the GitLab account first.** Reviews are posted as whoever `glab` is logged in as. Use a dedicated bot user with at least Reporter access to each repo (Developer if you want it to see protected refs), and log in as it:

```bash
glab auth login                          # gitlab.com
glab auth login --hostname git.example.org   # self-hosted
```

**Claude must already work non-interactively** for this user: `claude -p "say ok"` should print `ok`. Whatever model and provider it uses by default (API key, subscription, a router) is what the reviewer uses, unless you set `agent.model`.

## 2. Install the binary

From a release:

```bash
curl -fsSL https://raw.githubusercontent.com/<owner>/mr-reviewbot/main/scripts/install.sh | REVIEWBOT_REPO=<owner>/mr-reviewbot sh
```

This downloads the archive for your OS/CPU, verifies its SHA-256 against `checksums.txt`, and installs `reviewbot` to `~/.local/bin` (`REVIEWBOT_BIN_DIR` to change it, `REVIEWBOT_VERSION=v0.1.0` to pin).

Or from source (Go ≥ 1.22):

```bash
git clone <repo-url> mr-reviewbot && cd mr-reviewbot
make build && install -m 0755 bin/reviewbot ~/.local/bin/
```

Check: `reviewbot version`

## 3. Write the config

```bash
reviewbot init \
  --repo your-group/your-project \
  --repo your-group/another-project
```

Useful flags: `--host git.example.org` (self-hosted GitLab), `--language id` (Indonesian comments), `--model <name>` (otherwise the agent's default), `--max-per-run 5`. The config lands in `~/.config/mr-reviewbot/config.json`; edit it directly afterwards (format in [config.example.json](config.example.json)).

Check: `reviewbot validate`

## 4. Check the host

```bash
reviewbot doctor
```

Every line must say `ok`: git, the glab login (it prints the account reviews will be posted as), the agent CLI, the state dir, and API + git access for each repo.

Then prove the reviewer cannot read outside its checkout (one agent call):

```bash
reviewbot doctor --probe
```

## 5. Try it without posting

```bash
reviewbot scan                                   # which MRs would be reviewed, and why the rest are skipped
reviewbot review your-project!12 --dry-run       # full review, nothing posted
```

The rendered comment is in `~/.local/state/mr-reviewbot/out/<project>!<iid>-<sha>.md`. Read one before going live.

## 6. Post one real review

```bash
reviewbot review your-project!12
```

Check the comment on the MR. Running the same command again must answer `skipped up-to-date`.

## 7. Schedule it

```bash
reviewbot service install            # every 30 minutes, counted from the end of the previous run
reviewbot service status
```

- **macOS:** a LaunchAgent in `~/Library/LaunchAgents/mr-reviewbot.plist`. LaunchAgents only run while this user is logged in to the GUI; keep the machine awake (`sudo pmset -a sleep 0` on an always-on Mac).
- **Linux:** `~/.config/systemd/user/mr-reviewbot.{service,timer}`. To keep it running after logout: `sudo loginctl enable-linger $USER`.
- The scheduler gets the `PATH` you installed it with, so it finds the same `glab` and `claude`. Re-run `service install` if they move.
- `--interval 15m` to change the cadence, `reviewbot service print` to see the files without installing, `reviewbot service uninstall` to stop.

Logs: `~/.local/state/mr-reviewbot/log/poll.{out,err}.log`, one line per review with cost and verdict in `log/reviews.jsonl`.

## 8. Optional: trigger from chat

Give your chat agent the `review-mr` skill so "review api!357" in chat runs `reviewbot review`:

```bash
reviewbot skill install <your-agent-skills-dir>          # e.g. ~/.hermes/profiles/<profile>/skills
# or, from a checkout: ln -s "$PWD/skills/review-mr" <your-agent-skills-dir>/review-mr
```

The installed copy calls the binary by absolute path, because agent runtimes often start with a minimal `PATH`.

## Operations

| Need | Command |
|---|---|
| What is queued or failing | `reviewbot queue` (shows the last error; after 3 failures on the same sha a job is parked until a new push or a manual `review`) |
| Re-review the same sha | `reviewbot review <MR> --force` |
| Stop reviewing one MR | add the `no-ai-review` label |
| Stop one repo | `"enabled": false` in the config |
| Stop everything | `reviewbot service uninstall` |
| Clear the git cache | `rm -rf ~/.local/state/mr-reviewbot/{mirrors,work}` (rebuilt on demand) |
| Upgrade | re-run the install script; the scheduler picks up the new binary on its next run |

## Troubleshooting

- `glab login: … 401` → the token expired or belongs to another host: `glab auth login [--hostname …]`.
- `repo …: git ls-remote … Authentication failed` → the account can use the API but not git; check its role on the repo.
- `claude failed` / `no valid review JSON` → run `claude -p "say ok"` as the same user; check the model or router behind it. The job retries on the next run.
- Exit `3` → another run holds the worker lock; the MR stays queued.
