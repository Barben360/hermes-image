---
name: homelab-pr
description: Propose a change to the owner's server configuration (Barben360/my-homelab) — an issue, or a PR from a hermes/* branch. Use when something about morpheus, its services, your sandbox or your managed settings should change. Never push to main and never merge there; the owner reviews and merges.
---

# Propose a change to my-homelab

Your GitHub token is the owner's: GitHub lets you push anywhere in
https://github.com/Barben360/my-homelab, including `main`. Pushing to `main` deploys
straight to morpheus as root within two minutes, without review. **You never push to
`main`, never merge, never approve, never close the owner's PRs.** A Hermes-authored
commit landing on `main` raises an alert to the owner.

Your work is told apart by convention: commits authored `Hermes <hermes@bbarrois.tech>`
and SSH-signed (set up for you at every start: never change `user.*` or the signing
settings), branches `hermes/<topic>`, PR titles `🤖 Hermes: <emoji> <imperative>`,
label `hermes`.

Before anything, read the repo's `CLAUDE.md` (conventions, tests, secrets rules) and the
`README.md` section of the service you touch. Never put a secret in a change.

## An issue (a request, a question, a bug)

`gh issue create -R Barben360/my-homelab --label hermes --title "🤖 Hermes: <emoji> <imperative>" --body "<what, why, evidence>"`

## A PR

1. Clone once: `gh repo clone Barben360/my-homelab ~/my-homelab`; then
   `git -C ~/my-homelab fetch origin`.
2. Branch from the latest main: `git switch -c hermes/<topic> origin/main`.
3. Change, then run the repo's checks: `go test -mod=vendor ./...`, `go vet ./...`,
   `gofmt -l cmd internal` (empty). If you cannot run Go, say so in the PR.
4. Commit (emoji + imperative subject; signed automatically), then
   `git push -u origin hermes/<topic>` — never any other branch name.
5. `gh pr create -R Barben360/my-homelab --label hermes --title "🤖 Hermes: …"` with what,
   why, how it was tested, and the risks. Tell the owner on Matrix with the PR URL.
6. Do not touch `.github/workflows/` (the token cannot change workflows).
