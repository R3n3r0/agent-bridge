# agent-bridge

**English** · [Italiano](README.it.md)

`agent-bridge` is an HTTP server written in Go that exposes an OpenAI-compatible
API (`/v1/chat/completions`, `/v1/models`) and fulfils every request by running
the official Claude Code CLI in headless mode
(`claude -p --output-format stream-json`).

It can also run the OpenAI Codex CLI (`codex exec --json`) instead, with the
same API: see the [Codex backend](docs/codex.md).

Any client or framework that speaks OpenAI can use it by changing only
`base_url`, `api_key` and `model`: chat, streaming, function calling (tool
calling) and image input work with the official SDKs unchanged.

In short: an **OpenAI-compatible API server, proxy and gateway for Claude
Code**. It lets OpenAI-based multi-agent systems, ReAct orchestrators and
frameworks that accept a custom OpenAI `base_url` (for example LangChain,
LlamaIndex, CrewAI, AutoGen or the OpenAI SDKs for Python, Node and Go) use
Claude Code headless as a drop-in chat completions model or as a coding agent,
with tool calling, MCP servers, sessions, streaming (SSE) and per-agent models.
The same server is an OpenAI-compatible API, proxy and gateway for the
OpenAI Codex CLI.

> **Independent project.** agent-bridge is not developed, endorsed or
> supported by Anthropic. It runs the official `claude` binary installed on
> the machine, without modifying it, and never accesses its credentials.

- Single binary, Go standard library only at runtime.
- Linux, macOS and Windows (amd64 and arm64).
- Same build for development (subscription login) and production (API key).
- OpenAI function calling (`tools`, `tool_calls`), streaming included.
- Image input (`image_url` parts: data URLs and http(s) URLs).
- Two CLI backends: the default one and the OpenAI Codex CLI
  (`backend: "codex"`, see [docs/codex.md](docs/codex.md)).
- Free software, licensed under the GNU GPL v3 or later.

## Two ways to use it

| | As a model | As an agent on the server |
| --- | --- | --- |
| Who defines the agent | Your client or platform: `system`, `tools`, model choice | The bridge configuration file |
| Available tools | Only the functions the client sends in `tools`, executed by the client | CLI tools on the server machine (files, commands, search, MCP servers), plus any client functions |
| `model` | `bridge/opus`, `bridge/sonnet`, `bridge/haiku`, `bridge/<model name>` | The name of the configured agent |
| Configuration | None | JSON file |
| Section | [Using it as a model](#using-it-as-a-model) | [Agents on the server](#agents-on-the-server) |

"Tools" therefore come in two kinds, both supported:

- **client functions** (OpenAI function calling): defined and executed by the
  client; the model calls them through `tool_calls`;
- **agent tools**: `Read`, `Edit`, `Bash`, MCP servers… executed by the CLI on
  the server, within the agent's permissions; the client only sees the final
  result.

## Quick start

Prerequisite: the `claude` CLI installed, version **2.1.128 or later**, and one
interactive login (`claude`).

```sh
go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@latest
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/haiku","messages":[{"role":"user","content":"Hello!"}]}'
```

Without a configuration file the server listens on `127.0.0.1:8787` and accepts
the models `bridge/opus`, `bridge/sonnet`, `bridge/haiku` and
`bridge/<model name>` (the name after the prefix is passed to the CLI as
is; the `bridge/` prefix can be changed with `builtin_prefix`). They are
*chat-only* models: no server tools, an empty temporary working directory,
but they accept client functions and images.

Alternatively, download a binary from the releases page (archives and
`checksums.txt` for every platform).

## Using it as a model

This is the scenario of a platform that already has its own agents, graph and
lifecycle (for example a ReAct system): the bridge behaves like an OpenAI
model and the agent is the one defined in the platform.

| In the platform | In the request |
| --- | --- |
| Agent instructions | `system` message |
| Agent tools | `tools` |
| Model | `bridge/<alias or model name>` |

Two options make the bridge stateless and make the platform's agent the only
agent. Set them in the configuration, or per request with a header:

| Configuration key | Header | Effect |
| --- | --- | --- |
| `client_tools.mode: "stateless"` | `X-Agent-Bridge-Tool-Mode: stateless` | After returning `tool_calls` the process exits: every request is one step of the graph and the history sent by the client is the only source of truth. Branches, retries and checkpoints stay in the platform |
| `system_messages: "replace"` | `X-Agent-Bridge-System-Mode: replace` | `system` messages become the effective system prompt instead of the CLI one, at every step. With `append` they are added to it |

Recommended configuration for this scenario:

```json
{ "client_tools": { "mode": "stateless" }, "system_messages": "replace" }
```

In this scenario do not send `X-Session-Id`: every request starts a new process
with the full history. It costs some latency per step, in exchange for no
hidden state in the bridge.

## Function calling

Functions defined by the client with `tools` work as with the OpenAI API: the
model answers with `tool_calls` and `finish_reason: "tool_calls"`, the client
runs the functions and sends the results back as `role: "tool"` messages, and
the model continues. It also works when streaming (`delta.tool_calls`) and
without session headers.

```python
r = client.chat.completions.create(model="bridge/sonnet", messages=messages, tools=tools)
while r.choices[0].finish_reason == "tool_calls":
    messages.append(r.choices[0].message)
    for call in r.choices[0].message.tool_calls:
        messages.append({"role": "tool", "tool_call_id": call.id,
                         "content": run(call.function.name, call.function.arguments)})
    r = client.chat.completions.create(model="bridge/sonnet", messages=messages, tools=tools)
```

Complete example: [examples/python/tools.py](examples/python/tools.py).

Supported: `tools` (type `function`), `tool_choice` (`auto`, `none`,
`required`, a specific function), `parallel_tool_calls` (parallel calls
returned together) and the deprecated `functions`/`function_call` format.

**How it works.** Client functions are offered to the CLI as tools of a
standard MCP server, served by the bridge itself (internal `mcp-tools`
subcommand). When the model calls one, the bridge returns the call to the
client. Then, depending on `client_tools.mode`:

| Mode | What happens | When to choose it |
| --- | --- | --- |
| `stateful` (default) | The process waits; the request carrying the results is recognized by its `tool_call_id`s and the same process resumes where it stopped | Simple clients: lower latency between steps |
| `stateless` | The process exits; the request carrying the results starts a new process with the full history | Orchestrators with their own state (see [Using it as a model](#using-it-as-a-model)) |

Differences to be aware of:

- `tool_choice` other than `auto`/`none` and `parallel_tool_calls: false` are
  expressed as instructions to the model: the model follows them, but they
  are not hard constraints as in the OpenAI API. Function `parameters`
  schemas (and `strict`) also guide the model but are not enforced.
- In `stateful` mode the waiting process holds a concurrency slot. If the
  results do not arrive within `client_tools.wait_timeout_seconds` (default
  600) the process is stopped; a later request with the results starts a new
  process from the full history, so it still works.
- With the deprecated `functions` format results carry no id: every call ends
  the process, as in `stateless` mode.
- In a `tool_calls` response `usage` covers only the model's last message;
  turn totals are in the logs and in `/stats`.

## Images

Messages can contain `image_url` parts, as in the OpenAI API, with any model:

```python
r = client.chat.completions.create(model="bridge/sonnet", messages=[{"role": "user", "content": [
    {"type": "text", "text": "What is in this picture?"},
    {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}},
]}])
```

- **Sources**: `data:` URLs (base64) and `http(s)` URLs, which the bridge
  downloads. Downloads from non-public addresses (loopback, private networks,
  cloud metadata) are refused unless `images.allow_private_networks` is set;
  `images.fetch_urls: false` accepts only `data:` URLs.
- **Formats**: PNG, JPEG, GIF, WebP, recognized from the content. Up to
  `images.max_count` images per request (default 20) and `images.max_bytes`
  each (default 5 MB); the whole input, images included, must stay within
  9 MB.
- **How**: images are sent to the CLI as image blocks of a structured
  message (`--input-format stream-json`); the text refers to them as
  `[image 1]`, `[image 2]`… in order. Requests without images keep plain-text
  input.
- In a resumed session only the images of the new messages are sent; earlier
  ones are already in the session. `images.enabled: false` rejects images with
  400 `unsupported_content`.

## Generation parameters

The bridge drives the CLI, not the model API directly, so it supports the
OpenAI parameters the CLI can express, and emulates two more:

| OpenAI parameter | How | Fidelity |
| --- | --- | --- |
| `reasoning_effort` | CLI `--effort`; `none`/`minimal` become `low`, `xhigh` and `max` are accepted as an extension | Direct |
| `response_format` (`json_object`, `json_schema`) | CLI `--json-schema`: the answer is validated against the schema and returned as compact JSON in `content` | Direct, validated |
| `max_tokens`, `max_completion_tokens` | `CLAUDE_CODE_MAX_OUTPUT_TOKENS` for the process | Partial: limits every model response of the turn, not the turn total; `finish_reason` is not set to `length` |
| `stop` (string or up to 4) | The final text is cut at the first occurrence | Emulated: the model still generates the full answer; never applied to `response_format` JSON or to progress texts |
| `n` (1-8) | `n` independent processes in parallel, one choice each, also when streaming | Emulated: costs `n` times; no session resume, and client functions run stateless |
| `temperature`, `top_p`, `seed`, `logprobs`, `presence_penalty`, `frequency_penalty`, `logit_bias` | — | Accepted and ignored: the CLI has no equivalent |

If `response_format` cannot be satisfied (the CLI gives up after its retries,
or ends without structured output), the request fails with 502.

## Agents on the server

A JSON file maps a model name to an agent: model, system prompt, CLI tools,
permissions and working directory. Use it when the agent must work on the
server machine, for example on a repository.

```json
{
  "agents": {
    "reviewer": {
      "model": "opus",
      "system_prompt": "You are a strict reviewer. Do not modify files.",
      "allowed_tools": ["Read", "Grep", "Glob"],
      "cwd": "/srv/project"
    },
    "coder": {
      "model": "sonnet",
      "allowed_tools": ["Read", "Edit", "Write", "Bash(go test *)"],
      "permission_mode": "acceptEdits",
      "cwd": "/srv/project"
    }
  }
}
```

```sh
agent-bridge check-config -config agents.json   # validate and print the resolved agents
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -config agents.json
```

The client picks the agent with the `model` field:

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8787/v1", api_key="unused")
r = client.chat.completions.create(
    model="reviewer",
    messages=[{"role": "user", "content": "Review the error handling."}],
    extra_headers={"X-Session-Id": "review-42"},
)
print(r.choices[0].message.content)
```

An agent on the server can also receive client functions in `tools`: it will
use both its own tools and those.

Full reference: [docs/configuration.md](docs/configuration.md). Annotated
example: [examples/agents.example.json](examples/agents.example.json).

## Conversations and sessions

- **Without `X-Session-Id`** (OpenAI behavior): every request starts a new
  process; the history sent by the client is turned into a single prompt.
- **With `X-Session-Id`** (or the `user` field): the same key resumes the CLI
  session with `--resume`, and the CLI receives only the new messages (user
  messages and tool results) after the assistant's last answer. Faster in long
  conversations. The key, generated when missing, is always returned in the
  `X-Session-Id` response header.
- `system` messages are placed at the top of the prompt
  (`system_messages: "prompt"`, default): in a resumed session only those of
  the first turn count. With `append` or `replace` they are passed as the
  system prompt and apply to every turn.
- Answers to `tool_calls` in `stateful` mode resume the waiting process through
  the `tool_call_id`s, regardless of the session.

## Development with a subscription, production with an API key

The mode is chosen only by configuration, with the mandatory
`AGENT_BRIDGE_AUTH_MODE` variable:

| Mode | Use | Credentials |
| --- | --- | --- |
| `subscription` | Development on your own machine | Login done with `claude`; `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` must not be set |
| `api` | Production and shared instances | `ANTHROPIC_API_KEY` required (Claude Console key) |

The server refuses to start if the mode and the environment are inconsistent.
In `subscription` mode the API key variables are removed from the child
process environment anyway, and if the CLI reports using an API key the server
logs an error on every turn.

Every turn records tokens and cost (logs, optional `usage_log` file, `/stats`,
`/metrics`). **In `subscription` mode `cost_usd` is an estimate computed by the
CLI at API prices, not a charge**: it helps forecast what production with an
API key will cost.

## Codex backend

With `backend: "codex"` (or `-backend codex`, `AGENT_BRIDGE_BACKEND=codex`)
every turn runs `codex exec --json` instead of the default CLI. The API,
sessions, function calling, images, structured output and reasoning effort
work the same way; built-in models become `bridge/default` (the model
configured in Codex) and `bridge/<model>`; agents with tools use a `sandbox`
(`read-only`, `workspace-write`, `danger-full-access`) instead of
`permission_mode` and `allowed_tools`. Authentication: `codex login` in
`subscription` mode, `CODEX_API_KEY` in `api` mode.

```sh
codex login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend codex
```

Full mapping, agent fields and differences: [docs/codex.md](docs/codex.md).

## Permitted use

agent-bridge is a tool: whoever uses it remains responsible for complying with
the terms of their plan. In short:

1. **No access to credentials.** The code never reads, copies, stores, logs or
   forwards credential files or tokens. Login happens only through the CLI's
   official flow, and the subscription token is never used to call the API
   directly.
2. **One subscription, one user.** In `subscription` mode the server must serve
   only the account holder, on their own machine. The default configuration
   listens on loopback only; remote access must be enabled explicitly and is
   meant for reaching your own instance from your own laptop (e.g. a personal
   VPN), not for other users.
3. **Shared instances only with an API key.** An instance used by several
   people (e.g. a lab or a team) must run in `api` mode with the owner's key,
   without reselling or brokering costs.

References, to be checked again because **the rules may change**:

- [Claude Code: Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)
- [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)

## Security

An agent on the server with tools enabled can read, write and run commands:
whoever reaches the server has the equivalent of a restricted shell. Defaults
are closed and every opening is explicit.

- **Network.** Listens on `127.0.0.1:8787` by default. A non-loopback address
  requires `allow_remote: true` (or `-allow-remote`) **and**
  `AGENT_BRIDGE_API_KEY`. There is no built-in TLS: for remote access use a
  reverse proxy with TLS, an SSH tunnel or a VPN.
- **Client authentication.** With `AGENT_BRIDGE_API_KEY` set, `/v1/*`, `/stats`
  and `/metrics` require `Authorization: Bearer <key>` (constant-time
  comparison). `/healthz` and `/readyz` stay open.
- **CORS** is disabled; enable it with an exact list of origins
  (`cors_origins`), no wildcards.
- **`bridge/*` models** are always chat-only: no server tools, in any
  mode.
- **Client functions** are executed only by the client, never on the server.
  Calls go through an internal broker on `127.0.0.1` with a random port and a
  different token for every turn; the turn's files are private and removed at
  the end.
- **Per-request overrides** (`X-Agent-Cwd`, `X-Agent-Allowed-Tools`,
  `X-Agent-System-Prompt`) are rejected with 403 unless enabled in
  `overrides`; they can only narrow tools, the directory must be inside an
  allowed root (also after resolving symlinks), and the system prompt is only
  appended. Never applicable to `bridge/*` models.
- **`system_messages`.** With `replace` the CLI system prompt is replaced by
  the request's messages (the agent's `system_prompt` stays first); the
  permissions of the agent's tools do not change.
- **`bypassPermissions`** is rejected unless `allow_bypass_permissions: true`,
  in which case a warning is printed at startup for every affected agent.
- **Logs.** At `info` level and above, prompts and answers are never logged;
  at `debug` they are truncated to 500 characters. `sk-ant-…` keys and the
  bridge key are replaced with `[REDACTED]` in logs and errors.
- **Processes.** Every turn is a separate process, started without a shell,
  with the prompt on stdin. Timeouts, client disconnection or server shutdown
  terminate the whole process tree (process group on Unix, `taskkill /T` on
  Windows).

## Quick reference

Client examples:

- Go with the official SDK: [examples/go](examples/go/main.go)
- Python with the official SDK: [examples/python/client.py](examples/python/client.py)
- Python, function calling: [examples/python/tools.py](examples/python/tools.py)
- Python, images: [examples/python/images.py](examples/python/images.py)
- curl: [examples/curl.sh](examples/curl.sh)

Endpoints:

| Method and path | Purpose |
| --- | --- |
| `POST /v1/chat/completions` | Runs a turn (JSON or SSE) |
| `GET /v1/models`, `GET /v1/models/{id}` | Lists configured agents and `bridge/*` aliases (`bridge/<model name>` names answer the detail endpoint but are not listed); the prefix follows `builtin_prefix` |
| `GET /stats` | Per-agent statistics; `?reset=true` resets them after returning them |
| `GET /metrics` | Prometheus metrics (with `metrics: true`) |
| `GET /healthz`, `GET /readyz` | Liveness and readiness |

Request headers:

| Header | Effect |
| --- | --- |
| `X-Session-Id`, `X-Session-Reset` | Session to resume or restart |
| `X-Agent-Bridge-Tool-Mode` | `stateful` or `stateless` for client functions |
| `X-Agent-Bridge-System-Mode` | `prompt`, `append` or `replace` for `system` messages |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | Overrides, only if enabled |
| `X-Request-Id` | Identifier reported in logs and in the response |

Response headers: `X-Session-Id`, `X-Request-Id` and, in non-stream responses,
`X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`, `X-Agent-Bridge-Duration-Ms`.

Commands: `agent-bridge serve` (default), `check-config`, `doctor [-live]`,
`version`; `mcp-tools` is internal, started by the CLI for client functions.
`doctor` checks the CLI and its version, the consistency of the authentication
mode and directory permissions; with `-live` it runs a test turn on Haiku (with
the Codex backend: `codex login status`, and the model configured in Codex).

## Known limitations

- `temperature`, `top_p`, `seed`, `logprobs`, `presence_penalty`,
  `frequency_penalty` and `logit_bias` are accepted and ignored; `stop`, `n`
  and `max_tokens` are emulated or partial (see
  [Generation parameters](#generation-parameters)).
- Function calling: a binding `tool_choice`, `parallel_tool_calls: false` and
  function schemas are instructions to the model, not enforced constraints.
- Content parts other than text and `image_url` (audio, files) return 400
  `unsupported_content`; `detail` on images is accepted and ignored.
- No support for `/v1/responses`, `/v1/embeddings`, image generation, audio,
  or other providers.
- Prompts up to 9 MB (the CLI limits stdin to 10 MB).
- When streaming, the `X-Agent-Bridge-Turns`, `-Cost-USD` and `-Duration-Ms`
  response headers are not available (headers are sent before execution);
  cost and tokens are in the logs, in `/stats` and in the `usage` chunk when
  requested with `stream_options.include_usage`.
- When streaming, progress is sent as single-line SSE comments without a
  following blank line, because some SDKs treat a blank line after a comment
  as an empty event.
- A turn can take minutes: the model may work through several steps before
  answering.
- The exact values of the `apiKeySource` field of the `init` event are not
  documented: the server treats only `none` as "no API key".

## FAQ

**Does it support tools?** Yes, of two kinds: client functions through OpenAI
function calling, and agent tools on the server (files, commands, search, MCP
servers) for configured agents. See [Two ways to use it](#two-ways-to-use-it).

**Why don't `temperature` and `top_p` work?** The bridge drives the Claude
Code CLI, which has no option for them, and calling the model API directly
would bypass the CLI (and, with a subscription, its terms). What the CLI does
offer is mapped: see [Generation parameters](#generation-parameters).

**Does it accept images?** Yes, `image_url` parts with `data:` or `http(s)`
URLs, as in the OpenAI API. See [Images](#images).

**Do I need to change my OpenAI client?** No: change `base_url`
(`http://127.0.0.1:8787/v1`), `api_key` (the `AGENT_BRIDGE_API_KEY`, or any
value if it is not set) and `model`.

**Do I need a configuration file for every agent?** No, if agents are defined
in the client: instructions in `system`, tools in `tools`, a `bridge/*`
model. The file is only needed for agents that use the server's tools.

**Is the context duplicated if I send the whole history?** Without
`X-Session-Id` every request starts again from the full history, like a
stateless API. With `X-Session-Id` the session is resumed and the CLI receives
only the new messages.

**What happens if I change working directory mid-conversation?** CLI sessions
are tied to the project directory: the server starts a new one.

**Can I expose the server on the network?** Only explicitly (`allow_remote`
plus `AGENT_BRIDGE_API_KEY`), behind TLS, and in `subscription` mode only for
the account holder. For several users use `api` mode.

**Two requests on the same session?** They are serialized (`on_busy: wait`) or
the second gets 409 (`on_busy: reject`).

## Development

Layered structure with interfaces and replaceable implementations:
[docs/architecture.md](docs/architecture.md). Full technical specification,
revision 1.4: [docs/specification.md](docs/specification.md).

```sh
go test -race ./...          # uses a fake CLI binary, no credentials
./scripts/coverage.sh        # minimum coverage of the main packages
```

CI never uses the real CLI or credentials. Manual checks before every release
are in [docs/release-checklist.md](docs/release-checklist.md).

## License

agent-bridge is free software: you can redistribute it and/or modify it under
the terms of the GNU General Public License as published by the Free Software
Foundation, either version 3 of the License, or (at your option) any later
version. See [LICENSE](LICENSE).

The license covers this project's code only. Claude Code is a separate product
with its own terms, installed and used by you under your agreement with
Anthropic.
