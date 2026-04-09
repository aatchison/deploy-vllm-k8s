#!/bin/bash
# Tool-use demo: ask all three endpoints to call a "get_weather" tool.
# vLLM: /v1/chat/completions with tools array (OpenAI format)
# ollama: /api/chat with tools array

NODE_IP="192.168.122.78"
E2B_URL="http://${NODE_IP}:30801/v1/chat/completions"
E4B_URL="http://${NODE_IP}:30802/v1/chat/completions"
OLLAMA_URL="http://localhost:31434/api/chat"

TOOLS_JSON='[
  {
    "type": "function",
    "function": {
      "name": "get_weather",
      "description": "Get the current weather for a location",
      "parameters": {
        "type": "object",
        "properties": {
          "location": {
            "type": "string",
            "description": "City and country, e.g. Paris, France"
          },
          "unit": {
            "type": "string",
            "enum": ["celsius", "fahrenheit"],
            "description": "Temperature unit"
          }
        },
        "required": ["location"]
      }
    }
  }
]'

PROMPT="What is the weather like in Tokyo right now?"

echo "========================================"
echo " Tool-use demo: get_weather"
echo " Prompt: \"${PROMPT}\""
echo "========================================"
echo ""

# --- E2B ---
echo "--- E2B [bg-digitalservices/Gemma-4-E2B-it-NVFP4] ---"
RESP=$(curl -sf "${E2B_URL}" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"bg-digitalservices/Gemma-4-E2B-it-NVFP4\",
    \"messages\": [{\"role\": \"user\", \"content\": \"${PROMPT}\"}],
    \"tools\": ${TOOLS_JSON},
    \"tool_choice\": \"auto\",
    \"max_tokens\": 256
  }" 2>&1)
echo "$RESP" | python3 -c "
import json, sys
d = json.load(sys.stdin)
choice = d['choices'][0]
msg = choice['message']
if msg.get('tool_calls'):
    for tc in msg['tool_calls']:
        fn = tc['function']
        print(f'  -> Tool call: {fn[\"name\"]}({fn[\"arguments\"]})')
elif msg.get('content'):
    print(f'  -> Text reply: {msg[\"content\"][:200]}')
else:
    print(f'  -> finish_reason={choice[\"finish_reason\"]}  raw={msg}')
" 2>/dev/null || echo "  -> ERROR: $RESP"

echo ""

# --- E4B ---
echo "--- E4B [google/gemma-4-E4B-it BF16] ---"
RESP=$(curl -sf "${E4B_URL}" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"google/gemma-4-E4B-it\",
    \"messages\": [{\"role\": \"user\", \"content\": \"${PROMPT}\"}],
    \"tools\": ${TOOLS_JSON},
    \"tool_choice\": \"auto\",
    \"max_tokens\": 256
  }" 2>&1)
echo "$RESP" | python3 -c "
import json, sys
d = json.load(sys.stdin)
choice = d['choices'][0]
msg = choice['message']
if msg.get('tool_calls'):
    for tc in msg['tool_calls']:
        fn = tc['function']
        print(f'  -> Tool call: {fn[\"name\"]}({fn[\"arguments\"]})')
elif msg.get('content'):
    print(f'  -> Text reply: {msg[\"content\"][:200]}')
else:
    print(f'  -> finish_reason={choice[\"finish_reason\"]}  raw={msg}')
" 2>/dev/null || echo "  -> ERROR: $RESP"

echo ""

# --- Ollama ---
OLLAMA_MODEL="${1:-devstral:latest}"
echo "--- ollama [${OLLAMA_MODEL}] ---"
RESP=$(curl -sf "${OLLAMA_URL}" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"${OLLAMA_MODEL}\",
    \"messages\": [{\"role\": \"user\", \"content\": \"${PROMPT}\"}],
    \"tools\": ${TOOLS_JSON},
    \"stream\": false
  }" 2>&1)
echo "$RESP" | python3 -c "
import json, sys
d = json.load(sys.stdin)
msg = d.get('message', {})
if msg.get('tool_calls'):
    for tc in msg['tool_calls']:
        fn = tc.get('function', tc)
        print(f'  -> Tool call: {fn.get(\"name\")}({json.dumps(fn.get(\"arguments\", {}))})')
elif msg.get('content'):
    print(f'  -> Text reply: {msg[\"content\"][:200]}')
else:
    print(f'  -> raw: {d}')
" 2>/dev/null || echo "  -> ERROR: $RESP"

echo ""
echo "========================================"
