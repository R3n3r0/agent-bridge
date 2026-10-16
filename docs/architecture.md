# Architecture

**English** · [Italiano](architecture.it.md)

The code is divided into layers. Each layer depends on the **interfaces** of the
layer below, never on concrete implementations; wiring happens in a
single place, `cmd/agent-bridge/serve.go`.

```text
HTTP (internal/server)            OpenAI parsing, SSE, auth, CORS, error mapping
   │  turn.Service, stats.Reporter
   ▼
Domain (internal/turn)            session, prompt, queue, execution, resume, statistics,
   │                              suspended turns awaiting function results
   │  session.Store, session.Locker, runner.Backend, runner.Executor, runner.Limiter, stats.Recorder, tools.Broker
   ▼
Infrastructure                    replaceable implementations
   internal/session   MemoryStore, FileStore, KeyLocker
   internal/runner    StreamJSONBackend, CodexBackend, KimiBackend, CLIExecutor (real process), FIFOLimiter, per-OS process control
   internal/stats     Collector, JSONLUsageLog, DiscardUsage
   internal/tools     HTTPBroker, client functions MCP server (ServeMCP)
   internal/media     HTTPResolver: data: URLs and image downloads with address checks
   internal/login     ProcessManager: the CLIs' official login flows

web/admin                         admin page (React, TypeScript), built into internal/server/adminui
```

## Interfaces

| Interface | Package | Implementations | Responsibility |
| --- | --- | --- | --- |
| `Service` | `turn` | default service (`NewService`) | Runs a turn: `Admit` (checks before the response) and `Execute` |
| `Store` | `session` | `MemoryStore`, `FileStore` | Session persistence with TTL and LRU |
| `Locker` | `session` | `KeyLocker` | Per-session mutual exclusion |
| `Backend` | `runner` | `StreamJSONBackend`, `CodexBackend`, `KimiBackend` | CLI dialect: builds the invocation of a turn, parses its output, detects a missing resumed session |
| `Parser` | `runner` | one per backend | Turns stdout lines into the bridge's events |
| `Responder` | `runner` | `acpDialogue` (Kimi) | Optional on a `Parser`: receives the process stdin to hold a dialogue (ACP JSON-RPC) instead of a single prompt |
| `Executor` | `runner` | `CLIExecutor` | Runs a CLI process and collects its events |
| `Limiter` | `runner` | `FIFOLimiter` | Maximum concurrency and FIFO queue |
| `Recorder`, `Reporter` | `stats` | `Collector` | Recording of turns; exposure of `/stats` and `/metrics` |
| `UsageSink` | `stats` | `JSONLUsageLog`, `DiscardUsage` | Destination of usage records |
| `Broker` | `tools` | `HTTPBroker` | Routes client function calls to the turn waiting for them |
| `Handler` | `tools` | the running turn | Receives a call and blocks it until the client's result |
| `Manager` | `login` | `ProcessManager` | Runs the CLIs' official login flows for the admin page and reports their state |
| `Resolver` | `media` | `HTTPResolver` | Turns an image URL (`data:` or `http(s)`) into validated bytes |
| `processController` (internal) | `runner` | process group on Unix, `taskkill` on Windows | Termination of the process tree |

Pure functions (prompt construction in `prompt`, arguments and environment in
`runner.Args`/`runner.Env`, event parsing, configuration
validation) remain functions: they have no state or side effects to
replace.

## Function calling

```text
client ──tools──▶ server ──▶ turn.Service ──starts─▶ CLI ──MCP stdio──▶ agent-bridge mcp-tools
                                  ▲                                          │ HTTP loopback + token
                                  └──────────── tools.HTTPBroker ◀───────────┘
```

1. The turn registers a `Handler` on the broker and writes the MCP configuration
   that starts `agent-bridge mcp-tools` with the function definitions.
2. The model calls `mcp__functions__<name>`; the call reaches the broker and
   the handler, which holds it pending. After `batch_window_ms` the
   collected calls are returned to the client with `finish_reason: "tool_calls"`.
3. `stateful` mode (default): the process stays alive, holding the session
   lock and concurrency slot, and the returned `tool_call_id`s index
   the suspended turn. The next request with `tool` messages is
   attached to the turn via the ids; the results unblock the calls and the
   model continues. Once `wait_timeout_seconds` expires, the process is stopped.
4. `stateless` mode: the process is closed right after
   returning the calls, and any associated session is
   deleted.
5. In both cases, results that do not find a waiting turn
   start a new process with the full history sent by the client.

## `system` messages

With `system_messages` (or the `X-Agent-Bridge-System-Mode` header) set to `append` or
`replace`, the service moves the `system`/`developer` messages from the request
to the CLI's system prompt (`turn.applySystemMode`) and the prompt is
built without them (`prompt.Options.OmitInstructions`).

## Errors

The domain returns `*turn.Error` with a `Kind` (`KindInvalid`,
`KindSessionBusy`, `KindQueueFull`, `KindTimeout`, `KindUpstream`,
`KindCanceled`, `KindShutdown`) and a message already scrubbed of secrets. Only
the HTTP layer knows the status codes: the mapping is in
`server.toAPIError`.

## Code organization

- Every interface is declared in its own file, named after the concept
  (`session/session.go`, `runner/backend.go`, `runner/executor.go`, `runner/limiter.go`,
  `tools/broker.go`, `media/media.go`, `stats/usage.go`, `turn/service.go`).
- Every implementation lives in a separate file (`session/memory.go`,
  `session/file.go`, `runner/cli.go`, `runner/fifo.go`,
  `runner/streamjson.go`, `runner/codex.go`,
  `tools/httpbroker.go`, `media/httpresolver.go`, `stats/collector.go`,
  `stats/jsonl.go`, `turn/default.go`).
- Large implementations are split by responsibility: the default turn service
  is in `default.go` (admission, start), `liveturn.go` (running process),
  `calls.go` (client functions), `input.go` (prompt and images),
  `classify.go` (outcome mapping) and `errors.go`.
- Production files stay under about 300 lines; a file that grows beyond that
  is split by responsibility.
- The admin page follows the same rule: `api.ts` (HTTP client),
  `hooks/useLogins.ts` (state and polling) and one file per component in
  `components/`.

## Extending

- **Shared store** (e.g. Redis for multiple instances in `api` mode):
  implement `session.Store` and, if a distributed lock is needed,
  `session.Locker`; change only the wiring in `serve.go`.
- **Another usage destination** (e.g. a database): implement
  `stats.UsageSink`.
- **Remote or container execution**: implement `runner.Executor`.
- **Another CLI**: implement `runner.Backend` (and its `Parser`) in its own
  files, as `runner/codex.go`, `codex_config.go` and `codex_events.go` do for
  the Codex CLI, and add it to `runner.NewBackend`. A CLI that needs a
  dialogue on stdin implements `runner.Responder` in its parser, as
  `runner/kimi_acp*.go` do for the Kimi Code CLI over ACP.
- **Tests**: the service is tested with fakes of the interfaces, without processes or
  files (`internal/turn/service_test.go`); the server is tested end-to-end with a
  fake binary of the CLI (`testdata/fakecli`).
