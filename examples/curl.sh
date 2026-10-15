#!/usr/bin/env sh
# Example requests with curl. Set AGENT_BRIDGE_API_KEY if the server uses one.
set -eu
URL=${AGENT_BRIDGE_URL:-http://127.0.0.1:8787}
AUTH="Authorization: Bearer ${AGENT_BRIDGE_API_KEY:-unused}"

curl -s "$URL/v1/models" -H "$AUTH"
echo

# First turn: the response header X-Session-Id identifies the conversation.
curl -s -i "$URL/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' \
  -H 'X-Session-Id: curl-demo' \
  -d '{"model":"claude-code/haiku","messages":[{"role":"user","content":"Pick a number between 1 and 10."}]}'
echo

# Second turn on the same session, streamed.
curl -s -N "$URL/v1/chat/completions" -H "$AUTH" -H 'Content-Type: application/json' \
  -H 'X-Session-Id: curl-demo' \
  -d '{"model":"claude-code/haiku","stream":true,"messages":[
        {"role":"user","content":"Pick a number between 1 and 10."},
        {"role":"assistant","content":"7"},
        {"role":"user","content":"Which number did you pick?"}]}'
echo

curl -s "$URL/stats" -H "$AUTH"
echo
