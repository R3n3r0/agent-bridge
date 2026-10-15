# agent-bridge — Technical specification

**English** · [Italiano](specification.it.md)

Oct 15, 2026 · @Someone — **rev. 1.2**: OpenAI function calling (section 13), use from external orchestrators (section 13.8), alignment with the implementation (section 14)

## 1. Overview and goals

agent-bridge is an HTTP server written in Go that exposes an OpenAI-compatible API (`/v1/chat/completions`, `/v1/models`) and fulfills each request by running the official Claude Code binary in headless mode (`claude -p --output-format stream-json`). Any client or framework that speaks OpenAI can thus use Claude Code as if it were a model, with no changes to the client code.

The problem it solves: multi-agent systems already built on OpenAI APIs cannot use Claude Code as an execution engine (reading and editing files, running commands, searching code), nor assign a different Claude model to each agent while keeping flow control in their own orchestrator.

**Two kinds of "tools", both supported.** The term "tool" refers to two distinct things that must not be confused:

| | Agent tools | Client functions (OpenAI function calling) |
| --- | --- | --- |
| Who defines them | The agent configuration (`allowed_tools`, `mcp_config`) | The client, in the request's `tools` field |
| Examples | `Read`, `Edit`, `Bash`, `Grep`, MCP servers configured on the server | `get_weather`, `search_orders`, any client function |
| Who runs them | Claude Code, on the server machine | The client, in its own code |
| What the client sees | Only the effect and the final result | The calls (`tool_calls`), which it answers with `tool` messages |
| Specification | Sections 4.3 and 7 | Section 13 |

An agent can use both in the same turn: for example, read repository files with `Read` and then call a client function to open a ticket.

Measurable goals for v1.0:

1. A standard OpenAI client (official Go, Python, Node SDKs) works by changing only `base_url`, `api_key` and `model`.
2. Zero configuration for basic use: started without a configuration file, the server exposes at least the models `claude-code/opus`, `claude-code/sonnet`, `claude-code/haiku` in chat-only mode.
3. Configurable agents: a JSON file maps a model name to a Claude model, system prompt, tools, permissions and working directory.
4. Multi-turn conversations through Claude Code sessions resumed with `--resume`.
5. Same build for development (login with a Claude subscription) and production (API key), selected by configuration only.
6. Single binary, Go standard library only, releases for Linux, macOS and Windows (amd64 and arm64).
7. OpenAI-compatible function calling: the standard loop of the official SDKs (request with `tools`, response with `tool_calls`, next request with the `tool` messages) works with no client changes, including in streaming and without session headers (section 13).

Working name: `agent-bridge`. The final name must not contain "Claude Code" or "Anthropic" (see section 2).

## 2. Non-goals and compliance constraints

The following constraints derive from the [Claude Code Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) page and are requirements, not suggestions: any PR that violates them must be rejected.

1. **Unmodified binary.** The server runs `claude` as installed by Anthropic, through `os/exec`. No patches, wrappers that alter the binary, or reimplementations of the Claude Code protocol.
2. **No access to credentials.** The code must never read, copy, store, log or forward credential files (`~/.claude/.credentials.json`, macOS Keychain) or OAuth tokens. Login happens only through the official flow (interactive `claude`). Calling the Anthropic API directly with the subscription token is explicitly forbidden.
3. **One subscription, one user.** In subscription mode the server serves only the account holder on their own machine. With the default configuration it must not be possible to expose it to other users (see section 8, network bind).
4. **Shared instances only with an API key.** An instance used by several people (e.g. a lab) must run in `api` mode, with the holder's key, without reselling or brokering costs.
5. **Name and trademarks.** The project name, logo and description do not use "Claude Code" or "Anthropic" as part of the name and do not suggest affiliation. The README may state in plain text that the tool runs Claude Code and must declare that it is an independent project.
6. **README with a "Permitted use" section** that summarizes points 2–4 and links the legal page and the article [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan), noting that the rules may change.

Non-goals for v1.0:

- Support for the OpenAI parameters `response_format`, `temperature`, `top_p`, `n`, `stop`, `logprobs`: they are accepted and ignored. (`tools`, `tool_choice`, `parallel_tool_calls`, `functions` and `function_call` are supported instead: section 13.)
- Hard constraints on `tool_choice`, `parallel_tool_calls: false` and on the functions' `parameters`/`strict` schemas: they are rendered as instructions to the model, not enforced (section 13.6).
- The `/v1/responses` and `/v1/embeddings` APIs, images, audio.
- Non-Claude models: the server does not act as a router to other providers.
- Prompt injection into an open interactive session (channels): out of scope.
- Web interface or graphical dashboard.

## 3. Architecture

The bridge is a translator: it receives an OpenAI request, turns it into a `claude -p` turn and translates the events back into an OpenAI response. Its only state is the session registry (section 6) and, in `stateful` mode, the turns waiting for client function results (section 13); in `stateless` mode it holds no conversation state. Authentication to Anthropic stays entirely inside the official binary.

```text
client OpenAI ──HTTP──▶ server ──▶ turn ──os/exec──▶ claude -p (stream-json) ──▶ API Anthropic
                                    │  ▲                   │ MCP stdio
                         session, stats │                   ▼
                                    tools.HTTPBroker ◀── agent-bridge mcp-tools
```

Request flow: the server authenticates the client, resolves `model` to an agent and passes the validated request to the `turn` domain service; `turn` finds or creates the session and locks its mutex, has `prompt` build the stdin text, waits for a `runner` slot, launches `claude` with the agent's flags and reads its events; on the `result` event it saves the new `session_id`, updates `stats`, and the server responds with JSON or closes the SSE stream. With client functions (section 13) the turn can stop before the `result`, return the calls to the client and resume on the next request.

The code is organized in layers that communicate through interfaces (details in `docs/architecture.md`):

| Layer | Package | Interfaces used | Implementations |
| --- | --- | --- | --- |
| HTTP | `server` | `turn.Service`, `stats.Reporter` | — |
| Domain | `turn` | `session.Store`, `session.Locker`, `runner.Executor`, `runner.Limiter`, `stats.Recorder`, `tools.Broker` | default service |
| Infrastructure | `session`, `runner`, `stats`, `tools` | — | `MemoryStore`, `FileStore`, `KeyLocker`, `CLIExecutor`, `FIFOLimiter`, `Collector`, `JSONLUsageLog`, `HTTPBroker` |

The implementations are wired together in a single place (`cmd/agent-bridge/serve.go`).

## 4. Configuration

Configuration comes from four sources with this precedence: CLI flags > environment variables > JSON file > defaults. The file is optional; the format is JSON so as to stay on the standard library only.

### 4.1 Commands and flags

| Command | Effect |
| --- | --- |
| `agent-bridge serve` (default) | Starts the server |
| `agent-bridge check-config -config FILE` | Validates the file, prints the resolved agents, exits with code 0 or 1 |
| `agent-bridge doctor` | Checks `claude` in the PATH and the minimum version, consistency of the authentication mode, directory permissions; with `-live` runs a one-turn test request on Haiku |
| `agent-bridge version` | Prints version, commit and build date |
| `agent-bridge mcp-tools -spec FILE` | Internal: MCP server on stdio with the client functions, launched by Claude Code (section 13) |

`serve` flags (the same apply to `check-config` and `doctor`): `-config`, `-listen`, `-allow-remote`, `-max-concurrent`, `-claude-bin`, `-log-format text|json`, `-log-level debug|info|warn|error`.

### 4.2 Environment variables

| Variable | Required | Meaning |
| --- | --- | --- |
| `AGENT_BRIDGE_AUTH_MODE` | yes | `subscription` (Claude Code login) or `api` (API key) |
| `ANTHROPIC_API_KEY` | only with `api` | Claude Console key; forbidden with `subscription` |
| `AGENT_BRIDGE_API_KEY` | no, but required with `-allow-remote` | Key that clients send as `Authorization: Bearer` |
| `AGENT_BRIDGE_CONFIG` | no | Path of the configuration file |
| `AGENT_BRIDGE_LISTEN` | no | Listen address, default `127.0.0.1:8787` |
| `AGENT_BRIDGE_LOG_FORMAT` / `AGENT_BRIDGE_LOG_LEVEL` | no | Same as the flags of the same name |

### 4.3 Configuration file

Top-level keys: `listen`, `allow_remote` (bool, default false), `claude_binary` (default `claude`), `max_concurrent` (default 2), `max_queue` (default 32), `queue_timeout_seconds` (default 300), `shutdown_grace_seconds` (default 30), `stream_progress` (`comments`, `content` or `none`; default `comments`), `usage_log` (JSONL path, optional), `metrics` (bool, default false), `cors_origins` (list, default empty), `allow_bypass_permissions` (bool, default false), `builtin_models` (bool, default true), `builtin_any_model` (bool, default true, section 13.8), `system_messages` (`prompt`, `append` or `replace`; default `prompt`, section 13.8), `client_tools` (section 13.7), `defaults` (`timeout_seconds` default 1800, `max_turns` default 0 = unlimited), `sessions` (section 6), `overrides` (section 8), `agents`.

Fields of each agent in `agents` (the key is the name exposed as `model`; allowed characters `[a-zA-Z0-9._/-]`, at most 64):

| Field | Type | Default | Maps to |
| --- | --- | --- | --- |
| `model` | string | `sonnet` | `--model` (alias or full name) |
| `description` | string | empty | Exposed in `/v1/models` |
| `system_prompt` | string | empty | `--append-system-prompt` or `--system-prompt` |
| `system_prompt_mode` | `append` \| `replace` | `append` | Selects the flag above |
| `allowed_tools` | \[\]string | empty | `--allowedTools` |
| `disallowed_tools` | \[\]string | empty | `--disallowedTools` |
| `tools_enabled` | bool | true | false = chat-only (section 7.2) |
| `permission_mode` | string | `default` | `--permission-mode` |
| `max_turns` | int | `defaults.max_turns` | `--max-turns` |
| `cwd` | string | required if `tools_enabled` | Working directory of the process |
| `add_dirs` | \[\]string | empty | `--add-dir` (once per entry) |
| `mcp_config` | string | empty | `--mcp-config` (file path) |
| `timeout_seconds` | int | `defaults.timeout_seconds` | Process timeout |

Validation at startup, with an error that names the agent and the field: invalid name, `cwd` nonexistent or not a directory, `cwd` missing with tools enabled, `system_prompt_mode` or `permission_mode` outside the allowed values, negative numbers, name conflict with a built-in model. Relative paths are resolved against the directory of the configuration file.

### 4.4 Built-in models (zero-config)

With `builtin_models: true` the server registers `claude-code/opus`, `claude-code/sonnet` and `claude-code/haiku`: server tools disabled, `cwd` in an empty temporary directory created at startup and removed at shutdown, no additional system prompt. They accept client functions (section 13). With `builtin_any_model: true`, `claude-code/<model>` is also accepted for any model name (section 13.8). They serve as plain models for clients and orchestrators that define their agents themselves, and for trying out the server without configuration.

## 5. HTTP API

The server implements the subset of the OpenAI Chat Completions API required by the official SDKs, plus the service endpoints. All `/v1/*`, `/stats` and `/metrics` endpoints require `Authorization: Bearer <AGENT_BRIDGE_API_KEY>` when the key is set (constant-time comparison).

| Method and path | Purpose |
| --- | --- |
| `POST /v1/chat/completions` | Runs one agent turn |
| `GET /v1/models` | Lists agents and built-in models |
| `GET /v1/models/{id}` | Details of one model, 404 if missing |
| `GET /stats` | Cumulative per-agent statistics (section 9) |
| `GET /healthz` | 200 `ok` if the process is alive, no authentication |
| `GET /readyz` | 200 if `claude` is executable and the configuration is valid, otherwise 503 |
| `GET /metrics` | Prometheus metrics, only with `metrics: true` (section 9.4) |

### 5.1 `POST /v1/chat/completions` request

Fields used: `model` (required, agent name or `claude-code/<model>`), `messages` (required, at least one `user` message; after the last `assistant` message there must be at least one `user` or `tool` message), `stream` (bool), `stream_options.include_usage` (bool), `user` (string, used as a fallback session key), `tools`, `tool_choice`, `parallel_tool_calls`, `functions`, `function_call` (section 13), `max_completion_tokens` / `max_tokens` (ignored, but accepted). Accepted roles: `system`, `developer`, `user`, `assistant` (also with `tool_calls` or `function_call` and null `content`), `tool` (with `tool_call_id`), `function` (deprecated, treated as `tool`). `content` can be a string, null or an array of parts; only `type: "text"` parts are used, the others (images, audio) produce 400 `unsupported_content` in v1.0. Any other field is ignored without error. Maximum body size 10 MB.

Optional incoming headers:

| Header | Effect |
| --- | --- |
| `X-Session-Id` | Session key chosen by the client (section 6), at most 128 characters `[a-zA-Z0-9._:-]` |
| `X-Session-Reset: true` | Ignores the existing session and starts a new one with the same key |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | Per-request overrides, accepted only if enabled (section 8.3) |
| `X-Agent-Bridge-Tool-Mode: stateful\|stateless` | Client function mode for this request (section 13.8) |
| `X-Agent-Bridge-System-Mode: prompt\|append\|replace` | Destination of the `system`/`developer` messages for this request (section 13.8) |
| `X-Request-Id` | Request identifier (1-128 characters `[a-zA-Z0-9._:-]`), included in the logs; generated if missing |

### 5.2 Non-stream response

Standard `chat.completion` object: `id` (`chatcmpl-` + 24 random hex characters), `object`, `created`, `model` (the requested agent name), `choices` with a single element (`index` 0, `message.role` `assistant`, `message.content` equal to the `result` field of Claude Code's final event, `finish_reason`), `usage` (`prompt_tokens` = input + cache creation + cache read, `completion_tokens` = output, `total_tokens`). `finish_reason` is `stop` if the outcome is `success`, `length` if Claude Code ends with `error_max_turns`, `tool_calls` if the model called client functions (in that case `message.tool_calls` contains the calls and `message.content` is the text written before the calls, or `null`; section 13.3). With the deprecated `functions` format the response contains `message.function_call` and `finish_reason: "function_call"`.

Outgoing headers: `X-Session-Id` (always, even when generated by the server), `X-Request-Id`, `X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`, `X-Agent-Bridge-Duration-Ms`. In streaming only `X-Session-Id` and `X-Request-Id`, because the headers are sent before execution.

### 5.3 SSE streaming

With `stream: true` the response is `text/event-stream`. Mandatory sequence:

1. A `chat.completion.chunk` chunk with `delta: {"role": "assistant"}`, sent immediately.
2. During execution, only SSE comment lines (starting with `:`): one for each intermediate assistant text block (normalized to a single line, at most 300 characters) and `: keepalive` every 15 seconds without other events. Comment lines are **not** followed by a blank line: some SDKs (including `openai-go`) interpret a blank line after a comment as an empty event and fail.
3. At the end, a chunk with `delta.content` equal to the final result (omitted if the turn stops on function calls without text), then, if there are calls, one chunk for each with `delta.tool_calls: [{index, id, type: "function", function: {name, arguments}}]` (or `delta.function_call` in the deprecated format), then a chunk with `delta: {}` and `finish_reason`.
4. If `include_usage` is true, a chunk with `choices: []` and `usage`.
5. `data: [DONE]`.

The final content is identical to that of non-stream mode. Configuration option `stream_progress: "comments" | "content" | "none"` (default `comments`): with `content` the intermediate texts are sent as real `delta.content`, separated by a blank line, and the final result follows; with `none` there is no intermediate text, only keepalives.

### 5.4 Errors

OpenAI format: `{"error": {"message", "type", "code"}}`, with `type` and `code` equal. In streaming, if the error occurs after the headers have been sent, a `data:` event with the same `error` object is sent, followed by `data: [DONE]`.

| HTTP | `type` / `code` | When |
| --- | --- | --- |
| 400 | `invalid_request_error` | Invalid JSON, empty `messages`, no `user` message, no `user`/`tool` message after the last `assistant`, invalid headers, invalid functions (section 13.2) |
| 400 | `unsupported_content` | Non-text content parts |
| 401 | `authentication_error` | Missing or wrong Bearer key |
| 403 | `override_not_allowed` | Per-request override not enabled or out of bounds |
| 404 | `model_not_found` | Nonexistent agent or invalid `claude-code/<model>` name |
| 409 | `session_busy` | Only if `sessions.on_busy: "reject"` and the session is in use |
| 429 | `queue_full` | Wait queue full or wait expired (section 7.5), with a `Retry-After` header |
| 502 | `claude_code_error` | Process exited with an error, outcome other than `success`/`error_max_turns`, unparseable output |
| 503 | `server_error` | Server shutdown during the turn |
| 504 | `timeout` | The agent's `timeout_seconds` was exceeded |

The 502 error message includes at most the last 2000 characters of stderr, scrubbed of any string starting with `sk-ant-`.

## 6. Sessions and prompt construction

Each (agent, session key) pair corresponds to one Claude Code session. If the client reuses the same key (`X-Session-Id` header or `user` field), subsequent turns resume the session with `--resume` and only the new messages reach the CLI, so the client can keep sending the whole history as with OpenAI without the context being duplicated. Without a key, each request receives a new key and starts a new session with the full history (stateless behavior, like the OpenAI API); the client can reuse the key returned in the `X-Session-Id` header to switch to resumption.

### 6.1 Key and registry

The key is, in order: the `X-Session-Id` header, the `user` field, or a generated key `sess_` + 24 hex characters, returned in the `X-Session-Id` header. The internal key is `<agent>|<key>`, so the same key used with different agents produces distinct sessions.

Each registry entry contains: `claude_session_id`, `agent`, `cwd`, `created_at`, `last_used_at`, `turns`. Configuration in `sessions`:

| Field | Default | Meaning |
| --- | --- | --- |
| `store` | `memory` | `memory` or `file` |
| `path` | `sessions.json` next to the config | File for `store: file`, written atomically (temporary file + rename) |
| `ttl_hours` | 168 | Entries unused for longer are ignored and removed |
| `max_entries` | 10000 | Beyond the limit, the least recently used entry is removed |
| `on_busy` | `wait` | `wait` queues the request, `reject` responds 409 |

The registry stores only identifiers and metadata, never conversation contents or credentials.

### 6.2 Concurrency on the same session

Two parallel `--resume` runs on the same session are forbidden: each key has a mutex. With `on_busy: wait` the request waits until the session is free or the client disconnects.

### 6.3 Resumption and edge cases

1. The session ID to save is always the last `session_id` seen in the `system/init` or `result` events, because resumption can produce a new ID.
2. If the request's effective `cwd` differs from the registered one, a new session is started (Claude Code sessions are tied to the project directory).
3. If resumption fails because the session no longer exists, the server deletes the entry and retries the request once as a new session, with the flattened history (6.4), logging a warning.
4. `X-Session-Reset: true` deletes the entry before running.
5. A client disconnect during a turn terminates the process; the entry stays valid and points to the last known ID.

### 6.4 Prompt construction

The prompt is passed to `claude` through stdin, never as an argument.

**New session.** If the request contains a single message, with role `user`, the prompt is the text of that message (with `system_messages` `append` or `replace` the `system`/`developer` messages do not count, because they go into the system prompt). Otherwise the server builds:

```text
<instructions>
…text of the system/developer messages, separated by a blank line…
</instructions>

<conversation_history>
<user>…</user>
<assistant>…text…
<tool_call id="call_…" name="get_weather">{"city":"Rome"}</tool_call></assistant>
<tool tool_call_id="call_…" name="get_weather">…result…</tool>
</conversation_history>

<current_request>
…user and tool messages after the last assistant message…
</current_request>
```

Empty sections are omitted. With `system_messages` `append` or `replace` (section 13.8) the `system`/`developer` messages do not enter the prompt and there is no `<instructions>` section. `tool` messages without `tool_call_id` or name are rendered as `<tool>…</tool>`; calls in the deprecated `function_call` format as `<tool_call name="…">`.

**Resumed session.** The prompt contains only the `user` and `tool` messages after the request's last `assistant` message, joined by a blank line (the `tool` messages rendered with the `<tool tool_call_id=… name=…>` tag). This does not apply when the request resumes a turn waiting for functions (section 13.4): there the results go directly to the process, without a new prompt. With `system_messages: "prompt"` (default) the `system` messages are ignored, because they are already in the session from the first turn; if they differ from those of the first turn the server logs a debug message. With `append` or `replace` they are passed as the system prompt on every turn (section 13.8).

Limit: prompt over 9 MB in UTF-8 (the CLI limits stdin to 10 MB since v2.1.128) → 400 `invalid_request_error`.

## 7. Running Claude Code

Each turn is a separate `claude` process, started with `os/exec` without a shell, which ends when it emits the `result` event (or earlier, when the bridge closes it after returning function calls in `stateless` mode, section 13.8). The `runner` module is the only place in the code that knows the CLI's flags and event format.

### 7.1 Arguments

Fixed order: `-p`, `--output-format stream-json`, `--verbose`, then only the flags for fields that are set: `--model`, `--append-system-prompt` or `--system-prompt` (with the agent's system prompt, the request's `system` messages if `system_messages` is `append`/`replace`, and the instructions derived from `tool_choice`), `--tools ""` (chat-only), `--allowedTools` (entries joined by commas, plus `mcp__functions` if there are client functions), `--disallowedTools`, `--permission-mode`, `--max-turns`, `--add-dir` (repeated), the agent's `--mcp-config`, the client functions' `--mcp-config` (section 13), `--resume`. No positional arguments: the prompt goes on stdin.

Before v1.0, each flag must be checked against the [CLI documentation](https://code.claude.com/docs/en/cli-reference) for the minimum supported version, which `doctor` checks with `claude --version`. The minimum version is a constant in the code, documented in the README.

### 7.2 Chat-only mode

With `tools_enabled: false` no agent tool must be available: the runner passes `--tools ""`, which according to the [CLI reference](https://code.claude.com/docs/en/cli-reference) disables all built-in tools, and does not pass the `allowed_tools` entries, `--add-dir` or the agent's `mcp_config`. Mandatory test: in this mode a request asking to read a file must not produce `tool_use` events.

Exception: client functions (section 13) remain available also in chat-only mode and for built-in models, because they are executed by the client and give no access to the server machine. To be verified on the real CLI (checklist 11.4): that MCP tools remain available with `--tools ""`.

### 7.3 Child process environment

It starts from `os.Environ()` and all `AGENT_BRIDGE_*` variables are always removed. In `subscription` mode `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` are also removed. In `api` mode the environment is left unchanged. With client functions, if `MCP_TOOL_TIMEOUT` is not already set, it is set to the agent's timeout in milliseconds, so a call can wait for the client as long as the turn can last. The working directory is the agent's effective `cwd`.

### 7.4 Reading events

Stdout is read line by line (buffer up to 64 MB per line); each line is a JSON object. Invalid lines and unknown types are ignored with a debug log, for compatibility with future CLI versions.

| Event | Fields used | Action |
| --- | --- | --- |
| `system`, subtype `init` | `session_id`, `model`, `apiKeySource` | Saves the ID and the effective model; logs the credential source |
| `assistant` | `message.content[]` of type `text` | Forwards the text as progress (section 5.3) |
| `assistant` | `message.content[]` of type `tool_use` | Debug log with the tool name |
| `assistant` | `message.usage` | Kept as the `usage` of the `tool_calls` response (section 13.3) |
| `user` | tool results | Ignored |
| `result` | `subtype`, `result`, `is_error`, `session_id`, `total_cost_usd`, `usage`, `num_turns`, `duration_ms` | Final outcome of the turn |

Outcome mapping: `success` with `is_error` false → 200; `error_max_turns` → 200 with `finish_reason: length` and the available text; any other subtype or `is_error` true → 502. Process exited without a `result` event → 502 with the tail of stderr, or 504 if the timeout expired.

### 7.5 Concurrency and queue

A global semaphore limits active processes to `max_concurrent`. Excess requests enter a FIFO queue of at most `max_queue` (default 32) and wait at most `queue_timeout_seconds` (default 300); beyond that, 429 `queue_full` with `Retry-After: 30`. Time spent in the queue does not count toward the agent's `timeout_seconds`.

### 7.6 Timeout and interruption

The process context is canceled on agent timeout, client disconnect or server shutdown. The process must be terminated together with all its children (commands launched by the Bash tool): on Unix with a process group (`Setpgid`) and a signal to the group, first `SIGTERM` then `SIGKILL` after 5 seconds; on Windows by terminating the process tree. `cmd.WaitDelay` set to 5 seconds to avoid getting stuck on the pipes.

On shutdown (`SIGINT`/`SIGTERM`) the server stops accepting requests, waits up to `shutdown_grace_seconds` (default 30) for in-flight turns, then terminates them as above.

## 8. Authentication and security

An agent with tools enabled can read, write and run commands on the machine: whoever can reach the server has the equivalent of a restricted shell. The default settings must therefore be closed, and every opening must be explicit.

### 8.1 Authentication mode toward Anthropic

At startup the server reads `AGENT_BRIDGE_AUTH_MODE` and refuses to start, with a message explaining how to fix it, if:

- the variable is missing or has a value other than `subscription` / `api`;
- it is `subscription` and the environment contains `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN`;
- it is `api` and `ANTHROPIC_API_KEY` is missing.

The active mode is printed in the first startup log and exposed in `/stats`. If, in `subscription` mode, the `init` event reports an `apiKeySource` indicating an API key, the server logs a highly visible error for every turn (the exact values of the field must be verified during implementation).

### 8.2 Network exposure

1. Default: listen on loopback only (`127.0.0.1:8787`).
2. A non-loopback address requires `allow_remote: true` (or `-allow-remote`) and `AGENT_BRIDGE_API_KEY` to be set, otherwise the server does not start.
3. In `subscription` mode with remote access the server prints a warning at startup: the instance must serve only the account holder (e.g. access from their own laptop via a personal VPN), not other users.
4. No built-in TLS: for remote access the README recommends a reverse proxy, SSH tunnel or VPN.
5. CORS disabled; configurable with `cors_origins` (exact list of origins, no wildcards).

### 8.3 Per-request overrides

The `X-Agent-*` headers are rejected with 403 unless they are enabled in the `overrides` section of the configuration:

| Field | Default | Rule |
| --- | --- | --- |
| `allowed_roots` | empty (`cwd` override disabled) | `X-Agent-Cwd` is accepted only if, after `filepath.Abs` and `EvalSymlinks`, it is an existing directory inside one of the roots |
| `allow_tools` | false | `X-Agent-Allowed-Tools` (comma-separated list) can only narrow the agent's `allowed_tools`, never add to them |
| `allow_system_prompt` | false | `X-Agent-System-Prompt` (at most 16 KB, base64-encoded) is appended after the agent's, never replacing it |

Overrides never apply to `claude-code/*` models (aliases and arbitrary models).

### 8.4 Dangerous permissions

`permission_mode: bypassPermissions` is rejected during validation unless the configuration contains `allow_bypass_permissions: true`; in that case a warning appears at startup for each affected agent.

### 8.5 Client function broker

Client function calls go through an internal HTTP broker listening only on `127.0.0.1` on a random port. Each turn has a random 32-byte token, compared in constant time and invalidated at the end of the turn. Token and definitions are written to per-turn files with `0600` permissions, removed at the end. The functions do not execute anything on the server: their result comes from the client and is treated as conversation content.

### 8.6 Sensitive data in logs

At `info` level and above, prompt or response texts are never logged. At `debug` level they are logged truncated to 500 characters. Every log and error message passes through a redaction function that replaces `sk-ant-…` strings and the value of `AGENT_BRIDGE_API_KEY` with `[REDACTED]`. Each request has an ID (the incoming `X-Request-Id` if present, otherwise generated), included in all logs and in the response header.

## 9. Observability and costs

Each turn produces a log line, an optional entry in the usage file and an update of the in-memory statistics, so that during development with the subscription one can estimate how much production with an API key will cost.

### 9.1 Logs

`log/slog` library, `text` (default) or `json` format. Fields of each end-of-turn line: `request_id`, `agent`, `model` (effective, from the `init` event), `session_key`, `claude_session_id`, `resumed`, `turns`, `duration_ms`, `queue_ms`, `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `outcome` (`success`, `max_turns`, `error`, `timeout`, `canceled`, `tool_calls`). The `tool_calls` outcome indicates a process closed on purpose after returning function calls (`stateless` mode); the tokens are those of the last message.

### 9.2 Usage file

If `usage_log` is set, the same fields plus `ts` (RFC 3339) and `auth_mode` are appended as one JSON line per turn. Writes are serialized by a mutex; write errors are logged as warnings without failing the request. The file never contains prompt or response texts.

### 9.3 `GET /stats`

JSON response with `auth_mode`, `started_at`, `uptime_seconds`, `active`, `queued` and an `agents` object that reports for each agent `requests`, `errors`, `timeouts`, `cost_usd`, the four token counters and `avg_duration_ms`. With `?reset=true` it resets the counters after returning them.

In `subscription` mode the `cost_usd` field is the estimate computed by Claude Code at API prices, not a charge: the README must say so explicitly.

### 9.4 Prometheus metrics (optional)

With `metrics: true` the server exposes `GET /metrics` in Prometheus text format, hand-written without dependencies: `agent_bridge_requests_total{agent,outcome}`, `agent_bridge_cost_usd_total{agent}`, `agent_bridge_tokens_total{agent,kind}`, `agent_bridge_turn_duration_seconds` (sum and count), `agent_bridge_active`, `agent_bridge_queued`. Requires the Bearer key if set.

## 10. Repository, build and release

Go 1.22 or later, standard library only at runtime; external tools allowed only in CI. A single static binary (`CGO_ENABLED=0`).

### 10.1 Structure

```text
cmd/agent-bridge/        main, subcommands serve | check-config | doctor | version | mcp-tools
internal/config/         loading of file/env/flags, defaults, validation
internal/turn/           domain service: session, prompt, queue, execution, turns waiting for functions
internal/runner/         CLI arguments, environment, event parsing, Executor, Limiter
  proc_unix.go           process group and termination on Unix
  proc_windows.go        process tree termination on Windows
internal/prompt/         prompt construction (section 6.4)
internal/session/        Store (memory, file) and Locker
internal/tools/          client functions: broker, MCP server, definitions (section 13)
internal/server/         HTTP handlers, OpenAI types, SSE, errors, Bearer auth
internal/stats/          counters, usage file, /metrics
internal/redact/         redaction of secrets in logs and errors
testdata/fakecli/        fake binary of the CLI for tests (section 11)
examples/                agents.example.json, clients in Go, Python (including function calling) and curl
docs/                    configuration, architecture, release checklist, specification (English; Italian in *.it.md)
```

### 10.2 Quality and CI

GitHub Actions on every PR: empty `gofmt -l`, `go vet`, `staticcheck`, `go test -race ./...` on Ubuntu, macOS and Windows with Go 1.22 and the latest stable release. Minimum 80% coverage on `internal/prompt`, `internal/session`, `internal/config`, `internal/runner`, `internal/turn` and `internal/tools`.

### 10.3 Release

GoReleaser on `v*` tags: binaries for linux, darwin and windows on amd64 and arm64, archives, `checksums.txt`, release notes from the changelog. Version, commit and date injected with `-ldflags`. Installation also documented with `go install github.com/<owner>/agent-bridge/cmd/agent-bridge@latest`.

### 10.4 Documentation

The main documentation is in English (`README.md`, `docs/*.md`), with an Italian version of each document in `*.it.md` (`README.it.md`, `docs/*.it.md`). The README contains, in this order: what it does (with a declaration that it is an independent project), the two ways of using it (as a model and as an agent on the server) and the two kinds of tools, a quick start in three commands, use as a model from external orchestrators, function calling, agents on the server, conversations and sessions, development with a subscription and production with an API key, the "Permitted use" section (section 2), security (section 8), quick reference (examples, endpoints, headers, commands), known limitations, FAQ. Full configuration reference in `docs/configuration.md`.

Open decisions for the repository owner: license (MIT or Apache-2.0), final name, official Docker image (`api` mode only, can be deferred to v1.1).

## 11. Tests and acceptance criteria

CI never uses the real Claude Code or credentials: everything runs against a fake binary. Real tests are manual and documented.

### 11.1 `fakecli` fake binary

Go program in `testdata/fakecli`, compiled by the tests into a temporary directory and set as `claude_binary`. It reads the scenario from the `FAKE_SCENARIO` variable, records arguments, environment, cwd and stdin into a file given by `FAKE_RECORD`, and emits stream-json events. Minimum scenarios:

| Scenario | Behavior |
| --- | --- |
| `success` | `init`, one `assistant` with text and `tool_use`, successful `result` with usage and cost |
| `max_turns` | `result` with subtype `error_max_turns` |
| `exec_error` | `result` with `is_error` true |
| `no_result` | Writes to stderr and exits with code 1 without a `result` |
| `slow` | Waits N seconds before the `result` (for timeout, keepalive, cancellation) |
| `spawn_child` | Starts a sleeping child process, to verify termination of the tree |
| `resume_missing` | Fails if it receives `--resume`, succeeds without it |
| `new_session_id` | Returns in the `result` a `session_id` different from the one received |
| `garbage` | Non-JSON lines and unknown types interleaved with a valid stream |
| `tools` | Behaves like the CLI with client functions: starts the MCP server given in the last `--mcp-config` (the real `agent-bridge mcp-tools`), lists the tools, makes the calls in `FAKE_TOOL_CALLS` (in parallel with `FAKE_TOOL_PARALLEL=1`) and responds with the results; without `--mcp-config` it responds with the prompt received |

### 11.2 Mandatory unit tests

1. Prompt: all cases of section 6.4, string and array content, non-text parts, size limit.
2. Configuration: defaults, flag > env > file precedence, every validation error of section 4.3, relative paths.
3. Authentication mode: the four combinations of section 8.1 and the cleanup of the child environment (section 7.3).
4. Arguments: for each agent field, presence and form of the corresponding flag; `--tools ""` in chat-only mode.
5. Overrides: paths outside the roots, symlinks escaping the roots, tools not present in the agent, headers with overrides disabled.
6. Sessions: TTL, LRU, file persistence and reload, atomic write, `X-Session-Reset`.
7. Redaction: `sk-ant-` keys and the proxy key in logs and errors.
8. Client functions: validation of `tools`/`tool_choice`/`functions`, rendering of calls and results in the prompt, arguments and environment with functions (also in chat-only), MCP protocol (`initialize`, `tools/list`, `tools/call`, errors), broker (token, deregistration), `turn` service with fakes of the interfaces: single call, parallel, sequential with `parallel_tool_calls: false`, missing result, wait timeout and restart from the history, `tool_choice`.
9. External orchestrators: `system_messages` (`prompt`, `append`, `replace`, with and without the agent's system prompt), `stateless` mode (process closed, `tool_calls` outcome, session deleted, next step from the history), resolution of `claude-code/<model>` and invalid names.

### 11.3 Integration tests (with `httptest` and `fakecli`)

1. The official client `github.com/openai/openai-go` runs a chat completion and a stream without errors and reads `usage`.
2. Second turn with the same `X-Session-Id`: the child receives `--resume` with the ID from the last event and on stdin only the new user messages.
3. Two parallel requests on the same session: the child is never run twice concurrently.
4. `max_concurrent` = 1 and `max_queue` = 1: the third concurrent request receives 429 with `Retry-After`.
5. Agent timeout: 504 and no leftover process (including the child of `spawn_child`).
6. Client disconnect during the stream: process terminated within 10 seconds.
7. Streaming: exact sequence of section 5.3, keepalive with a reduced interval in tests, final content identical to non-stream.
8. Non-loopback bind without `allow_remote` or without a key: the server does not start.
9. Function calling with the official `openai-go` client, with no changes to the standard loop: `tool_calls` response, next request with `ToolMessage`, final response; the same in streaming with `ChatCompletionAccumulator`. A single process per conversation.
10. Function calling over HTTP: parallel calls in a single response with `content: null`, streaming with `delta.tool_calls`, deprecated `functions` format, results arriving after the wait timeout (new process with the full history).
11. External orchestrators over HTTP: `X-Agent-Bridge-Tool-Mode: stateless` header and `client_tools.mode` key, `X-Agent-Bridge-System-Mode` header and `system_messages` key, `claude-code/<model>` model in the request and in `GET /v1/models/{id}`.

### 11.4 Manual verification before each release

Checklist in `docs/release-checklist.md`, run with the real Claude Code at the minimum and the latest version: request with a built-in model in `subscription` mode; agent with tools that edits a file in a test repository; session resumption; the same test in `api` mode; `doctor --live` on Linux, macOS and Windows. For function calling: full loop with a built-in model (verifying that MCP tools are available with `--tools ""`), with an agent that already has its own `mcp_config` (verifying repeated `--mcp-config`), and waiting for results beyond the CLI's default timeouts (verifying `MCP_TOOL_TIMEOUT`).

### 11.5 Definition of done for v1.0

All tests of sections 11.2 and 11.3 green on three operating systems, checklist 11.4 run, complete README with the "Permitted use" section, no runtime dependencies beyond the standard library, no access to credential files, verifiable by searching the code for `.credentials`, `Keychain` and `oauth`.

## 12. Milestones and issues

The work is divided into five milestones, each releasable as a preview version; each row below is a GitHub issue, with the section references to cite in the description and the dependencies.

| # | Milestone | Issue | Sections | Depends on |
| --- | --- | --- | --- | --- |
| 1 | M1 Core (v0.1) | Repo skeleton, CI, `version`, package structure | 10.1, 10.2 | — |
| 2 | M1 Core (v0.1) | `fakecli` fake binary with all scenarios | 11.1 | 1 |
| 3 | M1 Core (v0.1) | Configuration loading and validation, built-in models | 4 | 1 |
| 4 | M1 Core (v0.1) | Runner: arguments, environment, event parsing, outcomes | 7.1–7.4 | 2, 3 |
| 5 | M1 Core (v0.1) | Prompt construction | 6.4 | 1 |
| 6 | M1 Core (v0.1) | Non-stream `POST /v1/chat/completions`, `/v1/models`, errors | 5.1, 5.2, 5.4 | 4, 5 |
| 7 | M2 Sessions and streaming (v0.2) | In-memory session registry, keys, mutex, resumption and edge cases | 6.1–6.3 | 6 |
| 8 | M2 Sessions and streaming (v0.2) | File store, TTL, LRU | 6.1 | 7 |
| 9 | M2 Sessions and streaming (v0.2) | SSE streaming and `stream_progress` | 5.3 | 6 |
| 10 | M2 Sessions and streaming (v0.2) | Semaphore, queue, 429 | 7.5 | 4 |
| 11 | M2 Sessions and streaming (v0.2) | Timeout, cancellation, process tree termination on Unix and Windows | 7.6 | 4 |
| 12 | M3 Security (v0.3) | Authentication mode and environment cleanup | 8.1, 7.3 | 4 |
| 13 | M3 Security (v0.3) | Bearer, network bind, CORS | 8.2 | 6 |
| 14 | M3 Security (v0.3) | Per-request overrides and dangerous permissions | 8.3, 8.4 | 6 |
| 15 | M3 Security (v0.3) | Redaction, request ID, logging rules | 8.6 | 6 |
| 16 | M4 Observability (v0.4) | slog logs, usage file, `/stats` | 9.1–9.3 | 6 |
| 17 | M4 Observability (v0.4) | `/metrics`, `/healthz`, `/readyz` | 9.4, 5 | 16 |
| 18 | M4 Observability (v0.4) | `check-config` and `doctor` | 4.1 | 3, 4 |
| 19 | M5 Release (v1.0) | Integration tests with `openai-go` | 11.3 | 7–15 |
| 20 | M5 Release (v1.0) | GoReleaser and installation | 10.3 | 1 |
| 21 | M5 Release (v1.0) | README, "Permitted use", `docs/configuration.md`, client examples | 2, 10.4 | all |
| 22 | M5 Release (v1.0) | Manual checklist and run on three systems | 11.4, 11.5 | 19–21 |
| 23 | M6 Function calling (v1.1) | Layered structure with interfaces and `turn` service | 3, 10.1 | 6–11 |
| 24 | M6 Function calling (v1.1) | Broker, `mcp-tools` MCP server, definitions | 13.1, 8.5 | 23 |
| 25 | M6 Function calling (v1.1) | Waiting turns, resumption via `tool_call_id`, wait timeout | 13.4, 13.5 | 24 |
| 26 | M6 Function calling (v1.1) | OpenAI request and response: `tools`, `tool_choice`, `tool_calls`, streaming, deprecated format | 5, 13.2, 13.3 | 25 |
| 27 | M6 Function calling (v1.1) | Tests with `openai-go`, `tools` scenario of the fake binary, checklist | 11 | 26 |
| 28 | M7 External orchestrators (v1.2) | Functions in `stateless` mode | 13.8 | 25 |
| 29 | M7 External orchestrators (v1.2) | `system_messages` and `X-Agent-Bridge-System-Mode` header | 13.8, 6.4 | 23 |
| 30 | M7 External orchestrators (v1.2) | `claude-code/<model>` models | 13.8, 4.4 | 3 |
| 31 | M7 External orchestrators (v1.2) | Documentation in English and Italian | 10.4 | 28–30 |

For implementers (a person or an agent on GitHub): each PR closes a single issue, includes the section 11 tests that concern it and introduces no runtime dependencies. When in doubt about the behavior of the Claude Code CLI, the official documentation prevails, and the discrepancy must be reported in the PR.

## 13. Function calling (client functions)

The bridge supports OpenAI API function calling: the client defines functions in `tools`, the model calls them, the client runs them and sends back the results, and the model continues. The standard SDK loop works with no changes. These functions are distinct from the agent tools (section 1): they run in the client's code, never on the server.

### 13.1 Mechanism

The Claude Code binary is not modified, nor is its protocol reimplemented. Client functions are offered to the CLI as tools of a **standard MCP server** served by the bridge itself:

1. For a turn with functions, the bridge registers the turn with the internal broker (section 8.5) and writes, into a private temporary directory, the function specification and an MCP configuration that launches `agent-bridge mcp-tools -spec <file>` as the `functions` server.
2. Claude Code receives `--mcp-config <file>` and `--allowedTools mcp__functions`; the model sees the tools `mcp__functions__<name>` with their description and parameter schema.
3. When the model calls a tool, `mcp-tools` forwards the call to the broker, which delivers it to the turn; the call stays pending.
4. After `batch_window_ms` from the first call in the group, the collected calls are returned to the client as `tool_calls`, each with an id `call_` + 24 hex characters.
5. In `stateful` mode (default) the Claude Code process stays alive, waiting: the next request containing the `tool` messages with those ids resumes the turn, the results unblock the pending calls and the model continues in the same process. In `stateless` mode (section 13.8) the process is closed and the next request restarts from the history.

### 13.2 Request

| Field | Support |
| --- | --- |
| `tools` | Only `type: "function"`; `function.name` `[a-zA-Z0-9_-]{1,64}`, unique; `description`; `parameters` (JSON Schema object, default empty object); `strict` accepted |
| `tool_choice` | `auto` (default), `none` (functions not offered to the model), `required`, `{"type":"function","function":{"name":…}}` |
| `parallel_tool_calls` | Default `true`; with `false` calls are returned one at a time |
| `functions`, `function_call` | Deprecated format: `none`, `auto`, `{"name":…}`; one call at a time |
| Messages | `assistant` with `tool_calls` (or `function_call`), `tool` with `tool_call_id`, `function` with `name` |

400 `invalid_request_error` errors, like the OpenAI API: tool of a type other than `function`, invalid or duplicate name, `parameters` not an object, `tool_choice`/`function_call` without functions, an invalid value, or one naming a nonexistent function; `function_call: "required"` in the deprecated format.

### 13.3 Response

Non-stream: `message.tool_calls` (`id`, `type: "function"`, `function.name`, `function.arguments` as a JSON string), `message.content` equal to the text written by the model before the calls or `null`, `finish_reason: "tool_calls"`. Deprecated format: `message.function_call` and `finish_reason: "function_call"`. The `usage` of a `tool_calls` response is that of the last assistant message read from stream-json; the turn total is in the final response, in the logs and in the statistics.

Stream: the sequence of section 5.3, with one `delta.tool_calls` chunk per call (incrementing `index`) before the chunk with `finish_reason`.

### 13.4 Resumption and state

- The waiting turn keeps the session lock and the concurrency slot. The ids of the waiting calls index the turn; `X-Session-Id` is not needed.
- A request is attached to a waiting turn if at least one of the `tool_call_id`s of its `tool` messages (after the last `assistant`) belongs to that turn and the agent matches. In this case `Admit` does not apply the session-busy and queue-full checks.
- Waiting calls without a result in the request receive an error result ("The caller did not return a result for this call.").
- Calls that arrived while the turn was waiting are returned in the next response.
- A client disconnect while attached to the turn terminates the process (as in 6.3).

### 13.5 Deadlines and restart

- If the results do not arrive within `client_tools.wait_timeout_seconds` the process is stopped with outcome `timeout`, recorded in the logs and in the statistics.
- A request with results that does not find a waiting turn (expired, server restarted, unknown ids) starts a new process with the full history built as in 6.4, calls and results included. The result is correct, just slower.
- In the deprecated `functions` format results have no ids: the process is stopped as soon as the call is returned and the next request restarts from the history.
- The agent's `timeout_seconds` remains the overall limit of the process, waits included.

### 13.6 Differences from the OpenAI API

- `tool_choice: "required"`, a specific function and `parallel_tool_calls: false` are rendered as instructions appended to the system prompt: the model follows them, but they are not guaranteed constraints. With `parallel_tool_calls: false` the bridge nonetheless returns only one call per response.
- The `parameters` schemas (and `strict`) guide the model but the arguments are not validated.
- Grouping of parallel calls depends on `batch_window_ms`: calls emitted further apart arrive in subsequent responses.
- A waiting turn occupies one `max_concurrent` slot.

### 13.7 Configuration

| `client_tools` key | Default | Meaning |
| --- | --- | --- |
| `enabled` | `true` | With `false` the `tools`/`functions` fields are accepted and ignored |
| `wait_timeout_seconds` | `600` | Maximum wait for results |
| `batch_window_ms` | `300` | Collection window for parallel calls |
| `mode` | `stateful` | `stateful` (sections 13.4–13.5) or `stateless` (section 13.8) |

### 13.8 Use from external orchestrators

When agents, graph and lifecycle are managed by an external platform (for example a ReAct system with its own graph), the bridge must behave like a stateless model. Three features make this possible without a configuration file:

1. **Functions in stateless mode** (`client_tools.mode: "stateless"` or header `X-Agent-Bridge-Tool-Mode: stateless`). Once the `tool_calls` are returned, the process is closed. The next request starts a new process with the history sent by the client (section 6.4), which remains the only source of truth: branching, retries, history edits and graph checkpoints are respected. The turn is recorded with outcome `tool_calls` and the tokens of the last message. Any associated session is deleted, because it ends with calls lacking results and must not be resumed. Parallel calls remain supported. Cost: one process per step, with the startup latency and less cache reuse.
2. **`system` messages as the system prompt** (`system_messages` or header `X-Agent-Bridge-System-Mode`). With `replace` the request's `system`/`developer` messages become the system prompt (`--system-prompt`) in place of the CLI's; with `append` they are added to it (`--append-system-prompt`). The system prompt configured in the agent, if any, stays first. The messages are applied on every turn, including in resumed sessions, and do not appear in the prompt.
3. **Any model** (`builtin_any_model`, default `true`). In addition to the three aliases, a name `claude-code/<model>` with `<model>` of 1-100 characters `[a-zA-Z0-9._[]-]` (the first alphanumeric) creates on the fly a chat-only agent that passes `<model>` to `--model`. Configured agents take precedence. `GET /v1/models` lists only agents and aliases; `GET /v1/models/{id}` also responds for these names.

Recommended configuration for this scenario: `{"client_tools": {"mode": "stateless"}, "system_messages": "replace"}` and no `X-Session-Id`.

## 14. Change log from rev. 1.0

| Item | Rev. 1.0 | Rev. 1.2 (implemented) | Reason |
| --- | --- | --- | --- |
| Function calling | `tools` & co. accepted and ignored | Supported (section 13) | Full compatibility with OpenAI clients |
| Code structure | Packages by function | Layers with interfaces and `turn` service (section 3) | Maintainability and testability |
| SSE comments | Not specified | Without a following blank line (5.3) | Compatibility with `openai-go` |
| Server shutdown | Not specified | 503 `server_error` (5.4) | Distinguish shutdown from a CLI error |
| Fake binary | `testdata/fakeclaude` | `testdata/fakecli` | Neutral name in repository paths |
| Prompt | `tool` messages in the history | `tool` messages after the last `assistant` in the current request; assistant calls rendered with `<tool_call>` (6.4) | Correct ordering of calls and results |
| Plain text | A single `user` message and no `system` | A single message in total, with role `user` | Avoid losing earlier `assistant` or `tool` messages |
| Minimum CLI version | To be defined | `2.1.128` (`runner.MinCLIVersion`) | 10 MB stdin limit used by 6.4 |
| External orchestrators | Not planned | Stateless mode, `system_messages`, arbitrary models (13.8) | Integration with platforms that manage graph and lifecycle |
| Deprecated `functions` format | — | The session interrupted on the calls is deleted, not resumed | Avoid resuming sessions with calls lacking results |
| Session registry | IDs and metadata | Also a fingerprint (hash) of the first turn's `system` messages | Debugging `system` changes (6.4), without storing their text |
| Sessions without a key | Implicit | Each request without a key is a new session with the full history (6) | Clarify the stateless behavior |
| Documentation | Italian | English main, Italian in `*.it.md` (10.4) | International audience |
