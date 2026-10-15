# Checklist di rilascio

Da eseguire a mano prima di ogni release, con la CLI reale **alla versione
minima supportata** (vedi `runner.MinCLIVersion`) **e all'ultima disponibile**.
Annotare versione della CLI, sistema operativo ed esito di ogni voce.

## Preparazione

- [ ] CI verde sul commit da rilasciare (Linux, macOS, Windows; Go 1.22 e stabile).
- [ ] `CHANGELOG.md` aggiornato.
- [ ] `grep -rniE '\.credentials|keychain|oauth' --include='*.go' .` non trova nulla.
- [ ] `go list -deps ./cmd/agent-bridge` contiene solo libreria standard e pacchetti del modulo.

## Modalità `subscription`

Ambiente senza `ANTHROPIC_API_KEY` né `ANTHROPIC_AUTH_TOKEN`, login fatto con la CLI.

- [ ] `agent-bridge doctor -live` termina con codice 0.
- [ ] Richiesta non-stream a `claude-code/haiku`: 200, `usage` valorizzato.
- [ ] Richiesta stream a `claude-code/sonnet` con `include_usage`: sequenza SSE completa, `[DONE]`.
- [ ] Il log del turno non riporta errori sulla fonte delle credenziali.
- [ ] Modello built-in a cui si chiede di leggere un file: nessun tool usato (log `debug`).
- [ ] Agente con tool che modifica un file in un repository di prova: modifica presente su disco.
- [ ] Ripresa di sessione: secondo turno con la stessa `X-Session-Id` ricorda il primo.
- [ ] Interruzione del client durante uno stream: nessun processo `claude` residuo.
- [ ] Avvio con `-listen 0.0.0.0:8787` senza `-allow-remote`: il server rifiuta di partire.

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
