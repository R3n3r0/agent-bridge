# Changelog

## Unreleased

Prima implementazione completa delle specifiche v1.0:

- API compatibile OpenAI: `POST /v1/chat/completions` (JSON e SSE), `GET /v1/models`, `GET /v1/models/{id}`.
- Modelli built-in solo chat e agenti configurabili da file JSON.
- Sessioni multi-turno con ripresa, store in memoria o su file, TTL, LRU, serializzazione per sessione.
- Coda con limite di concorrenza, timeout, terminazione dell'albero dei processi su Unix e Windows.
- Modalità `subscription` e `api`, bind loopback di default, chiave Bearer, CORS, override per richiesta.
- Redazione dei segreti, request ID, log strutturati, file di utilizzo, `/stats`, `/metrics`, `/healthz`, `/readyz`.
- Comandi `serve`, `check-config`, `doctor`, `version`.
- Function calling compatibile OpenAI: `tools`, `tool_choice`, `parallel_tool_calls`, `tool_calls` in risposta
  (JSON e streaming), messaggi `tool` in ingresso, formato deprecato `functions`/`function_call`. Le funzioni
  sono offerte alla CLI tramite un server MCP interno (`agent-bridge mcp-tools`) e il processo resta in attesa
  dei risultati del client tra una richiesta e l'altra.
- Uso da orchestratori esterni: funzioni in modalità senza stato (`client_tools.mode`, header
  `X-Agent-Bridge-Tool-Mode`), messaggi `system` come system prompt (`system_messages`, header
  `X-Agent-Bridge-System-Mode`), qualsiasi modello come `claude-code/<modello>` (`builtin_any_model`).
- Struttura a livelli con interfacce (`turn.Service`, `session.Store`/`Locker`, `runner.Executor`/`Limiter`,
  `stats.Recorder`/`Reporter`/`UsageSink`, `tools.Broker`).
