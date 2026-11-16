---
name: self-merge-image
description: Change your own Docker image (Barben360/hermes-image) — add a tool, bump a pinned release — and deploy it by merging your own PR once CI is green. Use when you need a binary or package that is not in your image.
---

# Self-merge an image change

Your image is built from https://github.com/Barben360/hermes-image. A merged PR on
`main` is pushed to `ghcr.io/barben360/hermes-agent:latest`, and morpheus redeploys
you within a few minutes: you will be restarted.

1. Read `AGENTS.md` in the repo: what you may and may not change.
2. Clone or update `~/hermes-image` (`gh repo clone Barben360/hermes-image` once,
   then `git pull`), create a branch `hermes/<short-topic>`.
   Use the same conventions as everywhere: branch `hermes/<topic>`, PR title
   `🤖 Hermes: …`; your commits are signed as Hermes automatically.
3. Make the change (Dockerfile layer + a check in `ci/smoke.sh` that fails without it).
4. Commit (emoji + imperative subject), push, `gh pr create` with what and why.
5. `gh pr checks <n> --watch`. Red: comment the failure on the PR, tell the owner on
   Matrix, stop. Never weaken a check to get green.
6. Green: before merging, write to memory "pending self-update: PR <n>, commit <sha>".
   Then `gh pr merge <n> --squash` and tell the owner on Matrix what is deploying.
7. After your restart: if `$HERMES_IMAGE_REVISION` equals the merge commit
   (`gh pr view <n> --json mergeCommit -q .mergeCommit.oid`), report success on
   Matrix and clear the memory note; if you have not restarted 30 minutes after the
   merge, or the revision differs, report it to the owner (deploy may have failed:
   alert `deploy:hermes`).
