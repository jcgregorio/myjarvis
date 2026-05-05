#!/bin/bash
# Restore the EmergentMethods Wikipedia snapshot into Qdrant.
#
# Pre-requisites:
#   1. download-wiki-snapshot.sh has finished and produced a single .snapshot
#      file in $SNAPSHOT_DIR.
#   2. docker-compose.yaml has a bind mount that exposes $SNAPSHOT_DIR inside
#      the qdrant-unified container at /qdrant/snapshots/$COLLECTION, e.g.
#         volumes:
#           - /home/jcgregorio/qdrant_data:/qdrant/storage:z
#           - /mnt/archive/wiki-snapshot:/qdrant/snapshots/WIKIPEDIA_ENGLISH:z
#      and Qdrant has been restarted so the mount is live.

set -euo pipefail

SNAPSHOT_DIR="${SNAPSHOT_DIR:-/mnt/archive/wiki-snapshot}"
COLLECTION="${COLLECTION:-WIKIPEDIA_ENGLISH}"
QDRANT_URL="${QDRANT_URL:-http://localhost:6333}"
QDRANT_CONTAINER="${QDRANT_CONTAINER:-qdrant-unified}"

echo "==> Locating snapshot file"
shopt -s nullglob
candidates=("$SNAPSHOT_DIR"/*.snapshot)
# Filter out partials in case download is still in flight.
snapshot_file=""
for f in "${candidates[@]}"; do
    [[ "$f" == *.snapshot.part* ]] && continue
    snapshot_file="$f"
    break
done
if [ -z "$snapshot_file" ]; then
    echo "No concatenated *.snapshot file in $SNAPSHOT_DIR" >&2
    echo "Run download-wiki-snapshot.sh first." >&2
    exit 1
fi
snapshot_basename="$(basename "$snapshot_file")"
echo "    $snapshot_file"

echo "==> Verifying mount inside container '$QDRANT_CONTAINER'"
container_path="/qdrant/snapshots/$COLLECTION/$snapshot_basename"
if ! docker exec "$QDRANT_CONTAINER" test -f "$container_path"; then
    cat >&2 <<EOF
Snapshot is not visible inside the container at:
    $container_path

Add this to the qdrant service in goldmine-prime/docker-compose.yaml:

    volumes:
      - /home/jcgregorio/qdrant_data:/qdrant/storage:z
      - $SNAPSHOT_DIR:/qdrant/snapshots/$COLLECTION:z

Then restart: (cd goldmine-prime && docker compose up -d)
EOF
    exit 1
fi

echo "==> Checking Qdrant health at $QDRANT_URL"
curl -fsS "$QDRANT_URL/healthz" >/dev/null

if curl -fsS "$QDRANT_URL/collections/$COLLECTION" >/dev/null 2>&1; then
    echo "WARNING: collection '$COLLECTION' already exists." >&2
    echo "Recovery will overwrite it. Press Ctrl-C within 10s to abort." >&2
    sleep 10
fi

echo "==> Triggering recovery (this can take a long time; do not interrupt)"
# --max-time 0 disables the timeout; recovery of ~380 GB takes hours.
http_status=$(curl -sS -o /tmp/qdrant-restore.json -w '%{http_code}' \
    --max-time 0 \
    -X PUT "$QDRANT_URL/collections/$COLLECTION/snapshots/recover" \
    -H 'Content-Type: application/json' \
    -d "{\"location\":\"file://$container_path\",\"priority\":\"snapshot\"}")

echo "    HTTP $http_status"
cat /tmp/qdrant-restore.json
echo
if [ "$http_status" != "200" ]; then
    echo "Recovery failed." >&2
    exit 1
fi

echo "==> Verifying collection"
curl -sS "$QDRANT_URL/collections/$COLLECTION" | sed 's/,/,\n  /g'
echo

cat <<EOF

==============================================================================
Restore complete.

Free disk by removing the downloaded parts (keep the merged .snapshot if you
want to be able to re-restore without re-downloading):

    rm "$SNAPSHOT_DIR"/*.snapshot.part*

If you also want the merged file gone:

    rm "$snapshot_file"

Sanity check from a quick search:

    curl -sS -X POST "$QDRANT_URL/collections/$COLLECTION/points/search" \\
         -H 'Content-Type: application/json' \\
         -d '{"vector":{"name":"bm25","vector":{"indices":[],"values":[]}},"limit":1}'
==============================================================================
EOF
