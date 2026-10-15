# Codex backend

**English** · [Italiano](codex.it.md)

agent-bridge can drive the OpenAI **Codex CLI** (`codex exec --json`) instead
of the default CLI. The HTTP API stays the same: chat, streaming, sessions,
function calling, images and generation parameters work as described in the
[README](../README.md); only the process that answers each turn changes.

## Quick start

Prerequisite: the `codex` CLI, version **0.162.0 or later**, and one
interactive login (`codex login`).

```sh
npm install -g @openai/codex
codex login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend codex
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/default","messages":[{"role":"user","content":"Hello!"}]}'
```

The backend is chosen with `backend: "codex"` in the configuration file, the
`-backend codex` flag or `AGENT_BRIDGE_BACKEND=codex`. The binary is `codex`
from `PATH`, or `codex_binary` / `-codex-bin`.

## Models

| `model` | Runs |
| --- | --- |
| `bridge/default` | The model configured in Codex itself (`config.toml`, profile) |
| `bridge/<model>` | That model, passed with `-m` (e.g. `bridge/gpt-5-codex`), with `builtin_any_model` |
| configured agent | The agent's `model`; without one, the model configured in Codex |

The prefix follows `builtin_prefix`. The aliases of the default CLI (`opus`,
`sonnet`, `haiku`) are not registered with this backend.

## How a turn is run

Every setting is passed as a `-c key=value` override, so the CLI's own
configuration file is never modified.

| Bridge | Codex CLI |
| --- | --- |
| New turn | `codex exec --json --skip-git-repo-check … -`, prompt on stdin |
| Resumed session | `codex exec resume --json … <thread id> -` |
| Chat-only agent (built-in models, `tools_enabled: false`) | `sandbox_mode="read-only"`, the features that give the model tools of its own (shell, view image, sub-agents, apps, plugins, image generation, browser and computer use…) turned off, `web_search="disabled"` |
| Agent with tools | `sandbox_mode` from the agent's `sandbox` field; `approval_policy="never"` (nobody can answer an approval) |
| `add_dirs` | `sandbox_workspace_write.writable_roots` |
| `mcp_config` | The same JSON file (`{"mcpServers": {...}}`, `command`/`args`/`env` or `url`/`headers`), converted to `mcp_servers.<name>.*`; its tools are pre-approved |
| Client functions | MCP server `functions` (`agent-bridge mcp-tools`), pre-approved, call timeout equal to the agent timeout |
| `system_prompt`, `append` mode, `system_messages: append` | `developer_instructions` (at most 100 KB) |
| `system_prompt_mode: replace`, `system_messages: replace` | `model_instructions_file`, a temporary file that replaces the CLI's base instructions |
| Images | Temporary files passed with `--image`, removed after the turn |
| `reasoning_effort` | `model_reasoning_effort` (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`; `max` becomes `xhigh`) |
| `response_format: json_schema` | `--output-schema` with a temporary file; the final message is returned as compact JSON |
| `response_format: json_object` | An instruction to answer with a single JSON object; the answer is validated as JSON |
| `max_tokens`, `max_completion_tokens` | Accepted and ignored: the CLI has no equivalent |
| Usage | From `turn.completed`. On resumed sessions the CLI reports the totals of the whole thread: the bridge stores them with the session and returns only the usage of the turn |

Codex events are mapped to the bridge's progress: every `agent_message` is an
intermediate text (SSE comment or content, per `stream_progress`), the last
one is the answer; shell commands, file changes, web searches and MCP calls
are logged as tool uses; warnings (`error` items) and reconnection notices are
logged at debug level.

## Agents

| Field | With the codex backend |
| --- | --- |
| `model` | `-m`; empty = model configured in Codex |
| `sandbox` | `read-only`, `workspace-write` (default) or `danger-full-access` (requires `allow_bypass_permissions: true`) |
| `system_prompt`, `system_prompt_mode` | See the table above |
| `tools_enabled`, `cwd`, `add_dirs`, `mcp_config`, `timeout_seconds`, `description` | As with the default CLI |
| `permission_mode`, `allowed_tools`, `disallowed_tools`, `max_turns` | Not supported: a configuration error (also `defaults.max_turns`) |

Example: [examples/agents.codex.example.json](../examples/agents.codex.example.json).

## Authentication

| Mode | Credentials |
| --- | --- |
| `subscription` | Login done with `codex login` (ChatGPT plan). `CODEX_API_KEY` and `CODEX_ACCESS_TOKEN` must not be set; they and `OPENAI_API_KEY` are removed from the child environment |
| `api` | `CODEX_API_KEY` required (OpenAI API key). `codex exec` does not read `OPENAI_API_KEY` |

`doctor` checks the CLI version and, in `subscription` mode, `codex login
status`; `doctor -live` runs a test turn on the model configured in Codex.

The Codex configuration directory (`CODEX_HOME`, default `~/.codex`) is still
loaded: model, provider, profiles and any MCP servers defined there apply to
every turn, chat-only ones included. On a machine dedicated to the bridge keep
it minimal, or point `CODEX_HOME` to a dedicated directory.

## Differences from the default CLI

- `cost_usd` is always 0: the CLI does not report a cost.
- `finish_reason: "length"` never occurs (no `max_turns`).
- `X-Agent-Allowed-Tools` cannot be used (agents have no `allowed_tools`).
- System messages of later requests are ignored on resumed sessions, as with
  the default CLI: Codex keeps the developer instructions of the first turn.
- Usage is not available in responses that stop on client function calls; it
  is reported in the final response of the turn.
- The JSON schema of `json_schema` must be accepted by the OpenAI strict mode
  (`additionalProperties: false`, every property in `required`); otherwise the
  upstream error is returned as `cli_error`.

## Permitted use

The same principles as for the default CLI apply (see the README): the bridge
never reads the CLI's credentials, a `subscription` instance serves only the
account holder, and shared instances use an API key. Check the current terms
of your OpenAI plan before exposing an instance to anyone else.
