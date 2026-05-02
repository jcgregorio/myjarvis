#!/bin/bash

SCRIPT_PATH="/home/jcgregorio/scripts/sync-obsidian.sh"

# 1. Make the sync script executable
chmod +x "$SCRIPT_PATH"

# 2. Add to crontab if it doesn't already exist
# This runs once per minute (* * * * *)
(crontab -l 2>/dev/null | grep -Fv "$SCRIPT_PATH" ; echo "* * * * * $SCRIPT_PATH") | crontab -

echo "Cron job installed. Obsidian will sync and backup every minute."