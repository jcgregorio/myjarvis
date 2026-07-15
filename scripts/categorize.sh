#!/usr/bin/env bash
# categorize.sh — test the LLM two-pass routing idea.
#
# Usage:
#   ./categorize.sh "turn on the kitchen lights"
#   ./categorize.sh "what is the mortgage rate in the windjammer"
#   ./categorize.sh "bananas" --prev-prompt "add potatoes to the shopping list" --prev-tool "add_to_list"
#
# Step 1: categorize into home_control | list_management | property_logging | search
# Step 2: if search, run LLM direct answer + Wikipedia RAG fetch in parallel
# Step 3: one comparison call picks the best of the two

set -euo pipefail

OLLAMA_URL="${OLLAMA_URL:-http://goldmine-prime:11434/v1}"
MODEL="${MODEL:-granite4:latest}"
RAG_URL="${RAG_URL:-http://goldmine-prime:8011}"

USER_PROMPT=""
PREV_PROMPT=""
PREV_TOOL=""
PREV_ASSISTANT=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prev-prompt)      PREV_PROMPT="$2"; shift 2 ;;
    --prev-tool)        PREV_TOOL="$2";   shift 2 ;;
    --prev-assistant)   PREV_ASSISTANT="$2";   shift 2 ;;
    *)                  USER_PROMPT="$1"; shift   ;;
  esac
done

if [[ -z "$USER_PROMPT" ]]; then
  echo "Usage: $0 <prompt> [--prev-prompt <text>] [--prev-tool <tool_name>]" >&2
  exit 1
fi

# ── Step 1: Categorize ────────────────────────────────────────────────────────

CATEGORIZE_SYSTEM="You are a helpful assistant to an suburban family living in Apex, North Carolina. When users make requests you are
to classify the families request.. Categorize the user request by calling the categorize_request tool. You MUST only respond in JSON.

Rules (apply in priority order):

1. If a previous action is given AND the current request is a bare word or short phrase with no clear category of its own, continue with the same category as the previous action.

2. home_control: the request names a device or automation to control (lights, locks, fans, scripts, automations).

3. list_management: the request adds, removes, checks off, or reads items from a shopping or todo list.

4. property_logging: records work done on a real-estate property ('log that we…', 'record that…'). Always an action, never a question.

5. search: any question or lookup — whether about the user's personal notes or general knowledge."

build_categorize_messages() {
  if [[ -n "$PREV_PROMPT" && -n "$PREV_TOOL" ]]; then
    jq -n \
      --arg sys "$CATEGORIZE_SYSTEM" \
      --arg pp "$PREV_PROMPT" \
      --arg pt "$PREV_TOOL" \
      --arg up "$USER_PROMPT" \
      --arg pa "$PREV_ASSISTANT" \
      '[
        {"role":"system","content":$sys},
        {"role":"assistant","content":"Previous prompt from user: \"\($pp)\". Previous categorization response: \"\($pa)\". Previous tool call: \"\($pt)\"."},
        {"role":"user","content":"\($up)"}
      ]'
  else
    jq -n \
      --arg sys "$CATEGORIZE_SYSTEM" \
      --arg up "$USER_PROMPT" \
      '[{"role":"system","content":$sys},{"role":"user","content":$up}]'
  fi
}

CATEGORIZE_BODY=$(jq -n \
  --arg model "$MODEL" \
  --argjson messages "$(build_categorize_messages)" \
  '{
    model: $model,
    messages: $messages,
    tools: [{
      type: "function",
      function: {
        name: "categorize_request",
        description: "Classify the user request into exactly one category.",
        parameters: {
          type: "object",
          properties: {
            category: {
              type: "string",
              enum: ["home_control","list_management","property_logging","search"],
              description: "home_control: device/automation control. list_management: list CRUD. property_logging: record work on a property. search: any question or lookup."
            },
            confidence: {type: "string", enum: ["high","medium","low"]},
            reasoning: {type: "string", description: "One sentence explaining the choice."}
          },
          required: ["category","confidence","reasoning"]
        }
      }
    }],
    tool_choice: {"type":"function","function":{"name":"categorize_request"}}
  }')

echo "=== Request ===" >&2
echo "  prompt:      $USER_PROMPT" >&2
[[ -n "$PREV_PROMPT" ]] && echo "  prev-prompt: $PREV_PROMPT" >&2
[[ -n "$PREV_TOOL"   ]] && echo "  prev-tool:   $PREV_TOOL"   >&2


printf "Request: $CATEGORIZE_BODY"

CAT_RESPONSE=$(curl -s -X POST "${OLLAMA_URL}/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ollama" \
  -d "$CATEGORIZE_BODY")

CAT_ARGS=$(echo "$CAT_RESPONSE" | jq -r '.choices[0].message.tool_calls[0].function.arguments // empty')
if [[ -z "$CAT_ARGS" ]]; then
  echo "=== No tool call from categorizer ===" >&2
  echo "$CAT_RESPONSE" | jq . >&2
  exit 1
fi

CATEGORY=$(echo "$CAT_ARGS" | jq -r '.category')
echo "" >&2
echo "=== Category ===" >&2
echo "$CAT_ARGS" | jq . >&2

if [[ "$CATEGORY" != "search" ]]; then
  echo "" >&2
  echo "=== Result (non-search) ==="
  echo "$CAT_ARGS" | jq '{category, confidence, reasoning}'
  exit 0
fi

# ── Step 2: LLM direct answer + Wikipedia RAG fetch in parallel ───────────────
# Then one comparison call that sees both and picks the best.

WIKI_FILE=$(mktemp)
LLM_FILE=$(mktemp)
trap 'rm -f "$WIKI_FILE" "$LLM_FILE"' EXIT

echo "" >&2
echo "=== Fetching: LLM direct + Wikipedia RAG ===" >&2

# LLM direct answer — no RAG, just the model's own knowledge.
curl -s -X POST "${OLLAMA_URL}/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ollama" \
  -d "$(jq -n --arg model "$MODEL" --arg q "$USER_PROMPT" '{
    model: $model,
    messages: [
      {"role":"system","content":"Answer the question concisely and accurately. Plain prose, no markdown, suitable for text-to-speech."},
      {"role":"user","content":$q}
    ]
  }')" > "$LLM_FILE" &

# Wikipedia RAG fetch.
curl -s -X POST "${RAG_URL}/search" \
  -H "Content-Type: application/json" \
  -d "$(jq -n --arg q "$USER_PROMPT" '{"collection":"WIKIPEDIA_ENGLISH","query":$q,"limit":5}')" \
  > "$WIKI_FILE" 2>/dev/null || echo "[]" > "$WIKI_FILE" &

wait

LLM_ANSWER=$(jq -r '.choices[0].message.content // "(no answer)"' "$LLM_FILE")
WIKI_COUNT=$(jq 'length' "$WIKI_FILE")
WIKI_CHUNKS=$(jq -r '
  if length == 0 then "(no results)"
  else .[] | "--- Wikipedia: \(.payload.title // "?") ---\n\(.payload.content // "" | .[0:500])\n"
  end' "$WIKI_FILE")

echo "  llm direct:     $(echo "$LLM_ANSWER" | head -c 100)" >&2
echo "  wikipedia hits: $WIKI_COUNT" >&2

# ── Step 3: Comparison call ───────────────────────────────────────────────────
# The LLM sees its own direct answer alongside the Wikipedia excerpts and
# picks whichever is more accurate/complete, or combines them.

COMPARE_BODY=$(jq -n \
  --arg model "$MODEL" \
  --arg llm "$LLM_ANSWER" \
  --arg wiki "$WIKI_CHUNKS" \
  --arg question "$USER_PROMPT" \
  '{
    model: $model,
    messages: [
      {
        "role": "system",
        "content": "You are given a question, a direct answer from your own knowledge, and Wikipedia excerpts. If the Wikipedia excerpts contain relevant, specific information, use them to verify or refine the answer. If they are irrelevant or empty, use the direct answer. Produce one final answer in plain prose suitable for text-to-speech. No markdown."
      },
      {
        "role": "user",
        "content": ("Question: " + $question + "\n\nDirect answer:\n" + $llm + "\n\nWikipedia excerpts:\n" + $wiki)
      }
    ],
    tools: [{
      "type": "function",
      "function": {
        "name": "provide_answer",
        "description": "Provide the final answer after comparing both sources.",
        "parameters": {
          "type": "object",
          "properties": {
            "source": {
              "type": "string",
              "enum": ["direct", "wikipedia", "combined"],
              "description": "Which source informed the final answer."
            },
            "answer": {
              "type": "string",
              "description": "Final answer in plain prose for text-to-speech."
            },
            "reasoning": {
              "type": "string",
              "description": "One sentence explaining the choice."
            }
          },
          "required": ["source", "answer", "reasoning"]
        }
      }
    }],
    "tool_choice": {"type":"function","function":{"name":"provide_answer"}}
  }')

COMPARE_RESPONSE=$(curl -s -X POST "${OLLAMA_URL}/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ollama" \
  -d "$COMPARE_BODY")

COMPARE_ARGS=$(echo "$COMPARE_RESPONSE" | jq -r '.choices[0].message.tool_calls[0].function.arguments // empty')
if [[ -z "$COMPARE_ARGS" ]]; then
  echo "=== No tool call from comparison LLM ===" >&2
  echo "$COMPARE_RESPONSE" | jq . >&2
  exit 1
fi

echo "" >&2
echo "=== Comparison ===" >&2
echo "$COMPARE_ARGS" | jq '{source, reasoning}' >&2

echo ""
echo "=== Result ==="
echo "$COMPARE_ARGS" | jq -r '.answer'
