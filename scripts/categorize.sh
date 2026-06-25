#!/usr/bin/env bash
# categorize.sh — test the LLM two-pass routing idea.
#
# Usage:
#   ./categorize.sh "turn on the kitchen lights"
#   ./categorize.sh "what is the mortgage rate in the windjammer"
#   ./categorize.sh "bananas" --prev-prompt "add potatoes to the shopping list" --prev-tool "add_to_list"
#
# Step 1: categorize into home_control | list_management | property_logging | search
# Step 2: if search, run obsidian + wikipedia synthesis in parallel and return
#         whichever answer has higher self-reported confidence.

set -euo pipefail

OLLAMA_URL="${OLLAMA_URL:-http://192.168.1.145:11434/v1}"
MODEL="${MODEL:-granite4:latest}"
RAG_URL="${RAG_URL:-http://192.168.1.145:8011}"

USER_PROMPT=""
PREV_PROMPT=""
PREV_TOOL=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    --prev-prompt) PREV_PROMPT="$2"; shift 2 ;;
    --prev-tool)   PREV_TOOL="$2";   shift 2 ;;
    *)             USER_PROMPT="$1"; shift   ;;
  esac
done

if [[ -z "$USER_PROMPT" ]]; then
  echo "Usage: $0 <prompt> [--prev-prompt <text>] [--prev-tool <tool_name>]" >&2
  exit 1
fi

# ── Step 1: Categorize ────────────────────────────────────────────────────────

CATEGORIZE_SYSTEM="You are a request classifier. Categorize the user request by calling the categorize_request tool.

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
      '[
        {"role":"system","content":$sys},
        {"role":"user","content":$pp},
        {"role":"assistant","content":"","tool_calls":[{"id":"prev","type":"function","function":{"name":$pt,"arguments":"{}"}}]},
        {"role":"tool","tool_call_id":"prev","content":"ok"},
        {"role":"user","content":$up}
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

# ── Step 2: Dual RAG fetch + single synthesis call ────────────────────────────
# Fetch both corpora in parallel, then let one LLM call read both and pick.

OBS_FILE=$(mktemp)
WIKI_FILE=$(mktemp)
trap 'rm -f "$OBS_FILE" "$WIKI_FILE"' EXIT

rag_search() {
  local collection="$1" outfile="$2"
  curl -s -X POST "${RAG_URL}/search" \
    -H "Content-Type: application/json" \
    -d "$(jq -n --arg c "$collection" --arg q "$USER_PROMPT" \
           '{"collection":$c,"query":$q,"limit":5}')" \
    > "$outfile" 2>/dev/null || echo "[]" > "$outfile"
}

echo "" >&2
echo "=== Fetching both corpora ===" >&2
rag_search "obsidian_vault"    "$OBS_FILE"  &
rag_search "WIKIPEDIA_ENGLISH" "$WIKI_FILE" &
wait

MAX_CHUNK=500  # characters per chunk — keeps total context manageable

format_obsidian() {
  # Filter out HA/ directory — those are tech setup docs, not personal life notes.
  jq -r --argjson max "$MAX_CHUNK" '
    map(select((.payload.path // "") | startswith("HA/") | not))
    | if length == 0 then "(no results)"
      else .[] | "--- \(.payload.path // "?")\(if (.payload.heading // "") != "" then " > \(.payload.heading)" else "" end) ---\n\(.payload.content // "" | .[0:$max])\n"
      end' "$OBS_FILE"
}

format_wikipedia() {
  jq -r --argjson max "$MAX_CHUNK" '
    if length == 0 then "(no results)"
    else .[] | "--- Wikipedia: \(.payload.title // "?") ---\n\(.payload.content // "" | .[0:$max])\n"
    end' "$WIKI_FILE"
}

OBS_CHUNKS=$(format_obsidian)
WIKI_CHUNKS=$(format_wikipedia)

echo "  obsidian hits:  $(jq length "$OBS_FILE")" >&2
echo "  wikipedia hits: $(jq length "$WIKI_FILE")" >&2

SYNTHESIS_BODY=$(jq -n \
  --arg model "$MODEL" \
  --arg obs "$OBS_CHUNKS" \
  --arg wiki "$WIKI_CHUNKS" \
  --arg question "$USER_PROMPT" \
  '{
    model: $model,
    messages: [
      {
        "role": "system",
        "content": "You answer questions by choosing the most relevant source from two sets of documents: the user'\''s personal Obsidian notes and Wikipedia excerpts. Prefer personal notes when they directly address the question (the user'\''s own properties, cars, computers, finances). Use Wikipedia for general knowledge. Plain prose only, no markdown, suitable for text-to-speech."
      },
      {
        "role": "user",
        "content": ("=== Personal notes (Obsidian) ===\n\n" + $obs + "\n\n=== Wikipedia ===\n\n" + $wiki + "\n\nQuestion: " + $question)
      }
    ],
    tools: [{
      "type": "function",
      "function": {
        "name": "provide_answer",
        "description": "Provide the answer after choosing the best source.",
        "parameters": {
          "type": "object",
          "properties": {
            "source": {
              "type": "string",
              "enum": ["obsidian", "wikipedia", "neither"],
              "description": "Which source actually answered the question."
            },
            "answer": {
              "type": "string",
              "description": "Answer suitable for text-to-speech. Plain prose, no markdown."
            },
            "reasoning": {
              "type": "string",
              "description": "One sentence explaining why this source was chosen."
            }
          },
          "required": ["source", "answer", "reasoning"]
        }
      }
    }],
    "tool_choice": {"type":"function","function":{"name":"provide_answer"}}
  }')

SYNTH_RESPONSE=$(curl -s -X POST "${OLLAMA_URL}/chat/completions" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer ollama" \
  -d "$SYNTHESIS_BODY")

SYNTH_ARGS=$(echo "$SYNTH_RESPONSE" | jq -r '.choices[0].message.tool_calls[0].function.arguments // empty')
if [[ -z "$SYNTH_ARGS" ]]; then
  echo "=== No tool call from synthesizer ===" >&2
  echo "$SYNTH_RESPONSE" | jq . >&2
  exit 1
fi

echo "" >&2
echo "=== Synthesis ===" >&2
echo "$SYNTH_ARGS" | jq '{source, reasoning}' >&2

echo ""
echo "=== Result ==="
echo "$SYNTH_ARGS" | jq -r '.answer'
