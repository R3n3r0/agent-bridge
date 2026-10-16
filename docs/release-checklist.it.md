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

- [ ] Tag `vX.Y.Z` creato e pubblicato; workflow `release` verde.
- [ ] Archivi e `checksums.txt` presenti per linux, darwin e windows su amd64 e arm64.
- [ ] `go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@vX.Y.Z` funziona.
