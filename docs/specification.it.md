# agent-bridge — Specifiche tecniche

[English](specification.md) · **Italiano**

Oct 15, 2026 · @Someone — **rev. 1.6**: CLI Kimi Code tramite ACP (sezione 18); rev. 1.5: backend CLI e CLI Codex (sezione 17); rev. 1.4: function calling OpenAI (sezione 13), uso da orchestratori esterni (sezione 13.8), immagini (sezione 15), parametri di generazione (sezione 16), prefisso dei modelli configurabile e licenza GPL (sezione 14)

## 1. Panoramica e obiettivi

agent-bridge è un server HTTP scritto in Go che espone un'API compatibile OpenAI (`/v1/chat/completions`, `/v1/models`) e soddisfa ogni richiesta eseguendo il binario ufficiale di Claude Code in modalità headless (`claude -p --output-format stream-json`). Qualsiasi client o framework che parla OpenAI può così usare Claude Code come se fosse un modello, senza modifiche al codice del client.

Il problema che risolve: i sistemi multi-agente già costruiti su API OpenAI non possono usare Claude Code come motore di esecuzione (lettura e modifica di file, comandi, ricerca nel codice), né assegnare un modello Claude diverso a ciascun agente mantenendo il controllo del flusso nel proprio orchestratore.

**Due tipi di "tool", entrambi supportati.** Il termine "tool" indica due cose distinte, da non confondere:

| | Tool dell'agente | Funzioni del client (function calling OpenAI) |
| --- | --- | --- |
| Chi li definisce | La configurazione dell'agente (`allowed_tools`, `mcp_config`) | Il client, nel campo `tools` della richiesta |
| Esempi | `Read`, `Edit`, `Bash`, `Grep`, server MCP configurati sul server | `get_weather`, `search_orders`, qualsiasi funzione del client |
| Chi li esegue | Claude Code, sulla macchina del server | Il client, nel proprio codice |
| Cosa vede il client | Solo l'effetto e il risultato finale | Le chiamate (`tool_calls`), a cui risponde con messaggi `tool` |
| Specifica | Sezioni 4.3 e 7 | Sezione 13 |

Un agente può usare entrambi nello stesso turno: per esempio leggere file del repository con `Read` e poi chiamare una funzione del client per aprire un ticket.

Obiettivi misurabili della v1.0:

1. Un client OpenAI standard (SDK ufficiali Go, Python, Node) funziona cambiando solo `base_url`, `api_key` e `model`.
2. Zero configurazione per l'uso base: avviato senza file di configurazione, il server espone almeno i modelli `bridge/opus`, `bridge/sonnet`, `bridge/haiku` in modalità solo chat.
3. Agenti configurabili: un file JSON mappa un nome di modello a modello Claude, system prompt, tool, permessi e cartella di lavoro.
4. Conversazioni multi-turno tramite sessioni Claude Code riprese con `--resume`.
5. Stessa build per sviluppo (login con abbonamento Claude) e produzione (API key), selezionata solo da configurazione.
6. Binario singolo, sola libreria standard Go, release per Linux, macOS e Windows (amd64 e arm64).
7. Function calling compatibile OpenAI: il ciclo standard degli SDK ufficiali (richiesta con `tools`, risposta con `tool_calls`, richiesta successiva con i messaggi `tool`) funziona senza modifiche al client, anche in streaming e senza header di sessione (sezione 13).
8. Immagini in ingresso: parti `image_url` (URL `data:` e `http(s)`) accettate con qualsiasi modello (sezione 15).

Nome di lavoro: `agent-bridge`. Il nome definitivo non deve contenere "Claude Code" né "Anthropic" (vedi sezione 2).

## 2. Non obiettivi e vincoli di conformità

I vincoli seguenti derivano dalla pagina [Legal and compliance di Claude Code](https://code.claude.com/docs/en/legal-and-compliance) e sono requisiti, non suggerimenti: ogni PR che li viola va respinta.

1. **Binario non modificato.** Il server esegue `claude` così come installato da Anthropic, tramite `os/exec`. Nessuna patch, wrapper che altera il binario o reimplementazione del protocollo di Claude Code.
2. **Nessun accesso alle credenziali.** Il codice non deve mai leggere, copiare, conservare, loggare o inoltrare file di credenziali (`~/.claude/.credentials.json`, Keychain macOS) o token OAuth. Il login avviene solo tramite il flusso ufficiale (`claude` interattivo). Divieto esplicito di chiamare l'API Anthropic direttamente con il token dell'abbonamento.
3. **Un abbonamento, un utente.** In modalità abbonamento il server serve solo il titolare dell'account sulla propria macchina. Non deve essere possibile, con la configurazione di default, esporlo ad altri utenti (vedi sezione 8, bind di rete).
4. **Istanze condivise solo con API key.** Un'istanza usata da più persone (es. un laboratorio) deve girare in modalità `api`, con la chiave del titolare, senza rivendita o intermediazione dei costi.
5. **Nome e marchi.** Il nome del progetto, il logo e la descrizione non usano "Claude Code" o "Anthropic" come parte del nome e non suggeriscono affiliazione. Il README può dire in testo semplice che lo strumento esegue Claude Code e deve dichiarare che è un progetto indipendente. Per lo stesso motivo i nomi dei modelli senza configurazione usano un prefisso neutro, `bridge/` di default, configurabile con `builtin_prefix`.
6. **README con sezione "Uso consentito"** che riassume i punti 2–4 e linka la pagina legale e l'articolo [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan), segnalando che le regole possono cambiare.

Non obiettivi della v1.0:

- Supporto ai parametri OpenAI `temperature`, `top_p`, `seed`, `logprobs`, `presence_penalty`, `frequency_penalty`, `logit_bias`: la CLI non ha equivalenti, quindi vengono accettati e ignorati. (`reasoning_effort`, `response_format`, `max_tokens`, `stop` e `n` sono supportati o emulati: sezione 16.) (`tools`, `tool_choice`, `parallel_tool_calls`, `functions` e `function_call` sono invece supportati: sezione 13.)
- Vincoli rigidi su `tool_choice`, `parallel_tool_calls: false` e sugli schemi `parameters`/`strict` delle funzioni: sono resi come istruzioni al modello, non imposti (sezione 13.6).
- API `/v1/responses`, `/v1/embeddings`, generazione di immagini, audio in ingresso e in uscita.
- Modelli non Claude: il server non fa da router verso altri provider.
- Iniezione di prompt in una sessione interattiva aperta (channels): fuori ambito.
- Interfaccia web o dashboard grafica.

## 3. Architettura

Il bridge è un traduttore: riceve una richiesta OpenAI, la trasforma in un turno di `claude -p` e ritraduce gli eventi in una risposta OpenAI. Il suo unico stato è il registro delle sessioni (sezione 6) e, in modalità `stateful`, i turni in attesa dei risultati delle funzioni del client (sezione 13); in modalità `stateless` non ha stato di conversazione. L'autenticazione verso Anthropic resta interamente dentro il binario ufficiale.

```text
client OpenAI ──HTTP──▶ server ──▶ turn ──os/exec──▶ claude -p (stream-json) ──▶ API Anthropic
                                    │  ▲                   │ MCP stdio
                         session, stats │                   ▼
                                    tools.HTTPBroker ◀── agent-bridge mcp-tools
```

Flusso di una richiesta: il server autentica il client, risolve `model` in un agente e passa la richiesta validata al servizio di dominio `turn`; `turn` trova o crea la sessione e ne blocca il mutex, fa costruire a `prompt` il testo per stdin, attende uno slot del `runner`, lancia `claude` con i flag dell'agente e ne legge gli eventi; all'evento `result` salva il nuovo `session_id`, aggiorna `stats` e il server risponde in JSON o chiude lo stream SSE. Con le funzioni del client (sezione 13) il turno può fermarsi prima del `result`, restituire le chiamate al client e riprendere alla richiesta successiva.

Il codice è organizzato a livelli che comunicano tramite interfacce (dettagli in `docs/architecture.it.md`):

| Livello | Pacchetto | Interfacce usate | Implementazioni |
| --- | --- | --- | --- |
| HTTP | `server` | `turn.Service`, `stats.Reporter` | — |
| Dominio | `turn` | `session.Store`, `session.Locker`, `runner.Backend`, `runner.Executor`, `runner.Limiter`, `stats.Recorder`, `tools.Broker` | servizio di default |
| Infrastruttura | `session`, `runner`, `stats`, `tools` | — | `MemoryStore`, `FileStore`, `KeyLocker`, `StreamJSONBackend`, `CodexBackend`, `KimiBackend`, `CLIExecutor`, `FIFOLimiter`, `Collector`, `JSONLUsageLog`, `HTTPBroker` |

Il cablaggio delle implementazioni avviene in un solo punto (`cmd/agent-bridge/serve.go`).

## 4. Configurazione

La configurazione arriva da quattro fonti con questa precedenza: flag CLI > variabili d'ambiente > file JSON > default. Il file è opzionale; il formato è JSON per restare sulla sola libreria standard.

### 4.1 Comandi e flag

| Comando | Effetto |
| --- | --- |
| `agent-bridge serve` (default) | Avvia il server |
| `agent-bridge check-config -config FILE` | Valida il file, stampa gli agenti risolti, esce con codice 0 o 1 |
| `agent-bridge doctor` | Verifica `claude` nel PATH e versione minima, coerenza della modalità di autenticazione, permessi delle cartelle; con `-live` esegue una richiesta di prova di un turno su Haiku |
| `agent-bridge version` | Stampa versione, commit e data di build |
| `agent-bridge mcp-tools -spec FILE` | Interno: server MCP su stdio con le funzioni del client, avviato da Claude Code (sezione 13) |

Flag di `serve` (gli stessi valgono per `check-config` e `doctor`): `-config`, `-listen`, `-allow-remote`, `-max-concurrent`, `-claude-bin`, `-log-format text|json`, `-log-level debug|info|warn|error`.

### 4.2 Variabili d'ambiente

| Variabile | Obbligatoria | Significato |
| --- | --- | --- |
| `AGENT_BRIDGE_AUTH_MODE` | sì | `subscription` (login Claude Code) oppure `api` (API key) |
| `ANTHROPIC_API_KEY` | solo con `api` | Chiave della Claude Console; vietata con `subscription` |
| `AGENT_BRIDGE_API_KEY` | no, ma obbligatoria se `-allow-remote` | Chiave che i client inviano come `Authorization: Bearer` |
| `AGENT_BRIDGE_CONFIG` | no | Percorso del file di configurazione |
| `AGENT_BRIDGE_LISTEN` | no | Indirizzo di ascolto, default `127.0.0.1:8787` |
| `AGENT_BRIDGE_LOG_FORMAT` / `AGENT_BRIDGE_LOG_LEVEL` | no | Come i flag omonimi |

### 4.3 File di configurazione

Chiavi di primo livello: `listen`, `allow_remote` (bool, default false), `claude_binary` (default `claude`), `max_concurrent` (default 2), `max_queue` (default 32), `queue_timeout_seconds` (default 300), `shutdown_grace_seconds` (default 30), `stream_progress` (`comments`, `content` o `none`; default `comments`), `usage_log` (percorso JSONL, opzionale), `metrics` (bool, default false), `cors_origins` (lista, default vuota), `allow_bypass_permissions` (bool, default false), `builtin_models` (bool, default true), `builtin_prefix` (default `bridge/`, sezione 4.4), `builtin_any_model` (bool, default true, sezione 13.8), `system_messages` (`prompt`, `append` o `replace`; default `prompt`, sezione 13.8), `client_tools` (sezione 13.7), `images` (sezione 15), `defaults` (`timeout_seconds` default 1800, `max_turns` default 0 = illimitato), `sessions` (sezione 6), `overrides` (sezione 8), `agents`.

Campi di ogni agente in `agents` (la chiave è il nome esposto come `model`; caratteri ammessi `[a-zA-Z0-9._/-]`, massimo 64):

| Campo | Tipo | Default | Mappa su |
| --- | --- | --- | --- |
| `model` | string | `sonnet` | `--model` (alias o nome completo) |
| `description` | string | vuoto | Esposto in `/v1/models` |
| `system_prompt` | string | vuoto | `--append-system-prompt` o `--system-prompt` |
| `system_prompt_mode` | `append` \| `replace` | `append` | Sceglie il flag sopra |
| `allowed_tools` | \[\]string | vuoto | `--allowedTools` |
| `disallowed_tools` | \[\]string | vuoto | `--disallowedTools` |
| `tools_enabled` | bool | true | false = solo chat (sezione 7.2) |
| `permission_mode` | string | `default` | `--permission-mode` |
| `max_turns` | int | `defaults.max_turns` | `--max-turns` |
| `cwd` | string | obbligatorio se `tools_enabled` | Directory di lavoro del processo |
| `add_dirs` | \[\]string | vuoto | `--add-dir` (una volta per voce) |
| `mcp_config` | string | vuoto | `--mcp-config` (percorso file) |
| `timeout_seconds` | int | `defaults.timeout_seconds` | Timeout del processo |

Validazione all'avvio, con errore che indica agente e campo: nome non valido, `cwd` inesistente o non directory, `cwd` mancante con tool abilitati, `system_prompt_mode` o `permission_mode` fuori dai valori ammessi, numeri negativi, conflitto di nome con un modello built-in. Percorsi relativi risolti rispetto alla cartella del file di configurazione.

### 4.4 Modelli built-in (zero-config)

Con `builtin_models: true` il server registra `bridge/opus`, `bridge/sonnet` e `bridge/haiku` (il prefisso `bridge/` è l'impostazione `builtin_prefix`: 1-32 caratteri `[a-zA-Z0-9._-]` seguiti da `/`): tool del server disattivati, `cwd` in una directory temporanea vuota creata all'avvio e rimossa all'arresto, nessun system prompt aggiuntivo. Accettano le funzioni del client (sezione 13). Con `builtin_any_model: true` è accettato anche `bridge/<modello>` per qualsiasi nome di modello (sezione 13.8). Servono come modelli puri per client e orchestratori che definiscono gli agenti da sé, e per provare il server senza configurazione.

## 5. API HTTP

Il server implementa il sottoinsieme dell'API OpenAI Chat Completions necessario agli SDK ufficiali, più gli endpoint di servizio. Tutti gli endpoint `/v1/*`, `/stats` e `/metrics` richiedono `Authorization: Bearer <AGENT_BRIDGE_API_KEY>` quando la chiave è impostata (confronto a tempo costante).

| Metodo e percorso | Scopo |
| --- | --- |
| `POST /v1/chat/completions` | Esegue un turno di un agente |
| `GET /v1/models` | Elenca agenti e modelli built-in |
| `GET /v1/models/{id}` | Dettaglio di un modello, 404 se assente |
| `GET /stats` | Statistiche cumulative per agente (sezione 9) |
| `GET /healthz` | 200 `ok` se il processo è vivo, senza autenticazione |
| `GET /readyz` | 200 se `claude` è eseguibile e la configurazione è valida, altrimenti 503 |
| `GET /metrics` | Metriche Prometheus, solo con `metrics: true` (sezione 9.4) |

### 5.1 Richiesta `POST /v1/chat/completions`

Campi usati: `model` (obbligatorio, nome dell'agente o `bridge/<modello>`), `messages` (obbligatorio, almeno un messaggio `user`; dopo l'ultimo messaggio `assistant` deve esserci almeno un messaggio `user` o `tool`), `stream` (bool), `stream_options.include_usage` (bool), `user` (string, usato come chiave di sessione di riserva), `tools`, `tool_choice`, `parallel_tool_calls`, `functions`, `function_call` (sezione 13), `max_completion_tokens` / `max_tokens`, `reasoning_effort`, `response_format`, `stop`, `n` (sezione 16). Ruoli accettati: `system`, `developer`, `user`, `assistant` (anche con `tool_calls` o `function_call` e `content` nullo), `tool` (con `tool_call_id`), `function` (deprecato, trattato come `tool`). Il `content` può essere stringa, nullo o array di parti; delle parti si usano quelle `type: "text"` e `type: "image_url"` (immagini: sezione 15), le altre (audio, file) producono 400 `unsupported_content`. Ogni altro campo è ignorato senza errore. Corpo massimo 10 MB.

Header opzionali in ingresso:

| Header | Effetto |
| --- | --- |
| `X-Session-Id` | Chiave di sessione scelta dal client (sezione 6), massimo 128 caratteri `[a-zA-Z0-9._:-]` |
| `X-Session-Reset: true` | Ignora la sessione esistente e ne inizia una nuova con la stessa chiave |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | Override per richiesta, accettati solo se abilitati (sezione 8.3) |
| `X-Agent-Bridge-Tool-Mode: stateful\|stateless` | Modalità delle funzioni del client per questa richiesta (sezione 13.8) |
| `X-Agent-Bridge-System-Mode: prompt\|append\|replace` | Destinazione dei messaggi `system`/`developer` per questa richiesta (sezione 13.8) |
| `X-Request-Id` | Identificativo della richiesta (1-128 caratteri `[a-zA-Z0-9._:-]`), riportato nei log; generato se assente |

### 5.2 Risposta non-stream

Oggetto `chat.completion` standard: `id` (`chatcmpl-` + 24 caratteri esadecimali casuali), `object`, `created`, `model` (il nome dell'agente richiesto), `choices` con un solo elemento (`index` 0, `message.role` `assistant`, `message.content` uguale al campo `result` dell'evento finale di Claude Code, `finish_reason`), `usage` (`prompt_tokens` = input + cache creation + cache read, `completion_tokens` = output, `total_tokens`). `finish_reason` vale `stop` se l'esito è `success`, `length` se Claude Code termina per `error_max_turns`, `tool_calls` se il modello ha chiamato funzioni del client (in quel caso `message.tool_calls` contiene le chiamate e `message.content` è il testo scritto prima delle chiamate, oppure `null`; sezione 13.3). Con il formato deprecato `functions` la risposta contiene `message.function_call` e `finish_reason: "function_call"`.

Header in uscita: `X-Session-Id` (sempre, anche se generato dal server), `X-Request-Id`, `X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`, `X-Agent-Bridge-Duration-Ms`. In streaming solo `X-Session-Id` e `X-Request-Id`, perché gli header partono prima dell'esecuzione.

### 5.3 Streaming SSE

Con `stream: true` la risposta è `text/event-stream`. Sequenza obbligatoria:

1. Chunk `chat.completion.chunk` con `delta: {"role": "assistant"}`, inviato subito.
2. Durante l'esecuzione, solo righe di commento SSE (che iniziano con `:`): una per ogni blocco di testo intermedio dell'assistente (normalizzato su una riga, massimo 300 caratteri) e `: keepalive` ogni 15 secondi senza altri eventi. Le righe di commento **non** sono seguite da una riga vuota: alcuni SDK (incluso `openai-go`) interpretano una riga vuota dopo un commento come un evento vuoto e falliscono.
3. Alla fine, un chunk con `delta.content` uguale al risultato finale (omesso se il turno si ferma su chiamate a funzione senza testo), poi, se ci sono chiamate, un chunk per ciascuna con `delta.tool_calls: [{index, id, type: "function", function: {name, arguments}}]` (o `delta.function_call` nel formato deprecato), poi un chunk con `delta: {}` e `finish_reason`.
4. Se `include_usage` è true, un chunk con `choices: []` e `usage`.
5. `data: [DONE]`.

Il contenuto finale è identico a quello della modalità non-stream. Opzione di configurazione `stream_progress: "comments" | "content" | "none"` (default `comments`): con `content` i testi intermedi sono inviati come `delta.content` veri, separati da una riga vuota, e il risultato finale segue; con `none` nessun testo intermedio, solo keepalive.

### 5.4 Errori

Formato OpenAI: `{"error": {"message", "type", "code"}}`, con `type` e `code` uguali. In streaming, se l'errore avviene dopo l'invio degli header, si invia un evento `data:` con lo stesso oggetto `error`, poi `data: [DONE]`.

| HTTP | `type` / `code` | Quando |
| --- | --- | --- |
| 400 | `invalid_request_error` | JSON non valido, `messages` vuoto, nessun messaggio `user`, nessun messaggio `user`/`tool` dopo l'ultimo `assistant`, header non validi, funzioni non valide (sezione 13.2) |
| 400 | `unsupported_content` | Parti di contenuto diverse da testo e `image_url`; immagini quando `images.enabled` è `false` |
| 401 | `authentication_error` | Chiave Bearer mancante o errata |
| 403 | `override_not_allowed` | Override per richiesta non abilitato o fuori dai limiti |
| 404 | `model_not_found` | Agente inesistente o nome `bridge/<modello>` non valido |
| 409 | `session_busy` | Solo se `sessions.on_busy: "reject"` e la sessione è in uso |
| 429 | `queue_full` | Coda di attesa piena o attesa scaduta (sezione 7.5), con header `Retry-After` |
| 502 | `cli_error` | Processo terminato con errore, esito diverso da `success`/`error_max_turns`, output non interpretabile |
| 503 | `server_error` | Arresto del server durante il turno |
| 504 | `timeout` | Superato `timeout_seconds` dell'agente |

Il messaggio d'errore 502 include al massimo gli ultimi 2000 caratteri di stderr, ripuliti da qualsiasi stringa che inizi con `sk-ant-`.

## 6. Sessioni e costruzione del prompt

Ogni coppia (agente, chiave di sessione) corrisponde a una sessione di Claude Code. Se il client riusa la stessa chiave (header `X-Session-Id` o campo `user`), i turni successivi riprendono la sessione con `--resume` e alla CLI arrivano solo i messaggi nuovi, così il client può continuare a inviare l'intera cronologia come con OpenAI senza che il contesto venga duplicato. Senza chiave ogni richiesta riceve una chiave nuova e avvia una sessione nuova con la cronologia completa (comportamento senza stato, come l'API OpenAI); il client può riusare la chiave restituita nell'header `X-Session-Id` per passare alla ripresa.

### 6.1 Chiave e registro

La chiave è, nell'ordine: header `X-Session-Id`, campo `user`, oppure una chiave generata `sess_` + 24 caratteri esadecimali, restituita nell'header `X-Session-Id`. La chiave interna è `<agente>|<chiave>`, quindi la stessa chiave usata con agenti diversi produce sessioni distinte.

Ogni voce del registro contiene: `cli_session_id`, `agent`, `cwd`, `created_at`, `last_used_at`, `turns`. Configurazione in `sessions`:

| Campo | Default | Significato |
| --- | --- | --- |
| `store` | `memory` | `memory` oppure `file` |
| `path` | `sessions.json` accanto alla config | File per `store: file`, scritto in modo atomico (file temporaneo + rename) |
| `ttl_hours` | 168 | Voci non usate da più tempo vengono ignorate e rimosse |
| `max_entries` | 10000 | Oltre il limite si rimuove la voce usata meno di recente |
| `on_busy` | `wait` | `wait` accoda la richiesta, `reject` risponde 409 |

Il registro conserva solo identificativi e metadati, mai contenuti delle conversazioni né credenziali.

### 6.2 Concorrenza sulla stessa sessione

Due `--resume` paralleli sulla stessa sessione sono vietati: ogni chiave ha un mutex. Con `on_busy: wait` la richiesta attende finché la sessione si libera o il client si disconnette.

### 6.3 Ripresa e casi limite

1. L'ID di sessione da salvare è sempre l'ultimo `session_id` visto negli eventi `system/init` o `result`, perché la ripresa può produrre un ID nuovo.
2. Se la `cwd` effettiva della richiesta differisce da quella registrata, si avvia una sessione nuova (le sessioni di Claude Code sono legate alla directory di progetto).
3. Se la ripresa fallisce perché la sessione non esiste più, il server cancella la voce e ripete la richiesta una sola volta come sessione nuova, con la cronologia appiattita (6.4), registrando un warning.
4. `X-Session-Reset: true` cancella la voce prima di eseguire.
5. L'interruzione del client durante un turno termina il processo; la voce resta valida e punta all'ultimo ID noto.

### 6.4 Costruzione del prompt

Il prompt è passato a `claude` tramite stdin, mai come argomento.

**Sessione nuova.** Se la richiesta contiene un solo messaggio, di ruolo `user`, il prompt è il testo di quel messaggio (con `system_messages` `append` o `replace` i messaggi `system`/`developer` non contano, perché vanno nel system prompt). Altrimenti il server costruisce:

```text
<instructions>
…testi dei messaggi system/developer, separati da riga vuota…
</instructions>

<conversation_history>
<user>…</user>
<assistant>…testo…
<tool_call id="call_…" name="get_weather">{"city":"Rome"}</tool_call></assistant>
<tool tool_call_id="call_…" name="get_weather">…risultato…</tool>
</conversation_history>

<current_request>
…messaggi user e tool successivi all'ultimo messaggio assistant…
</current_request>
```

Le sezioni vuote vengono omesse. Con `system_messages` `append` o `replace` (sezione 13.8) i messaggi `system`/`developer` non entrano nel prompt e la sezione `<instructions>` non c'è. I messaggi `tool` senza `tool_call_id` né nome sono resi come `<tool>…</tool>`; le chiamate del formato deprecato `function_call` come `<tool_call name="…">`.

**Sessione ripresa.** Il prompt contiene solo i messaggi `user` e `tool` successivi all'ultimo messaggio `assistant` della richiesta, uniti da una riga vuota (i `tool` resi con il tag `<tool tool_call_id=… name=…>`). Non si applica quando la richiesta riprende un turno in attesa di funzioni (sezione 13.4): lì i risultati vanno direttamente al processo, senza nuovo prompt. Con `system_messages: "prompt"` (default) i messaggi `system` vengono ignorati, perché sono già nella sessione dal primo turno; se differiscono da quelli del primo turno il server registra un messaggio di debug. Con `append` o `replace` sono passati come system prompt a ogni turno (sezione 13.8).

Limite: prompt oltre 9 MB in UTF-8 (la CLI limita lo stdin a 10 MB dalla v2.1.128) → 400 `invalid_request_error`.

## 7. Esecuzione di Claude Code

Ogni turno è un processo `claude` separato, avviato con `os/exec` senza shell, che termina quando emette l'evento `result` (o prima, quando il bridge lo chiude dopo aver restituito chiamate a funzione in modalità `stateless`, sezione 13.8). Il modulo `runner` è l'unico punto del codice che conosce i flag e il formato degli eventi della CLI.

### 7.1 Argomenti

Ordine fisso: `-p`, `--output-format stream-json`, `--verbose`, `--input-format stream-json` (solo se la richiesta contiene immagini, sezione 15), poi solo i flag dei campi valorizzati: `--model`, `--effort` (sezione 16), `--append-system-prompt` o `--system-prompt` (con il system prompt dell'agente, i messaggi `system` della richiesta se `system_messages` è `append`/`replace`, e le istruzioni derivate da `tool_choice`), `--tools ""` (solo chat), `--allowedTools` (voci unite da virgola, più `mcp__functions` se ci sono funzioni del client), `--disallowedTools`, `--permission-mode`, `--max-turns`, `--add-dir` (ripetuto), `--mcp-config` dell'agente, `--mcp-config` delle funzioni del client (sezione 13), `--json-schema` (sezione 16), `--resume`. Nessun argomento posizionale: il prompt va su stdin.

Prima della v1.0, ogni flag va verificato contro la [documentazione della CLI](https://code.claude.com/docs/en/cli-reference) per la versione minima supportata, che `doctor` controlla con `claude --version`. La versione minima è una costante nel codice, documentata nel README.

### 7.2 Modalità solo chat

Con `tools_enabled: false` nessun tool dell'agente deve essere disponibile: il runner passa `--tools ""`, che secondo la [CLI reference](https://code.claude.com/docs/en/cli-reference) disattiva tutti i tool built-in, e non passa le voci di `allowed_tools`, `--add-dir` né il `mcp_config` dell'agente. Test obbligatorio: in questa modalità una richiesta che chiede di leggere un file non deve produrre eventi `tool_use`.

Eccezione: le funzioni del client (sezione 13) restano disponibili anche in modalità solo chat e per i modelli built-in, perché sono eseguite dal client e non danno accesso alla macchina del server. Da verificare sulla CLI reale (checklist 11.4): che i tool MCP restino disponibili con `--tools ""`.

### 7.3 Ambiente del processo figlio

Si parte da `os.Environ()` e si rimuovono sempre tutte le variabili `AGENT_BRIDGE_*`. In modalità `subscription` si rimuovono anche `ANTHROPIC_API_KEY` e `ANTHROPIC_AUTH_TOKEN`. In modalità `api` l'ambiente resta invariato. Con funzioni del client, se `MCP_TOOL_TIMEOUT` non è già impostata, viene impostata al timeout dell'agente in millisecondi, così una chiamata può attendere il client quanto può durare il turno. La directory di lavoro è la `cwd` effettiva dell'agente.

### 7.4 Lettura degli eventi

Stdout si legge riga per riga (buffer fino a 64 MB per riga); ogni riga è un oggetto JSON. Righe non valide e tipi sconosciuti vengono ignorati con un log di debug, per compatibilità con versioni future della CLI.

| Evento | Campi usati | Azione |
| --- | --- | --- |
| `system`, subtype `init` | `session_id`, `model`, `apiKeySource` | Salva ID e modello effettivo; log della fonte delle credenziali |
| `assistant` | `message.content[]` di tipo `text` | Inoltra il testo come avanzamento (sezione 5.3) |
| `assistant` | `message.content[]` di tipo `tool_use` | Log di debug con nome del tool |
| `assistant` | `message.usage` | Conservato come `usage` della risposta `tool_calls` (sezione 13.3) |
| `user` | risultati dei tool | Ignorato |
| `result` | `subtype`, `result`, `is_error`, `session_id`, `total_cost_usd`, `usage`, `num_turns`, `duration_ms` | Esito finale del turno |

Mappatura dell'esito: `success` con `is_error` falso → 200; `error_max_turns` → 200 con `finish_reason: length` e il testo disponibile; ogni altro subtype o `is_error` vero → 502. Processo terminato senza evento `result` → 502 con la coda di stderr, oppure 504 se è scaduto il timeout.

### 7.5 Concorrenza e coda

Un semaforo globale limita i processi attivi a `max_concurrent`. Le richieste in eccesso entrano in una coda FIFO lunga al massimo `max_queue` (default 32) e attendono al massimo `queue_timeout_seconds` (default 300); oltre, 429 `queue_full` con `Retry-After: 30`. Il tempo in coda non conta nel `timeout_seconds` dell'agente.

### 7.6 Timeout e interruzione

Il contesto del processo si annulla per timeout dell'agente, disconnessione del client o arresto del server. Il processo va terminato insieme a tutti i figli (comandi lanciati dal tool Bash): su Unix con un process group (`Setpgid`) e segnale al gruppo, prima `SIGTERM` poi `SIGKILL` dopo 5 secondi; su Windows terminando l'albero dei processi. `cmd.WaitDelay` a 5 secondi per non restare bloccati sulle pipe.

All'arresto (`SIGINT`/`SIGTERM`) il server smette di accettare richieste, attende fino a `shutdown_grace_seconds` (default 30) i turni in corso, poi li termina come sopra.

## 8. Autenticazione e sicurezza

Un agente con tool abilitati può leggere, scrivere ed eseguire comandi sulla macchina: chi raggiunge il server ha l'equivalente di una shell limitata. Le impostazioni di default devono quindi essere chiuse, e ogni apertura deve essere esplicita.

### 8.1 Modalità di autenticazione verso Anthropic

All'avvio il server legge `AGENT_BRIDGE_AUTH_MODE` e rifiuta di partire, con messaggio che spiega come correggere, se:

- la variabile manca o ha un valore diverso da `subscription` / `api`;
- vale `subscription` e nell'ambiente c'è `ANTHROPIC_API_KEY` o `ANTHROPIC_AUTH_TOKEN`;
- vale `api` e manca `ANTHROPIC_API_KEY`.

La modalità attiva è stampata nel primo log di avvio ed esposta in `/stats`. Se, in modalità `subscription`, l'evento `init` riporta un `apiKeySource` che indica una API key, il server registra un errore ben visibile per ogni turno (i valori esatti del campo vanno verificati in implementazione).

### 8.2 Esposizione in rete

1. Default: ascolto solo su loopback (`127.0.0.1:8787`).
2. Un indirizzo non loopback richiede `allow_remote: true` (o `-allow-remote`) e `AGENT_BRIDGE_API_KEY` impostata, altrimenti il server non parte.
3. In modalità `subscription` con accesso remoto il server stampa all'avvio un avviso: l'istanza deve servire solo il titolare dell'account (es. accesso dal proprio portatile via VPN personale), non altri utenti.
4. Nessun TLS integrato: per l'accesso remoto il README consiglia reverse proxy, tunnel SSH o VPN.
5. CORS disattivato; configurabile con `cors_origins` (lista esatta di origini, nessun jolly).

### 8.3 Override per richiesta

Gli header `X-Agent-*` sono rifiutati con 403 a meno che non siano abilitati nella sezione `overrides` della configurazione:

| Campo | Default | Regola |
| --- | --- | --- |
| `allowed_roots` | vuoto (override `cwd` disabilitato) | `X-Agent-Cwd` è accettato solo se, dopo `filepath.Abs` ed `EvalSymlinks`, è una directory esistente dentro una delle radici |
| `allow_tools` | false | `X-Agent-Allowed-Tools` (lista separata da virgole) può solo restringere gli `allowed_tools` dell'agente, mai aggiungerne |
| `allow_system_prompt` | false | `X-Agent-System-Prompt` (massimo 16 KB, codificato in base64) viene aggiunto dopo quello dell'agente, mai in sostituzione |

Gli override non sono mai applicabili ai modelli `bridge/*` (alias e modelli qualsiasi).

### 8.4 Permessi pericolosi

`permission_mode: bypassPermissions` è rifiutato in validazione a meno che la configurazione non contenga `allow_bypass_permissions: true`; in quel caso all'avvio compare un avviso per ogni agente interessato.

### 8.5 Broker delle funzioni del client

Le chiamate alle funzioni del client passano da un broker HTTP interno in ascolto solo su `127.0.0.1` con porta casuale. Ogni turno ha un token casuale di 32 byte, confrontato a tempo costante e invalidato a fine turno. Token e definizioni sono scritti in file per turno con permessi `0600`, rimossi alla fine. Le funzioni non eseguono nulla sul server: il loro risultato arriva dal client ed è trattato come contenuto della conversazione.

### 8.6 Dati sensibili nei log

A livello `info` e superiori non si registrano mai testi di prompt o risposte. A livello `debug` si registrano troncati a 500 caratteri. Ogni messaggio di log ed errore passa da una funzione di redazione che sostituisce stringhe `sk-ant-…` e il valore di `AGENT_BRIDGE_API_KEY` con `[REDACTED]`. Ogni richiesta ha un ID (`X-Request-Id` in ingresso se presente, altrimenti generato), riportato in tutti i log e nell'header di risposta.

## 9. Osservabilità e costi

Ogni turno produce una riga di log, una voce opzionale nel file di utilizzo e un aggiornamento delle statistiche in memoria, così in sviluppo con l'abbonamento si può stimare quanto costerà la produzione con API key.

### 9.1 Log

Libreria `log/slog`, formato `text` (default) o `json`. Campi di ogni riga di fine turno: `request_id`, `agent`, `model` (effettivo, dall'evento `init`), `session_key`, `cli_session_id`, `resumed`, `turns`, `duration_ms`, `queue_ms`, `cost_usd`, `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_creation_tokens`, `outcome` (`success`, `max_turns`, `error`, `timeout`, `canceled`, `tool_calls`). L'esito `tool_calls` indica un processo chiuso di proposito dopo aver restituito chiamate a funzione (modalità `stateless`); i token sono quelli dell'ultimo messaggio.

### 9.2 File di utilizzo

Se `usage_log` è impostato, gli stessi campi più `ts` (RFC 3339) e `auth_mode` vengono aggiunti come una riga JSON per turno. Scrittura serializzata da un mutex; errori di scrittura loggati come warning senza far fallire la richiesta. Il file non contiene mai testi di prompt o risposte.

### 9.3 `GET /stats`

Risposta JSON con `auth_mode`, `started_at`, `uptime_seconds`, `active`, `queued` e un oggetto `agents` che per ogni agente riporta `requests`, `errors`, `timeouts`, `cost_usd`, i quattro contatori di token e `avg_duration_ms`. Con `?reset=true` azzera i contatori dopo averli restituiti.

In modalità `subscription` il campo `cost_usd` è la stima calcolata da Claude Code ai prezzi API, non un addebito: il README deve dirlo esplicitamente.

### 9.4 Metriche Prometheus (opzionale)

Con `metrics: true` il server espone `GET /metrics` in formato testo Prometheus, scritto a mano senza dipendenze: `agent_bridge_requests_total{agent,outcome}`, `agent_bridge_cost_usd_total{agent}`, `agent_bridge_tokens_total{agent,kind}`, `agent_bridge_turn_duration_seconds` (somma e conteggio), `agent_bridge_active`, `agent_bridge_queued`. Richiede la chiave Bearer se impostata.

## 10. Repository, build e rilascio

Go 1.22 o successivo, sola libreria standard a runtime; strumenti esterni ammessi solo in CI. Un unico binario statico (`CGO_ENABLED=0`).

### 10.1 Struttura

```text
cmd/agent-bridge/        main, sottocomandi serve | check-config | doctor | version | mcp-tools
internal/config/         caricamento file/env/flag, default, validazione
internal/turn/           servizio di dominio: sessione, prompt, coda, esecuzione, turni in attesa di funzioni
internal/runner/         Backend (CLI predefinita, CLI Codex, Kimi Code tramite ACP): argomenti, ambiente, parsing eventi; Executor, Limiter
  proc_unix.go           process group e terminazione su Unix
  proc_windows.go        terminazione dell'albero dei processi su Windows
internal/prompt/         costruzione del prompt (sezione 6.4)
internal/session/        Store (memory, file) e Locker
internal/tools/          funzioni del client: broker, server MCP, definizioni (sezione 13)
internal/media/          immagini: URL data:, download con controllo degli indirizzi, riconoscimento del formato (sezione 15)
internal/server/         handler HTTP, tipi OpenAI, SSE, errori, auth Bearer
internal/stats/          contatori, file di utilizzo, /metrics
internal/redact/         redazione di segreti in log ed errori
testdata/fakecli/        finto binario della CLI per i test (sezione 11)
examples/                agents.example.json, client in Go, Python (anche function calling) e curl
docs/                    configurazione, architettura, checklist di rilascio, specifica (inglese; italiano in *.it.md)
```

### 10.2 Qualità e CI

Workflow GitHub Actions `ci`, avviato a mano (`workflow_dispatch`; non parte su push né pull request): `gofmt -l` vuoto, `go vet`, `staticcheck`, `go test -race ./...` su Ubuntu, macOS e Windows con Go 1.22 e l'ultima stabile. Copertura minima dell'80% su `internal/prompt`, `internal/session`, `internal/config`, `internal/runner`, `internal/turn` e `internal/tools`.

### 10.3 Rilascio

GoReleaser su tag `v*`: binari per linux, darwin e windows su amd64 e arm64, archivi, `checksums.txt`, note di rilascio dal changelog. Versione, commit e data iniettati con `-ldflags`. Installazione documentata anche con `go install github.com/<owner>/agent-bridge/cmd/agent-bridge@latest`.

### 10.4 Documentazione

La documentazione principale è in inglese (`README.md`, `docs/*.md`), con una versione italiana per ciascun documento (`README.it.md`, `docs/*.it.md`). Il README contiene, in quest'ordine: cosa fa (con dichiarazione di progetto indipendente), i due modi d'uso (come modello e come agente sul server) e i due tipi di tool, avvio rapido in tre comandi, uso come modello da orchestratori esterni, function calling, agenti sul server, conversazioni e sessioni, sviluppo con abbonamento e produzione con API key, sezione "Uso consentito" (sezione 2), sicurezza (sezione 8), riferimento rapido (esempi, endpoint, header, comandi), limiti noti, FAQ. Riferimento completo della configurazione in `docs/configuration.it.md`.

Licenza: GNU GPL v3 o successiva (`LICENSE`). Decisioni aperte per il proprietario del repository: nome definitivo del repository (non deve contenere "Claude" né "Anthropic", sezione 2), immagine Docker ufficiale (solo modalità `api`).

## 11. Test e criteri di accettazione

La CI non usa mai il vero Claude Code né credenziali: tutto gira contro un finto binario. I test reali sono manuali e documentati.

### 11.1 Finto binario `fakecli`

Programma Go in `testdata/fakecli`, compilato dai test in una directory temporanea e indicato come `claude_binary`. Legge lo scenario dalla variabile `FAKE_SCENARIO`, registra argomenti, ambiente, cwd e stdin in un file indicato da `FAKE_RECORD`, ed emette eventi stream-json. Scenari minimi:

| Scenario | Comportamento |
| --- | --- |
| `success` | `init`, un `assistant` con testo e `tool_use`, `result` di successo con usage e costo |
| `max_turns` | `result` con subtype `error_max_turns` |
| `exec_error` | `result` con `is_error` vero |
| `no_result` | Scrive su stderr ed esce con codice 1 senza `result` |
| `slow` | Attende N secondi prima del `result` (per timeout, keepalive, cancellazione) |
| `spawn_child` | Avvia un processo figlio che dorme, per verificare la terminazione dell'albero |
| `resume_missing` | Fallisce se riceve `--resume`, riesce senza |
| `new_session_id` | Restituisce nel `result` un `session_id` diverso da quello ricevuto |
| `garbage` | Righe non JSON e tipi sconosciuti intercalati a un flusso valido |
| `tools` | Si comporta come la CLI con funzioni del client: avvia il server MCP indicato nell'ultimo `--mcp-config` (il vero `agent-bridge mcp-tools`), elenca i tool, esegue le chiamate di `FAKE_TOOL_CALLS` (in parallelo con `FAKE_TOOL_PARALLEL=1`) e risponde con i risultati; senza `--mcp-config` risponde con il prompt ricevuto |

### 11.2 Test unitari obbligatori

1. Prompt: tutti i casi della sezione 6.4, content stringa e array, parti non testuali, limite di dimensione.
2. Configurazione: default, precedenza flag > env > file, ogni errore di validazione della sezione 4.3, percorsi relativi.
3. Modalità di autenticazione: le quattro combinazioni di sezione 8.1 e la pulizia dell'ambiente del figlio (sezione 7.3).
4. Argomenti: per ogni campo dell'agente, presenza e forma del flag corrispondente; `--tools ""` in modalità solo chat.
5. Override: percorsi fuori dalle radici, symlink che escono dalle radici, tool non presenti nell'agente, header con override disabilitati.
6. Sessioni: TTL, LRU, persistenza su file e ricarica, scrittura atomica, `X-Session-Reset`.
7. Redazione: chiavi `sk-ant-` e chiave del proxy in log ed errori.
8. Funzioni del client: validazione di `tools`/`tool_choice`/`functions`, rendering di chiamate e risultati nel prompt, argomenti e ambiente con funzioni (anche in solo chat), protocollo MCP (`initialize`, `tools/list`, `tools/call`, errori), broker (token, deregistrazione), servizio `turn` con fake delle interfacce: chiamata singola, parallela, sequenziale con `parallel_tool_calls: false`, risultato mancante, timeout di attesa e ripartenza dalla cronologia, `tool_choice`.
9. Orchestratori esterni: `system_messages` (`prompt`, `append`, `replace`, con e senza system prompt dell'agente), modalità `stateless` (processo chiuso, esito `tool_calls`, sessione cancellata, passo successivo dalla cronologia), risoluzione di `bridge/<modello>` e nomi non validi.
10. Immagini: parsing di `image_url` e segnaposto `[image N]` (sessioni nuove e riprese), messaggio utente stream-json con blocchi immagine, URL `data:`, riconoscimento del formato, limiti di dimensione e numero, rifiuto degli indirizzi non pubblici, download, `images.enabled: false`.
11. Parametri di generazione: validazione di `n`, `stop`, `max_tokens`/`max_completion_tokens`, `reasoning_effort`, `response_format`; taglio per `stop`; `--effort`, `--json-schema` e `CLAUDE_CODE_MAX_OUTPUT_TOKENS` passati al processo; output strutturato come contenuto e relativi errori.

### 11.3 Test di integrazione (con `httptest` e `fakecli`)

1. Il client ufficiale `github.com/openai/openai-go` esegue una chat completion e uno stream senza errori e legge `usage`.
2. Secondo turno con stessa `X-Session-Id`: il figlio riceve `--resume` con l'ID dell'ultimo evento e su stdin solo i nuovi messaggi utente.
3. Due richieste parallele sulla stessa sessione: il figlio non viene mai eseguito due volte in contemporanea.
4. `max_concurrent` = 1 e `max_queue` = 1: la terza richiesta concorrente riceve 429 con `Retry-After`.
5. Timeout dell'agente: 504 e nessun processo residuo (incluso il figlio di `spawn_child`).
6. Disconnessione del client durante lo stream: processo terminato entro 10 secondi.
7. Streaming: sequenza esatta di sezione 5.3, keepalive con intervallo ridotto nei test, contenuto finale identico al non-stream.
8. Bind non loopback senza `allow_remote` o senza chiave: il server non parte.
9. Function calling con il client ufficiale `openai-go`, senza modifiche al ciclo standard: risposta `tool_calls`, richiesta successiva con `ToolMessage`, risposta finale; lo stesso in streaming con `ChatCompletionAccumulator`. Un solo processo per conversazione.
10. Function calling via HTTP: chiamate parallele in un'unica risposta con `content: null`, streaming con `delta.tool_calls`, formato deprecato `functions`, risultati arrivati dopo il timeout di attesa (nuovo processo con cronologia completa).
11. Orchestratori esterni via HTTP: header `X-Agent-Bridge-Tool-Mode: stateless` e chiave `client_tools.mode`, header `X-Agent-Bridge-System-Mode` e chiave `system_messages`, modello `bridge/<modello>` in richiesta e in `GET /v1/models/{id}`.
12. Immagini via HTTP: URL `data:` e `http(s)` in richieste non stream e stream, errori prima dell'avvio di qualsiasi processo, limiti, client ufficiale `openai-go` con `ImageContentPart`.
13. Parametri di generazione via HTTP: `n` scelte in JSON e in streaming (indici, totali di usage, nessuna ripresa di sessione, funzioni del client senza stato), errori che fanno fallire l\'intera richiesta, `stop`, `response_format`, client ufficiale `openai-go` con `N`, `ReasoningEffort` e `ResponseFormat`.

### 11.4 Verifica manuale prima di ogni release

Checklist in `docs/release-checklist.it.md`, eseguita con il vero Claude Code alla versione minima e all'ultima: richiesta con modello built-in in modalità `subscription`; agente con tool che modifica un file in un repository di prova; ripresa di sessione; stessa prova in modalità `api`; `doctor --live` su Linux, macOS e Windows. Per il function calling: ciclo completo con un modello built-in (verifica che i tool MCP siano disponibili con `--tools ""`), con un agente che ha già un proprio `mcp_config` (verifica di `--mcp-config` ripetuto), e attesa dei risultati oltre i timeout di default della CLI (verifica di `MCP_TOOL_TIMEOUT`).

### 11.5 Definizione di completato per la v1.0

Tutti i test delle sezioni 11.2 e 11.3 verdi su tre sistemi operativi, checklist 11.4 eseguita, README completo con sezione "Uso consentito", nessuna dipendenza runtime oltre alla libreria standard, nessun accesso a file di credenziali verificabile con una ricerca nel codice di `.credentials`, `Keychain` e `oauth`.

## 12. Milestone e issue

Il lavoro si divide in cinque milestone, ognuna rilasciabile come versione di anteprima; ogni riga sotto è una issue GitHub, con i riferimenti alle sezioni da citare nella descrizione e le dipendenze.

| # | Milestone | Issue | Sezioni | Dipende da |
| --- | --- | --- | --- | --- |
| 1 | M1 Nucleo (v0.1) | Scheletro repo, CI, `version`, struttura pacchetti | 10.1, 10.2 | — |
| 2 | M1 Nucleo (v0.1) | Finto binario `fakecli` con tutti gli scenari | 11.1 | 1 |
| 3 | M1 Nucleo (v0.1) | Caricamento e validazione configurazione, modelli built-in | 4 | 1 |
| 4 | M1 Nucleo (v0.1) | Runner: argomenti, ambiente, parsing eventi, esiti | 7.1–7.4 | 2, 3 |
| 5 | M1 Nucleo (v0.1) | Costruzione del prompt | 6.4 | 1 |
| 6 | M1 Nucleo (v0.1) | `POST /v1/chat/completions` non-stream, `/v1/models`, errori | 5.1, 5.2, 5.4 | 4, 5 |
| 7 | M2 Sessioni e streaming (v0.2) | Registro sessioni memory, chiavi, mutex, ripresa e casi limite | 6.1–6.3 | 6 |
| 8 | M2 Sessioni e streaming (v0.2) | Store su file, TTL, LRU | 6.1 | 7 |
| 9 | M2 Sessioni e streaming (v0.2) | Streaming SSE e `stream_progress` | 5.3 | 6 |
| 10 | M2 Sessioni e streaming (v0.2) | Semaforo, coda, 429 | 7.5 | 4 |
| 11 | M2 Sessioni e streaming (v0.2) | Timeout, cancellazione, terminazione albero processi Unix e Windows | 7.6 | 4 |
| 12 | M3 Sicurezza (v0.3) | Modalità di autenticazione e pulizia ambiente | 8.1, 7.3 | 4 |
| 13 | M3 Sicurezza (v0.3) | Bearer, bind di rete, CORS | 8.2 | 6 |
| 14 | M3 Sicurezza (v0.3) | Override per richiesta e permessi pericolosi | 8.3, 8.4 | 6 |
| 15 | M3 Sicurezza (v0.3) | Redazione, request ID, regole sui log | 8.6 | 6 |
| 16 | M4 Osservabilità (v0.4) | Log slog, file di utilizzo, `/stats` | 9.1–9.3 | 6 |
| 17 | M4 Osservabilità (v0.4) | `/metrics`, `/healthz`, `/readyz` | 9.4, 5 | 16 |
| 18 | M4 Osservabilità (v0.4) | `check-config` e `doctor` | 4.1 | 3, 4 |
| 19 | M5 Release (v1.0) | Test di integrazione con `openai-go` | 11.3 | 7–15 |
| 20 | M5 Release (v1.0) | GoReleaser e installazione | 10.3 | 1 |
| 21 | M5 Release (v1.0) | README, "Uso consentito", `docs/configuration.it.md`, esempi client | 2, 10.4 | tutte |
| 22 | M5 Release (v1.0) | Checklist manuale ed esecuzione su tre sistemi | 11.4, 11.5 | 19–21 |
| 23 | M6 Function calling (v1.1) | Struttura a livelli con interfacce e servizio `turn` | 3, 10.1 | 6–11 |
| 24 | M6 Function calling (v1.1) | Broker, server MCP `mcp-tools`, definizioni | 13.1, 8.5 | 23 |
| 25 | M6 Function calling (v1.1) | Turni in attesa, ripresa tramite `tool_call_id`, timeout di attesa | 13.4, 13.5 | 24 |
| 26 | M6 Function calling (v1.1) | Richiesta e risposta OpenAI: `tools`, `tool_choice`, `tool_calls`, streaming, formato deprecato | 5, 13.2, 13.3 | 25 |
| 27 | M6 Function calling (v1.1) | Test con `openai-go`, scenario `tools` del finto binario, checklist | 11 | 26 |
| 28 | M7 Orchestratori esterni (v1.2) | Funzioni in modalità `stateless` | 13.8 | 25 |
| 29 | M7 Orchestratori esterni (v1.2) | `system_messages` e header `X-Agent-Bridge-System-Mode` | 13.8, 6.4 | 23 |
| 30 | M7 Orchestratori esterni (v1.2) | Modelli `bridge/<modello>` | 13.8, 4.4 | 3 |
| 31 | M7 Orchestratori esterni (v1.2) | Documentazione in inglese e italiano | 10.4 | 28–30 |
| 32 | M8 Immagini e pubblicazione (v1.3) | Ingresso `image_url`, impostazioni `images`, `media.Resolver` | 15 | 6 |
| 33 | M8 Immagini e pubblicazione (v1.3) | Prefisso dei modelli configurabile, default neutro | 2, 4.4 | 3 |
| 34 | M8 Immagini e pubblicazione (v1.3) | Licenza GPL v3 | 10.4 | — |
| 35 | M9 Parametri di generazione (v1.4) | `reasoning_effort`, `response_format`, `max_tokens` mappati sulla CLI | 16 | 6 |
| 36 | M9 Parametri di generazione (v1.4) | Emulazione di `stop` e `n`, anche in streaming | 16 | 35 |

Per chi implementa (persona o agente su GitHub): ogni PR chiude una sola issue, include i test della sezione 11 che la riguardano e non introduce dipendenze runtime. In caso di dubbio sul comportamento della CLI di Claude Code vale la documentazione ufficiale, e la discrepanza va segnalata nella PR.

## 13. Function calling (funzioni del client)

Il bridge supporta il function calling dell'API OpenAI: il client definisce funzioni in `tools`, il modello le chiama, il client le esegue e rimanda i risultati, e il modello prosegue. Il ciclo standard degli SDK funziona senza modifiche. Queste funzioni sono distinte dai tool dell'agente (sezione 1): girano nel codice del client, mai sul server.

### 13.1 Meccanismo

Il binario di Claude Code non viene modificato né il suo protocollo reimplementato. Le funzioni del client vengono offerte alla CLI come tool di un **server MCP standard** servito dal bridge stesso:

1. Per un turno con funzioni, il bridge registra il turno sul broker interno (sezione 8.5) e scrive in una cartella temporanea privata la specifica delle funzioni e una configurazione MCP che avvia `agent-bridge mcp-tools -spec <file>` come server `functions`.
2. Claude Code riceve `--mcp-config <file>` e `--allowedTools mcp__functions`; il modello vede i tool `mcp__functions__<nome>` con descrizione e schema dei parametri.
3. Quando il modello chiama un tool, `mcp-tools` inoltra la chiamata al broker, che la consegna al turno; la chiamata resta in sospeso.
4. Dopo `batch_window_ms` dalla prima chiamata del gruppo, le chiamate raccolte vengono restituite al client come `tool_calls`, ciascuna con un id `call_` + 24 caratteri esadecimali.
5. In modalità `stateful` (default) il processo di Claude Code resta vivo, in attesa: la richiesta successiva che contiene i messaggi `tool` con quegli id riprende il turno, i risultati sbloccano le chiamate in sospeso e il modello continua nello stesso processo. In modalità `stateless` (sezione 13.8) il processo viene chiuso e la richiesta successiva riparte dalla cronologia.

### 13.2 Richiesta

| Campo | Supporto |
| --- | --- |
| `tools` | Solo `type: "function"`; `function.name` `[a-zA-Z0-9_-]{1,64}`, univoco; `description`; `parameters` (oggetto JSON Schema, default oggetto vuoto); `strict` accettato |
| `tool_choice` | `auto` (default), `none` (funzioni non offerte al modello), `required`, `{"type":"function","function":{"name":…}}` |
| `parallel_tool_calls` | Default `true`; con `false` le chiamate sono restituite una alla volta |
| `functions`, `function_call` | Formato deprecato: `none`, `auto`, `{"name":…}`; una chiamata alla volta |
| Messaggi | `assistant` con `tool_calls` (o `function_call`), `tool` con `tool_call_id`, `function` con `name` |

Errori 400 `invalid_request_error`, come l'API OpenAI: tool di tipo diverso da `function`, nome non valido o duplicato, `parameters` non oggetto, `tool_choice`/`function_call` senza funzioni, valore non valido, o che nomina una funzione inesistente; `function_call: "required"` nel formato deprecato.

### 13.3 Risposta

Non-stream: `message.tool_calls` (`id`, `type: "function"`, `function.name`, `function.arguments` come stringa JSON), `message.content` uguale al testo scritto dal modello prima delle chiamate oppure `null`, `finish_reason: "tool_calls"`. Formato deprecato: `message.function_call` e `finish_reason: "function_call"`. `usage` di una risposta `tool_calls` è quello dell'ultimo messaggio dell'assistente letto da stream-json; il totale del turno è nella risposta finale, nei log e nelle statistiche.

Stream: sequenza della sezione 5.3, con un chunk `delta.tool_calls` per chiamata (`index` progressivo) prima del chunk con `finish_reason`.

### 13.4 Ripresa e stato

- Il turno in attesa mantiene lock di sessione e slot di concorrenza. Gli id delle chiamate in attesa indicizzano il turno; non serve `X-Session-Id`.
- Una richiesta viene agganciata a un turno in attesa se almeno uno dei `tool_call_id` dei suoi messaggi `tool` (successivi all'ultimo `assistant`) appartiene a quel turno e l'agente coincide. In questo caso `Admit` non applica i controlli di sessione occupata e coda piena.
- Le chiamate in attesa senza risultato nella richiesta ricevono un risultato di errore ("The caller did not return a result for this call.").
- Le chiamate arrivate mentre il turno era in attesa vengono restituite nella risposta successiva.
- La disconnessione del client mentre è agganciato al turno termina il processo (come in 6.3).

### 13.5 Scadenze e ripartenza

- Se i risultati non arrivano entro `client_tools.wait_timeout_seconds` il processo viene fermato con esito `timeout`, registrato nei log e nelle statistiche.
- Una richiesta con risultati che non trova un turno in attesa (scaduto, server riavviato, id sconosciuti) avvia un processo nuovo con la cronologia completa costruita come in 6.4, chiamate e risultati inclusi. Il risultato è corretto, solo più lento.
- Nel formato deprecato `functions` i risultati non hanno id: il processo viene fermato appena restituita la chiamata e la richiesta successiva riparte dalla cronologia.
- `timeout_seconds` dell'agente resta il limite complessivo del processo, attese comprese.

### 13.6 Differenze dall'API OpenAI

- `tool_choice: "required"`, una funzione specifica e `parallel_tool_calls: false` sono resi come istruzioni aggiunte al system prompt: il modello le segue, ma non sono vincoli garantiti. Con `parallel_tool_calls: false` il bridge restituisce comunque una sola chiamata per risposta.
- Gli schemi `parameters` (e `strict`) guidano il modello ma gli argomenti non vengono validati.
- Il raggruppamento delle chiamate parallele dipende da `batch_window_ms`: chiamate emesse a distanza maggiore arrivano in risposte successive.
- Un turno in attesa occupa uno slot di `max_concurrent`.

### 13.7 Configurazione

| Chiave `client_tools` | Default | Significato |
| --- | --- | --- |
| `enabled` | `true` | Con `false` i campi `tools`/`functions` vengono accettati e ignorati |
| `wait_timeout_seconds` | `600` | Attesa massima dei risultati |
| `batch_window_ms` | `300` | Finestra di raccolta delle chiamate parallele |
| `mode` | `stateful` | `stateful` (sezioni 13.4–13.5) o `stateless` (sezione 13.8) |

### 13.8 Uso da orchestratori esterni

Quando agenti, grafo e ciclo di vita sono gestiti da una piattaforma esterna (per esempio un sistema ReAct con un proprio grafo), il bridge deve comportarsi come un modello senza stato. Tre funzionalità lo permettono senza file di configurazione:

1. **Funzioni in modalità senza stato** (`client_tools.mode: "stateless"` o header `X-Agent-Bridge-Tool-Mode: stateless`). Restituite le `tool_calls`, il processo viene chiuso. La richiesta successiva avvia un processo nuovo con la cronologia inviata dal client (sezione 6.4), che resta l'unica fonte di verità: ramificazioni, retry, modifiche della cronologia e checkpoint del grafo sono rispettati. Il turno è registrato con esito `tool_calls` e i token dell'ultimo messaggio. La sessione eventualmente associata viene cancellata, perché termina con chiamate senza risultato e non va ripresa. Le chiamate parallele restano supportate. Costo: un processo per passo, con la latenza di avvio e meno riuso della cache.
2. **Messaggi `system` come system prompt** (`system_messages` o header `X-Agent-Bridge-System-Mode`). Con `replace` i messaggi `system`/`developer` della richiesta diventano il system prompt (`--system-prompt`) al posto di quello della CLI; con `append` vi si aggiungono (`--append-system-prompt`). Il system prompt configurato nell'agente, se presente, resta in testa. I messaggi sono applicati a ogni turno, anche nelle sessioni riprese, e non compaiono nel prompt.
3. **Qualsiasi modello** (`builtin_any_model`, default `true`). Oltre ai tre alias, un nome `bridge/<modello>` con `<modello>` di 1-100 caratteri `[a-zA-Z0-9._[]-]` (il primo alfanumerico) crea al volo un agente solo chat che passa `<modello>` a `--model`. Gli agenti configurati hanno la precedenza. `GET /v1/models` elenca solo agenti e alias, `GET /v1/models/{id}` risponde anche per questi nomi.

Configurazione consigliata per questo scenario: `{"client_tools": {"mode": "stateless"}, "system_messages": "replace"}` e nessun `X-Session-Id`.

## 14. Registro delle modifiche rispetto alla rev. 1.0

| Punto | Rev. 1.0 | Rev. 1.2 (implementata) | Motivo |
| --- | --- | --- | --- |
| Function calling | `tools` & co. accettati e ignorati | Supportati (sezione 13) | Compatibilità completa con i client OpenAI |
| Struttura del codice | Pacchetti per funzione | Livelli con interfacce e servizio `turn` (sezione 3) | Manutenibilità e testabilità |
| Commenti SSE | Non specificato | Senza riga vuota successiva (5.3) | Compatibilità con `openai-go` |
| Arresto del server | Non specificato | 503 `server_error` (5.4) | Distinguere l'arresto da un errore della CLI |
| Finto binario | `testdata/fakeclaude` | `testdata/fakecli` | Nome neutro nei percorsi del repository |
| Prompt | Messaggi `tool` nella cronologia | Messaggi `tool` dopo l'ultimo `assistant` nella richiesta corrente; chiamate dell'assistente rese con `<tool_call>` (6.4) | Ordine corretto di chiamate e risultati |
| Testo semplice | Un solo messaggio `user` e nessun `system` | Un solo messaggio in totale, di ruolo `user` | Evitare di perdere messaggi `assistant` o `tool` precedenti |
| Versione minima CLI | Da definire | `2.1.128` (`runner.MinCLIVersion`) | Limite di 10 MB su stdin usato da 6.4 |
| Orchestratori esterni | Non previsto | Modalità senza stato, `system_messages`, modelli qualsiasi (13.8) | Integrazione con piattaforme che gestiscono grafo e ciclo di vita |
| Formato deprecato `functions` | — | La sessione interrotta sulle chiamate viene cancellata, non ripresa | Evitare di riprendere sessioni con chiamate senza risultato |
| Registro sessioni | ID e metadati | Anche un'impronta (hash) dei messaggi `system` del primo turno | Debug del cambio di `system` (6.4), senza conservarne il testo |
| Sessioni senza chiave | Implicito | Ogni richiesta senza chiave è una sessione nuova con la cronologia completa (6) | Chiarire il comportamento senza stato |
| Documentazione | Italiano | Inglese principale, italiano in `*.it.md` (10.4) | Pubblico internazionale |
| Prefisso dei modelli | `claude-code/` | `bridge/`, configurabile con `builtin_prefix` (4.4) | I nomi di prodotto non devono usare il nome "Claude Code" (sezione 2) |
| Immagini | Rifiutate (400) | Accettate come parti `image_url` (15) | Parità con i client OpenAI |
| Licenza | Da decidere | GNU GPL v3 o successiva (10.4) | Decisione del proprietario |
| Parametri di generazione | Tutti accettati e ignorati | `reasoning_effort`, `response_format`, `max_tokens` mappati; `stop`, `n` emulati (16) | Usare ogni opzione offerta dalla CLI |
| CLI | Solo la CLI predefinita | Interfaccia `runner.Backend`, CLI predefinita, CLI Codex (17) e CLI Kimi Code tramite ACP (18) | Stessa API sopra uno qualsiasi di questi agenti |
| Codice di errore a monte | `claude_code_error` | `cli_error` | Neutro con ogni backend |
| Campo id di sessione in log, file dei consumi e registro | `claude_session_id` | `cli_session_id` | Neutro con ogni backend |

## 15. Immagini

I messaggi possono contenere parti di contenuto `image_url`, come nell'API OpenAI, con qualsiasi modello, anche solo chat: le immagini vanno al modello, non in file sul server.

### 15.1 Sorgenti e validazione

- URL `data:` con contenuto base64; URL `http(s)` scaricati dal server se `images.fetch_urls` è `true` (default).
- I download rifiutano gli indirizzi non pubblici (loopback, privati, link-local, condivisi 100.64/10, multicast, non specificati) a meno che `images.allow_private_networks` sia `true`. Il controllo avviene nel dialer sull'indirizzo risolto di ogni connessione, redirect compresi, quindi trucchi DNS e redirect verso host interni sono bloccati. In questa modalità i download ignorano `HTTP(S)_PROXY`, che nasconderebbe la destinazione reale. Redirect solo verso `http`/`https`, al massimo 5.
- Formato riconosciuto dai byte, non dal tipo dichiarato: PNG, JPEG, GIF, WebP. Dimensione fino a `images.max_bytes` (default 5 MB) ciascuna, al massimo `images.max_count` (default 20) per richiesta; timeout di download `images.fetch_timeout_seconds` (default 20).
- Ogni problema produce 400 `invalid_request_error` con il numero dell'immagine ("image 2: …") prima che parta qualsiasi processo. Con `images.enabled: false` le richieste con immagini ricevono 400 `unsupported_content`.

### 15.2 Prompt e ingresso della CLI

- Nel testo del prompt (sezione 6.4) ogni immagine diventa un segnaposto numerato `[image N]` nella posizione in cui compariva; la numerazione segue l'ordine del prompt costruito.
- Con immagini il bridge passa `--input-format stream-json` e scrive su stdin un solo messaggio utente stream-json: un blocco di testo con il prompt, poi per ogni immagine un blocco di testo `[image N]` e un blocco immagine `{"type":"image","source":{"type":"base64","media_type":…,"data":…}}`. Senza immagini lo stdin resta testo semplice, come nella sezione 6.4.
- L'intera riga su stdin, base64 compreso, deve restare entro 9 MB (sezione 6.4).
- Sessione nuova: vengono inviate le immagini di tutta la cronologia. Sessione ripresa: solo quelle dei messaggi nuovi, numerate da 1.
- `detail` è accettato e ignorato. Le immagini nella ripresa di un turno in attesa della sezione 13.4 (risultati dei tool) non vengono inviate.

### 15.3 Da verificare sulla CLI reale (checklist 11.4)

Il formato esatto dell'ingresso stream-json per i blocchi immagine, il limite di stdin con l'ingresso strutturato e il comportamento con `--resume`.

## 16. Parametri di generazione

Il bridge pilota la CLI e non chiama mai direttamente l'API del modello (sezione 2), quindi può rispettare solo i parametri OpenAI che la CLI sa esprimere. Altri due sono emulati.

| Parametro OpenAI | Mappatura | Note |
| --- | --- | --- |
| `reasoning_effort` | `--effort <livello>` | `none` e `minimal` → `low`; `low`, `medium`, `high` invariati; `xhigh` e `max` accettati come estensione. Altri valori → 400. I livelli disponibili dipendono dal modello |
| `response_format` | `text`: nulla. `json_object`: `--json-schema '{"type":"object"}'`. `json_schema`: `--json-schema` con `json_schema.schema` (compatto, al massimo 64 KB) | La risposta è lo `structured_output` del risultato, compatto, come `content`. Subtype `error_max_structured_output_retries`, o successo senza `structured_output` → 502 `cli_error`. `json_schema.name` e `strict` sono accettati; la CLI valida sempre |
| `max_completion_tokens`, `max_tokens` | Variabile `CLAUDE_CODE_MAX_OUTPUT_TOKENS` per il processo (sostituisce un valore ereditato); prevale `max_completion_tokens` | Solo interi positivi. Limita ogni risposta del modello nel turno, non il totale del turno; `finish_reason` non diventa `length` |
| `stop` | Stringa o array di al massimo 4 stringhe non vuote; il testo finale viene tagliato prima della prima occorrenza | Emulato dopo la generazione. Non applicato al JSON di `response_format`, ai testi di avanzamento in streaming né agli argomenti delle chiamate a funzione |
| `n` | 1-8 turni indipendenti in parallelo, la scelta `i` dal turno `i` | Ogni scelta riceve una chiave di sessione nuova (nessuna ripresa) e funzioni del client senza stato (sezione 13.8). Usage, turni e costo sono sommati, la durata è la più lunga. Streaming: prima i chunk di ruolo per tutti gli indici, poi i chunk finali di ogni scelta appena completa. Il primo errore annulla le altre scelte e fa fallire la richiesta. Un rifiuto di `Admit` rilascia le scelte già ammesse (`turn.Service.Release`) |
| `temperature`, `top_p`, `seed`, `logprobs`, `top_logprobs`, `presence_penalty`, `frequency_penalty`, `logit_bias` | — | Accettati e ignorati: nessun equivalente nella CLI |

Da verificare sulla CLI reale (checklist 11.4): i livelli di `--effort` per modello, il campo `structured_output` e il subtype di errore con `-p --output-format stream-json`, e `CLAUDE_CODE_MAX_OUTPUT_TOKENS`.

## 17. Backend CLI e la CLI Codex

L'interfaccia `runner.Backend` isola tutto ciò che dipende dalla CLI: costruzione dell'invocazione di un turno (`Prepare`: argomenti, ambiente, stdin, file temporanei), lettura dell'output (`NewParser`), riconoscimento di una sessione ripresa che non esiste più (`ResumeMissing`), versione minima e se i consumi sono cumulativi. L'impostazione `backend` (`claude` per default, oppure `codex`; flag `-backend`, variabile `AGENT_BRIDGE_BACKEND`) sceglie l'implementazione all'avvio; API HTTP, sessioni, coda, funzioni del client, immagini e statistiche non cambiano.

| Aspetto | `claude` (`StreamJSONBackend`) | `codex` (`CodexBackend`) |
| --- | --- | --- |
| Comando | `-p --output-format stream-json` | `exec --json`, `exec resume --json <thread>` |
| Impostazioni | Flag | Override `-c chiave=valore` |
| Solo chat | `--tools ""` | `sandbox_mode="read-only"`, funzioni con strumenti integrati disattivate, ricerca web disattivata |
| Permessi degli strumenti | `permission_mode`, `allowed_tools`, `disallowed_tools` | Campo `sandbox` dell'agente; `approval_policy="never"` |
| Server MCP, funzioni del client | File `--mcp-config` | Gli stessi file convertiti in `mcp_servers.*`, strumenti pre-approvati |
| Immagini | Messaggio utente stream-json | File temporanei e `--image` |
| Output strutturato | `--json-schema`, `structured_output` | `--output-schema`, messaggio finale interpretato come JSON; `json_object` come istruzione |
| Massimo token in uscita | `CLAUDE_CODE_MAX_OUTPUT_TOKENS` | Non disponibile, ignorato |
| Evento di risultato | `result` | Sintetizzato da `turn.completed` / `turn.failed` |
| Consumi | Per turno | Totali del thread: il bridge li salva nella sessione (`cli_usage`) e restituisce la differenza |
| Costo | `total_cost_usd` | Non riportato (0) |
| Credenziali rimosse in `subscription` | `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` | `CODEX_API_KEY`, `CODEX_ACCESS_TOKEN`, `OPENAI_API_KEY` |
| La modalità `api` richiede | `ANTHROPIC_API_KEY` | `CODEX_API_KEY` |
| Versione minima | `2.1.128` | `0.162.0` |

La mappatura di Codex è stata verificata con la CLI reale (0.162.1) collegata a un finto server locale della Responses API: chat, ripresa, function calling tramite MCP, immagini, `--output-schema`, effort, istruzioni sostituite, ripiego sulla ripresa e consumi cumulativi. Guida utente: `docs/codex.it.md`.

## 18. CLI Kimi Code tramite ACP

Con `backend: "kimi"` il bridge esegue `kimi acp` ed è il client di una sessione Agent Client Protocol (JSON-RPC 2.0, un messaggio per riga su stdin e stdout). La modalità print della CLI (`kimi -p`) non è usata: riceve il prompt solo come argomento della riga di comando, approva ogni strumento da sola e non ha server MCP per invocazione.

Per tenere un dialogo su stdin, il `Parser` di un backend può implementare anche `runner.Responder`: l'executor gli passa allora lo stdin del processo invece di scrivere un prompt fisso. `KimiBackend.Prepare` restituisce un parser di questo tipo (`acpDialogue`) costruito per il turno.

| Passo | Messaggio |
| --- | --- |
| Avvio | `initialize` (versione del protocollo 1, nessuna capacità di file system o terminale) |
| Sessione | `session/new` {`cwd`, `mcpServers`, `additionalDirectories`} oppure `session/resume` {`sessionId`, `cwd`, `mcpServers`} |
| Impostazioni | `session/set_config_option` per `model` (errore se sconosciuto), `thinking` (facoltativo: un errore è solo registrato) e `mode` (se diversa da quella corrente) |
| Prompt | `session/prompt` con un blocco di testo (prompt di sistema dell'agente in `<system_instructions>` sulle nuove sessioni; istruzione JSON per `response_format`; una `/` iniziale viene neutralizzata) e i blocchi immagine |
| Aggiornamenti | `agent_message_chunk` raccolti; `tool_call` emette il testo come testo intermedio e registra lo strumento |
| Richieste dell'agente | `session/request_permission` risposta secondo la politica (sezione in `docs/kimi.it.md`); ogni altra richiesta riceve l'errore -32601 |
| Fine | La risposta a `session/prompt`: `end_turn` con testo → successo, senza testo → errore; `max_tokens`/`max_turn_requests` → `finish_reason: "length"`; `refusal`, `cancelled` → errore; un errore JSON-RPC → errore. Poi stdin viene chiuso e la CLI termina |

Altre scelte: `max_turns` → `KIMI_LOOP_MAX_STEPS_PER_TURN`; timeout delle funzioni del client → `KIMI_MCP_TOOL_TIMEOUT_MS`; `KIMI_DISABLE_TELEMETRY=1` e `KIMI_CODE_NO_AUTO_UPDATE=1` se non impostate; credenziali rimosse in `subscription`: `KIMI_MODEL_API_KEY`, `KIMI_MODEL_NAME`, `KIMI_API_KEY`; la modalità `api` richiede `KIMI_MODEL_NAME` e `KIMI_MODEL_API_KEY`; versione minima `2.2.0`. ACP non riporta consumi né costi (0).

Verificato con la CLI Kimi Code reale 2.2.0 collegata a un finto server locale delle chat completions: chat, ripresa e ripiego, scelta di modello e thinking, politica dei permessi (`Bash` rifiutato e approvato), function calling stateful e stateless tramite MCP, immagini, output strutturato e `doctor -live`. Guida utente: `docs/kimi.it.md`.
