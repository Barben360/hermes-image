---
name: workspace-journal
description: Version your own state (skills, SOUL.md, user config.yaml) in the private repo Barben360/hermes-workspace. Run after every change you make to yourself, and daily at 05:00 Europe/Paris.
---

# Workspace journal

Keeps a reviewable history of how you change yourself. Never commit secrets.

1. Clone once: `gh repo clone Barben360/hermes-workspace ~/hermes-workspace`; then
   `git -C ~/hermes-workspace pull --ff-only`.
2. Copy into it (replace, mirror deletions): `$HERMES_HOME/skills/` → `skills/`,
   `$HERMES_HOME/SOUL.md` → `SOUL.md`, `$HERMES_HOME/config.yaml` → `config.yaml`.
3. Never copy `.env`, `auth.json`, `memories/`, `sessions/`, `state.db*`,
   `platforms/` (Matrix E2EE keys), `home/.config/gh/`, `recovery-key*`, or any file
   holding a token. Before committing, check the staged diff for secrets
   (`token`, `key`, `password`, `ghp_`, `github_pat_`, `sk-`); if any, unstage and
   tell the owner.
4. If there are changes: commit "📝 Journal <date>: <one-line summary>" and push to
   `main`. Nothing changed: do nothing.
5. Schedule: a daily cron job at 05:00 Europe/Paris (the nightly backup stops you
   around 03:00 UTC). Create it once if it does not exist.
