# Kimi backend

**English** · [Italiano](kimi.it.md)

agent-bridge can drive the **Kimi Code CLI** (Moonshot AI) instead of the
default CLI. The HTTP API stays the same: chat, streaming, sessions, function
calling, images and generation parameters work as described in the
[README](../README.md); only the agent that answers each turn changes.

The bridge runs `kimi acp` and talks to it with the **Agent Client Protocol**
(JSON-RPC over stdin and stdout), acting as the ACP client. Unlike the
CLI's print mode, ACP takes the prompt on stdin (no command-line size
limit), accepts images and per-session MCP servers, and lets the bridge
answer every permission request.

## Quick start

Prerequisite: the `kimi` CLI (Kimi Code), version **2.2.0 or later**, and
one interactive login (`kimi login`).

```sh
npm install -g @moonshot-ai/kimi-code   # or the official install script
kimi login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend kimi
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/default","messages":[{"role":"user","content":"Hello!"}]}'
```

The backend is chosen with `backend: "kimi"` in the configuration file, the
`-backend kimi` flag or `AGENT_BRIDGE_BACKEND=kimi`. The binary is `kimi`
from `PATH`, or `kimi_binary` / `-kimi-bin`. The older Python `kimi-cli` is
not supported: it is no longer maintained.

## Models

| `model` | Runs |
| --- | --- |
| `bridge/default` | The default model configured in Kimi Code |
| `bridge/<model>` | The model with that alias or display name among those configured in Kimi Code (e.g. `bridge/kimi-k2`); an unknown name fails with the list of available models |
| configured agent | The agent's `model`; without one, the default model |

## How a turn is run

| Bridge | ACP / Kimi Code |
| --- | --- |
| Process | `kimi acp`; the bridge sends `initialize`, then `session/new` (or `session/resume`), the settings, `session/prompt`, and closes stdin when the prompt ends |
| New session | `session/new` with the working directory, `add_dirs` as `additionalDirectories` and the MCP servers |
| Resumed session | `session/resume` with the stored session id and the turn's MCP servers; an unknown id falls back to a new session with the full history |
| Model | `session/set_config_option` `model` |
| `reasoning_effort` | `session/set_config_option` `thinking`: `none`/`minimal` → `off`, the same level when the model offers it, the highest level for `xhigh`/`max`, otherwise `on`. Ignored if the model does not support it |
| `permission_mode` | `session/set_config_option` `mode`: `default`, `plan`, `auto`, `yolo` (requires `allow_bypass_permissions`) |
| Permission requests | Answered by the bridge: client functions always allowed; chat-only agents refuse everything else; `disallowed_tools` always refused; in `default` mode only `allowed_tools` are approved, in the other modes whatever the CLI still asks about is approved |
| Other agent requests (file system, terminals, questions) | Refused: the bridge does not offer those client capabilities |
| `mcp_config` | The same JSON file, converted to ACP MCP servers (stdio or HTTP) |
| Client functions | MCP server `functions` (`agent-bridge mcp-tools`), tools named `mcp__functions__<name>`; `KIMI_MCP_TOOL_TIMEOUT_MS` equal to the agent timeout |
| Agent `system_prompt` | ACP has no system prompt field: it is sent at the top of the first prompt of a session, inside `<system_instructions>`. `system_prompt_mode: replace` behaves like `append` |
| `system` messages | As with the other backends (`system_messages`: in the prompt by default) |
| Images | ACP `image` blocks |
| `response_format` | An instruction to answer with a single JSON object (with the schema, for `json_schema`); the answer is validated as JSON |
| `max_turns` | `KIMI_LOOP_MAX_STEPS_PER_TURN` |
| `max_tokens` | Accepted and ignored |
| Prompt starting with `/` | A space is added so it is not run as a CLI command |
| Environment | `KIMI_DISABLE_TELEMETRY=1` and `KIMI_CODE_NO_AUTO_UPDATE=1` unless already set |

Session updates are mapped to the bridge's progress: message chunks are
collected, the text before each tool call is an intermediate text, the text
after the last tool call is the answer; tool calls and permission decisions
are logged.

## Agents

| Field | With the kimi backend |
| --- | --- |
| `model` | Model alias or name configured in Kimi Code; empty = default model |
| `permission_mode` | `default` (default), `plan`, `auto`, `yolo` |
| `allowed_tools`, `disallowed_tools` | Kimi Code tool names (`Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep`, `FetchURL`, `mcp__<server>__<tool>`…), used to answer permission requests |
| `max_turns` | Maximum steps per turn |
| `system_prompt`, `tools_enabled`, `cwd`, `add_dirs`, `mcp_config`, `timeout_seconds`, `description` | As with the default CLI (see the table above for the system prompt) |
| `sandbox` | Not supported (codex backend only) |

Example: [examples/agents.kimi.example.json](../examples/agents.kimi.example.json).

## Authentication

| Mode | Credentials |
| --- | --- |
| `subscription` | Login done with `kimi login` (Kimi membership). `KIMI_MODEL_API_KEY` and `KIMI_API_KEY` must not be set; they and `KIMI_MODEL_NAME` are removed from the child environment |
| `api` | `KIMI_MODEL_NAME` and `KIMI_MODEL_API_KEY` required: the CLI then uses that model with that key (Moonshot platform by default; `KIMI_MODEL_PROVIDER_TYPE` and `KIMI_MODEL_BASE_URL` select another provider) |

`doctor` checks the CLI version; `doctor -live` runs a test turn on the
default model, which also checks the login.

The Kimi Code home directory (`KIMI_CODE_HOME`, default `~/.kimi-code`) is
still loaded: providers, models, MCP servers and skills defined there apply to
every turn. Chat-only agents keep the CLI's tools in the model's list, but
every call is refused.

## Differences from the default CLI

- Token usage and cost are not reported by ACP: `usage` and `cost_usd` are 0.
- The agent's system prompt is part of the first prompt, not a real system
  prompt, and is not sent again on resumed sessions.
- A turn that the CLI ends without any text (for example after an upstream
  error it only logs) is reported as `cli_error`.
- An unknown model is reported when the turn runs, as `cli_error`.
- In `auto` and `yolo` modes the CLI does not ask about every tool, so
  `allowed_tools` and `disallowed_tools` apply only to the requests it makes.

## Permitted use

The same principles as for the other backends apply (see the README): the
bridge never reads the CLI's credentials, a `subscription` instance serves
only the account holder, and shared instances use an API key. Check the
current terms of your Kimi plan before exposing an instance to anyone else.
