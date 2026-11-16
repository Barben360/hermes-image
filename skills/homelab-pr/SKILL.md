---
name: homelab-pr
description: Propose a change to the owner's server configuration (Barben360/my-homelab) — an issue, or a PR from your own fork. Use when something about morpheus, its services, your sandbox or your managed settings should change. You can never merge there; the owner reviews and merges.
---

# Propose a change to my-homelab

You have **read** access to https://github.com/Barben360/my-homelab and cannot push to it.
Every change goes through a PR from your fork, merged by the owner; a merged PR is
deployed to morpheus by reconcile within minutes.

Before anything, read its `CLAUDE.md` (conventions, tests, secrets rules) and the
`README.md` section of the service you touch. Never put a secret in a change.

## An issue (a request, a question, a bug)

`gh issue create -R Barben360/my-homelab --title "<emoji> <imperative>" --body "<what, why, evidence>"`

## A PR

1. Fork once: `gh repo fork Barben360/my-homelab --clone=false` (your private fork).
   Clone it to `~/my-homelab` once, with the upstream remote:
   `gh repo clone <you>/my-homelab ~/my-homelab -- --origin fork` then
   `git -C ~/my-homelab remote add upstream https://github.com/Barben360/my-homelab`.
2. Start from upstream: `git fetch upstream && git switch -c <topic> upstream/main`.
3. Change, then run the repo's checks: `go test -mod=vendor ./...`, `go vet ./...`,
   `gofmt -l cmd internal` (empty). If you cannot run Go, say so in the PR.
4. Commit (emoji + imperative subject), `git push fork <topic>`.
5. `gh pr create -R Barben360/my-homelab --head <you>:<topic>` with what, why, how it
   was tested, and the risks. Tell the owner on Matrix with the PR URL.
6. Do not touch `.github/workflows/` (your token cannot push workflow changes). If a push
   to your fork is refused because upstream changed a workflow, tell the owner: they
   delete your fork and you fork again.
