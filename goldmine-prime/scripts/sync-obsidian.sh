#!/bin/bash
#
# Cron sync for the Obsidian vault on goldmine-prime.
# Pulls the vault git repo, mirrors to /mnt/archive, and reindexes Qdrant
# (obsidian_vault) only when HEAD changed. The indexer's hash-skip means
# unchanged chunks are not re-embedded, so reindex is cheap on small diffs.

set -e

VAULT_DIR="/home/jcgregorio/obsidian"
BACKUP_DIR="/mnt/archive/obsidian_backups"
LOG_FILE="/home/jcgregorio/.obsidian_sync.log"
GOLDMINE_DIR="/home/jcgregorio/myjarvis/goldmine-prime"
INDEXER_PY="$GOLDMINE_DIR/.venv/bin/python $GOLDMINE_DIR/indexer.py"
LOCK="/tmp/sync-obsidian.lock"

exec 9>"$LOCK"
flock -n 9 || exit 0

mkdir -p "$BACKUP_DIR"
cd "$VAULT_DIR"

OLD_REV=$(git rev-parse HEAD)
git pull --ff-only origin main >> "$LOG_FILE" 2>&1
NEW_REV=$(git rev-parse HEAD)

if [ "$OLD_REV" != "$NEW_REV" ]; then
    echo "[$(date)] Change detected ($OLD_REV -> $NEW_REV). Mirroring to archive..." >> "$LOG_FILE"
    rsync -a --delete "$VAULT_DIR/" "$BACKUP_DIR/" >> "$LOG_FILE" 2>&1
    echo "[$(date)] Backup complete. Reindexing Qdrant..." >> "$LOG_FILE"
    cd "$GOLDMINE_DIR"
    $INDEXER_PY >> "$LOG_FILE" 2>&1
    echo "[$(date)] Reindex complete." >> "$LOG_FILE"
fi
