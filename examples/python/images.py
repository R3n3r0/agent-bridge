"""Image input against agent-bridge with the official OpenAI Python SDK.

    pip install openai
    python images.py picture.png
"""
import base64
import mimetypes
import os
import sys

from openai import OpenAI

client = OpenAI(
    base_url=os.environ.get("AGENT_BRIDGE_URL", "http://127.0.0.1:8787/v1"),
    api_key=os.environ.get("AGENT_BRIDGE_API_KEY", "unused"),
)

path = sys.argv[1]
media_type = mimetypes.guess_type(path)[0] or "image/png"
with open(path, "rb") as f:
    data_url = f"data:{media_type};base64,{base64.b64encode(f.read()).decode()}"

response = client.chat.completions.create(
    model="bridge/sonnet",
    messages=[
        {
            "role": "user",
            "content": [
                {"type": "text", "text": "Describe this image in two sentences."},
                {"type": "image_url", "image_url": {"url": data_url}},
            ],
        }
    ],
)
print(response.choices[0].message.content)
