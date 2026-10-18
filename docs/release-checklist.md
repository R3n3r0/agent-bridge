# Release checklist

**English** · [Italiano](release-checklist.it.md)

To be run by hand before every release, with the real CLI **at the minimum
supported version** (see `runner.MinCLIVersion`) **and at the latest available**.
Record the CLI version, operating system and outcome of each item.

## Preparation

- [ ] The `ci` workflow, started by hand (Actions → ci → Run workflow), green on the commit to be released (Linux, macOS, Windows; Go 1.22 and stable).
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

## Kimi backend

With `backend: "kimi"`, the Kimi Code CLI at version `2.2.0` (`runner.KimiMinVersion`) and the latest one, logged in with `kimi login`.

- [ ] `agent-bridge doctor -live -backend kimi` exits with code 0.
- [ ] `bridge/default` and `bridge/<model>`: 200; second turn with the same `X-Session-Id` remembers the first.
- [ ] Chat-only model asked to run a command: the permission request is refused (`debug` log `refused Bash`).
- [ ] Agent with `allowed_tools: ["Bash"]` that creates a file: approved and present on disk.
- [ ] Function calling loop with `examples/python/tools.py`, `stateful` and `stateless`.
- [ ] Image described; `json_schema`: valid JSON; `reasoning_effort: "high"` sets the thinking level.
- [ ] `api` mode with `KIMI_MODEL_NAME` and `KIMI_MODEL_API_KEY`: same requests work.

## Docker

- [ ] `docker compose build` and `docker compose up -d` with `.env` from `.env.example`: three services running.
- [ ] `http://127.0.0.1:8787/admin` with the API key: the three CLIs installed and not logged in.
- [ ] Login of each CLI from the page with a real account; the state becomes "logged in" and survives `docker compose down` and `up` (volume `home`).
- [ ] A request to each service (`8787`, `8788`, `8789`) answers.
- [ ] `docker compose exec codex agent-bridge doctor -live` exits with code 0.

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

- [ ] `internal/server/adminui` rebuilt with `make ui` and committed if `web/admin` changed (the workflow fails otherwise).
- [ ] Tag `vX.Y.Z` created and pushed (`git tag vX.Y.Z && git push origin vX.Y.Z`); `release` workflow green: tests, binaries, image. A suffix (`vX.Y.Z-rc.1`) makes a pre-release without the `latest` image tag.
- [ ] Image `ghcr.io/r3n3r0/agent-bridge:X.Y.Z` (and `latest`) published for `linux/amd64` and `linux/arm64`; on the first release, the package made public (GitHub → Packages → agent-bridge → Package settings → Change visibility).
- [ ] Archives and `checksums.txt` present for linux, darwin and windows on amd64 and arm64.
- [ ] `go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@vX.Y.Z` works.
