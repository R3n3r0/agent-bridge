# Backend Kimi

[English](kimi.md) · **Italiano**

agent-bridge può pilotare la **CLI Kimi Code** (Moonshot AI) al posto della
CLI predefinita. L'API HTTP resta la stessa: chat, streaming, sessioni,
function calling, immagini e parametri di generazione funzionano come
descritto nel [README](../README.it.md); cambia solo l'agente che risponde a
ogni turno.

Il bridge esegue `kimi acp` e ci parla con l'**Agent Client Protocol**
(JSON-RPC su stdin e stdout), facendo da client ACP. A differenza della
modalità print della CLI, ACP riceve il prompt su stdin (nessun limite di
lunghezza della riga di comando), accetta immagini e server MCP per sessione,
e lascia al bridge la risposta a ogni richiesta di permesso.

## Avvio rapido

Prerequisito: la CLI `kimi` (Kimi Code), versione **2.2.0 o successiva**, e
un login interattivo (`kimi login --region global` per kimi.ai, il servizio
fuori dalla Cina continentale; senza `--region` la CLI usa kimi.com). La CLI
ricorda la regione insieme al login, quindi `kimi acp` usa lo stesso servizio.

```sh
npm install -g @moonshot-ai/kimi-code   # oppure lo script di installazione ufficiale
kimi login --region global
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend kimi
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/default","messages":[{"role":"user","content":"Ciao!"}]}'
```

Il backend si sceglie con `backend: "kimi"` nel file di configurazione, con
il flag `-backend kimi` o con `AGENT_BRIDGE_BACKEND=kimi`. Il binario è
`kimi` dal `PATH`, oppure `kimi_binary` / `-kimi-bin`. Il vecchio `kimi-cli`
in Python non è supportato: non è più mantenuto.

## Modelli

| `model` | Esegue |
| --- | --- |
| `bridge/default` | Il modello predefinito configurato in Kimi Code |
| `bridge/<modello>` | Il modello con quell'alias o nome visualizzato tra quelli configurati in Kimi Code (es. `bridge/kimi-k2`); un nome sconosciuto fallisce con l'elenco dei modelli disponibili |
| agente configurato | Il `model` dell'agente; senza, il modello predefinito |

## Come si esegue un turno

| Bridge | ACP / Kimi Code |
| --- | --- |
| Processo | `kimi acp`; il bridge invia `initialize`, poi `session/new` (o `session/resume`), le impostazioni, `session/prompt`, e chiude stdin quando il prompt termina |
| Nuova sessione | `session/new` con la directory di lavoro, `add_dirs` come `additionalDirectories` e i server MCP |
| Sessione ripresa | `session/resume` con l'id di sessione salvato e i server MCP del turno; un id sconosciuto ripiega su una nuova sessione con tutta la cronologia |
| Modello | `session/set_config_option` `model` |
| `reasoning_effort` | `session/set_config_option` `thinking`: `none`/`minimal` → `off`, lo stesso livello se il modello lo offre, il livello più alto per `xhigh`/`max`, altrimenti `on`. Ignorato se il modello non lo supporta |
| `permission_mode` | `session/set_config_option` `mode`: `default`, `plan`, `auto`, `yolo` (richiede `allow_bypass_permissions`) |
| Richieste di permesso | Risponde il bridge: funzioni del client sempre consentite; gli agenti solo chat rifiutano tutto il resto; `disallowed_tools` sempre rifiutati; in modalità `default` sono approvati solo gli `allowed_tools`, nelle altre modalità è approvato ciò che la CLI chiede ancora |
| Altre richieste dell'agente (file system, terminali, domande) | Rifiutate: il bridge non offre queste capacità del client |
| `mcp_config` | Lo stesso file JSON, convertito in server MCP ACP (stdio o HTTP) |
| Funzioni del client | Server MCP `functions` (`agent-bridge mcp-tools`), strumenti chiamati `mcp__functions__<nome>`; `KIMI_MCP_TOOL_TIMEOUT_MS` pari al timeout dell'agente |
| `system_prompt` dell'agente | ACP non ha un campo per il prompt di sistema: è inviato in cima al primo prompt di una sessione, dentro `<system_instructions>`. `system_prompt_mode: replace` si comporta come `append` |
| Messaggi `system` | Come con gli altri backend (`system_messages`: nel prompt per default) |
| Immagini | Blocchi `image` ACP |
| `response_format` | Un'istruzione di rispondere con un solo oggetto JSON (con lo schema, per `json_schema`); la risposta è validata come JSON |
| `max_turns` | `KIMI_LOOP_MAX_STEPS_PER_TURN` |
| `max_tokens` | Accettato e ignorato |
| Prompt che inizia con `/` | Viene aggiunto uno spazio perché non sia eseguito come comando della CLI |
| Ambiente | `KIMI_DISABLE_TELEMETRY=1` e `KIMI_CODE_NO_AUTO_UPDATE=1` se non già impostate |

Gli aggiornamenti di sessione sono mappati sull'avanzamento del bridge: i
frammenti di messaggio vengono raccolti, il testo prima di ogni chiamata a
uno strumento è un testo intermedio, il testo dopo l'ultima chiamata è la
risposta; chiamate agli strumenti e decisioni sui permessi sono registrate
nei log.

## Agenti

| Campo | Con il backend kimi |
| --- | --- |
| `model` | Alias o nome del modello configurato in Kimi Code; vuoto = modello predefinito |
| `permission_mode` | `default` (predefinito), `plan`, `auto`, `yolo` |
| `allowed_tools`, `disallowed_tools` | Nomi degli strumenti di Kimi Code (`Bash`, `Read`, `Write`, `Edit`, `Glob`, `Grep`, `FetchURL`, `mcp__<server>__<strumento>`…), usati per rispondere alle richieste di permesso |
| `max_turns` | Numero massimo di passi per turno |
| `system_prompt`, `tools_enabled`, `cwd`, `add_dirs`, `mcp_config`, `timeout_seconds`, `description` | Come con la CLI predefinita (per il prompt di sistema vedi la tabella sopra) |
| `sandbox` | Non supportato (solo backend codex) |

Esempio: [examples/agents.kimi.example.json](../examples/agents.kimi.example.json).

## Autenticazione

| Modalità | Credenziali |
| --- | --- |
| `subscription` | Login fatto con `kimi login` (abbonamento Kimi). `KIMI_MODEL_API_KEY` e `KIMI_API_KEY` non devono essere impostate; queste e `KIMI_MODEL_NAME` sono rimosse dall'ambiente del processo figlio |
| `api` | `KIMI_MODEL_NAME` e `KIMI_MODEL_API_KEY` obbligatorie: la CLI usa quel modello con quella chiave (piattaforma Moonshot per default; `KIMI_MODEL_PROVIDER_TYPE` e `KIMI_MODEL_BASE_URL` scelgono un altro provider) |

`doctor` controlla la versione della CLI; `doctor -live` esegue un turno di
prova sul modello predefinito, che verifica anche il login.

La directory di Kimi Code (`KIMI_CODE_HOME`, predefinita `~/.kimi-code`)
resta caricata: provider, modelli, server MCP e skill definiti lì valgono per
ogni turno. Gli agenti solo chat lasciano gli strumenti della CLI
nell'elenco del modello, ma ogni chiamata viene rifiutata.

## Differenze rispetto alla CLI predefinita

- ACP non riporta consumi né costi: `usage` e `cost_usd` sono 0.
- Il prompt di sistema dell'agente fa parte del primo prompt, non è un vero
  prompt di sistema, e non viene reinviato sulle sessioni riprese.
- Un turno che la CLI chiude senza alcun testo (per esempio dopo un errore a
  monte che registra solo nei suoi log) è riportato come `cli_error`.
- Un modello sconosciuto è segnalato all'esecuzione del turno, come
  `cli_error`.
- Nelle modalità `auto` e `yolo` la CLI non chiede per ogni strumento, quindi
  `allowed_tools` e `disallowed_tools` valgono solo per le richieste che fa.

## Uso consentito

Valgono gli stessi principi degli altri backend (vedi il README): il bridge
non legge mai le credenziali della CLI, un'istanza `subscription` serve solo
il titolare dell'account e le istanze condivise usano una chiave API.
Verifica i termini attuali del tuo piano Kimi prima di esporre un'istanza a
chiunque altro.
