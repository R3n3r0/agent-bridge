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
   │  session.Store, session.Locker, runner.Executor, runner.Limiter, stats.Recorder, tools.Broker
   ▼
Infrastruttura                    implementazioni sostituibili
   internal/session   MemoryStore, FileStore, KeyLocker
   internal/runner    CLIExecutor (processo reale), FIFOLimiter, controllo processi per OS
   internal/stats     Collector, JSONLUsageLog, DiscardUsage
   internal/tools     HTTPBroker, server MCP delle funzioni del client (ServeMCP)
```

## Interfacce

| Interfaccia | Pacchetto | Implementazioni | Responsabilità |
| --- | --- | --- | --- |
| `Service` | `turn` | servizio di default (`NewService`) | Esegue un turno: `Admit` (controlli prima della risposta) ed `Execute` |
| `Store` | `session` | `MemoryStore`, `FileStore` | Persistenza delle sessioni con TTL e LRU |
| `Locker` | `session` | `KeyLocker` | Esclusione mutua per sessione |
| `Executor` | `runner` | `CLIExecutor` | Esegue un processo della CLI e ne raccoglie gli eventi |
| `Limiter` | `runner` | `FIFOLimiter` | Concorrenza massima e coda FIFO |
| `Recorder`, `Reporter` | `stats` | `Collector` | Registrazione dei turni; esposizione di `/stats` e `/metrics` |
| `UsageSink` | `stats` | `JSONLUsageLog`, `DiscardUsage` | Destinazione dei record di utilizzo |
| `Broker` | `tools` | `HTTPBroker` | Instrada le chiamate alle funzioni del client verso il turno che le attende |
| `Handler` | `tools` | il turno in esecuzione | Riceve una chiamata e la blocca fino al risultato del client |
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

## Estendere

- **Store condiviso** (es. Redis per più istanze in modalità `api`):
  implementare `session.Store` e, se serve un lock distribuito,
  `session.Locker`; cambiare solo il cablaggio in `serve.go`.
- **Altra destinazione dei consumi** (es. database): implementare
  `stats.UsageSink`.
- **Esecuzione remota o in container**: implementare `runner.Executor`.
- **Test**: il servizio si prova con fake delle interfacce, senza processi né
  file (`internal/turn/service_test.go`); il server si prova end-to-end con un
  finto binario della CLI (`testdata/fakecli`).
