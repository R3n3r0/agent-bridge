"""Example client using the official OpenAI Python SDK against agent-bridge.

    pip install openai
    python client.py
"""
import os

from openai import OpenAI

client = OpenAI(
    base_url=os.environ.get("AGENT_BRIDGE_URL", "http://127.0.0.1:8787/v1"),
    api_key=os.environ.get("AGENT_BRIDGE_API_KEY", "unused"),
)

# One agent per role: the model name selects the agent configured in agents.json.
review = client.chat.completions.create(
    model="reviewer",
    messages=[{"role": "user", "content": "Review the error handling in internal/server."}],
    extra_headers={"X-Session-Id": "review-42"},
)
print(review.choices[0].message.content)

# Streaming with usage.
stream = client.chat.completions.create(
    model="claude-code/sonnet",
    messages=[{"role": "user", "content": "Write a haiku about pipes."}],
    stream=True,
    stream_options={"include_usage": True},
)
for chunk in stream:
    if chunk.choices and chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="", flush=True)
    if chunk.usage:
        print(f"\ntokens: {chunk.usage.total_tokens}")
