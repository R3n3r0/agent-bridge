# Configuration reference

**English** · [Italiano](configuration.it.md)

Configuration comes from four sources, with this precedence:
**CLI flags > environment variables > JSON file > defaults**. The file is optional.
Authentication mode and keys come only from environment variables.

## Commands

| Command | Effect |
| --- | --- |
| `agent-bridge serve` (default) | Starts the server |
| `agent-bridge check-config -config FILE` | Validates the file, prints the resolved agents, exits with 0 or 1 |
| `agent-bridge doctor [-live]` | Checks the CLI and minimum version, authentication mode, directory permissions; with `-live` runs a test turn on Haiku |
| `agent-bridge version` | Prints version, commit and build date |
| `agent-bridge mcp-tools -spec FILE` | Internal: stdio MCP server with the client functions, started by the CLI |

Flags (shared by `serve`, `check-config` and `doctor`):

| Flag | Equivalent |
| --- | --- |
| `-config FILE` | `AGENT_BRIDGE_CONFIG` |
| `-listen ADDR` | `listen`, `AGENT_BRIDGE_LISTEN` |
| `-allow-remote` | `allow_remote` |
| `-max-concurrent N` | `max_concurrent` |
| `-claude-bin PATH` | `claude_binary` |
| `-log-format text\|json` | `AGENT_BRIDGE_LOG_FORMAT` |
| `-log-level debug\|info\|warn\|error` | `AGENT_BRIDGE_LOG_LEVEL` |

## Environment variables

| Variable | Required | Meaning |
| --- | --- | --- |
| `AGENT_BRIDGE_AUTH_MODE` | yes | `subscription` (CLI login) or `api` (API key) |
| `ANTHROPIC_API_KEY` | only with `api` | Console key; forbidden with `subscription` (as is `ANTHROPIC_AUTH_TOKEN`) |
| `AGENT_BRIDGE_API_KEY` | if `allow_remote` | Key that clients send as `Authorization: Bearer` |
| `AGENT_BRIDGE_CONFIG` | no | Path of the configuration file |
| `AGENT_BRIDGE_LISTEN` | no | Listen address, default `127.0.0.1:8787` |
| `AGENT_BRIDGE_LOG_FORMAT`, `AGENT_BRIDGE_LOG_LEVEL` | no | Same as the flags of the same name |

All `AGENT_BRIDGE_*` variables are removed from the child process's
environment; in `subscription` mode, so are `ANTHROPIC_API_KEY` and
`ANTHROPIC_AUTH_TOKEN`.

## Top-level keys

| Key | Default | Meaning |
| --- | --- | --- |
| `listen` | `127.0.0.1:8787` | Listen address |
| `allow_remote` | `false` | Allows a non-loopback address (requires `AGENT_BRIDGE_API_KEY`) |
| `claude_binary` | `claude` | Name or path of the CLI |
| `max_concurrent` | `2` | Processes active at the same time (minimum 1) |
| `max_queue` | `32` | Requests waiting for a slot; beyond that, 429 |
| `queue_timeout_seconds` | `300` | Maximum wait in the queue; beyond that, 429 |
| `shutdown_grace_seconds` | `30` | Wait for in-progress turns on shutdown |
| `stream_progress` | `comments` | `comments`, `content` or `none` (see below) |
| `usage_log` | empty | JSONL file with one line per turn |
| `metrics` | `false` | Enables `GET /metrics` |
| `cors_origins` | `[]` | Allowed origins, exact, no wildcards |
| `allow_bypass_permissions` | `false` | Allows `permission_mode: bypassPermissions` |
| `builtin_models` | `true` | Registers `claude-code/opus`, `claude-code/sonnet`, `claude-code/haiku`: chat-only agents, without server tools, that accept client functions |
| `builtin_any_model` | `true` | Also accepts `claude-code/<model>` for any model name (`[a-zA-Z0-9][a-zA-Z0-9._[]-]`, up to 100 characters), as a chat-only agent |
| `system_messages` | `prompt` | How `system`/`developer` messages are delivered: `prompt` (at the start of the prompt, in `<instructions>`), `append` (appended to the CLI's system prompt), `replace` (they become the system prompt in place of the CLI's). Can be overridden per request with `X-Agent-Bridge-System-Mode` |
| `defaults.timeout_seconds` | `1800` | Default turn timeout |
| `defaults.max_turns` | `0` | Default maximum turns (0 = unlimited) |
| `sessions` | | See below |
| `overrides` | | See below |
| `client_tools` | | See below |
| `agents` | `{}` | Agents, by name |

Unknown keys in the file are an error, to catch typos.

### `stream_progress`

- `comments`: intermediate assistant texts are sent as SSE comments
  (`: text`), normalized to a single line and truncated to 300 characters; the client
  sees only the final result.
- `content`: intermediate texts are sent as `delta.content`, each
  followed by an empty line, then the final result.
- `none`: no intermediate text, only `: keepalive`.

In all cases, if there are no other events for 15 seconds, `: keepalive` is sent.

## Agents

The key in the `agents` object is the name exposed as `model`: characters
`[a-zA-Z0-9._/-]`, at most 64.

| Field | Type | Default | CLI flag |
| --- | --- | --- | --- |
| `model` | string | `sonnet` | `--model` (alias or full name) |
| `description` | string | empty | Exposed in `/v1/models` |
| `system_prompt` | string | empty | `--append-system-prompt` or `--system-prompt` |
| `system_prompt_mode` | `append` \| `replace` | `append` | Selects the flag above |
| `allowed_tools` | []string | empty | `--allowedTools` (entries joined by commas) |
| `disallowed_tools` | []string | empty | `--disallowedTools` |
| `tools_enabled` | bool | `true` | `false` = chat-only: `--tools ""`, without the agent's `allowed_tools` entries, `--add-dir` and `mcp_config`. Client functions remain available (`--allowedTools mcp__functions` and their `--mcp-config`) |
| `permission_mode` | string | `default` | `--permission-mode` (`default`, `acceptEdits`, `plan`, `bypassPermissions`, `dontAsk`, `auto`) |
| `max_turns` | int | `defaults.max_turns` | `--max-turns` (omitted if 0) |
| `cwd` | string | required if `tools_enabled` | Working directory of the process |
| `add_dirs` | []string | empty | `--add-dir`, once per entry |
| `mcp_config` | string | empty | `--mcp-config` |
| `timeout_seconds` | int | `defaults.timeout_seconds` | Process timeout (0 = none) |

Relative paths (`cwd`, `add_dirs`, `mcp_config`, `usage_log`,
`sessions.path`, `overrides.allowed_roots`) are resolved relative to the directory
of the configuration file. Chat-only agents without `cwd` use the same
empty temporary directory as the built-in models, created at startup and removed
at shutdown.

Validation errors (reported with agent and field): invalid name,
`cwd` nonexistent or not a directory, `cwd` missing with tools enabled,
`system_prompt_mode` or `permission_mode` outside the allowed values, negative
numbers, conflict with a built-in model, `bypassPermissions` without
`allow_bypass_permissions`.

Arguments passed to the CLI, in fixed order: `-p --output-format stream-json
--verbose`, then only the flags for fields that are set, then the `--mcp-config` for
client functions if present, then `--resume <id>` if the session is being
resumed. The prompt is always on stdin.

## `sessions`

| Field | Default | Meaning |
| --- | --- | --- |
| `store` | `memory` | `memory` or `file` |
| `path` | `sessions.json` next to the config | File for `store: file`, written atomically |
| `ttl_hours` | `168` | Entries unused for longer are removed |
| `max_entries` | `10000` | Beyond the limit, the least recently used entry is removed |
| `on_busy` | `wait` | `wait` queues the request, `reject` responds 409 |

The session store keeps only identifiers and metadata (session ID, agent,
directory, dates, turn count, fingerprint of the system messages), never
conversation contents or credentials.

Session key: the `X-Session-Id` header (1-128 characters `[a-zA-Z0-9._:-]`),
otherwise the `user` field (if it contains other characters, a fingerprint
`user_…` of it is used), otherwise a generated key `sess_…`. The key is
always returned in the `X-Session-Id` header. The same key used with different
agents produces distinct sessions. `X-Session-Reset: true` deletes the session
before the turn.

## `overrides`

| Field | Default | Rule |
| --- | --- | --- |
| `allowed_roots` | `[]` (override disabled) | `X-Agent-Cwd` must be an absolute path of an existing directory that, after resolving symlinks, lies inside one of the roots |
| `allow_tools` | `false` | `X-Agent-Allowed-Tools` (comma-separated list) can only narrow `allowed_tools` |
| `allow_system_prompt` | `false` | `X-Agent-System-Prompt` (base64, at most 16 KB decoded) is appended after the agent's prompt |

Overrides never apply to `claude-code/*` models (aliases and
arbitrary models). An override that is not enabled or is out of bounds produces 403
`override_not_allowed`.

## `client_tools`

Functions defined by clients with `tools` (OpenAI function calling), executed
by the clients.

| Field | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | With `false`, the `tools`/`functions` fields are ignored |
| `mode` | `stateful` | `stateful`: the process waits for the results and resumes; `stateless`: the process exits after returning the calls and the next request starts again from the history. Can be overridden per request with `X-Agent-Bridge-Tool-Mode` |
| `wait_timeout_seconds` | `600` | Maximum wait for results; then the process is stopped and a subsequent request starts again from the history |
| `batch_window_ms` | `300` | Time to collect parallel calls to return together |

The functions are exposed to the CLI as the `functions` MCP server (tools
`mcp__functions__<name>`), started with `agent-bridge mcp-tools`. Calls
go through an internal broker listening on `127.0.0.1` on a random port with a
different token for each turn; the turn's configuration files are private
(permissions `0600`) and removed at the end. The child process's `MCP_TOOL_TIMEOUT`
variable is set to the agent's timeout, if not already present.

Validation (400 `invalid_request_error`, like the OpenAI API): tools of a type
other than `function`, names outside `[a-zA-Z0-9_-]{1,64}` or duplicates,
`parameters` not an object, `tool_choice` without `tools` or naming a nonexistent
function.

## Per-request headers

| Header | Values | Effect |
| --- | --- | --- |
| `X-Session-Id` | 1-128 characters `[a-zA-Z0-9._:-]` | Session key |
| `X-Session-Reset` | `true`, `false` | Restarts the session |
| `X-Agent-Bridge-Tool-Mode` | `stateful`, `stateless` | Client functions mode |
| `X-Agent-Bridge-System-Mode` | `prompt`, `append`, `replace` | Destination of `system` messages |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | see `overrides` | Overrides, only if enabled |
| `X-Request-Id` | 1-128 characters `[a-zA-Z0-9._:-]` | Request identifier, reported in the logs and in the response; generated if absent |

Response headers: `X-Session-Id` and `X-Request-Id` always; in non-stream
responses also `X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`,
`X-Agent-Bridge-Duration-Ms`.

With `system_messages` `append` or `replace`, the system prompt configured
in the agent always stays first and the request's messages are
appended after it. `system` messages are applied on every turn, including in
resumed sessions.

## HTTP errors

OpenAI format: `{"error": {"message", "type", "code"}}`, with `type` and `code`
equal.

| HTTP | Code | When |
| --- | --- | --- |
| 400 | `invalid_request_error` | Invalid JSON, body over 10 MB, `messages` empty or without `user` messages, no `user` or `tool` message after the last `assistant`, invalid headers, invalid functions (see `client_tools`), prompt over 9 MB |
| 400 | `unsupported_content` | Non-text content parts |
| 401 | `authentication_error` | Missing or wrong Bearer key |
| 403 | `override_not_allowed` | Override not enabled or out of bounds |
| 404 | `model_not_found` | Nonexistent agent or invalid `claude-code/<model>` name |
| 409 | `session_busy` | `on_busy: reject` and session in use |
| 429 | `queue_full` | Queue full or wait expired, with `Retry-After: 30` |
| 502 | `claude_code_error` | Process exited with an error or without a result; includes at most 2000 characters of stderr, scrubbed of secrets |
| 503 | `server_error` | Server shutdown during the turn |
| 504 | `timeout` | Agent's `timeout_seconds` exceeded |

In streaming, an error after the headers have been sent is a `data:` event with the
same `error` object, followed by `data: [DONE]`.

## Observability

Each turn produces a log line with `request_id`, `agent`, `model`,
`session_key`, `claude_session_id`, `resumed`, `turns`, `duration_ms`,
`queue_ms`, `cost_usd`, the four token counters and `outcome` (`success`,
`max_turns`, `error`, `timeout`, `canceled`, `tool_calls` for processes closed
after returning calls in `stateless` mode). With `usage_log` the same fields,
plus `ts` and `auth_mode`, are appended as a JSON line.

`GET /stats` returns `auth_mode`, `started_at`, `uptime_seconds`, `active`,
`queued` and, per agent, `requests`, `errors`, `timeouts`, `cost_usd`, tokens and
`avg_duration_ms`.

`GET /metrics` (with `metrics: true`): `agent_bridge_requests_total{agent,outcome}`,
`agent_bridge_cost_usd_total{agent}`, `agent_bridge_tokens_total{agent,kind}`,
`agent_bridge_turn_duration_seconds` (sum and count), `agent_bridge_active`,
`agent_bridge_queued`. Requires the Bearer key if set.
