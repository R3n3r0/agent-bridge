# Release checklist

**English** · [Italiano](release-checklist.it.md)

To be run by hand before every release, with the real CLI **at the minimum
supported version** (see `runner.MinCLIVersion`) **and at the latest available**.
Record the CLI version, operating system and outcome of each item.

## Preparation

- [ ] CI green on the commit to be released (Linux, macOS, Windows; Go 1.22 and stable).
- [ ] `CHANGELOG.md` updated.
- [ ] `grep -rniE '\.credentials|keychain|oauth' --include='*.go' .` finds nothing.
- [ ] `go list -deps ./cmd/agent-bridge` contains only the standard library and packages from the module.

## `subscription` mode

Environment without `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN`, logged in with the CLI.

- [ ] `agent-bridge doctor -live` exits with code 0.
- [ ] Non-stream request to `bridge/haiku`: 200, `usage` populated.
- [ ] Stream request to `bridge/sonnet` with `include_usage`: complete SSE sequence, `[DONE]`.
- [ ] The turn log reports no errors about the credential source.
- [ ] Built-in model asked to read a file: no tool used (`debug` log).
- [ ] Agent with tools that modifies a file in a test repository: change present on disk.
- [ ] Session resume: second turn with the same `X-Session-Id` remembers the first.
- [ ] Client interruption during a stream: no leftover `claude` process.
- [ ] Startup with `-listen 0.0.0.0:8787` without `-allow-remote`: the server refuses to start.

## Function calling

- [ ] Full loop with `examples/python/tools.py` on `bridge/sonnet`: the client's MCP tools are available even with `--tools ""` (chat-only models).
- [ ] Same loop in `stateful` mode (default): the request with the results resumes the same process.
- [ ] Same loop with `X-Agent-Bridge-Tool-Mode: stateless`: one process per step, correct final response.
- [ ] Parallel calls: two functions called in the same step arrive in the same response.
- [ ] Agent configured with its own `mcp_config` and client functions: both MCP servers available (`--mcp-config` repeated).
- [ ] Results sent after more than 2 minutes in `stateful` mode: the call does not time out before `wait_timeout_seconds` (`MCP_TOOL_TIMEOUT`).
- [ ] `X-Agent-Bridge-System-Mode: replace`: the behavior follows the request's `system` message.
- [ ] Model `bridge/<full name>` accepted by the CLI.

## Generation parameters

- [ ] `reasoning_effort: "low"` and `"high"`: `--effort` accepted by the CLI for the chosen model.
- [ ] `response_format` `json_schema`: the answer is the validated JSON; an impossible schema returns 502.
- [ ] `max_completion_tokens: 50`: the answer is short (`CLAUDE_CODE_MAX_OUTPUT_TOKENS` honored).
- [ ] `n: 2` with and without `stream`: two independent choices.

## Images

- [ ] `examples/python/images.py` with a PNG and a JPEG: the answer describes the image (stream-json input with image blocks accepted by the CLI).
- [ ] Image given as an `https` URL: downloaded and described.
- [ ] Two images in one message: the answer refers to them as image 1 and image 2.
- [ ] Image close to 5 MB: accepted (stdin limit with structured input).
- [ ] Second turn with the same `X-Session-Id` and a new image: `--resume` with structured input works.

## Codex backend

With `backend: "codex"`, the codex CLI at version `0.162.0` (`runner.CodexMinVersion`) and the latest one, logged in with `codex login`.

- [ ] `agent-bridge doctor -live -backend codex` exits with code 0 (`codex login status` ok).
- [ ] `bridge/default` and `bridge/<model>`: 200, `usage` populated; second turn with the same `X-Session-Id` remembers the first and reports only that turn's usage.
- [ ] Chat-only model asked to run a command: no command executed (`debug` log shows no tool use).
- [ ] Agent with `sandbox: workspace-write` that edits a file in a test repository: change present on disk.
- [ ] Function calling loop with `examples/python/tools.py`, `stateful` and `stateless`.
- [ ] Image: the answer describes it; `json_schema` with a strict schema: valid JSON; `reasoning_effort: "high"` accepted.
- [ ] `api` mode with `CODEX_API_KEY`: same requests work.

## `api` mode

`AGENT_BRIDGE_AUTH_MODE=api` and `ANTHROPIC_API_KEY` set.

- [ ] `agent-bridge doctor -live` exits with code 0.
- [ ] Same request, agent-with-tools and session-resume tests.
- [ ] `/stats` reports costs consistent with the Console.

## Per operating system

| Test | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `doctor -live` | [ ] | [ ] | [ ] |
| Timeout of an agent that runs a long command: process and children terminated | [ ] | [ ] | [ ] |
| Shutdown with Ctrl+C during a turn: wait and then termination | [ ] | [ ] | [ ] |

## Release

- [ ] Tag `vX.Y.Z` created and pushed; `release` workflow green.
- [ ] Archives and `checksums.txt` present for linux, darwin and windows on amd64 and arm64.
- [ ] `go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@vX.Y.Z` works.
