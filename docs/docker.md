# Docker and logins from the admin page

**English** · [Italiano](docker.it.md)

The image contains agent-bridge and the three supported CLIs (the default
CLI, Codex and Kimi Code). `compose.yaml` starts one service per CLI, all
sharing the same home volume, so each CLI is logged in once and the login
survives restarts and image updates. Logins are done from the bridge's
admin page, which runs each CLI's official login flow.

## Quick start

```sh
cp .env.example .env            # set AGENT_BRIDGE_API_KEY to a long random string
mkdir -p workspace
docker compose up -d            # or only some services: docker compose up -d codex
```

| Service | Backend | Host address |
| --- | --- | --- |
| `claude` | default CLI | `http://127.0.0.1:8787/v1` |
| `codex` | Codex CLI | `http://127.0.0.1:8788/v1` |
| `kimi` | Kimi Code CLI | `http://127.0.0.1:8789/v1` |

Ports are published on the host's loopback only (`CLAUDE_PORT`, `CODEX_PORT`,
`KIMI_PORT` in `.env`). Inside the container the bridge listens on all
interfaces, so the API key is mandatory: clients send it as
`Authorization: Bearer <AGENT_BRIDGE_API_KEY>`.

Then open `http://127.0.0.1:8787/admin` (any of the three services works:
they share the logins), enter the API key and log in to the CLIs you use.

## Logging in from the admin page

The page lists each CLI with its state (installed, logged in or not) and a
**Log in** button that starts the CLI's own login command on the server:

| CLI | Command | What you do |
| --- | --- | --- |
| Default CLI | `auth login` | Open the link, sign in, copy the code the sign-in page shows at the end, paste it in **Send code** |
| Codex | `login --device-auth` | Open the link and enter the one-time code shown |
| Kimi Code | `login` | Open the link (the code is already in it) and confirm |

The page refreshes every two seconds while a login is running and shows the
outcome; **Cancel** stops it. A login expires after 20 minutes.

What the bridge does and does not do:

- it runs the CLI's official login command and relays what the CLI prints
  (links, one-time codes) and the code you paste back;
- it never reads, copies, stores or logs the credentials: the CLI writes
  them to its own directory in the home volume;
- it logs only that a login started and how it ended, never its output or
  the pasted code;
- the status comes from the CLIs' own commands (`auth status`,
  `login status`, `provider list`).

The same flows can also be run in a terminal:
`docker compose exec codex codex login --device-auth`,
`docker compose exec kimi kimi login`,
`docker compose exec -it claude claude auth login`.

## Admin API

Enabled with `admin: true` or `AGENT_BRIDGE_ADMIN=1` (the image sets it).

| Method and path | Effect |
| --- | --- |
| `GET /admin` | The page (static, no data) |
| `GET /admin/api/logins` | State of every CLI and of its current or last login |
| `POST /admin/api/logins/{cli}` | Starts the login of `claude`, `codex` or `kimi` (or returns the running one) |
| `POST /admin/api/logins/{cli}/input` | Sends `{"text": "..."}` (one line) to the running login |
| `DELETE /admin/api/logins/{cli}` | Cancels the running login |

Every API call requires the Bearer key when one is set; the calls that change
state also require the header `X-Agent-Bridge-Admin: 1`, which a browser
cannot send from another site without a CORS preflight the bridge never
grants.

## Volumes and configuration

| Path | Volume | Content |
| --- | --- | --- |
| `/home/bridge` | `home` | CLI logins and settings (`.claude`, `.codex`, `.kimi-code`…) |
| `/work` | `./workspace` | Working directory for agents with tools |
| `/data` | `data` | Free for a sessions file or a usage log |

To use a configuration file, mount it and point to it, for example in a
`compose.override.yaml`:

```yaml
services:
  codex:
    volumes:
      - ./config/codex.json:/config/codex.json:ro
    environment:
      AGENT_BRIDGE_CONFIG: /config/codex.json
```

Agents with tools should use `cwd` inside `/work`. With the Codex backend the
container is already a sandbox: agents with tools may need
`sandbox: "danger-full-access"` (with `allow_bypass_permissions: true`) if
the CLI's own sandbox cannot start inside the container.

## Building

`docker compose build` (or `docker build -t agent-bridge:local .`). Build
arguments: `CLAUDE_CODE_VERSION`, `CODEX_VERSION`, `KIMI_CODE_VERSION`
(default `latest`), `GO_VERSION`, `NODE_VERSION`, and `BASE_REGISTRY` to pull
the base images from a mirror of the Docker Hub official images (for example
`mirror.gcr.io/library`). Rebuild to update the CLIs; logins stay in the
volume.

## Permitted use

A container in `subscription` mode is still a single-user instance: run it on
your own machine for yourself, with ports on loopback. Shared instances must
use `api` mode with API keys (`ANTHROPIC_API_KEY`, `CODEX_API_KEY`,
`KIMI_MODEL_NAME` and `KIMI_MODEL_API_KEY` in `.env`).
