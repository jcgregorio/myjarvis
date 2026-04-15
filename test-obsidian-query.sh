#!/bin/bash
#
# Test: ask the LLM a question with obsidian vault context, get a TTS-friendly answer.

OLLAMA_URL="http://192.168.1.145:11434/v1/chat/completions"
MODEL="qwen3:14b-64k"
VAULT="$HOME/obsidian"
QUESTION="What CPU does Austin's computer have?"

# Gather relevant vault files — search for "austin" (case-insensitive)
context=""
while IFS= read -r file; do
  content=$(<"$file")
  context+="--- File: ${file#$VAULT/} ---"$'\n'"$content"$'\n\n'
done < <(grep -ril "austin" "$VAULT" --include="*.md" 2>/dev/null)

# If grep found nothing, try the filename
if [[ -z "$context" ]]; then
  while IFS= read -r file; do
    content=$(<"$file")
    context+="--- File: ${file#$VAULT/} ---"$'\n'"$content"$'\n\n'
  done < <(find "$VAULT" -iname "*austin*" -name "*.md" 2>/dev/null)
fi

echo "=== Context files found ==="
echo "$context" | grep "^--- File:"
echo ""
echo "=== Question ==="
echo "$QUESTION"
echo ""
echo "=== LLM Response ==="

curl -s "$OLLAMA_URL" \
  -H "Content-Type: application/json" \
  -d "$(python3 -c "
import json, sys

context = '''$context'''
question = '''$QUESTION'''

msg = json.dumps({
    'model': '$MODEL',
    'messages': [
        {
            'role': 'system',
            'content': 'You are a helpful home assistant. Answer questions using the provided documents. Give short, natural answers suitable for text-to-speech — no markdown, no lists, no special formatting. Just speak naturally as if answering a person out loud.'
        },
        {
            'role': 'user',
            'content': f'Here are some documents from my notes:\n\n{context}\n\nQuestion: {question}'
        }
    ],
    'stream': False
})
print(msg)
")" | python3 -c "
import json, sys
data = json.load(sys.stdin)
content = data['choices'][0]['message']['content']
# Strip <think>...</think> blocks if present (qwen3 thinking)
import re
content = re.sub(r'<think>.*?</think>', '', content, flags=re.DOTALL).strip()
print(content)
"
