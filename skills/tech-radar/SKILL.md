---
name: tech-radar
description: Daily Tech Radar digest written to the owner's Obsidian vault by a no-agent cron script (deepseek-flash). Read before touching the tech-radar cron job, its script or its note.
---

# Tech Radar

Every day at 07:00 Europe/Paris, cron job `tech-radar` runs `tech-radar/tech_radar.py`
(installed from this image into `$HERMES_HOME/scripts/tech-radar/` at every start) with
`no_agent: true`: no agent turn, its stdout is posted to Matrix as is.

What the script does: reads `Tech/Tech Radar/interests.md` in `/opt/vault` (the owner's,
never edit it unless asked), the feeds of `feeds.json`, drops links sent on an earlier day
(`/opt/data/tech-radar/seen.json`, 14 days), asks `deepseek-flash` once to pick the 10 most
relevant distinct stories, fetches and summarizes them, writes
`Tech/Tech Radar/Dailies/YYYY/MM/YYYY-MM-DD.md` and prints one line: `✅ …`, `ℹ️ No new
tech news…` or `❌ Error generating tech radar (<step>: <reason>)`.

- Its DeepSeek key is the file `/run/secrets/tech_radar_deepseek_key` (my-homelab,
  Proton Pass `tech-radar/deepseek-api-key`). Never print, copy or log it.
- Run it by hand: `hermes cron run <id>` (or
  `/opt/hermes/.venv/bin/python $HERMES_HOME/scripts/tech-radar/tech_radar.py`). A rerun the
  same day rebuilds the day's note.
- Never turn the job into an agent job or change its model: the whole point is zero agent
  tokens. Changes to the script, feeds or prompts go through a PR in hermes-image
  (`skills/tech-radar/`, tests in `tests/`); the copy in the volume is overwritten at start.
- Create (or recreate) the job, once, from a shell in the container (`cd /opt/data` first);
  check the flags with `hermes cron create --help`:

  ```sh
  hermes cron create '0 7 * * *' --name tech-radar --script tech-radar/tech_radar.py --no-agent \
    --interpreter /opt/hermes/.venv/bin/python --timezone Europe/Paris --catch-up on \
    --deliver 'matrix:!PD7fJIs5meRecg5sKAMY7Mpg1uNizVELoglwQeqMn5w'
  ```

  (`💬 Général`). Then check its record in `$HERMES_HOME/cron/jobs.json`: `no_agent: true`,
  `script: tech-radar/tech_radar.py`, no prompt, no skill, no model.
- A run that never happens (Hermes down past the catch-up window, job paused) sends nothing:
  if the owner says the morning line is missing, check `hermes cron list` and the job's last run.
