#!/usr/bin/env bash
#
# bench-synth.sh — sweep RAG answer-synthesis quality (synthesis_test.go
# / TestSynthesis) across many Ollama models. For each model it runs the
# 6 Wikipedia synthesis cases through the real AnswerFromWikipedia path
# (retrieval held constant), scores facts/clean/attrib/nonempty + latency
# from the machine-readable SYNTH_CSV line, and appends every answer
# verbatim to bench-synth-answers.md for qualitative review.
#
# Unlike routing, synthesis is a plain chat call (no tool support
# needed), so the model set here is broader (gemma3, phi3/4, mistral,
# llama all included).
#
# Usage:
#   scripts/bench-synth.sh                 # full sweep
#   DRY_RUN=1 scripts/bench-synth.sh       # plan + estimate only
#   AUTO_PULL=0 scripts/bench-synth.sh     # skip models not pulled
#   FORCE=1 scripts/bench-synth.sh         # re-run rows already in CSV
#
# Safe to interrupt/re-run: incremental CSV + resume. A pidfile is
# written so an external watcher can wait on this exact process without
# the pgrep-self-match foot-gun.

set -uo pipefail

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OLLAMA_URL="${OLLAMA_URL:-http://192.168.1.145:11434/v1}"
RAG_URL="${RAG_URL:-http://192.168.1.145:8011}"
OLLAMA_SSH="${OLLAMA_SSH:-jcgregorio@192.168.1.145}"
RESULTS="${RESULTS:-$REPO_DIR/bench-synth-results.csv}"
ANSWERS="${ANSWERS:-$REPO_DIR/bench-synth-answers.md}"
PIDFILE="${PIDFILE:-$REPO_DIR/.bench-synth.pid}"
GO_TIMEOUT="${GO_TIMEOUT:-30m}"
WALL_TIMEOUT="${WALL_TIMEOUT:-1800}"
AUTO_PULL="${AUTO_PULL:-1}"
DRY_RUN="${DRY_RUN:-0}"
FORCE="${FORCE:-0}"

export CGO_ENABLED=1
export CGO_CFLAGS="-I/usr/include/onnxruntime"
export CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime"
export LD_LIBRARY_PATH="/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export OLLAMA_URL RAG_URL

# Chat-capable models (no tool support required for synthesis). The
# pathologically slow giants (qwen3.6:latest ~27s/call, gemma-4-26B) are
# intentionally excluded — not serious prod synthesizers, and they would
# dominate sweep time. Edit freely.
MODELS=(
  granite4:latest          # current prod — the model under scrutiny
  granite3.3:8b
  qwen3.5:9b
  qwen3.5:9b-64k
  qwen3:8b
  qwen3:14b-64k            # prior prod — quality baseline
  qwen3:4b
  gemma4:latest
  gemma4:16k
  gemma3:4b
  gemma3:12b
  nemotron-3-nano:4b
  mistral:7b
  phi4-mini:latest
  phi4:latest
  phi3:latest
  llama3.1:8b
)

HDR="model,cases,facts_pct,clean_pct,attrib_pct,nonempty_pct,p50_ms,mean_ms"

log()  { printf '\033[1;36m[synth]\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m[synth]\033[0m %s\n' "$*" >&2; }

INSTALLED=""
refresh_installed() { INSTALLED="$(ssh "$OLLAMA_SSH" 'ollama list' 2>/dev/null | awk 'NR>1{print $1}')"; }
have_model() { grep -qxF "$1" <<<"$INSTALLED"; }

pull_model() {
  log "pulling $1 ..."
  if ssh "$OLLAMA_SSH" "ollama pull '$1'" >&2 2>&1; then INSTALLED+=$'\n'"$1"; return 0; fi
  warn "pull failed for $1 — skipping"; return 1
}

already_done() {
  [[ -f "$RESULTS" ]] || return 1
  grep -qE "^$(sed 's/[][\.*^$/]/\\&/g' <<<"$1")," "$RESULTS"
}

run_one() {
  local tag="$1"
  if [[ $FORCE != 1 ]] && already_done "$tag"; then log "skip (already in CSV): $tag"; return 0; fi

  local logf; logf="$(mktemp)"
  log "running synthesis: $tag"
  local t0 t1; t0=$(date +%s)
  ( cd "$REPO_DIR" && \
    timeout "$WALL_TIMEOUT" env MODEL="$tag" SYNTH_ANSWERS_FILE="$ANSWERS" \
      go test -run TestSynthesis -count=1 -v -timeout "$GO_TIMEOUT" . \
  ) >"$logf" 2>&1
  local rc=$?; t1=$(date +%s)

  local csv; csv="$(grep -ho 'SYNTH_CSV:.*' "$logf" | tail -1)"; csv="${csv#SYNTH_CSV:}"
  if [[ -z "$csv" ]]; then
    if [[ $rc == 124 ]]; then warn "TIMED OUT: $tag"; echo "$tag,,TIMEOUT,,,,," >>"$RESULTS"
    else warn "no result (rc=$rc): $tag — $logf"; tail -5 "$logf" >&2; echo "$tag,,ERROR,,,,," >>"$RESULTS"; fi
    return 1
  fi
  echo "$csv" >>"$RESULTS"
  log "done in $((t1-t0))s: $csv"
  rm -f "$logf"
}

refresh_installed
runs=0
log "Plan:"
for tag in "${MODELS[@]}"; do
  if have_model "$tag"; then st="present"; runs=$((runs+1))
  elif [[ $AUTO_PULL == 1 ]]; then st="MISSING (will pull)"; runs=$((runs+1))
  else st="MISSING (will skip)"; fi
  printf '  %-24s %s\n' "$tag" "$st" >&2
done
log "~$runs runs, 6 cases each. Rough estimate ~$((runs*3)) min (granite4 fast, thinking models slower)."
log "CSV: $RESULTS   Answers: $ANSWERS"
[[ $DRY_RUN == 1 ]] && { log "DRY_RUN=1 — not executing."; exit 0; }

echo $$ > "$PIDFILE"
trap 'rm -f "$PIDFILE"' EXIT

[[ -f "$RESULTS" ]] || echo "$HDR" >"$RESULTS"
[[ -f "$ANSWERS" ]] || printf '# RAG synthesis answers (bench-synth)\n' >"$ANSWERS"

for tag in "${MODELS[@]}"; do
  if ! have_model "$tag"; then
    if [[ $AUTO_PULL == 1 ]]; then pull_model "$tag" || continue
    else warn "skip (not pulled): $tag"; continue; fi
  fi
  run_one "$tag"
done

log "Sweep complete. Ranked by facts%% (desc), then mean latency (asc):"
{
  echo "$HDR"
  tail -n +2 "$RESULTS" | awk -F, '$3 ~ /^[0-9]+$/' | sort -t, -k3,3nr -k8,8n
  tail -n +2 "$RESULTS" | awk -F, '$3 !~ /^[0-9]+$/'
} | column -t -s, | sed 's/^/  /' >&2
log "Raw CSV: $RESULTS   |   Answers for qualitative review: $ANSWERS"
