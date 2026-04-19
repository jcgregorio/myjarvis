#!/bin/bash
#
# Test Ollama models for myjarvis tool-calling accuracy and speed.
# Sends voice-assistant-style prompts via the OpenAI-compatible API
# with the myjarvis tool schema and measures response time + correctness.

set -uo pipefail

REMOTE="192.168.1.145"
API="http://$REMOTE:11434/v1/chat/completions"
RESULTS_DIR="test-results"
REPORT="$RESULTS_DIR/report.txt"

# Models to test — all should fit in 16GB VRAM at Q4
declare -a MODELS=(
  "qwen3:8b"
  "qwen3:4b"
  "qwen3:14b-64k"
  "qwen2.5-coder:7b"
  "gemma3:4b"
  "gemma3:12b"
  "gemma4:latest"
  "nemotron-3-nano:4b"
  "llama3.1:8b"
  "mistral:7b"
  "phi4-mini"
)

# Tool schema
TOOLS='[
  {
    "type": "function",
    "function": {
      "name": "add_to_list",
      "description": "Add an item to a list. Use list \"ShoppingList\" for groceries (default if not specified).",
      "parameters": {
        "type": "object",
        "properties": {
          "item": {"type": "string", "description": "The item or task to add"},
          "list": {"type": "string", "description": "The list to add to.", "enum": ["ShoppingList", "TODO", "Chores"]}
        },
        "required": ["item"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "check_off_item",
      "description": "Mark an item as done on a list. Use this when the user says they got something, completed something, or wants to check off an item.",
      "parameters": {
        "type": "object",
        "properties": {
          "list": {"type": "string", "description": "The list the item is on", "enum": ["ShoppingList", "TODO", "Chores"]},
          "item": {"type": "string", "description": "The item to check off"}
        },
        "required": ["list", "item"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "set_state",
      "description": "Turn a Home Assistant entity on or off",
      "parameters": {
        "type": "object",
        "properties": {
          "entity": {"type": "string", "description": "The name of the entity to control", "enum": ["kitchen lights", "living room lights", "bedroom fan", "porch light"]},
          "state": {"type": "string", "enum": ["on", "off"]}
        },
        "required": ["entity", "state"]
      }
    }
  },
  {
    "type": "function",
    "function": {
      "name": "search_notes",
      "description": "Search personal notes to answer a question. Use this when the user asks about people, computers, cars, schedules, or anything that might be in their notes.",
      "parameters": {
        "type": "object",
        "properties": {
          "query": {"type": "string", "description": "Search keywords to find relevant notes (e.g. austin computer or telluride oil)"},
          "question": {"type": "string", "description": "The original question to answer using the found notes"}
        },
        "required": ["query", "question"]
      }
    }
  }
]'

SYSTEM_PROMPT="You are a home assistant voice controller. When the user gives a command, call the appropriate tool to execute it. Only make tool calls — do not respond with prose unless no tool applies. If the command is ambiguous, make a reasonable assumption."

# Test cases: prompt | expected_tool | expected_args_fragment
declare -a TEST_PROMPTS=(
  "Add milk to the shopping list"
  "Check off peanut butter from the shopping list"
  "Add vacuum the garage to the chores list"
  "Turn off the kitchen lights"
  "Which CPU is in Austin's computer"
)
declare -a EXPECTED_TOOLS=(
  "add_to_list"
  "check_off_item"
  "add_to_list"
  "set_state"
  "search_notes"
)
declare -a EXPECTED_ARGS=(
  '"milk"'
  '"peanut butter"'
  '"vacuum'
  '"off"'
  '"austin'
)

rm -rf "$RESULTS_DIR"
mkdir -p "$RESULTS_DIR"

# ─── Helper: call the API ──────────────────────────────────────────────────

call_model() {
  local model="$1"
  local prompt="$2"
  local payload
  payload=$(python3 -c "
import json, sys
print(json.dumps({
    'model': '$model',
    'messages': [
        {'role': 'system', 'content': '''$SYSTEM_PROMPT'''},
        {'role': 'user', 'content': '''$prompt'''}
    ],
    'tools': $TOOLS,
    'stream': False
}))
")
  curl -s --max-time 120 "$API" \
    -H "Content-Type: application/json" \
    -d "$payload" 2>&1
}

# ─── Helper: stop loaded model ──────────────────────────────────────────────

stop_loaded_model() {
  local running
  running=$(ssh "$REMOTE" "ollama ps 2>/dev/null | tail -n +2 | awk '{print \$1}'" 2>/dev/null || true)
  if [[ -n "$running" ]]; then
    curl -s "http://$REMOTE:11434/api/generate" \
      -d "{\"model\": \"$running\", \"keep_alive\": 0}" > /dev/null 2>&1
    sleep 2
  fi
}

# ─── Phase 1: Pull models ──────────────────────────────────────────────────

echo "============================================"
echo "  Phase 1: Pulling models"
echo "============================================"
echo ""

for model in "${MODELS[@]}"; do
  echo -n ">>> $model ... "
  if curl -s "http://$REMOTE:11434/api/pull" -d "{\"name\": \"$model\", \"stream\": false}" | grep -q '"status":"success"'; then
    echo "OK"
  else
    echo "FAILED (will skip)"
  fi
done

echo ""

# ─── Phase 2: Test each model ──────────────────────────────────────────────

echo "============================================"
echo "  Phase 2: Running tests"
echo "============================================"
echo ""

for model in "${MODELS[@]}"; do
  echo "──────────────────────────────────────────"
  echo "  Testing: $model"
  echo "──────────────────────────────────────────"

  # Stop any loaded model to get clean VRAM
  stop_loaded_model

  # Warm up: force model load with a trivial prompt (no tools)
  echo ">>> Loading $model ..."
  warmup_start=$(date +%s%3N)
  warmup_out=$(curl -s --max-time 180 "http://$REMOTE:11434/api/generate" \
    -d "{\"model\": \"$model\", \"prompt\": \"Reply with just the word hello.\", \"stream\": false}" 2>&1)
  warmup_end=$(date +%s%3N)
  warmup_ms=$(( warmup_end - warmup_start ))

  if ! echo "$warmup_out" | python3 -c "import json,sys; json.load(sys.stdin)['response']" >/dev/null 2>&1; then
    echo "    Warm-up failed, skipping"
    echo "$model | SKIP | warm-up failed" >> "$RESULTS_DIR/summary.csv"
    echo ""
    continue
  fi
  echo "    Loaded in ${warmup_ms}ms"

  # Run each test case
  for i in "${!TEST_PROMPTS[@]}"; do
    prompt="${TEST_PROMPTS[$i]}"
    expected_tool="${EXPECTED_TOOLS[$i]}"
    expected_arg="${EXPECTED_ARGS[$i]}"

    echo ""
    echo "  Test $((i+1)): \"$prompt\""
    echo "    Expected: $expected_tool containing $expected_arg"

    start_ms=$(date +%s%3N)
    response=$(call_model "$model" "$prompt")
    end_ms=$(date +%s%3N)
    elapsed_ms=$(( end_ms - start_ms ))

    # Save raw response
    echo "$response" > "$RESULTS_DIR/${model//:/_}_test$((i+1)).json"

    # Parse response
    result=$(python3 -c "
import json, sys, re

raw = sys.stdin.read()
try:
    data = json.loads(raw)
except:
    print('ERROR|parse_failed|' + raw[:200])
    sys.exit(0)

msg = data.get('choices', [{}])[0].get('message', {})
tool_calls = msg.get('tool_calls', [])
content = msg.get('content', '')

# Strip think tags
content = re.sub(r'(?s)<think>.*?</think>', '', content).strip()

if tool_calls:
    tc = tool_calls[0]
    name = tc.get('function', {}).get('name', '')
    args = tc.get('function', {}).get('arguments', '')
    print(f'TOOL|{name}|{args}')
elif content:
    # Check for text-format tool calls (qwen2.5 quirk)
    m = re.search(r'\(\(\((\w+)\s+(\{.*?\})\)\)\)', content)
    if m:
        print(f'TOOL|{m.group(1)}|{m.group(2)}')
    else:
        print(f'TEXT|no_tool_call|{content[:200]}')
else:
    print('EMPTY|no_response|')
" <<< "$response")

    result_type=$(echo "$result" | cut -d'|' -f1)
    result_tool=$(echo "$result" | cut -d'|' -f2)
    result_detail=$(echo "$result" | cut -d'|' -f3-)

    # Grade
    grade="FAIL"
    if [[ "$result_type" == "TOOL" && "$result_tool" == "$expected_tool" ]]; then
      if echo "$result_detail" | grep -qi "$expected_arg"; then
        grade="PASS"
      else
        grade="WRONG_ARGS"
      fi
    fi

    echo "    Result:   $result_type | $result_tool | $result_detail"
    echo "    Grade:    $grade (${elapsed_ms}ms)"

    echo "$model|test$((i+1))|$prompt|$expected_tool|$expected_arg|$result_type|$result_tool|$result_detail|$grade|${elapsed_ms}ms" >> "$RESULTS_DIR/summary.csv"
  done

  echo ""
done

# ─── Phase 3: Report ───────────────────────────────────────────────────────

echo ""
echo "============================================"
echo "  Phase 3: Summary Report"
echo "============================================"
echo ""

{
  echo "Model Test Report — $(date)"
  echo ""
  printf "%-24s  %-6s  %-6s  %-6s  %-6s  %-6s  %-8s  %-8s  %-8s  %-8s  %-8s\n" \
    "MODEL" "T1" "T2" "T3" "T4" "T5" "T1ms" "T2ms" "T3ms" "T4ms" "T5ms"
  printf "%-24s  %-6s  %-6s  %-6s  %-6s  %-6s  %-8s  %-8s  %-8s  %-8s  %-8s\n" \
    "------------------------" "------" "------" "------" "------" "------" "--------" "--------" "--------" "--------" "--------"

  for model in "${MODELS[@]}"; do
    grades=()
    times=()
    for t in 1 2 3 4 5; do
      line=$(grep "^${model}|test${t}|" "$RESULTS_DIR/summary.csv" 2>/dev/null || echo "")
      if [[ -n "$line" ]]; then
        grade=$(echo "$line" | cut -d'|' -f9)
        ms=$(echo "$line" | cut -d'|' -f10)
        grades+=("$grade")
        times+=("$ms")
      else
        grades+=("SKIP")
        times+=("—")
      fi
    done
    printf "%-24s  %-6s  %-6s  %-6s  %-6s  %-6s  %-8s  %-8s  %-8s  %-8s  %-8s\n" \
      "$model" "${grades[0]}" "${grades[1]}" "${grades[2]}" "${grades[3]}" "${grades[4]}" \
      "${times[0]}" "${times[1]}" "${times[2]}" "${times[3]}" "${times[4]}"
  done

  echo ""
  echo "Tests:"
  echo "  T1: \"Add milk to the shopping list\"           → add_to_list(milk)"
  echo "  T2: \"Check off peanut butter from the list\"   → check_off_item(peanut butter)"
  echo "  T3: \"Add vacuum the garage to chores\"         → add_to_list(vacuum..., Chores)"
  echo "  T4: \"Turn off the kitchen lights\"             → set_state(kitchen lights, off)"
  echo "  T5: \"Which CPU is in Austin's computer\"       → search_notes(austin...)"
  echo ""
  echo "Grades: PASS = correct tool + args, WRONG_ARGS = right tool wrong args,"
  echo "        FAIL = wrong tool or no tool call, SKIP = model failed to load"

} | tee "$REPORT"

# ─── Cleanup ────────────────────────────────────────────────────────────────

echo ""
echo ">>> Restoring llama3.1:8b ..."
stop_loaded_model
curl -s "http://$REMOTE:11434/api/generate" \
  -d '{"model": "llama3.1:8b", "prompt": "hi", "stream": false}' > /dev/null
echo "Done. Report saved to $REPORT"
