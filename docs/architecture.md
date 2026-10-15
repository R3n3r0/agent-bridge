# Architettura

Il codice è diviso in livelli. Ogni livello dipende dalle **interfacce** del
livello sotto, mai dalle implementazioni concrete; il cablaggio avviene in un
solo punto, `cmd/agent-bridge/serve.go`.

```text
HTTP (internal/server)            parsing OpenAI, SSE, auth, CORS, mappatura errori
   │  turn.Service, stats.Reporter
   ▼
Dominio (internal/turn)           sessione, prompt, coda, esecuzione, ripresa, statistiche
   │  session.Store, session.Locker, runner.Executor, runner.Limiter, stats.Recorder
   ▼
Infrastruttura                    implementazioni sostituibili
   internal/session   MemoryStore, FileStore, KeyLocker
   internal/runner    CLIExecutor (processo reale), FIFOLimiter, controllo processi per OS
   internal/stats     Collector, JSONLUsageLog, DiscardUsage
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
| `processController` (interna) | `runner` | process group su Unix, `taskkill` su Windows | Terminazione dell'albero dei processi |

Le funzioni pure (costruzione del prompt in `prompt`, argomenti e ambiente in
`runner.Args`/`runner.Env`, parsing degli eventi, validazione della
configurazione) restano funzioni: non hanno stato né effetti collaterali da
sostituire.

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
