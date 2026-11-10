# AGENTS.md

Rules for agents and contributors working on mr-reviewbot. `CLAUDE.md` is a symlink to this file.

## What this is

A Go CLI (stdlib only) that reviews merge requests read-only and posts one comment per new head sha. User docs: [README.md](README.md), install steps: [SETUP.md](SETUP.md).

## Layout

```
cmd/reviewbot/          entry point (adds Homebrew and ~/.local/bin to PATH)
internal/app/           subcommands; setup.go (init, doctor), service.go (launchd/systemd), skill.go
internal/forge/         Forge interface; GitLab via `glab api`, GitHub via `gh api`
internal/agent/         Agent interface; claude, codex, opencode, pi adapters (pi_guard.ts: pi path guard, embedded)
internal/config/        config.json parsing, XDG paths
internal/state/         sha marker, review/skip decision
internal/gitops/        blobless mirror, review range, worktree (symlinks checked out as plain files)
internal/review/        prompts (incl. the recent human discussion), JSON schema, answer validation
internal/render/        comment markdown, i18n (en, id)
internal/queue/         one-file-per-MR queue, flock worker lock, parking after 3 failures
prompts/system.md       reviewer instructions (embedded)
skills/review-mr/       chat-trigger skill (embedded; `reviewbot skill install`)
skills/thirdparty/      pinned third-party skills (embedded, unmodified, see NOTICE)
scripts/install.sh      release installer
```

## Rules

- **Never post from a dev machine.** The forge CLI posts as whoever is logged in. Use `--dry-run` for `poll` and `review` unless you are on the bot host and mean it.
- **Read-only reviewer is the security boundary.** A new agent adapter must: allow only read/search tools, ignore repo-level agent config and hooks, run in the worktree, never get the forge token, and pass `reviewbot doctor --probe` on a real host before it is called supported in SETUP.md. Add an args test like those in `internal/agent/*_test.go`. The app also gates every review on the probe (`ensureIsolated`); never add a way to skip it.
- **A new forge adapter** implements `forge.Forge` and maps its PR/MR into `forge.MR` (`State: "opened"` when open). The marker format `<!-- mr-reviewbot v1 sha=… -->` must not change; existing comments are the only state.
- **Behaviour changes need tests**, especially in `state`, `gitops`, `render`, `queue` and `app` (the end-to-end test in `internal/app/app_test.go` uses a fake forge and agent with a local git remote).
- **New user-facing text goes into every language** in `internal/render/i18n.go`; `TestEveryLanguageCoversEveryKey` enforces the keys.
- **Keep the docs in step.** The agent/forge status table lives in both README.md and SETUP.md; change both, and link the tracking issue for anything not yet verified.
- Keep code comments short (why, not what).
- Third-party skills stay unmodified; update by bumping the pinned commit in `VENDORED.md` and `NOTICE`. Never name a directory `vendor`: Go drops it from module zips and `go install` breaks.

## Checks

```bash
make test     # must pass before every commit
make dist     # cross-compiles all release targets
```
