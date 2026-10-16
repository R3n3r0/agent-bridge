# Run agent-bridge with Docker Compose on this machine.
#
#   make up        build if needed, start the three services, print the addresses
#   make admin     open the login page
#   make help      every target
#
# SERVICES selects the services (default: all), e.g. make up SERVICES=codex

SHELL := /bin/sh
COMPOSE ?= docker compose
SERVICES ?=
ENV_FILE := .env

# Ports from .env, with the defaults of compose.yaml.
port = $(or $(shell sed -n 's/^$(1)=//p' $(ENV_FILE) 2>/dev/null | tail -n 1),$(2))
CLAUDE_PORT = $(call port,CLAUDE_PORT,8787)
CODEX_PORT = $(call port,CODEX_PORT,8788)
KIMI_PORT = $(call port,KIMI_PORT,8789)
API_KEY = $(shell sed -n 's/^AGENT_BRIDGE_API_KEY=//p' $(ENV_FILE) 2>/dev/null | tail -n 1)

.DEFAULT_GOAL := help
.PHONY: help env build up down restart ps logs admin key urls doctor smoke \
	login-claude login-codex login-kimi shell update reset test

help: ## Show this help
	@echo "agent-bridge with Docker Compose"
	@echo
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  make %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)
	@echo
	@echo "  SERVICES=claude|codex|kimi limits up, down, logs, restart (default: all)"

env: $(ENV_FILE) ## Create .env with a random API key if it does not exist

$(ENV_FILE):
	@cp .env.example $(ENV_FILE)
	@key=$$(openssl rand -hex 24 2>/dev/null || head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n'); \
	sed "s/^AGENT_BRIDGE_API_KEY=.*/AGENT_BRIDGE_API_KEY=$$key/" $(ENV_FILE) > $(ENV_FILE).tmp && mv $(ENV_FILE).tmp $(ENV_FILE)
	@echo "Created $(ENV_FILE) with a random AGENT_BRIDGE_API_KEY (see: make key)"

workspace:
	@mkdir -p workspace

build: env ## Build the image (the CLIs at their latest versions)
	$(COMPOSE) build

up: env workspace ## Start the services in the background
	$(COMPOSE) up -d $(SERVICES)
	@$(MAKE) --no-print-directory urls

down: ## Stop the services (logins are kept)
	$(COMPOSE) down $(SERVICES)

restart: ## Restart the services
	$(COMPOSE) restart $(SERVICES)

ps: ## Show the services
	$(COMPOSE) ps

logs: ## Follow the logs
	$(COMPOSE) logs -f --tail=100 $(SERVICES)

urls: ## Print the addresses and the API key
	@echo
	@echo "  Login page:  http://127.0.0.1:$(CLAUDE_PORT)/admin"
	@echo "  API key:     $(API_KEY)"
	@echo
	@echo "  claude  http://127.0.0.1:$(CLAUDE_PORT)/v1   model bridge/sonnet"
	@echo "  codex   http://127.0.0.1:$(CODEX_PORT)/v1   model bridge/default"
	@echo "  kimi    http://127.0.0.1:$(KIMI_PORT)/v1   model bridge/default"
	@echo
	@echo "  Next: make admin (log in to the CLIs), then make smoke"

key: ## Print the API key
	@echo "$(API_KEY)"

admin: ## Open the login page in the browser
	@url="http://127.0.0.1:$(CLAUDE_PORT)/admin"; \
	if command -v open >/dev/null 2>&1; then open "$$url"; \
	elif command -v xdg-open >/dev/null 2>&1; then xdg-open "$$url"; \
	else echo "Open $$url"; fi; \
	echo "API key: $(API_KEY)"

doctor: ## Check every service (CLI, version, authentication)
	@for s in claude codex kimi; do \
		echo "== $$s"; $(COMPOSE) exec -T $$s agent-bridge doctor || true; \
	done

smoke: ## Send one chat request to every service
	@for pair in claude:$(CLAUDE_PORT):bridge/sonnet codex:$(CODEX_PORT):bridge/default kimi:$(KIMI_PORT):bridge/default; do \
		name=$${pair%%:*}; rest=$${pair#*:}; port=$${rest%%:*}; model=$${rest#*:}; \
		echo "== $$name ($$model)"; \
		curl -sS --max-time 300 "http://127.0.0.1:$$port/v1/chat/completions" \
			-H "Authorization: Bearer $(API_KEY)" -H "Content-Type: application/json" \
			-d "{\"model\":\"$$model\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with the single word OK.\"}]}"; \
		echo; \
	done

login-claude: ## Log in to the default CLI from the terminal instead of the page
	$(COMPOSE) exec claude claude auth login

login-codex: ## Log in to Codex from the terminal
	$(COMPOSE) exec codex codex login --device-auth

login-kimi: ## Log in to Kimi Code from the terminal
	$(COMPOSE) exec kimi kimi login

shell: ## Open a shell in a service (claude, or SERVICES=codex)
	$(COMPOSE) exec $(or $(SERVICES),claude) bash

update: env ## Rebuild with the latest CLIs and restart
	$(COMPOSE) build --pull --no-cache
	$(COMPOSE) up -d $(SERVICES)

reset: ## Stop everything and DELETE the volumes, logins included
	@printf "This deletes the logins and the data volume. Type yes to continue: "; \
	read answer; [ "$$answer" = yes ] || { echo "Canceled."; exit 1; }
	$(COMPOSE) down -v

test: ## Run the Go tests (needs Go on this machine)
	go test ./...
