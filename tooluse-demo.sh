#!/bin/bash
# Tool-use demo: ask all three vLLM endpoints to call a "get_weather" function.
# Uses OpenAI-compatible /v1/chat/completions with tools array.
#
# Usage:
#   bash tooluse-demo.sh
#
# Requires triple deployment: ./deploy.sh triple

# Auto-detect node IP from kubectl, or use NODE_IP env var
NODE_IP="${NODE_IP:-$(microk8s kubectl get nodes -o jsonpath='{.items[0].status.addresses[?(@.type=="InternalIP")].address}' 2>/dev/null)}"
if [ -z "$NODE_IP" ]; then
    echo "ERROR: Could not detect node IP. Set NODE_IP env var or check kubectl." >&2
    exit 1
fi

E2B_URL="http://${NODE_IP}:30801/v1/chat/completions"
E4B_URL="http://${NODE_IP}:30802/v1/chat/completions"
B31_URL="http://${NODE_IP}:30803/v1/chat/completions"

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

# --- 31B ---
echo "--- 31B [nvidia/Gemma-4-31B-IT-NVFP4] ---"
RESP=$(curl -sf "${B31_URL}" \
  -H "Content-Type: application/json" \
  -d "{
    \"model\": \"nvidia/Gemma-4-31B-IT-NVFP4\",
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
echo "========================================"
