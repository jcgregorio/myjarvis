#!/bin/bash

set -ex

# Configuration
VAULT_DIR="/home/jcgregorio/obsidian"
BACKUP_DIR="/mnt/archive/obsidian_backups"
LOG_FILE="/home/jcgregorio/.obsidian_sync.log"

# Ensure backup directory exists
mkdir -p "$BACKUP_DIR"

# Navigate to vault
cd "$VAULT_DIR" || exit

# 1. Capture the state before pulling
OLD_REV=$(git rev-parse HEAD)

# 2. Pull changes using gh/git
# We use --ff-only to ensure we don't get into merge conflict hell in a cron job
git pull --ff-only origin main >> "$LOG_FILE" 2>&1

# 3. Capture state after pulling
NEW_REV=$(git rev-parse HEAD)

# 4. If the revision changed, sync to the 7.2TB drive
if [ "$OLD_REV" != "$NEW_REV" ]; then
    echo "[$(date)] Change detected ($OLD_REV -> $NEW_REV). Mirroring to archive..." >> "$LOG_FILE"
    
    # rsync -a: archive mode (preserves permissions/times)
    # --delete: removes files in backup that were deleted in the vault
    rsync -a --delete "$VAULT_DIR/" "$BACKUP_DIR/"
    
    echo "[$(date)] Backup complete." >> "$LOG_FILE"
fi