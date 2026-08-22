#!/bin/bash
#
# Run the Go integration routing test against every model on goldmine-prime.
# Uses `make test-routing` so the tool schema and test cases stay in sync.
# Results land in test-results/<model>/{think,nothink}.log and summary.csv.
#
# no_think mode (/no_think suffix) is qwen3-specific; other models only run
# the default (think) pass.

set -uo pipefail

REMOTE="goldmine-prime"
RESULTS_DIR="test-results"
REPORT="$RESULTS_DIR/report.txt"
SUMMARY_CSV="$RESULTS_DIR/summary.csv"

# Models to test — all confirmed ≤16GB on the RTX 5000.
declare -a MODELS=(
  "gemma3:4b"
  "gemma3:12b"
  "gemma4:latest"
  "gemma4:16k"
  "gemma4:32k"
  "granite3.3:8b"
  "granite4:latest"
  "llama3.1:8b"
  "mistral:7b"
  "nemotron-3-nano:4b"
  "nemotron-mini:latest"
  "phi4-mini:latest"
  "phi4:latest"
  "phi4-reasoning:latest"
  "qwen2.5-coder:7b"
  "qwen2.5-coder:14b"
  "qwen3:4b"
  "qwen3:8b"
  "qwen3:8b-128k"
  "qwen3:8b-q8_0"
  "qwen3:14b-64k"
  "qwen3:14b-q4_K_M"
  "qwen3.5:9b"
  "qwen3.5:9b-64k"
)

# Models that support /no_think (qwen3 family only).
is_qwen3() {
  [[ "$1" == qwen3:* || "$1" == qwen3.5:* ]]
}

mkdir -p "$RESULTS_DIR"
> "$SUMMARY_CSV"

# Stop the currently loaded model to free VRAM before loading the next.
stop_loaded_model() {
  local running
  running=$(ssh "$REMOTE" "ollama ps 2>/dev/null | tail -n +2 | awk '{print \$1}'" 2>/dev/null || true)
  if [[ -n "$running" ]]; then
    curl -s "http://$REMOTE:11434/api/generate" \
      -d "{\"model\": \"$running\", \"keep_alive\": 0}" > /dev/null 2>&1
    sleep 2
  fi
}

run_test() {
  local model="$1" nothink="$2" logfile="$3"
  local mode="think"
  [[ $nothink -eq 1 ]] && mode="nothink"
  echo "  >>> $mode ..."

  if [[ $nothink -eq 1 ]]; then
    OLLAMA_URL="http://$REMOTE:11434/v1" MODEL="$model" MYJARVIS_NOTHINK=1 \
      make test-routing 2>&1 | tee "$logfile" || true
  else
    OLLAMA_URL="http://$REMOTE:11434/v1" MODEL="$model" \
      make test-routing 2>&1 | tee "$logfile" || true
  fi

  if grep -q "ROUTING_CSV:" "$logfile"; then
    csv=$(grep "ROUTING_CSV:" "$logfile" | tail -1 | sed 's/.*ROUTING_CSV://')
    echo "  $csv"
    echo "$csv" >> "$SUMMARY_CSV"
  else
    echo "  FAILED or no ROUTING_CSV line"
    echo "$model,$nothink,0,0,0,0,0,0,0,0" >> "$SUMMARY_CSV"
  fi
}

echo "============================================"
echo "  Phase 1: Pulling models"
echo "============================================"
for model in "${MODELS[@]}"; do
  echo -n ">>> $model ... "
  if curl -s "http://$REMOTE:11434/api/pull" -d "{\"name\": \"$model\", \"stream\": false}" | grep -q '"status":"success"'; then
    echo "OK"
  else
    echo "FAILED (will skip)"
  fi
done

echo ""
echo "============================================"
echo "  Phase 2: Running routing tests"
echo "============================================"

for model in "${MODELS[@]}"; do
  echo ""
  echo "──────────────────────────────────────────"
  echo "  Testing: $model"
  echo "──────────────────────────────────────────"

  stop_loaded_model

  slug="${model//:/_}"
  slug="${slug//\//_}"
  outdir="$RESULTS_DIR/$slug"
  mkdir -p "$outdir"

  run_test "$model" 0 "$outdir/think.log"
  if is_qwen3 "$model"; then
    run_test "$model" 1 "$outdir/nothink.log"
  fi
done

echo ""
echo "============================================"
echo "  Phase 3: Summary Report"
echo "============================================"
{
  echo "Model Routing Report"
  echo ""
  printf "%-35s  %-7s  %-6s  %-7s  %-8s  %-8s  %-8s\n" \
    "MODEL" "THINK" "PASS" "TOTAL" "ACC%" "P50ms" "MEANms"
  printf "%-35s  %-7s  %-6s  %-7s  %-8s  %-8s  %-8s\n" \
    "-----------------------------------" "-------" "------" "-------" "--------" "--------" "--------"

  while IFS=, read -r model nothink pass total acc min p50 p95 max mean; do
    think_label="think"
    [[ "$nothink" == "1" ]] && think_label="nothink"
    printf "%-35s  %-7s  %-6s  %-7s  %-8s  %-8s  %-8s\n" \
      "$model" "$think_label" "$pass" "$total" "${acc}%" "${p50}ms" "${mean}ms"
  done < "$SUMMARY_CSV"
} | tee "$REPORT"

echo ""
echo "Full logs in $RESULTS_DIR/<model>/{think,nothink}.log"
echo "CSV summary in $SUMMARY_CSV"
echo "Report in $REPORT"
