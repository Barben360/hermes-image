# AGENTS.md — rules for Hermes (and any agent) changing this repo

This repo builds the image Hermes runs in. A merged PR deploys automatically to
morpheus: treat every change as a production deploy of yourself.

## You may

- Add a tool to the `Dockerfile` (apt package, or a pinned release checked by sha256)
  and a matching check in `ci/smoke.sh` (the check must fail without your change).
- Bump `HIMALAYA_*` / `MCP_GRAFANA_*` (version + sha256 of the release asset).
- Edit `README.md`, this file's examples, and `skills/` (your own skills).
- Merge your own PR once the `image` check is green (skill `self-merge-image`).

## You may not

- Touch `.github/workflows/` (your token has no Workflows permission: the push fails)
  or the repository settings and rulesets.
- Remove or weaken a check in `ci/smoke.sh`, or `rootfs/etc/cont-init.d/50-homelab-credentials`'s
  rule that a missing token removes `hosts.yml`.
- Add secrets, tokens or credentials to the repo; add `privileged` features, Docker
  socket access, or anything that needs new capabilities: those belong to
  `services/hermes` in my-homelab, through a PR the owner merges.
- Use floating base tags, release candidates or `-desktop` variants.

## Procedure

Branch → change → `ci/smoke.sh` locally if you can build, otherwise rely on CI →
PR with what and why → `gh pr checks --watch` → merge only when green
(`gh pr merge <n> --squash`) → report on Matrix → after the restart, confirm
`$HERMES_IMAGE_REVISION` equals the merge commit. On red CI: report and stop; never
force, never retry by weakening the test.
