# Changelog

## Unreleased

First complete implementation of the specification ([docs/specification.md](docs/specification.md), rev. 1.2):

- OpenAI-compatible API: `POST /v1/chat/completions` (JSON and SSE), `GET /v1/models`, `GET /v1/models/{id}`.
- Chat-only built-in models (`claude-code/opus`, `claude-code/sonnet`, `claude-code/haiku`, `claude-code/<model>`)
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
  `X-Agent-Bridge-System-Mode`), any model as `claude-code/<model>` (`builtin_any_model`).
- Layered structure with interfaces (`turn.Service`, `session.Store`/`Locker`, `runner.Executor`/`Limiter`,
  `stats.Recorder`/`Reporter`/`UsageSink`, `tools.Broker`).
- Documentation in English, with an Italian version of every document (`*.it.md`).
