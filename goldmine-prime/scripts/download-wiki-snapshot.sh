#!/bin/bash
# Download EmergentMethods/en_qdrant_wikipedia and restore it into the local
# Qdrant instance.
#
# Sizes: ~367 GB download, ~380 GB after concat (so plan ~750 GB free during
# the run, then delete the .part files).
#
# Requires: hf CLI (huggingface_hub), curl, and a running Qdrant >=1.15 at
# $QDRANT_URL.

set -euo pipefail

SNAPSHOT_DIR="${SNAPSHOT_DIR:-/mnt/archive/wiki-snapshot}"
COLLECTION="${COLLECTION:-WIKIPEDIA_ENGLISH}"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"
HF_REPO="EmergentMethods/en_qdrant_wikipedia"

mkdir -p "$SNAPSHOT_DIR"

echo "==> Downloading $HF_REPO -> $SNAPSHOT_DIR"
hf download "$HF_REPO" \
    --repo-type dataset \
    --local-dir "$SNAPSHOT_DIR"

echo "==> Locating snapshot parts"
shopt -s nullglob
parts=("$SNAPSHOT_DIR"/*.snapshot.part*)
if [ ${#parts[@]} -eq 0 ]; then
    echo "No *.snapshot.part* files in $SNAPSHOT_DIR" >&2
    exit 1
fi

# Sort lexicographically so .part00, .part01, ... concat in the right order.
IFS=$'\n' parts=($(printf '%s\n' "${parts[@]}" | sort))
unset IFS

first_part="${parts[0]}"
snapshot_file="${first_part%.part*}"
snapshot_basename="$(basename "$snapshot_file")"

if [ -f "$snapshot_file" ]; then
    echo "==> Already concatenated: $snapshot_file"
else
    echo "==> Concatenating ${#parts[@]} parts -> $snapshot_file"
    cat "${parts[@]}" > "$snapshot_file"
fi

ls -lh "$snapshot_file"

cat <<EOF

==============================================================================
Snapshot is ready at:
    $snapshot_file

Uploading ~380 GB through the HTTP snapshot-upload endpoint is impractical, so
restore via Qdrant's file:// recovery URL instead. That requires the snapshot
to be visible inside the qdrant-unified container.

1. Edit goldmine-prime/docker-compose.yaml and add a second volume mount under
   the qdrant service:

       volumes:
         - /home/jcgregorio/qdrant_data:/qdrant/storage:z
         - $SNAPSHOT_DIR:/qdrant/snapshots/$COLLECTION:z

2. Restart Qdrant:

       (cd $(dirname "$(realpath "$0")")/.. && docker compose up -d)

3. Trigger the restore (this blocks until done; can take a long while):

       curl -X PUT "$QDRANT_URL/collections/$COLLECTION/snapshots/recover" \\
            -H 'Content-Type: application/json' \\
            -d '{"location":"file:///qdrant/snapshots/$COLLECTION/$snapshot_basename"}'

4. Verify:

       curl -s "$QDRANT_URL/collections/$COLLECTION" | jq .

5. Once you've confirmed the collection is healthy, free disk by removing the
   downloaded parts:

       rm "$SNAPSHOT_DIR"/*.snapshot.part*
==============================================================================
EOF
