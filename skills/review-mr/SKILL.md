---
name: review-mr
description: Trigger a read-only code review of one merge request from the whitelisted repos via the reviewbot CLI. Use when someone asks to review, re-review, or check an MR/PR by URL or group/project!iid, e.g. from chat.
---

# Review MR (reviewbot)

Runs `reviewbot` for one merge request. The reviewer only reads code (no test/lint/build) and posts one comment on the MR with a verdict, findings, and a prompt for the next iteration.

## Steps

1. Take the MR reference from the request. Accepted: the MR URL, `group/project!iid`, or a short repo name such as `api!357`. A bare id (`56`) is rejected when more than one repo is whitelisted; ask which repo.
2. Run:
   ```bash
   reviewbot review <MR> --json
   ```
   - Add `--force` only when asked to re-review the same commit.
   - Add `--dry-run` when asked to "try first" or not to post.
3. Read the JSON result and reply briefly: status (`posted` / `skipped` / `failed` / `dry-run`), verdict, number of findings, and the reason when `skipped`.

## Rules

- Only whitelisted repos are reviewed (see `reviewbot validate` for the config path). Exit code `2` with "not a whitelisted repo" or "ambiguous" means the repo was refused or unclear: pass the message on as is. Do not edit the whitelist unless its owner asks.
- Common `skipped` reasons: `up-to-date` (this commit was already reviewed), `draft`, `skip-label:no-ai-review`, `not-open`. Offer `--force` only for `up-to-date`.
- Exit code `3` means another worker is running. The MR stays queued and will be processed; do not retry.
- Do not paste the whole review comment into chat: give a summary and the MR link.
