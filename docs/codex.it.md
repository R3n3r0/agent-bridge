# Backend Codex

[English](codex.md) · **Italiano**

agent-bridge può pilotare la **CLI Codex** di OpenAI (`codex exec --json`) al
posto della CLI predefinita. L'API HTTP resta la stessa: chat, streaming,
sessioni, function calling, immagini e parametri di generazione funzionano
come descritto nel [README](../README.it.md); cambia solo il processo che
risponde a ogni turno.

## Avvio rapido

Prerequisito: la CLI `codex`, versione **0.162.0 o successiva**, e un login
interattivo (`codex login`).

```sh
npm install -g @openai/codex
codex login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend codex
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/default","messages":[{"role":"user","content":"Ciao!"}]}'
```

Il backend si sceglie con `backend: "codex"` nel file di configurazione, con
il flag `-backend codex` o con `AGENT_BRIDGE_BACKEND=codex`. Il binario è
`codex` dal `PATH`, oppure `codex_binary` / `-codex-bin`.

## Modelli

| `model` | Esegue |
| --- | --- |
| `bridge/default` | Il modello configurato in Codex (`config.toml`, profilo) |
| `bridge/<modello>` | Quel modello, passato con `-m` (es. `bridge/gpt-5-codex`), con `builtin_any_model` |
| agente configurato | Il `model` dell'agente; senza, il modello configurato in Codex |

Il prefisso segue `builtin_prefix`. Gli alias della CLI predefinita (`opus`,
`sonnet`, `haiku`) non sono registrati con questo backend.

## Come si esegue un turno

Ogni impostazione è passata come override `-c chiave=valore`, quindi il file
di configurazione della CLI non viene mai modificato.

| Bridge | CLI Codex |
| --- | --- |
| Nuovo turno | `codex exec --json --skip-git-repo-check … -`, prompt su stdin |
| Sessione ripresa | `codex exec resume --json … <id thread> -` |
| Agente solo chat (modelli integrati, `tools_enabled: false`) | `sandbox_mode="read-only"`, disattivate le funzioni che danno al modello strumenti propri (shell, visualizzazione immagini, sotto-agenti, app, plugin, generazione immagini, browser e computer use…), `web_search="disabled"` |
| Agente con strumenti | `sandbox_mode` dal campo `sandbox` dell'agente; `approval_policy="never"` (nessuno può rispondere a un'approvazione) |
| `add_dirs` | `sandbox_workspace_write.writable_roots` |
| `mcp_config` | Lo stesso file JSON (`{"mcpServers": {...}}`, `command`/`args`/`env` oppure `url`/`headers`), convertito in `mcp_servers.<nome>.*`; i suoi strumenti sono pre-approvati |
| Funzioni del client | Server MCP `functions` (`agent-bridge mcp-tools`), pre-approvato, timeout delle chiamate pari al timeout dell'agente |
| `system_prompt`, modalità `append`, `system_messages: append` | `developer_instructions` (al massimo 100 KB) |
| `system_prompt_mode: replace`, `system_messages: replace` | `model_instructions_file`, un file temporaneo che sostituisce le istruzioni di base della CLI |
| Immagini | File temporanei passati con `--image`, rimossi dopo il turno |
| `reasoning_effort` | `model_reasoning_effort` (`none`, `minimal`, `low`, `medium`, `high`, `xhigh`; `max` diventa `xhigh`) |
| `response_format: json_schema` | `--output-schema` con un file temporaneo; il messaggio finale è restituito come JSON compatto |
| `response_format: json_object` | Un'istruzione di rispondere con un solo oggetto JSON; la risposta è validata come JSON |
| `max_tokens`, `max_completion_tokens` | Accettati e ignorati: la CLI non ha un equivalente |
| Consumi | Da `turn.completed`. Sulle sessioni riprese la CLI riporta i totali dell'intero thread: il bridge li salva con la sessione e restituisce solo i consumi del turno |

Gli eventi di Codex sono mappati sull'avanzamento del bridge: ogni
`agent_message` è un testo intermedio (commento SSE o contenuto, secondo
`stream_progress`), l'ultimo è la risposta; comandi shell, modifiche ai file,
ricerche web e chiamate MCP sono registrati come uso di strumenti; avvisi
(elementi `error`) e notifiche di riconnessione sono registrati a livello
debug.

## Agenti

| Campo | Con il backend codex |
| --- | --- |
| `model` | `-m`; vuoto = modello configurato in Codex |
| `sandbox` | `read-only`, `workspace-write` (predefinito) o `danger-full-access` (richiede `allow_bypass_permissions: true`) |
| `system_prompt`, `system_prompt_mode` | Vedi la tabella sopra |
| `tools_enabled`, `cwd`, `add_dirs`, `mcp_config`, `timeout_seconds`, `description` | Come con la CLI predefinita |
| `permission_mode`, `allowed_tools`, `disallowed_tools`, `max_turns` | Non supportati: errore di configurazione (anche `defaults.max_turns`) |

Esempio: [examples/agents.codex.example.json](../examples/agents.codex.example.json).

## Autenticazione

| Modalità | Credenziali |
| --- | --- |
| `subscription` | Login fatto con `codex login` (piano ChatGPT). `CODEX_API_KEY` e `CODEX_ACCESS_TOKEN` non devono essere impostate; queste e `OPENAI_API_KEY` sono rimosse dall'ambiente del processo figlio |
| `api` | `CODEX_API_KEY` obbligatoria (chiave API OpenAI). `codex exec` non legge `OPENAI_API_KEY` |

`doctor` controlla la versione della CLI e, in modalità `subscription`,
`codex login status`; `doctor -live` esegue un turno di prova sul modello
configurato in Codex.

La directory di configurazione di Codex (`CODEX_HOME`, predefinita
`~/.codex`) resta caricata: modello, provider, profili ed eventuali server MCP
definiti lì valgono per ogni turno, compresi quelli solo chat. Su una macchina
dedicata al bridge tienila minima, oppure punta `CODEX_HOME` a una directory
dedicata.

## Differenze rispetto alla CLI predefinita

- `cost_usd` è sempre 0: la CLI non riporta un costo.
- `finish_reason: "length"` non si verifica mai (niente `max_turns`).
- `X-Agent-Allowed-Tools` non è utilizzabile (gli agenti non hanno
  `allowed_tools`).
- I messaggi di sistema delle richieste successive sono ignorati sulle
  sessioni riprese, come con la CLI predefinita: Codex mantiene le istruzioni
  developer del primo turno.
- I consumi non sono disponibili nelle risposte che si fermano sulle chiamate
  a funzioni del client; sono riportati nella risposta finale del turno.
- Lo schema JSON di `json_schema` deve essere accettato dalla modalità strict
  di OpenAI (`additionalProperties: false`, ogni proprietà in `required`);
  altrimenti l'errore a monte è restituito come `cli_error`.

## Uso consentito

Valgono gli stessi principi della CLI predefinita (vedi il README): il bridge
non legge mai le credenziali della CLI, un'istanza `subscription` serve solo
il titolare dell'account e le istanze condivise usano una chiave API.
Verifica i termini attuali del tuo piano OpenAI prima di esporre un'istanza a
chiunque altro.
