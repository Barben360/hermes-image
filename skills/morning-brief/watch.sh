#!/bin/sh
# Cron job morning-brief-watch (no agent): one line when today's brief was not delivered, else nothing.
exec /usr/local/bin/morning-brief watch --job morning-brief
