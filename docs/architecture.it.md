# Architettura

[English](architecture.md) · **Italiano**

Il codice è diviso in livelli. Ogni livello dipende dalle **interfacce** del
livello sotto, mai dalle implementazioni concrete; il cablaggio avviene in un
solo punto, `cmd/agent-bridge/serve.go`.

```text
HTTP (internal/server)            parsing OpenAI, SSE, auth, CORS, mappatura errori
   │  turn.Service, stats.Reporter
   ▼
Dominio (internal/turn)           sessione, prompt, coda, esecuzione, ripresa, statistiche,
   │                              turni sospesi in attesa dei risultati delle funzioni
   │  session.Store, session.Locker, runner.Backend, runner.Executor, runner.Limiter, stats.Recorder, tools.Broker
   ▼
Infrastruttura                    implementazioni sostituibili
   internal/session   MemoryStore, FileStore, KeyLocker
   internal/runner    StreamJSONBackend, CodexBackend, KimiBackend, CLIExecutor (processo reale), FIFOLimiter, controllo processi per OS
   internal/stats     Collector, JSONLUsageLog, DiscardUsage
   internal/tools     HTTPBroker, server MCP delle funzioni del client (ServeMCP)
   internal/media     HTTPResolver: URL data: e download di immagini con controllo degli indirizzi
   internal/login     ProcessManager: i flussi di login ufficiali delle CLI

web/admin                         pagina di amministrazione (React, TypeScript), compilata in internal/server/adminui
```

## Interfacce

| Interfaccia | Pacchetto | Implementazioni | Responsabilità |
| --- | --- | --- | --- |
| `Service` | `turn` | servizio di default (`NewService`) | Esegue un turno: `Admit` (controlli prima della risposta) ed `Execute` |
| `Store` | `session` | `MemoryStore`, `FileStore` | Persistenza delle sessioni con TTL e LRU |
| `Locker` | `session` | `KeyLocker` | Esclusione mutua per sessione |
| `Backend` | `runner` | `StreamJSONBackend`, `CodexBackend`, `KimiBackend` | Dialetto della CLI: costruisce l'invocazione di un turno, ne interpreta l'output, riconosce una sessione ripresa che non esiste più |
| `Parser` | `runner` | uno per backend | Trasforma le righe di stdout negli eventi del bridge |
| `Responder` | `runner` | `acpDialogue` (Kimi) | Facoltativa su un `Parser`: riceve lo stdin del processo per tenere un dialogo (JSON-RPC ACP) invece di un prompt unico |
| `Executor` | `runner` | `CLIExecutor` | Esegue un processo della CLI e ne raccoglie gli eventi |
| `Limiter` | `runner` | `FIFOLimiter` | Concorrenza massima e coda FIFO |
| `Recorder`, `Reporter` | `stats` | `Collector` | Registrazione dei turni; esposizione di `/stats` e `/metrics` |
| `UsageSink` | `stats` | `JSONLUsageLog`, `DiscardUsage` | Destinazione dei record di utilizzo |
| `Broker` | `tools` | `HTTPBroker` | Instrada le chiamate alle funzioni del client verso il turno che le attende |
| `Handler` | `tools` | il turno in esecuzione | Riceve una chiamata e la blocca fino al risultato del client |
| `Manager` | `login` | `ProcessManager` | Esegue i flussi di login ufficiali delle CLI per la pagina di amministrazione e ne riporta lo stato |
| `Resolver` | `media` | `HTTPResolver` | Trasforma l'URL di un'immagine (`data:` o `http(s)`) in byte validati |
| `processController` (interna) | `runner` | process group su Unix, `taskkill` su Windows | Terminazione dell'albero dei processi |

Le funzioni pure (costruzione del prompt in `prompt`, argomenti e ambiente in
`runner.Args`/`runner.Env`, parsing degli eventi, validazione della
configurazione) restano funzioni: non hanno stato né effetti collaterali da
sostituire.

## Function calling

```text
client ──tools──▶ server ──▶ turn.Service ──avvia──▶ CLI ──MCP stdio──▶ agent-bridge mcp-tools
                                  ▲                                          │ HTTP loopback + token
                                  └──────────── tools.HTTPBroker ◀───────────┘
```

1. Il turno registra un `Handler` sul broker e scrive la configurazione MCP
   che avvia `agent-bridge mcp-tools` con le definizioni delle funzioni.
2. Il modello chiama `mcp__functions__<nome>`; la chiamata arriva al broker e
   all'handler, che la tiene in sospeso. Dopo `batch_window_ms` le chiamate
   raccolte tornano al client con `finish_reason: "tool_calls"`.
3. Modalità `stateful` (default): il processo resta vivo, con lock di
   sessione e slot di concorrenza, e i `tool_call_id` restituiti indicizzano
   il turno sospeso. La richiesta successiva con messaggi `tool` viene
   agganciata al turno tramite gli id; i risultati sbloccano le chiamate e il
   modello continua. Scaduto `wait_timeout_seconds` il processo viene fermato.
4. Modalità `stateless`: il processo viene chiuso subito dopo aver
   restituito le chiamate, e la sessione eventualmente associata viene
   cancellata.
5. In entrambi i casi, risultati che non trovano un turno in attesa fanno
   partire un processo nuovo con la cronologia completa inviata dal client.

## Messaggi `system`

Con `system_messages` (o l'header `X-Agent-Bridge-System-Mode`) a `append` o
`replace`, il servizio sposta i messaggi `system`/`developer` dalla richiesta
al system prompt della CLI (`turn.applySystemMode`) e il prompt viene
costruito senza di essi (`prompt.Options.OmitInstructions`).

## Errori

Il dominio restituisce `*turn.Error` con un `Kind` (`KindInvalid`,
`KindSessionBusy`, `KindQueueFull`, `KindTimeout`, `KindUpstream`,
`KindCanceled`, `KindShutdown`) e un messaggio già ripulito dai segreti. Solo
il livello HTTP conosce i codici di stato: la mappatura è in
`server.toAPIError`.

## Organizzazione del codice

- Ogni interfaccia è dichiarata in un file proprio, con il nome del concetto
  (`session/session.go`, `runner/backend.go`, `runner/executor.go`, `runner/limiter.go`,
  `tools/broker.go`, `media/media.go`, `stats/usage.go`, `turn/service.go`).
- Ogni implementazione sta in un file separato (`session/memory.go`,
  `session/file.go`, `runner/cli.go`, `runner/fifo.go`,
  `runner/streamjson.go`, `runner/codex.go`,
  `tools/httpbroker.go`, `media/httpresolver.go`, `stats/collector.go`,
  `stats/jsonl.go`, `turn/default.go`).
- Le implementazioni grandi sono divise per responsabilità: il servizio dei
  turni predefinito è in `default.go` (ammissione, avvio), `liveturn.go`
  (processo in esecuzione), `calls.go` (funzioni del client), `input.go`
  (prompt e immagini), `classify.go` (mappatura dell'esito) ed `errors.go`.
- I file di produzione restano sotto le 300 righe circa; un file che cresce
  oltre si divide per responsabilità.
- La pagina di amministrazione segue la stessa regola: `api.ts` (client
  HTTP), `hooks/useLogins.ts` (stato e polling) e un file per componente in
  `components/`.

## Estendere

- **Store condiviso** (es. Redis per più istanze in modalità `api`):
  implementare `session.Store` e, se serve un lock distribuito,
  `session.Locker`; cambiare solo il cablaggio in `serve.go`.
- **Altra destinazione dei consumi** (es. database): implementare
  `stats.UsageSink`.
- **Esecuzione remota o in container**: implementare `runner.Executor`.
- **Un'altra CLI**: implementare `runner.Backend` (e il suo `Parser`) in file
  propri, come fanno `runner/codex.go`, `codex_config.go` e `codex_events.go`
  per la CLI Codex, e aggiungerlo a `runner.NewBackend`. Una CLI che
  richiede un dialogo su stdin implementa `runner.Responder` nel suo parser,
  come fanno `runner/kimi_acp*.go` per la CLI Kimi Code tramite ACP.
- **Test**: il servizio si prova con fake delle interfacce, senza processi né
  file (`internal/turn/service_test.go`); il server si prova end-to-end con un
  finto binario della CLI (`testdata/fakecli`).
