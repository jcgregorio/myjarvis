#!/usr/bin/env bash
#
# bench-models.sh — sweep the routing test suite (routing_test.go /
# TestRouting) across many Ollama models, sizes and quantizations and
# collect accuracy + latency into a CSV, then print a sorted table.
#
# It drives the existing Go harness; for each model it runs the 33-prompt
# routing suite and parses the machine-readable `ROUTING_CSV:` line the
# test emits. Qwen-family models are run twice (with and without
# chain-of-thought via the /no_think soft switch). Other families are
# run once in their native mode — the /no_think token is only actually
# honored by Qwen3/3.5/3.6, so "without thinking" is recorded for the
# reasoning-toggle families (Nemotron, Granite) but may be a no-op there;
# that caveat is intentional ("with and without thinking as possible").
#
# Usage:
#   scripts/bench-models.sh                 # run the full sweep
#   DRY_RUN=1 scripts/bench-models.sh       # print the plan + estimate only
#   AUTO_PULL=0 scripts/bench-models.sh     # skip models not already pulled
#   FORCE=1 scripts/bench-models.sh         # re-run rows already in the CSV
#   RESULTS=foo.csv scripts/bench-models.sh # custom output file
#
# Safe to interrupt and re-run: results are written incrementally and
# already-completed (model,no_think) rows are skipped unless FORCE=1.

set -uo pipefail

# ----------------------------------------------------------------------
# Config (env-overridable)
# ----------------------------------------------------------------------
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OLLAMA_URL="${OLLAMA_URL:-http://goldmine-prime:11434/v1}"
OLLAMA_SSH="${OLLAMA_SSH:-jcgregorio@goldmine-prime}"   # for `ollama list`/`pull`
RESULTS="${RESULTS:-$REPO_DIR/bench-results.csv}"
GO_TIMEOUT="${GO_TIMEOUT:-30m}"     # passed to `go test -timeout`
WALL_TIMEOUT="${WALL_TIMEOUT:-2400}" # hard wall-clock cap per run (sec)
AUTO_PULL="${AUTO_PULL:-1}"
DRY_RUN="${DRY_RUN:-0}"
FORCE="${FORCE:-0}"

# onnxruntime build env — the `main` package pulls in CGO via vad.go, so
# the test binary needs these to compile (mirrors `make test-routing`).
export CGO_ENABLED=1
export CGO_CFLAGS="-I/usr/include/onnxruntime"
export CGO_LDFLAGS="-L/usr/lib/x86_64-linux-gnu -lonnxruntime"
export LD_LIBRARY_PATH="/usr/lib/x86_64-linux-gnu${LD_LIBRARY_PATH:+:$LD_LIBRARY_PATH}"
export OLLAMA_URL

# ----------------------------------------------------------------------
# Model matrix.  Format: "tag|mode"
#   mode = both    -> run default (thinking) AND /no_think  (Qwen hybrid)
#   mode = think   -> single run in the model's native mode (non-thinking
#                     families: Gemma, Mistral/Ministral, base Phi)
#   mode = nothink -> single run with /no_think only
# Tags not already pulled are pulled automatically (AUTO_PULL=1); any tag
# that fails to pull is logged and skipped, so best-guess tags are safe.
# Edit freely.
# ----------------------------------------------------------------------
MODELS=(
  # --- Qwen (hybrid thinking → both) : sizes + context + quant spread ---
  "qwen3:4b|both"
  "qwen3:8b|both"
  "qwen3:8b-128k|both"
  "qwen3:14b-64k|both"
  "qwen3.5:9b|both"
  "qwen3.5:9b-64k|both"
  "qwen3.6:latest|both"
  "qwen3:8b-q8_0|both"            # higher-precision quant (pull)
  "qwen3:14b-q4_K_M|both"         # explicit quant (pull)

  # --- Gemma 4 / 3 (no thinking → single) : sizes + quant ---
  "gemma4:latest|think"
  "gemma4:16k|think"
  "hf.co/unsloth/gemma-4-26B-A4B-it-GGUF:UD-Q4_K_M|think"
  "gemma3:4b|think"
  "gemma3:12b|think"

  # --- Nemotron (reasoning toggle → both; /no_think may be a no-op) ---
  "nemotron-3-nano:4b|both"
  "nemotron-mini:latest|both"     # pull (tag best-guess)

  # --- Granite 4.1 (reasoning toggle → both; pull, tags best-guess) ---
  "granite4.1:latest|both"
  "granite4:latest|both"
  "granite3.3:8b|both"

  # --- Ministral / Mistral (no thinking → single) ---
  "ministral:8b|think"            # pull (tag best-guess)
  "ministral-3b:latest|think"     # pull (tag best-guess)
  "mistral:7b|think"

  # --- Phi (base = single; reasoning variant always thinks) ---
  "phi4-mini:latest|think"
  "phi4:latest|think"             # pull
  "phi4-reasoning:latest|think"   # pull; always reasons (cannot disable)
  "phi3:latest|think"             # pull
)

# ----------------------------------------------------------------------
HDR="model,no_think,pass,total,accuracy_pct,min_ms,p50_ms,p95_ms,max_ms,mean_ms"

log() { printf '\033[1;36m[bench]\033[0m %s\n' "$*" >&2; }
warn() { printf '\033[1;33m[bench]\033[0m %s\n' "$*" >&2; }

# Cache of pulled models (one ssh round-trip).
INSTALLED=""
refresh_installed() {
  INSTALLED="$(ssh "$OLLAMA_SSH" 'ollama list' 2>/dev/null | awk 'NR>1{print $1}')"
}
have_model() { grep -qxF "$1" <<<"$INSTALLED"; }

pull_model() {
  local tag="$1"
  log "pulling $tag (this can take a while) ..."
  if ssh "$OLLAMA_SSH" "ollama pull '$tag'" >&2 2>&1; then
    INSTALLED+=$'\n'"$tag"
    return 0
  fi
  warn "pull failed for $tag — skipping"
  return 1
}

already_done() {  # model , no_think
  [[ -f "$RESULTS" ]] || return 1
  grep -qE "^$(sed 's/[][\.*^$/]/\\&/g' <<<"$1"),$2," "$RESULTS"
}

# run_one <tag> <nothink 0|1>
run_one() {
  local tag="$1" nothink="$2" label
  label="$tag (think=$([[ $nothink == 1 ]] && echo off || echo on))"

  if [[ $FORCE != 1 ]] && already_done "$tag" "$nothink"; then
    log "skip (already in CSV): $label"
    return 0
  fi

  local logf; logf="$(mktemp)"
  local env_nt=()
  [[ $nothink == 1 ]] && env_nt=(MYJARVIS_NOTHINK=1)

  log "running: $label"
  local t0 t1
  t0=$(date +%s)
  ( cd "$REPO_DIR" && \
    timeout "$WALL_TIMEOUT" env MODEL="$tag" "${env_nt[@]}" \
      go test -run TestRouting -count=1 -v -timeout "$GO_TIMEOUT" . \
  ) >"$logf" 2>&1
  local rc=$?
  t1=$(date +%s)

  local csv
  csv="$(grep -ho 'ROUTING_CSV:.*' "$logf" | tail -1)"
  csv="${csv#ROUTING_CSV:}"

  if [[ -z "$csv" ]]; then
    if [[ $rc == 124 ]]; then
      warn "TIMED OUT after ${WALL_TIMEOUT}s: $label"
      echo "$tag,$nothink,,,TIMEOUT,,,,," >>"$RESULTS"
    else
      warn "no result parsed (rc=$rc): $label — see $logf"
      tail -5 "$logf" >&2
      echo "$tag,$nothink,,,ERROR,,,,," >>"$RESULTS"
    fi
    return 1
  fi

  echo "$csv" >>"$RESULTS"
  log "done in $((t1 - t0))s: $csv"
  rm -f "$logf"
}

# ----------------------------------------------------------------------
# Plan / dry-run
# ----------------------------------------------------------------------
refresh_installed

runs=0
declare -a PLAN
for entry in "${MODELS[@]}"; do
  tag="${entry%%|*}"; mode="${entry##*|}"
  case "$mode" in
    both)    modes=(0 1) ;;
    nothink) modes=(1) ;;
    *)       modes=(0) ;;
  esac
  if have_model "$tag"; then state="present"; else state="MISSING$([[ $AUTO_PULL == 1 ]] && echo ' (will pull)' || echo ' (will skip)')"; fi
  PLAN+=("$(printf '  %-48s %-8s %s' "$tag" "$mode" "$state")")
  if have_model "$tag" || [[ $AUTO_PULL == 1 ]]; then
    runs=$((runs + ${#modes[@]}))
  fi
done

log "Plan ($(echo "${MODELS[@]}" | wc -w) model entries, ~$runs test runs):"
printf '%s\n' "${PLAN[@]}" >&2
log "Rough time estimate: ~$((runs * 3)) min of test runs (plus model pulls/cold-loads)."
log "Results CSV: $RESULTS"

if [[ $DRY_RUN == 1 ]]; then
  log "DRY_RUN=1 — not executing. Re-run without DRY_RUN to start the sweep."
  exit 0
fi

# ----------------------------------------------------------------------
# Execute
# ----------------------------------------------------------------------
[[ -f "$RESULTS" ]] || echo "$HDR" >"$RESULTS"

for entry in "${MODELS[@]}"; do
  tag="${entry%%|*}"; mode="${entry##*|}"

  if ! have_model "$tag"; then
    if [[ $AUTO_PULL == 1 ]]; then
      pull_model "$tag" || continue
    else
      warn "skip (not pulled, AUTO_PULL=0): $tag"
      continue
    fi
  fi

  case "$mode" in
    both)    modes=(0 1) ;;
    nothink) modes=(1) ;;
    *)       modes=(0) ;;
  esac
  for nt in "${modes[@]}"; do
    run_one "$tag" "$nt"
  done
done

# ----------------------------------------------------------------------
# Final sorted report (by mean latency, ascending; errors/timeouts last)
# ----------------------------------------------------------------------
log "Sweep complete. Ranked by mean routing latency (errors/timeouts last):"
{
  echo "$HDR"
  # Numeric-mean rows sorted ascending, then non-numeric (ERROR/TIMEOUT).
  tail -n +2 "$RESULTS" | awk -F, '$10 ~ /^[0-9]+$/' | sort -t, -k10,10n
  tail -n +2 "$RESULTS" | awk -F, '$10 !~ /^[0-9]+$/'
} | column -t -s, | sed 's/^/  /' >&2

log "Raw CSV at: $RESULTS"
