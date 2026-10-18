# Checklist di rilascio

[English](release-checklist.md) · **Italiano**

Da eseguire a mano prima di ogni release, con la CLI reale **alla versione
minima supportata** (vedi `runner.MinCLIVersion`) **e all'ultima disponibile**.
Annotare versione della CLI, sistema operativo ed esito di ogni voce.

## Preparazione

- [ ] Workflow `ci`, avviato a mano (Actions → ci → Run workflow), verde sul commit da rilasciare (Linux, macOS, Windows; Go 1.22 e stabile).
- [ ] `CHANGELOG.md` aggiornato.
- [ ] `grep -rniE '\.credentials|keychain|oauth' --include='*.go' .` non trova nulla.
- [ ] `go list -deps ./cmd/agent-bridge` contiene solo libreria standard e pacchetti del modulo.

## Modalità `subscription`

Ambiente senza `ANTHROPIC_API_KEY` né `ANTHROPIC_AUTH_TOKEN`, login fatto con la CLI.

- [ ] `agent-bridge doctor -live` termina con codice 0.
- [ ] Richiesta non-stream a `bridge/haiku`: 200, `usage` valorizzato.
- [ ] Richiesta stream a `bridge/sonnet` con `include_usage`: sequenza SSE completa, `[DONE]`.
- [ ] Il log del turno non riporta errori sulla fonte delle credenziali.
- [ ] Modello built-in a cui si chiede di leggere un file: nessun tool usato (log `debug`).
- [ ] Agente con tool che modifica un file in un repository di prova: modifica presente su disco.
- [ ] Ripresa di sessione: secondo turno con la stessa `X-Session-Id` ricorda il primo.
- [ ] Interruzione del client durante uno stream: nessun processo `claude` residuo.
- [ ] Avvio con `-listen 0.0.0.0:8787` senza `-allow-remote`: il server rifiuta di partire.

## Function calling

- [ ] Ciclo completo con `examples/python/tools.py` su `bridge/sonnet`: i tool MCP del client sono disponibili anche con `--tools ""` (modelli solo chat).
- [ ] Stesso ciclo in modalità `stateful` (default): la richiesta con i risultati riprende lo stesso processo.
- [ ] Stesso ciclo con `X-Agent-Bridge-Tool-Mode: stateless`: un processo per passo, risposta finale corretta.
- [ ] Chiamate parallele: due funzioni chiamate nello stesso passo arrivano nella stessa risposta.
- [ ] Agente configurato con un proprio `mcp_config` e funzioni del client: entrambi i server MCP disponibili (`--mcp-config` ripetuto).
- [ ] Risultati inviati dopo più di 2 minuti in modalità `stateful`: la chiamata non scade prima di `wait_timeout_seconds` (`MCP_TOOL_TIMEOUT`).
- [ ] `X-Agent-Bridge-System-Mode: replace`: il comportamento segue il messaggio `system` della richiesta.
- [ ] Modello `bridge/<nome completo>` accettato dalla CLI.

## Parametri di generazione

- [ ] `reasoning_effort: "low"` e `"high"`: `--effort` accettato dalla CLI per il modello scelto.
- [ ] `response_format` `json_schema`: la risposta è il JSON validato; uno schema impossibile restituisce 502.
- [ ] `max_completion_tokens: 50`: la risposta è breve (`CLAUDE_CODE_MAX_OUTPUT_TOKENS` rispettato).
- [ ] `n: 2` con e senza `stream`: due scelte indipendenti.

## Immagini

- [ ] `examples/python/images.py` con un PNG e un JPEG: la risposta descrive l'immagine (ingresso stream-json con blocchi immagine accettato dalla CLI).
- [ ] Immagine passata come URL `https`: scaricata e descritta.
- [ ] Due immagini nello stesso messaggio: la risposta le cita come image 1 e image 2.
- [ ] Immagine vicina a 5 MB: accettata (limite di stdin con ingresso strutturato).
- [ ] Secondo turno con la stessa `X-Session-Id` e una nuova immagine: `--resume` con ingresso strutturato funziona.

## Backend Codex

Con `backend: "codex"`, la CLI codex alla versione `0.162.0` (`runner.CodexMinVersion`) e all'ultima disponibile, con login fatto con `codex login`.

- [ ] `agent-bridge doctor -live -backend codex` termina con codice 0 (`codex login status` ok).
- [ ] `bridge/default` e `bridge/<modello>`: 200, `usage` valorizzato; un secondo turno con lo stesso `X-Session-Id` ricorda il primo e riporta solo i consumi di quel turno.
- [ ] Modello solo chat a cui si chiede di eseguire un comando: nessun comando eseguito (il log `debug` non mostra uso di strumenti).
- [ ] Agente con `sandbox: workspace-write` che modifica un file in un repository di prova: modifica presente su disco.
- [ ] Ciclo di function calling con `examples/python/tools.py`, `stateful` e `stateless`.
- [ ] Immagine: la risposta la descrive; `json_schema` con uno schema strict: JSON valido; `reasoning_effort: "high"` accettato.
- [ ] Modalità `api` con `CODEX_API_KEY`: le stesse richieste funzionano.

## Backend Kimi

Con `backend: "kimi"`, la CLI Kimi Code alla versione `2.2.0` (`runner.KimiMinVersion`) e all'ultima disponibile, con login fatto con `kimi login`.

- [ ] `agent-bridge doctor -live -backend kimi` termina con codice 0.
- [ ] `bridge/default` e `bridge/<modello>`: 200; un secondo turno con lo stesso `X-Session-Id` ricorda il primo.
- [ ] Modello solo chat a cui si chiede di eseguire un comando: la richiesta di permesso è rifiutata (log `debug` `refused Bash`).
- [ ] Agente con `allowed_tools: ["Bash"]` che crea un file: approvato e presente su disco.
- [ ] Ciclo di function calling con `examples/python/tools.py`, `stateful` e `stateless`.
- [ ] Immagine descritta; `json_schema`: JSON valido; `reasoning_effort: "high"` imposta il livello di thinking.
- [ ] Modalità `api` con `KIMI_MODEL_NAME` e `KIMI_MODEL_API_KEY`: le stesse richieste funzionano.

## Docker

- [ ] `docker compose build` e `docker compose up -d` con `.env` da `.env.example`: tre servizi in esecuzione.
- [ ] `http://127.0.0.1:8787/admin` con la chiave API: le tre CLI installate e senza login.
- [ ] Login di ogni CLI dalla pagina con un account reale; lo stato diventa "logged in" e sopravvive a `docker compose down` e `up` (volume `home`).
- [ ] Una richiesta a ogni servizio (`8787`, `8788`, `8789`) risponde.
- [ ] `docker compose exec codex agent-bridge doctor -live` termina con codice 0.

## Modalità `api`

`AGENT_BRIDGE_AUTH_MODE=api` e `ANTHROPIC_API_KEY` impostata.

- [ ] `agent-bridge doctor -live` termina con codice 0.
- [ ] Stesse prove di richiesta, agente con tool e ripresa di sessione.
- [ ] `/stats` riporta costi coerenti con la Console.

## Per sistema operativo

| Prova | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `doctor -live` | [ ] | [ ] | [ ] |
| Timeout di un agente che lancia un comando lungo: processo e figli terminati | [ ] | [ ] | [ ] |
| Arresto con Ctrl+C durante un turno: attesa e poi terminazione | [ ] | [ ] | [ ] |

## Rilascio

- [ ] `internal/server/adminui` ricostruita con `make ui` e committata se `web/admin` è cambiata (altrimenti il workflow fallisce).
- [ ] Tag `vX.Y.Z` creato e pubblicato (`git tag vX.Y.Z && git push origin vX.Y.Z`); workflow `release` verde: test, binari, immagine. Un suffisso (`vX.Y.Z-rc.1`) produce una pre-release senza il tag `latest` dell'immagine.
- [ ] Immagine `ghcr.io/r3n3r0/agent-bridge:X.Y.Z` (e `latest`) pubblicata per `linux/amd64` e `linux/arm64`; alla prima release, pacchetto reso pubblico (GitHub → Packages → agent-bridge → Package settings → Change visibility).
- [ ] Archivi e `checksums.txt` presenti per linux, darwin e windows su amd64 e arm64.
- [ ] `go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@vX.Y.Z` funziona.
