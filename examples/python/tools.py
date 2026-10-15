"""Function calling against agent-bridge with the official OpenAI Python SDK.

The loop is the standard OpenAI one: nothing here is specific to the bridge
except base_url, model and the optional headers.

    pip install openai
    python tools.py
"""
import json
import os

from openai import OpenAI

client = OpenAI(
    base_url=os.environ.get("AGENT_BRIDGE_URL", "http://127.0.0.1:8787/v1"),
    api_key=os.environ.get("AGENT_BRIDGE_API_KEY", "unused"),
    # Optional: one process per step, the conversation lives only here, and
    # the system message becomes the model's system prompt.
    default_headers={
        "X-Agent-Bridge-Tool-Mode": "stateless",
        "X-Agent-Bridge-System-Mode": "replace",
    },
)

tools = [
    {
        "type": "function",
        "function": {
            "name": "get_weather",
            "description": "Current weather for a city",
            "parameters": {
                "type": "object",
                "properties": {"city": {"type": "string"}},
                "required": ["city"],
            },
        },
    }
]


def get_weather(city: str) -> str:
    return f"{city}: sunny, 24C"  # call your real system here


messages = [
    {"role": "system", "content": "You are a concise travel assistant."},
    {"role": "user", "content": "Should I bring an umbrella to Rome today?"},
]

response = client.chat.completions.create(model="claude-code/sonnet", messages=messages, tools=tools)
while response.choices[0].finish_reason == "tool_calls":
    message = response.choices[0].message
    messages.append(message)
    for call in message.tool_calls:
        args = json.loads(call.function.arguments)
        messages.append({"role": "tool", "tool_call_id": call.id, "content": get_weather(**args)})
    response = client.chat.completions.create(model="claude-code/sonnet", messages=messages, tools=tools)

print(response.choices[0].message.content)
