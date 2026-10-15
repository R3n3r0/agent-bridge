# Riferimento della configurazione

[English](configuration.md) · **Italiano**

La configurazione arriva da quattro fonti, con questa precedenza:
**flag CLI > variabili d'ambiente > file JSON > default**. Il file è opzionale.
Modalità di autenticazione e chiavi arrivano solo dalle variabili d'ambiente.

## Comandi

| Comando | Effetto |
| --- | --- |
| `agent-bridge serve` (default) | Avvia il server |
| `agent-bridge check-config -config FILE` | Valida il file, stampa gli agenti risolti, esce con 0 o 1 |
| `agent-bridge doctor [-live]` | Verifica CLI e versione minima, modalità di autenticazione, permessi delle cartelle; con `-live` esegue un turno di prova su Haiku |
| `agent-bridge version` | Stampa versione, commit e data di build |
| `agent-bridge mcp-tools -spec FILE` | Interno: server MCP su stdio con le funzioni del client, avviato dalla CLI |

Flag (comuni a `serve`, `check-config` e `doctor`):

| Flag | Equivalente |
| --- | --- |
| `-config FILE` | `AGENT_BRIDGE_CONFIG` |
| `-listen ADDR` | `listen`, `AGENT_BRIDGE_LISTEN` |
| `-allow-remote` | `allow_remote` |
| `-max-concurrent N` | `max_concurrent` |
| `-claude-bin PATH` | `claude_binary` |
| `-backend claude\|codex\|kimi` | `backend`, `AGENT_BRIDGE_BACKEND` |
| `-codex-bin PATH` | `codex_binary` |
| `-kimi-bin PATH` | `kimi_binary` |
| `-log-format text\|json` | `AGENT_BRIDGE_LOG_FORMAT` |
| `-log-level debug\|info\|warn\|error` | `AGENT_BRIDGE_LOG_LEVEL` |

## Variabili d'ambiente

| Variabile | Obbligatoria | Significato |
| --- | --- | --- |
| `AGENT_BRIDGE_AUTH_MODE` | sì | `subscription` (login della CLI) oppure `api` (API key) |
| `ANTHROPIC_API_KEY` | solo con `api` | Chiave della Console; vietata con `subscription` (lo è anche `ANTHROPIC_AUTH_TOKEN`) |
| `AGENT_BRIDGE_BACKEND` | no | `claude` (predefinito), `codex` o `kimi` |
| `AGENT_BRIDGE_ADMIN` | no | `1`/`true` attiva la pagina di amministrazione (`admin`) |
| `AGENT_BRIDGE_ALLOW_REMOTE` | no | `1`/`true` consente un indirizzo non loopback (`allow_remote`); usata dall'immagine Docker |
| `KIMI_MODEL_NAME`, `KIMI_MODEL_API_KEY` | solo con `api` e il backend kimi | Modello e chiave usati dalla CLI Kimi Code; la chiave è vietata con `subscription` (lo è anche `KIMI_API_KEY`) |
| `CODEX_API_KEY` | solo con `api` e il backend codex | Chiave API OpenAI; vietata con `subscription` (lo è anche `CODEX_ACCESS_TOKEN`) |
| `AGENT_BRIDGE_API_KEY` | se `allow_remote` | Chiave che i client inviano come `Authorization: Bearer` |
| `AGENT_BRIDGE_CONFIG` | no | Percorso del file di configurazione |
| `AGENT_BRIDGE_LISTEN` | no | Indirizzo di ascolto, default `127.0.0.1:8787` |
| `AGENT_BRIDGE_LOG_FORMAT`, `AGENT_BRIDGE_LOG_LEVEL` | no | Come i flag omonimi |

Tutte le variabili `AGENT_BRIDGE_*` vengono rimosse dall'ambiente del processo
figlio; in modalità `subscription` anche `ANTHROPIC_API_KEY` e
`ANTHROPIC_AUTH_TOKEN` (con il backend codex: `CODEX_API_KEY`,
`CODEX_ACCESS_TOKEN` e `OPENAI_API_KEY`; con il backend kimi:
`KIMI_MODEL_API_KEY`, `KIMI_MODEL_NAME` e `KIMI_API_KEY`).

## Chiavi di primo livello

| Chiave | Default | Significato |
| --- | --- | --- |
| `listen` | `127.0.0.1:8787` | Indirizzo di ascolto |
| `allow_remote` | `false` | Consente un indirizzo non loopback (richiede `AGENT_BRIDGE_API_KEY`) |
| `backend` | `claude` | CLI che esegue i turni: `claude` (CLI predefinita), `codex` ([backend Codex](codex.it.md)) o `kimi` ([backend Kimi](kimi.it.md)) |
| `claude_binary` | `claude` | Nome o percorso della CLI predefinita |
| `codex_binary` | `codex` | Nome o percorso della CLI codex |
| `kimi_binary` | `kimi` | Nome o percorso della CLI Kimi Code |
| `max_concurrent` | `2` | Processi attivi contemporaneamente (minimo 1) |
| `max_queue` | `32` | Richieste in attesa di uno slot; oltre, 429 |
| `queue_timeout_seconds` | `300` | Attesa massima in coda; oltre, 429 |
| `shutdown_grace_seconds` | `30` | Attesa dei turni in corso all'arresto |
| `stream_progress` | `comments` | `comments`, `content` o `none` (vedi sotto) |
| `usage_log` | vuoto | File JSONL con una riga per turno |
| `metrics` | `false` | Abilita `GET /metrics` |
| `admin` | `false` | Attiva `GET /admin`, la pagina che esegue i flussi di login delle CLI, e `/admin/api/logins` ([Docker e login](docker.it.md)) |
| `cors_origins` | `[]` | Origini ammesse, esatte, senza jolly |
| `allow_bypass_permissions` | `false` | Consente `permission_mode: bypassPermissions` |
| `builtin_models` | `true` | Registra `bridge/opus`, `bridge/sonnet`, `bridge/haiku` (con i backend codex e kimi: `bridge/default`): agenti solo chat, senza tool del server, che accettano le funzioni del client |
| `builtin_prefix` | `bridge/` | Prefisso dei modelli senza configurazione: 1-32 caratteri `[a-zA-Z0-9._-]` seguiti da `/` |
| `builtin_any_model` | `true` | Accetta anche `bridge/<modello>` per qualsiasi nome di modello (`[a-zA-Z0-9][a-zA-Z0-9._[]-]`, fino a 100 caratteri), come agente solo chat |
| `system_messages` | `prompt` | Come arrivano i messaggi `system`/`developer`: `prompt` (in testa al prompt, in `<instructions>`), `append` (aggiunti al system prompt della CLI), `replace` (diventano il system prompt al posto di quello della CLI). Sovrascrivibile per richiesta con `X-Agent-Bridge-System-Mode` |
| `defaults.timeout_seconds` | `1800` | Timeout di default dei turni |
| `defaults.max_turns` | `0` | Turni massimi di default (0 = illimitato) |
| `sessions` | | Vedi sotto |
| `overrides` | | Vedi sotto |
| `client_tools` | | Vedi sotto |
| `images` | | Vedi sotto |
| `agents` | `{}` | Agenti, per nome |

Chiavi sconosciute nel file sono un errore, per intercettare refusi.

### `stream_progress`

- `comments`: i testi intermedi dell'assistente sono inviati come commenti SSE
  (`: testo`), normalizzati su una riga e troncati a 300 caratteri; il client
  vede solo il risultato finale.
- `content`: i testi intermedi sono inviati come `delta.content`, ciascuno
  seguito da una riga vuota, poi il risultato finale.
- `none`: nessun testo intermedio, solo `: keepalive`.

In tutti i casi, senza altri eventi per 15 secondi viene inviato `: keepalive`.

## Agenti

La chiave dell'oggetto `agents` è il nome esposto come `model`: caratteri
`[a-zA-Z0-9._/-]`, al massimo 64.

| Campo | Tipo | Default | Flag della CLI |
| --- | --- | --- | --- |
| `model` | string | `sonnet` | `--model` (alias o nome completo) |
| `description` | string | vuoto | Esposto in `/v1/models` |
| `system_prompt` | string | vuoto | `--append-system-prompt` o `--system-prompt` |
| `system_prompt_mode` | `append` \| `replace` | `append` | Sceglie il flag sopra |
| `allowed_tools` | []string | vuoto | `--allowedTools` (voci unite da virgola) |
| `disallowed_tools` | []string | vuoto | `--disallowedTools` |
| `tools_enabled` | bool | `true` | `false` = solo chat: `--tools ""`, senza le voci di `allowed_tools`, `--add-dir` e `mcp_config` dell'agente. Le funzioni del client restano disponibili (`--allowedTools mcp__functions` e il loro `--mcp-config`) |
| `permission_mode` | string | `default` | `--permission-mode` (`default`, `acceptEdits`, `plan`, `bypassPermissions`, `dontAsk`, `auto`) |
| `max_turns` | int | `defaults.max_turns` | `--max-turns` (omesso se 0) |
| `cwd` | string | obbligatorio se `tools_enabled` | Directory di lavoro del processo |
| `add_dirs` | []string | vuoto | `--add-dir`, una volta per voce |
| `mcp_config` | string | vuoto | `--mcp-config` |
| `sandbox` | string | `workspace-write` | Solo backend codex: `read-only`, `workspace-write` o `danger-full-access` (richiede `allow_bypass_permissions`) |
| `timeout_seconds` | int | `defaults.timeout_seconds` | Timeout del processo (0 = nessuno) |

I percorsi relativi (`cwd`, `add_dirs`, `mcp_config`, `usage_log`,
`sessions.path`, `overrides.allowed_roots`) sono risolti rispetto alla cartella
del file di configurazione. Gli agenti solo chat senza `cwd` usano la stessa
cartella temporanea vuota dei modelli built-in, creata all'avvio e rimossa
all'arresto.

Errori di validazione (riportati con agente e campo): nome non valido,
`cwd` inesistente o non directory, `cwd` mancante con tool abilitati,
`system_prompt_mode` o `permission_mode` fuori dai valori ammessi, numeri
negativi, conflitto con un modello built-in, `bypassPermissions` senza
`allow_bypass_permissions`.

Argomenti passati alla CLI, in ordine fisso: `-p --output-format stream-json
--verbose`, poi solo i flag dei campi valorizzati, poi il `--mcp-config` delle
funzioni del client se presenti, poi `--resume <id>` se la sessione viene
ripresa. Il prompt è sempre su stdin.

Con il backend codex i campi diventano invece override di `codex exec`, e
`permission_mode`, `allowed_tools`, `disallowed_tools` e `max_turns` sono
errori di configurazione: vedi [codex.it.md](codex.it.md). Con il backend
kimi `permission_mode` è uno tra `default`, `plan`, `auto`, `yolo`, e
`allowed_tools`/`disallowed_tools` rispondono alle richieste di permesso
della CLI: vedi [kimi.it.md](kimi.it.md).

## `sessions`

| Campo | Default | Significato |
| --- | --- | --- |
| `store` | `memory` | `memory` oppure `file` |
| `path` | `sessions.json` accanto alla config | File per `store: file`, scritto in modo atomico |
| `ttl_hours` | `168` | Voci non usate da più tempo vengono rimosse |
| `max_entries` | `10000` | Oltre il limite si rimuove la voce usata meno di recente |
| `on_busy` | `wait` | `wait` accoda la richiesta, `reject` risponde 409 |

Il registro conserva solo identificativi e metadati (ID di sessione, agente,
cartella, date, numero di turni, impronta dei messaggi di sistema), mai
contenuti delle conversazioni né credenziali.

Chiave di sessione: header `X-Session-Id` (1-128 caratteri `[a-zA-Z0-9._:-]`),
altrimenti il campo `user` (se contiene altri caratteri ne viene usata
un'impronta `user_…`), altrimenti una chiave generata `sess_…`. La chiave è
sempre restituita nell'header `X-Session-Id`. La stessa chiave usata con agenti
diversi produce sessioni distinte. `X-Session-Reset: true` cancella la sessione
prima del turno.

## `overrides`

| Campo | Default | Regola |
| --- | --- | --- |
| `allowed_roots` | `[]` (override disabilitato) | `X-Agent-Cwd` deve essere un percorso assoluto di una directory esistente che, risolti i symlink, sta dentro una delle radici |
| `allow_tools` | `false` | `X-Agent-Allowed-Tools` (lista separata da virgole) può solo restringere `allowed_tools` |
| `allow_system_prompt` | `false` | `X-Agent-System-Prompt` (base64, al massimo 16 KB decodificati) viene aggiunto dopo il prompt dell'agente |

Gli override non sono mai applicabili ai modelli `bridge/*` (alias e
modelli qualsiasi). Un override non abilitato o fuori dai limiti produce 403
`override_not_allowed`.

## `client_tools`

Funzioni definite dai client con `tools` (function calling OpenAI), eseguite
dai client.

| Campo | Default | Significato |
| --- | --- | --- |
| `enabled` | `true` | Con `false` i campi `tools`/`functions` vengono ignorati |
| `mode` | `stateful` | `stateful`: il processo attende i risultati e riprende; `stateless`: il processo si chiude dopo aver restituito le chiamate e la richiesta successiva riparte dalla cronologia. Sovrascrivibile per richiesta con `X-Agent-Bridge-Tool-Mode` |
| `wait_timeout_seconds` | `600` | Attesa massima dei risultati; poi il processo viene fermato e una richiesta successiva riparte dalla cronologia |
| `batch_window_ms` | `300` | Tempo per raccogliere chiamate parallele da restituire insieme |

Le funzioni sono esposte alla CLI come server MCP `functions` (tool
`mcp__functions__<nome>`), avviato con `agent-bridge mcp-tools`. Le chiamate
passano da un broker interno in ascolto su `127.0.0.1` con porta casuale e un
token diverso per ogni turno; i file di configurazione del turno sono privati
(permessi `0600`) e rimossi alla fine. La variabile `MCP_TOOL_TIMEOUT` del
processo figlio viene impostata al timeout dell'agente, se non già presente.

Validazione (400 `invalid_request_error`, come l'API OpenAI): tool di tipo
diverso da `function`, nomi fuori da `[a-zA-Z0-9_-]{1,64}` o duplicati,
`parameters` non oggetto, `tool_choice` senza `tools` o che nomina una funzione
inesistente.

## Header per richiesta

| Header | Valori | Effetto |
| --- | --- | --- |
| `X-Session-Id` | 1-128 caratteri `[a-zA-Z0-9._:-]` | Chiave di sessione |
| `X-Session-Reset` | `true`, `false` | Ricomincia la sessione |
| `X-Agent-Bridge-Tool-Mode` | `stateful`, `stateless` | Modalità delle funzioni del client |
| `X-Agent-Bridge-System-Mode` | `prompt`, `append`, `replace` | Destinazione dei messaggi `system` |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | vedi `overrides` | Override, solo se abilitati |
| `X-Request-Id` | 1-128 caratteri `[a-zA-Z0-9._:-]` | Identificativo della richiesta, riportato nei log e nella risposta; generato se assente |

Header di risposta: `X-Session-Id` e `X-Request-Id` sempre; nelle risposte non
stream anche `X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`,
`X-Agent-Bridge-Duration-Ms`.

Con `system_messages` `append` o `replace`, il system prompt configurato
nell'agente resta sempre in testa e i messaggi della richiesta vengono
aggiunti dopo. I messaggi `system` sono applicati a ogni turno, anche nelle
sessioni riprese.

## `images`

Parti di contenuto `image_url`. Le immagini arrivano alla CLI come blocchi
immagine di un messaggio utente stream-json (`--input-format stream-json`), a
cui il testo fa riferimento come `[image 1]`, `[image 2]`… nell'ordine; le
richieste senza immagini mantengono lo stdin in testo semplice.

| Campo | Default | Significato |
| --- | --- | --- |
| `enabled` | `true` | Con `false` le richieste con immagini sono rifiutate con 400 `unsupported_content` |
| `fetch_urls` | `true` | Accetta URL `http(s)`, scaricati dal server; con `false` solo URL `data:` |
| `allow_private_networks` | `false` | Consente download da indirizzi non pubblici (loopback, privati, link-local, condivisi, multicast). Il controllo avviene sull'indirizzo risolto di ogni connessione, redirect compresi; senza questa opzione i download non usano `HTTP(S)_PROXY` |
| `max_bytes` | `5242880` | Dimensione massima di un'immagine, decodificata |
| `max_count` | `20` | Numero massimo di immagini per richiesta (0 = nessun limite) |
| `fetch_timeout_seconds` | `20` | Timeout di ogni download |

Formati accettati, riconosciuti dal contenuto: PNG, JPEG, GIF, WebP. L'intero
messaggio su stdin, immagini in base64 comprese, deve restare entro 9 MB.
Immagini non valide, irraggiungibili o troppo grandi producono 400
`invalid_request_error` prima che parta qualsiasi processo.

## Errori HTTP

Formato OpenAI: `{"error": {"message", "type", "code"}}`, con `type` e `code`
uguali.

| HTTP | Codice | Quando |
| --- | --- | --- |
| 400 | `invalid_request_error` | JSON non valido, corpo oltre 10 MB, `messages` vuoto o senza messaggi `user`, nessun messaggio `user` o `tool` dopo l'ultimo `assistant`, header non validi, funzioni non valide (vedi `client_tools`), prompt oltre 9 MB |
| 400 | `unsupported_content` | Parti di contenuto diverse da testo e `image_url`; immagini quando `images.enabled` è `false` |
| 401 | `authentication_error` | Chiave Bearer mancante o errata |
| 403 | `override_not_allowed` | Override non abilitato o fuori dai limiti |
| 404 | `model_not_found` | Agente inesistente o nome `bridge/<modello>` non valido |
| 409 | `session_busy` | `on_busy: reject` e sessione in uso |
| 429 | `queue_full` | Coda piena o attesa scaduta, con `Retry-After: 30` |
| 502 | `cli_error` | Processo terminato con errore o senza risultato; include al massimo 2000 caratteri di stderr, ripuliti dai segreti |
| 503 | `server_error` | Arresto del server durante il turno |
| 504 | `timeout` | Superato `timeout_seconds` dell'agente |

In streaming, un errore dopo l'invio degli header è un evento `data:` con lo
stesso oggetto `error`, seguito da `data: [DONE]`.

## Osservabilità

Ogni turno produce una riga di log con `request_id`, `agent`, `model`,
`session_key`, `cli_session_id`, `resumed`, `turns`, `duration_ms`,
`queue_ms`, `cost_usd`, i quattro contatori di token e `outcome` (`success`,
`max_turns`, `error`, `timeout`, `canceled`, `tool_calls` per i processi chiusi
dopo aver restituito chiamate in modalità `stateless`). Con `usage_log` gli stessi campi,
più `ts` e `auth_mode`, sono aggiunti come riga JSON.

`GET /stats` restituisce `auth_mode`, `started_at`, `uptime_seconds`, `active`,
`queued` e, per agente, `requests`, `errors`, `timeouts`, `cost_usd`, token e
`avg_duration_ms`.

`GET /metrics` (con `metrics: true`): `agent_bridge_requests_total{agent,outcome}`,
`agent_bridge_cost_usd_total{agent}`, `agent_bridge_tokens_total{agent,kind}`,
`agent_bridge_turn_duration_seconds` (somma e conteggio), `agent_bridge_active`,
`agent_bridge_queued`. Richiede la chiave Bearer se impostata.
