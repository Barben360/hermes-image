#!/bin/sh
# Cron job morning-brief (skills/morning-brief): its stdout is the agent turn's data.
exec /usr/local/bin/morning-brief run --config /opt/data/morning-brief/config.yaml
