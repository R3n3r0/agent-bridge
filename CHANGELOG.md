# Changelog

## Unreleased

First complete implementation of the specification ([docs/specification.md](docs/specification.md), rev. 1.4):

- OpenAI-compatible API: `POST /v1/chat/completions` (JSON and SSE), `GET /v1/models`, `GET /v1/models/{id}`.
- Chat-only built-in models (`bridge/opus`, `bridge/sonnet`, `bridge/haiku`, `bridge/<model>`)
  and agents configured from a JSON file.
- Multi-turn sessions with resume, in-memory or file store, TTL, LRU, per-session serialization.
- Queue with a concurrency limit, timeouts, process-tree termination on Unix and Windows.
- `subscription` and `api` modes, loopback bind by default, Bearer key, CORS, per-request overrides.
- Secret redaction, request IDs, structured logs, usage file, `/stats`, `/metrics`, `/healthz`, `/readyz`.
- Commands `serve`, `check-config`, `doctor`, `version`.
- OpenAI-compatible function calling: `tools`, `tool_choice`, `parallel_tool_calls`, `tool_calls` in responses
  (JSON and streaming), incoming `tool` messages, deprecated `functions`/`function_call` format. Functions are
  offered to the CLI through an internal MCP server (`agent-bridge mcp-tools`).
- Use from external orchestrators: stateless client functions (`client_tools.mode`, header
  `X-Agent-Bridge-Tool-Mode`), `system` messages as the system prompt (`system_messages`, header
  `X-Agent-Bridge-System-Mode`), any model as `bridge/<model>` (`builtin_any_model`).
- Layered structure with interfaces (`turn.Service`, `session.Store`/`Locker`, `runner.Executor`/`Limiter`,
  `stats.Recorder`/`Reporter`/`UsageSink`, `tools.Broker`).
- Image input: `image_url` parts with `data:` and `http(s)` URLs, sent to the CLI as image blocks; downloads
  refuse non-public addresses; `images` settings.
- Generation parameters: `reasoning_effort` (`--effort`), `response_format` with validated structured output
  (`--json-schema`), `max_tokens`/`max_completion_tokens` (`CLAUDE_CODE_MAX_OUTPUT_TOKENS`); `stop` and `n` (1-8,
  also streaming) emulated. `temperature`, `top_p` and similar remain ignored: the CLI has no equivalent.
- Neutral, configurable prefix for zero-config models (`builtin_prefix`, default `bridge/`).
- Documentation in English, with an Italian version of every document (`*.it.md`).
- Licensed under the GNU GPL v3 or later.
- Codex CLI backend (`backend: "codex"`, [docs/codex.md](docs/codex.md)): same API on top of
  `codex exec --json`, with sessions, function calling through MCP, images, structured output, reasoning
  effort, sandboxed agents and per-turn usage from the thread totals. `runner.Backend` interface for CLI
  dialects.
- Kimi Code CLI backend (`backend: "kimi"`, [docs/kimi.md](docs/kimi.md)): the bridge drives `kimi acp` as an
  Agent Client Protocol client, with sessions, function calling through per-session MCP servers, images,
  model and thinking selection, modes and permission requests answered from `allowed_tools` and
  `disallowed_tools`. `runner.Responder` lets a backend hold a dialogue on stdin.
- Docker image with the bridge and the three CLIs, `compose.yaml` with one service per backend sharing the
  logins ([docs/docker.md](docs/docker.md)); admin page (`admin: true`, `/admin`) that runs the CLIs'
  official login flows and shows their links and codes, without reading the credentials.
  `AGENT_BRIDGE_ALLOW_REMOTE` and `AGENT_BRIDGE_ADMIN` environment variables. `Makefile` to run it locally
  (`make up`, `make admin`, `make smoke`).
- Admin page rewritten in React (`web/admin`, Vite, TypeScript, Vitest tests), embedded in the binary from
  `internal/server/adminui`; the code typed in the login field is no longer cleared by the periodic refresh.
  `make ui`, `make ui-test`, `make ui-dev`.
- Release on tag `v*`: tests, binaries for Linux, macOS and Windows (amd64, arm64) with checksums, and the Docker
  image `ghcr.io/r3n3r0/agent-bridge` for `linux/amd64` and `linux/arm64`; `AGENT_BRIDGE_IMAGE` and `make pull` to
  use it with Compose.
- Kimi login region: `kimi_region` / `AGENT_BRIDGE_KIMI_REGION`, `global` (kimi.ai) by default or `mainland-cn`
  (kimi.com); the admin page and `make login-kimi` run `kimi login --region <region>`.
- Neutral names visible to clients and operators: error code `cli_error`, field `cli_session_id`.
- OpenAI-style keys (`sk-…`) are redacted from logs and errors.
- One file per interface and per implementation; large files split by responsibility.
