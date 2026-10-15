# agent-bridge with the three supported CLIs. Logins are done from the admin
# page (/admin) or with "docker compose exec"; the CLIs keep their
# credentials in the /home/bridge volume.

# BASE_REGISTRY can point to a mirror of the Docker Hub official images.
ARG BASE_REGISTRY=docker.io/library
ARG GO_VERSION=1.24
ARG NODE_VERSION=22

FROM ${BASE_REGISTRY}/golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/r3n3r0/agent-bridge/internal/version.Version=${VERSION}" \
    -o /out/agent-bridge ./cmd/agent-bridge

FROM ${BASE_REGISTRY}/node:${NODE_VERSION}-bookworm-slim
# script(1) from util-linux gives the login flows that need it a terminal.
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates git util-linux tini ripgrep \
 && rm -rf /var/lib/apt/lists/*
ARG CLAUDE_CODE_VERSION=latest
ARG CODEX_VERSION=latest
ARG KIMI_CODE_VERSION=latest
RUN npm install -g \
      "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
      "@openai/codex@${CODEX_VERSION}" \
      "@moonshot-ai/kimi-code@${KIMI_CODE_VERSION}" \
 && npm cache clean --force
RUN useradd --create-home --uid 10001 bridge \
 && mkdir -p /work /data \
 && chown bridge:bridge /work /data
COPY --from=build /out/agent-bridge /usr/local/bin/agent-bridge
USER bridge
WORKDIR /work
ENV HOME=/home/bridge \
    AGENT_BRIDGE_LISTEN=0.0.0.0:8787 \
    AGENT_BRIDGE_ALLOW_REMOTE=1 \
    AGENT_BRIDGE_ADMIN=1
VOLUME ["/home/bridge", "/work", "/data"]
EXPOSE 8787
ENTRYPOINT ["tini", "--", "agent-bridge"]
CMD ["serve"]
