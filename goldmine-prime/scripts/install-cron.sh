#!/bin/bash
#
# Install the every-minute Obsidian sync cron on goldmine-prime, pointing
# at the in-repo script. Removes any previous entry that pointed at the
# orphan /home/jcgregorio/scripts/sync-obsidian.sh location.

SCRIPT_PATH="/home/jcgregorio/myjarvis/goldmine-prime/scripts/sync-obsidian.sh"

chmod +x "$SCRIPT_PATH"

# Strip any prior sync-obsidian.sh entry (in-repo or orphan) and add the canonical one.
(crontab -l 2>/dev/null | grep -Fv "sync-obsidian.sh" ; echo "* * * * * $SCRIPT_PATH") | crontab -

echo "Cron installed: $SCRIPT_PATH (runs every minute)"
crontab -l
