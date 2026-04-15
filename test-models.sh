#!/bin/bash
#
# Test multiple Ollama models with OpenCode.
# Pulls models, creates context-boosted variants, runs a prompt, captures results,
# and produces a summary report.

set -uo pipefail

REMOTE="192.168.1.145"
PROMPT='The project has related design notes in an obsidian vault at ~/obsidian/HA. Update the project documentation to reference relevant design files.'
CONFIG="$HOME/.opencode.json"
TARGET="OpenCode.md"
BACKUP="${TARGET}.bak"
RESULTS_DIR="test-results"
REPORT="$RESULTS_DIR/report.txt"
NUM_CTX=40960  # max context that fits 100% GPU with the largest models

cd ~/myjarvis

# Models to test — all fit comfortably in 16GB VRAM at Q4
declare -a MODELS=(
  "qwen3:8b"
  "qwen3:4b"
  "qwen2.5-coder:7b"
  "gemma3:4b"
  "gemma3:12b"
  "gemma4:latest"
  "nemotron-3-nano:4b"
  "llama3.1:8b"
  "mistral:7b"
  "phi4-mini"
)

rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR"
cp "$TARGET" "$BACKUP"

# Save original config
cp "$CONFIG" "${CONFIG}.bak" 2>/dev/null || true

# ─── Phase 1: Pull all models ───────────────────────────────────────────────

echo "============================================"
echo "  Phase 1: Pulling models"
echo "============================================"
echo ""

for model in "${MODELS[@]}"; do
  echo -n ">>> $model ... "
  if ssh "$REMOTE" "ollama pull $model" >/dev/null 2>&1; then
    echo "OK"
  else
    echo "FAILED (will skip)"
  fi
done

echo ""

# ─── Phase 2: Test each model ───────────────────────────────────────────────

echo "============================================"
echo "  Phase 2: Running tests"
echo "============================================"
echo ""

declare -A RESULTS    # pass/no_changes/error
declare -A CONTEXTS   # context size
declare -A PROCESSORS # CPU/GPU split
declare -A DURATIONS  # wall clock seconds
declare -A DIFF_FILES # path to diff
declare -A ERRORS     # error messages

stop_loaded_model() {
  local running
  running=$(ssh "$REMOTE" "ollama ps 2>/dev/null | tail -n +2 | awk '{print \$1}'" 2>/dev/null || true)
  if [[ -n "$running" ]]; then
    ssh "$REMOTE" "ollama stop $running" 2>/dev/null || true
    sleep 3
  fi
}

for model in "${MODELS[@]}"; do
  echo "──────────────────────────────────────────"
  echo "  Testing: $model"
  echo "──────────────────────────────────────────"

  # Restore OpenCode.md
  cp "$BACKUP" "$TARGET"

  # Stop any loaded model
  stop_loaded_model

  # Pre-warm model with boosted context via the API options field
  echo ">>> Loading $model with num_ctx=$NUM_CTX ..."
  warmup_out=$(curl -s --max-time 120 "http://$REMOTE:11434/api/generate" \
    -d "{\"model\": \"$model\", \"prompt\": \"hi\", \"stream\": false, \"options\": {\"num_ctx\": $NUM_CTX}}" 2>&1)

  if ! echo "$warmup_out" | python3 -c "import json,sys; json.load(sys.stdin)['response']" >/dev/null 2>&1; then
    echo "    Warm-up failed: $(echo "$warmup_out" | head -c 200)"
    RESULTS["$model"]="error"
    ERRORS["$model"]="Warm-up failed"
    CONTEXTS["$model"]="n/a"
    PROCESSORS["$model"]="n/a"
    DURATIONS["$model"]="n/a"
    DIFF_FILES["$model"]=""
    echo ""
    continue
  fi

  # Capture model load info
  ps_out=$(ssh "$REMOTE" "ollama ps" 2>/dev/null)
  echo "$ps_out"
  ctx=$(echo "$ps_out" | tail -n +2 | awk '{for(i=1;i<=NF;i++) if($i ~ /^[0-9]+$/ && $i > 1000) print $i}' | head -1)
  proc=$(echo "$ps_out" | tail -n +2 | awk '{for(i=1;i<=NF;i++) if($i ~ /GPU/) print $i}' | head -1)
  CONTEXTS["$model"]="${ctx:-unknown}"
  PROCESSORS["$model"]="${proc:-unknown}"

  # Determine the model ID as seen by OpenCode via /v1/models
  model_id=$(curl -s "http://$REMOTE:11434/v1/models" | \
    python3 -c "
import json, sys
data = json.load(sys.stdin)['data']
target = '${model}'
# exact match first
for m in data:
    if m['id'] == target:
        print(m['id'])
        sys.exit(0)
# partial match
for m in data:
    if target.replace(':','-') in m['id'].replace(':','-'):
        print(m['id'])
        sys.exit(0)
print(target)
" 2>/dev/null)

  echo "    OpenCode model ID: local.$model_id"

  # Update opencode config
  cat > "$CONFIG" <<EOF
{
  "data": {},
  "tui": { "theme": "opencode" },
  "shell": {},
  "agents": {
    "coder": { "model": "local.$model_id" },
    "task": { "model": "local.$model_id" }
  }
}
EOF

  # Run OpenCode with timing
  echo ""
  echo ">>> Running OpenCode ..."
  start_time=$(date +%s)

  oc_output=$( opencode -p "$PROMPT" -c ~/myjarvis -q 2>&1 ) || true

  end_time=$(date +%s)
  elapsed=$(( end_time - start_time ))
  DURATIONS["$model"]="${elapsed}s"

  # Save full output
  echo "$oc_output" > "$RESULTS_DIR/${model//:/_}_output.txt"

  # Check for errors in output
  if echo "$oc_output" | grep -qi "error:\|failed:\|400 Bad Request\|500 Internal"; then
    errmsg=$(echo "$oc_output" | grep -i "error:\|failed:\|400\|500" | head -1 | sed 's/^[[:space:]]*//')
    RESULTS["$model"]="error"
    ERRORS["$model"]="$errmsg"
    DIFF_FILES["$model"]=""
    echo "    ERROR: $errmsg"
  else
    # Generate diff
    diff_file="$RESULTS_DIR/${model//:/_}_diff.txt"
    if diff -u "$BACKUP" "$TARGET" > "$diff_file" 2>&1; then
      RESULTS["$model"]="no_changes"
      ERRORS["$model"]="No edits made"
      DIFF_FILES["$model"]=""
      echo "    No changes to $TARGET"
    else
      RESULTS["$model"]="pass"
      ERRORS["$model"]=""
      DIFF_FILES["$model"]="$diff_file"
      echo "    Changes detected:"
      echo ""
      cat "$diff_file"
    fi
  fi

  echo ""
done

# ─── Phase 3: Report ────────────────────────────────────────────────────────

echo ""
echo "============================================"
echo "  Phase 3: Summary Report"
echo "============================================"
echo ""

{
  echo "Model Test Report — $(date)"
  echo "Prompt: $PROMPT"
  echo ""
  printf "%-24s %-12s %-12s %-10s %-8s %s\n" \
    "MODEL" "RESULT" "PROCESSOR" "CONTEXT" "TIME" "NOTES"
  printf "%-24s %-12s %-12s %-10s %-8s %s\n" \
    "------------------------" "------------" "------------" "----------" "--------" "-----"

  for model in "${MODELS[@]}"; do
    result="${RESULTS[$model]:-untested}"
    proc="${PROCESSORS[$model]:-?}"
    ctx="${CONTEXTS[$model]:-?}"
    dur="${DURATIONS[$model]:-?}"
    err="${ERRORS[$model]:-}"

    notes=""
    if [[ "$result" == "pass" ]]; then
      diff_file="${DIFF_FILES[$model]}"
      if [[ -n "$diff_file" && -f "$diff_file" ]]; then
        added=$(grep -c '^+[^+]' "$diff_file" 2>/dev/null || echo 0)
        removed=$(grep -c '^-[^-]' "$diff_file" 2>/dev/null || echo 0)
        notes="+${added}/-${removed} lines"
      fi
    elif [[ -n "$err" ]]; then
      notes="$err"
    fi

    printf "%-24s %-12s %-12s %-10s %-8s %s\n" \
      "$model" "$result" "$proc" "$ctx" "$dur" "${notes:0:80}"
  done

  echo ""
  echo "── Detailed diffs for passing models ──"
  echo ""

  for model in "${MODELS[@]}"; do
    if [[ "${RESULTS[$model]:-}" == "pass" ]]; then
      echo "━━━ $model ━━━"
      cat "${DIFF_FILES[$model]}"
      echo ""
    fi
  done

} | tee "$REPORT"

# ─── Cleanup ─────────────────────────────────────────────────────────────────

# Restore OpenCode.md
cp "$BACKUP" "$TARGET"
rm -f "$BACKUP"

# Restore original config
if [[ -f "${CONFIG}.bak" ]]; then
  mv "${CONFIG}.bak" "$CONFIG"
else
  cat > "$CONFIG" <<EOF
{
  "data": {},
  "tui": { "theme": "opencode" },
  "shell": {}
}
EOF
fi

# Clean up maxctx variants on the server
echo ""
echo ">>> Cleaning up test model variants on $REMOTE ..."
ssh "$REMOTE" "ollama list 2>/dev/null | grep maxctx | awk '{print \$1}' | xargs -r -n1 ollama rm" 2>/dev/null || true

# Reload production model
echo ">>> Restoring qwen3:14b-64k ..."
stop_loaded_model
curl -s "http://$REMOTE:11434/api/generate" \
  -d '{"model": "qwen3:14b-64k", "prompt": "hi", "stream": false}' > /dev/null
ssh "$REMOTE" "ollama ps"

echo ""
echo "Done. Report saved to $REPORT"
echo "Full outputs in $RESULTS_DIR/"
