# agent-bridge

[English](README.md) · **Italiano**

`agent-bridge` è un server HTTP scritto in Go che espone un'API compatibile OpenAI
(`/v1/chat/completions`, `/v1/models`) e soddisfa ogni richiesta eseguendo la CLI
ufficiale di Claude Code in modalità headless
(`claude -p --output-format stream-json`).

Può anche eseguire al suo posto la CLI Codex di OpenAI (`codex exec --json`)
o la CLI Kimi Code (`kimi acp`, tramite l'Agent Client Protocol), con la
stessa API: vedi il [backend Codex](docs/codex.it.md) e il
[backend Kimi](docs/kimi.it.md).

Qualsiasi client o framework che parla OpenAI può usarlo cambiando solo
`base_url`, `api_key` e `model`: chat, streaming, function calling (tool
calling) e immagini in ingresso funzionano con gli SDK ufficiali senza
modifiche.

In breve: un **server API compatibile OpenAI, proxy e gateway per Claude
Code**. Permette a sistemi multi-agente basati su OpenAI, orchestratori ReAct
e framework che accettano un `base_url` OpenAI personalizzato (per esempio
LangChain, LlamaIndex, CrewAI, AutoGen o gli SDK OpenAI per Python, Node e Go)
di usare Claude Code in modalità headless come modello chat completions o
come agente di programmazione, con tool calling, server MCP, sessioni,
streaming (SSE) e un modello per ogni agente. Lo stesso server è un'API
compatibile OpenAI, proxy e gateway per la CLI Codex di OpenAI e per la CLI
Kimi Code (Moonshot AI, Kimi K2), e un client ACP (Agent Client Protocol).

> **Progetto indipendente.** agent-bridge non è sviluppato, approvato né
> supportato da Anthropic. Esegue il binario ufficiale `claude` installato
> sulla macchina, senza modificarlo, e non accede mai alle sue credenziali.

- Binario singolo, sola libreria standard Go a runtime.
- Linux, macOS e Windows (amd64 e arm64).
- Stessa build per sviluppo (login con abbonamento) e produzione (API key).
- Function calling OpenAI (`tools`, `tool_calls`), anche in streaming.
- Immagini in ingresso (parti `image_url`: URL `data:` e `http(s)`).
- Tre backend CLI: quello predefinito, la CLI Codex di OpenAI
  (`backend: "codex"`, vedi [docs/codex.it.md](docs/codex.it.md)) e la CLI
  Kimi Code (`backend: "kimi"`, vedi [docs/kimi.it.md](docs/kimi.it.md)).
- Software libero, con licenza GNU GPL v3 o successiva.

## Due modi di usarlo

| | Come modello | Come agente sul server |
| --- | --- | --- |
| Chi definisce l'agente | Il tuo client o la tua piattaforma: `system`, `tools`, scelta del modello | Il file di configurazione del bridge |
| Tool disponibili | Solo le funzioni che il client manda in `tools`, eseguite dal client | Tool della CLI sulla macchina del server (file, comandi, ricerca, server MCP), più le eventuali funzioni del client |
| `model` | `bridge/opus`, `bridge/sonnet`, `bridge/haiku`, `bridge/<nome modello>` | Il nome dell'agente configurato |
| Configurazione | Nessuna | File JSON |
| Sezione | [Uso come modello](#uso-come-modello) | [Agenti sul server](#agenti-sul-server) |

I "tool" sono quindi di due tipi, entrambi supportati:

- **funzioni del client** (function calling OpenAI): le definisce e le esegue
  il client; il modello le chiama tramite `tool_calls`;
- **tool dell'agente**: `Read`, `Edit`, `Bash`, server MCP… eseguiti dalla CLI
  sul server, secondo i permessi dell'agente; il client vede solo il
  risultato finale.

## Avvio rapido

Prerequisito: la CLI `claude` installata, versione **2.1.128 o successiva**, e un
login fatto una volta in modo interattivo (`claude`).

```sh
go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@latest
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"bridge/haiku","messages":[{"role":"user","content":"Ciao!"}]}'
```

Senza file di configurazione il server ascolta su `127.0.0.1:8787` e accetta i
modelli `bridge/opus`, `bridge/sonnet`, `bridge/haiku` e
`bridge/<nome modello>` (il nome dopo il prefisso è passato alla CLI così
com'è; il prefisso `bridge/` si cambia con `builtin_prefix`). Sono modelli
*solo chat*: nessun tool del server, cartella di lavoro temporanea vuota, ma
accettano le funzioni del client e le immagini.

In alternativa scaricare un binario dalla pagina delle release (archivi e
`checksums.txt` per ogni piattaforma).

## Uso come modello

È lo scenario di chi ha già una piattaforma con agenti, grafo e ciclo di vita
propri (per esempio un sistema ReAct): il bridge si comporta come un modello
OpenAI e l'agente è quello definito nella piattaforma.

| Nella piattaforma | Nella richiesta |
| --- | --- |
| Istruzioni dell'agente | messaggio `system` |
| Tool dell'agente | `tools` |
| Modello | `bridge/<alias o nome modello>` |

Due opzioni rendono il bridge senza stato e fanno dell'agente della piattaforma
l'unico agente. Si impostano nella configurazione oppure per singola richiesta
con un header:

| Chiave di configurazione | Header | Effetto |
| --- | --- | --- |
| `client_tools.mode: "stateless"` | `X-Agent-Bridge-Tool-Mode: stateless` | Dopo aver restituito `tool_calls` il processo viene chiuso: ogni richiesta è un passo del grafo e la cronologia inviata dal client è l'unica fonte di verità. Ramificazioni, retry e checkpoint restano nella piattaforma |
| `system_messages: "replace"` | `X-Agent-Bridge-System-Mode: replace` | I messaggi `system` diventano il system prompt effettivo al posto di quello della CLI, a ogni passo. Con `append` vi si aggiungono |

Configurazione consigliata per questo scenario:

```json
{ "client_tools": { "mode": "stateless" }, "system_messages": "replace" }
```

In questo scenario non inviare `X-Session-Id`: ogni richiesta avvia un processo
nuovo con la cronologia completa. Costa un po' di latenza per passo, in cambio
di nessuno stato nascosto nel bridge.

## Function calling

Le funzioni definite dal client con `tools` funzionano come con l'API OpenAI:
il modello risponde con `tool_calls` e `finish_reason: "tool_calls"`, il client
esegue le funzioni e rimanda i risultati con messaggi `role: "tool"`, e il
modello prosegue. Funziona anche in streaming (`delta.tool_calls`) e senza
header di sessione.

```python
r = client.chat.completions.create(model="bridge/sonnet", messages=messages, tools=tools)
while r.choices[0].finish_reason == "tool_calls":
    messages.append(r.choices[0].message)
    for call in r.choices[0].message.tool_calls:
        messages.append({"role": "tool", "tool_call_id": call.id,
                         "content": run(call.function.name, call.function.arguments)})
    r = client.chat.completions.create(model="bridge/sonnet", messages=messages, tools=tools)
```

Esempio completo: [examples/python/tools.py](examples/python/tools.py).

Supportati: `tools` (tipo `function`), `tool_choice` (`auto`, `none`,
`required`, funzione specifica), `parallel_tool_calls` (chiamate parallele
restituite insieme) e il formato deprecato `functions`/`function_call`.

**Come funziona.** Le funzioni del client vengono offerte alla CLI come tool di
un server MCP standard, servito dal bridge stesso (sottocomando interno
`mcp-tools`). Quando il modello ne chiama una, il bridge restituisce la chiamata
al client. Poi, a seconda di `client_tools.mode`:

| Modalità | Cosa succede | Quando sceglierla |
| --- | --- | --- |
| `stateful` (default) | Il processo resta in attesa; la richiesta con i risultati viene riconosciuta dai `tool_call_id` e lo stesso processo riprende da dove si era fermato | Client semplici: meno latenza tra i passi |
| `stateless` | Il processo si chiude; la richiesta con i risultati avvia un processo nuovo con la cronologia completa | Orchestratori con stato proprio (vedi [Uso come modello](#uso-come-modello)) |

Differenze da conoscere:

- `tool_choice` diverso da `auto`/`none` e `parallel_tool_calls: false` sono
  resi come istruzioni al modello: il modello li rispetta, ma non sono vincoli
  rigidi come nell'API OpenAI. Anche gli schemi `parameters` (e `strict`)
  guidano il modello ma non vengono imposti.
- In modalità `stateful` il processo in attesa occupa uno slot di concorrenza.
  Se i risultati non arrivano entro `client_tools.wait_timeout_seconds`
  (default 600) il processo viene fermato; una richiesta successiva con i
  risultati riparte da un processo nuovo con la cronologia completa, quindi
  funziona comunque.
- Con il formato deprecato `functions` i risultati non hanno id: ogni chiamata
  chiude il processo, come in modalità `stateless`.
- In una risposta `tool_calls` l'`usage` riguarda solo l'ultimo messaggio del
  modello; i totali del turno sono nei log e in `/stats`.

## Immagini

I messaggi possono contenere parti `image_url`, come nell'API OpenAI, con
qualsiasi modello:

```python
r = client.chat.completions.create(model="bridge/sonnet", messages=[{"role": "user", "content": [
    {"type": "text", "text": "Cosa c'è in questa immagine?"},
    {"type": "image_url", "image_url": {"url": "data:image/png;base64,..."}},
]}])
```

- **Sorgenti**: URL `data:` (base64) e URL `http(s)`, che il bridge scarica.
  I download da indirizzi non pubblici (loopback, reti private, metadati
  cloud) sono rifiutati, a meno di `images.allow_private_networks`;
  `images.fetch_urls: false` accetta solo URL `data:`.
- **Formati**: PNG, JPEG, GIF, WebP, riconosciuti dal contenuto. Al massimo
  `images.max_count` immagini per richiesta (default 20) e `images.max_bytes`
  ciascuna (default 5 MB); l'ingresso complessivo, immagini comprese, deve
  restare entro 9 MB.
- **Come**: le immagini arrivano alla CLI come blocchi immagine di un
  messaggio strutturato (`--input-format stream-json`); il testo vi fa
  riferimento come `[image 1]`, `[image 2]`… nell'ordine. Le richieste senza
  immagini restano in testo semplice.
- In una sessione ripresa vengono inviate solo le immagini dei messaggi
  nuovi; le precedenti sono già nella sessione. Con `images.enabled: false`
  le immagini sono rifiutate con 400 `unsupported_content`.

## Parametri di generazione

Il bridge pilota la CLI, non l'API del modello direttamente, quindi supporta
i parametri OpenAI che la CLI può esprimere, e ne emula altri due:

| Parametro OpenAI | Come | Fedeltà |
| --- | --- | --- |
| `reasoning_effort` | `--effort` della CLI; `none`/`minimal` diventano `low`, `xhigh` e `max` sono accettati come estensione | Diretta |
| `response_format` (`json_object`, `json_schema`) | `--json-schema` della CLI: la risposta è validata contro lo schema e restituita come JSON compatto in `content` | Diretta, validata |
| `max_tokens`, `max_completion_tokens` | `CLAUDE_CODE_MAX_OUTPUT_TOKENS` per il processo | Parziale: limita ogni risposta del modello nel turno, non il totale; `finish_reason` non diventa `length` |
| `stop` (stringa o fino a 4) | Il testo finale viene tagliato alla prima occorrenza | Emulata: il modello genera comunque tutta la risposta; mai applicato al JSON di `response_format` né ai testi di avanzamento |
| `n` (1-8) | `n` processi indipendenti in parallelo, una scelta ciascuno, anche in streaming | Emulata: costa `n` volte; nessuna ripresa di sessione, e le funzioni del client sono senza stato |
| `temperature`, `top_p`, `seed`, `logprobs`, `presence_penalty`, `frequency_penalty`, `logit_bias` | — | Accettati e ignorati: la CLI non ha equivalenti |

Se `response_format` non può essere rispettato (la CLI rinuncia dopo i suoi
tentativi, o termina senza output strutturato), la richiesta fallisce con 502.

## Agenti sul server

Un file JSON associa un nome di modello a un agente: modello, system prompt,
tool della CLI, permessi e cartella di lavoro. Serve quando l'agente deve
lavorare sulla macchina del server, per esempio su un repository.

```json
{
  "agents": {
    "reviewer": {
      "model": "opus",
      "system_prompt": "Sei un revisore severo. Non modificare file.",
      "allowed_tools": ["Read", "Grep", "Glob"],
      "cwd": "/srv/progetto"
    },
    "coder": {
      "model": "sonnet",
      "allowed_tools": ["Read", "Edit", "Write", "Bash(go test *)"],
      "permission_mode": "acceptEdits",
      "cwd": "/srv/progetto"
    }
  }
}
```

```sh
agent-bridge check-config -config agents.json   # valida e mostra gli agenti risolti
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -config agents.json
```

Il client sceglie l'agente con il campo `model`:

```python
from openai import OpenAI
client = OpenAI(base_url="http://127.0.0.1:8787/v1", api_key="unused")
r = client.chat.completions.create(
    model="reviewer",
    messages=[{"role": "user", "content": "Rivedi la gestione degli errori."}],
    extra_headers={"X-Session-Id": "review-42"},
)
print(r.choices[0].message.content)
```

Un agente sul server può ricevere anche le funzioni del client in `tools`:
userà sia i propri tool sia quelle.

Riferimento completo: [docs/configuration.it.md](docs/configuration.it.md). Esempio
commentato: [examples/agents.example.json](examples/agents.example.json).

## Conversazioni e sessioni

- **Senza `X-Session-Id`** (comportamento OpenAI): ogni richiesta avvia un
  processo nuovo; la cronologia inviata dal client viene trasformata in un
  unico prompt.
- **Con `X-Session-Id`** (o il campo `user`): la stessa chiave riprende la
  sessione della CLI con `--resume`, e alla CLI arrivano solo i messaggi nuovi
  (utente e risultati dei tool) successivi all'ultima risposta dell'assistente.
  Più veloce nelle conversazioni lunghe. La chiave, generata se assente, è
  sempre restituita nell'header `X-Session-Id` della risposta.
- I messaggi `system` sono messi in testa al prompt (`system_messages: "prompt"`,
  default): in una sessione ripresa contano solo quelli del primo turno. Con
  `append` o `replace` sono passati come system prompt e valgono a ogni turno.
- Le risposte alle `tool_calls` in modalità `stateful` riprendono il processo
  in attesa tramite i `tool_call_id`, indipendentemente dalla sessione.

## Sviluppo con abbonamento, produzione con API key

La modalità è scelta solo da configurazione, con la variabile obbligatoria
`AGENT_BRIDGE_AUTH_MODE`:

| Modalità | Uso | Credenziali |
| --- | --- | --- |
| `subscription` | Sviluppo sulla propria macchina | Login fatto con `claude`; `ANTHROPIC_API_KEY` e `ANTHROPIC_AUTH_TOKEN` non devono essere impostate |
| `api` | Produzione e istanze condivise | `ANTHROPIC_API_KEY` obbligatoria (chiave della Claude Console) |

Il server rifiuta di partire se la modalità e l'ambiente non sono coerenti. In
modalità `subscription` le variabili di API key vengono comunque rimosse
dall'ambiente del processo figlio, e se la CLI segnala di usare una API key il
server registra un errore a ogni turno.

Ogni turno registra token e costo (log, file `usage_log` opzionale, `/stats`,
`/metrics`). **In modalità `subscription` il campo `cost_usd` è una stima
calcolata dalla CLI ai prezzi API, non un addebito**: serve a prevedere quanto
costerà la produzione con API key.

## Docker

`compose.yaml` esegue il bridge con tutte e tre le CLI, un servizio per
backend (porte 8787, 8788 e 8789 sul loopback dell'host), con un unico volume
home per i login delle CLI:

```sh
cp .env.example .env      # imposta AGENT_BRIDGE_API_KEY
docker compose up -d
```

Poi fai il login nelle CLI dalla pagina di amministrazione,
`http://127.0.0.1:8787/admin`: esegue sul server il flusso di login ufficiale
di ogni CLI e ne mostra link e codice monouso (o un campo dove incollare il
codice). Le credenziali restano alle CLI; il bridge non le legge mai.
Dettagli: [docs/docker.it.md](docs/docker.it.md).

## Backend Codex

Con `backend: "codex"` (oppure `-backend codex`, `AGENT_BRIDGE_BACKEND=codex`)
ogni turno esegue `codex exec --json` al posto della CLI predefinita. API,
sessioni, function calling, immagini, output strutturato e sforzo di
ragionamento funzionano allo stesso modo; i modelli integrati diventano
`bridge/default` (il modello configurato in Codex) e `bridge/<modello>`; gli
agenti con strumenti usano una `sandbox` (`read-only`, `workspace-write`,
`danger-full-access`) al posto di `permission_mode` e `allowed_tools`.
Autenticazione: `codex login` in modalità `subscription`, `CODEX_API_KEY` in
modalità `api`.

```sh
codex login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend codex
```

Mappatura completa, campi degli agenti e differenze:
[docs/codex.it.md](docs/codex.it.md).

## Backend Kimi

Con `backend: "kimi"` (oppure `-backend kimi`, `AGENT_BRIDGE_BACKEND=kimi`)
ogni turno esegue `kimi acp`, la CLI Kimi Code come agente Agent Client
Protocol, con il bridge come client ACP. API, sessioni, function calling,
immagini, output strutturato e sforzo di ragionamento funzionano allo stesso
modo; i modelli integrati diventano `bridge/default` (il modello predefinito
di Kimi Code) e `bridge/<modello>`; il bridge risponde alle richieste di
permesso della CLI in base a `allowed_tools`, `disallowed_tools` e
`permission_mode` (`default`, `plan`, `auto`, `yolo`). Autenticazione:
`kimi login` in modalità `subscription`, `KIMI_MODEL_NAME` e
`KIMI_MODEL_API_KEY` in modalità `api`. ACP non riporta consumi né costi.

```sh
kimi login
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge -backend kimi
```

Mappatura completa, campi degli agenti e differenze:
[docs/kimi.it.md](docs/kimi.it.md).

## Uso consentito

agent-bridge è uno strumento: chi lo usa resta responsabile del rispetto dei
termini del proprio piano. In sintesi:

1. **Nessun accesso alle credenziali.** Il codice non legge, copia, conserva,
   logga né inoltra file di credenziali o token. Il login avviene solo con il
   flusso ufficiale della CLI, e il token dell'abbonamento non viene mai usato
   per chiamare direttamente l'API.
2. **Un abbonamento, un utente.** In modalità `subscription` il server deve
   servire solo il titolare dell'account, sulla propria macchina. La
   configurazione di default ascolta solo su loopback; l'accesso remoto va
   abilitato esplicitamente ed è pensato per raggiungere la propria istanza dal
   proprio portatile (es. VPN personale), non per altri utenti.
3. **Istanze condivise solo con API key.** Un'istanza usata da più persone
   (es. un laboratorio o un team) deve girare in modalità `api` con la chiave
   del titolare, senza rivendita o intermediazione dei costi.

Riferimenti, da ricontrollare perché **le regole possono cambiare**:

- [Claude Code: Legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)
- [Use the Claude Agent SDK with your Claude plan](https://support.claude.com/en/articles/15036540-use-the-claude-agent-sdk-with-your-claude-plan)

## Sicurezza

Un agente sul server con tool abilitati può leggere, scrivere ed eseguire
comandi: chi raggiunge il server ha l'equivalente di una shell limitata. Le
impostazioni di default sono chiuse e ogni apertura è esplicita.

- **Rete.** Ascolto di default su `127.0.0.1:8787`. Un indirizzo non loopback
  richiede `allow_remote: true` (o `-allow-remote`) **e** `AGENT_BRIDGE_API_KEY`.
  Non c'è TLS integrato: per l'accesso remoto usare un reverse proxy con TLS, un
  tunnel SSH o una VPN.
- **Autenticazione dei client.** Con `AGENT_BRIDGE_API_KEY` impostata, `/v1/*`,
  `/stats` e `/metrics` richiedono `Authorization: Bearer <chiave>` (confronto a
  tempo costante). `/healthz` e `/readyz` restano aperti.
- **CORS** disattivato; si abilita con una lista esatta di origini
  (`cors_origins`), senza caratteri jolly.
- **Modelli `bridge/*`** sempre solo chat: nessun tool del server, in
  qualsiasi modalità.
- **Funzioni del client** eseguite solo dal client, mai sul server. Le chiamate
  passano da un broker interno su `127.0.0.1` con porta casuale e un token
  diverso per ogni turno; i file del turno sono privati e rimossi alla fine.
- **Override per richiesta** (`X-Agent-Cwd`, `X-Agent-Allowed-Tools`,
  `X-Agent-System-Prompt`) rifiutati con 403 se non abilitati in `overrides`;
  possono solo restringere i tool, la cartella deve stare dentro una radice
  ammessa (anche dopo la risoluzione dei symlink), il system prompt viene solo
  aggiunto. Mai applicabili ai modelli `bridge/*`.
- **`system_messages`.** Con `replace` il system prompt della CLI viene
  sostituito dai messaggi della richiesta (il `system_prompt` dell'agente
  resta in testa); i permessi dei tool dell'agente non cambiano.
- **`bypassPermissions`** è rifiutato a meno di `allow_bypass_permissions: true`,
  e in quel caso all'avvio compare un avviso per ogni agente interessato.
- **Log.** A livello `info` e superiori non vengono mai registrati prompt o
  risposte; a `debug` sono troncati a 500 caratteri. Chiavi `sk-ant-…` e la
  chiave del bridge vengono sostituite con `[REDACTED]` in log ed errori.
- **Processi.** Ogni turno è un processo separato, avviato senza shell, con il
  prompt su stdin. Timeout, disconnessione del client o arresto del server
  terminano l'intero albero dei processi (process group su Unix, `taskkill /T`
  su Windows).

## Riferimento rapido

Esempi di client:

- Go con l'SDK ufficiale: [examples/go](examples/go/main.go)
- Python con l'SDK ufficiale: [examples/python/client.py](examples/python/client.py)
- Python, function calling: [examples/python/tools.py](examples/python/tools.py)
- Python, immagini: [examples/python/images.py](examples/python/images.py)
- curl: [examples/curl.sh](examples/curl.sh)

Endpoint:

| Metodo e percorso | Scopo |
| --- | --- |
| `POST /v1/chat/completions` | Esegue un turno (JSON o SSE) |
| `GET /v1/models`, `GET /v1/models/{id}` | Elenca agenti configurati e alias `bridge/*` (i nomi `bridge/<nome modello>` rispondono al dettaglio ma non sono elencati); il prefisso segue `builtin_prefix` |
| `GET /stats` | Statistiche per agente; `?reset=true` le azzera dopo averle restituite |
| `GET /metrics` | Metriche Prometheus (con `metrics: true`) |
| `GET /healthz`, `GET /readyz` | Liveness e readiness |

Header di richiesta:

| Header | Effetto |
| --- | --- |
| `X-Session-Id`, `X-Session-Reset` | Sessione da riprendere o ricominciare |
| `X-Agent-Bridge-Tool-Mode` | `stateful` o `stateless` per le funzioni del client |
| `X-Agent-Bridge-System-Mode` | `prompt`, `append` o `replace` per i messaggi `system` |
| `X-Agent-Cwd`, `X-Agent-Allowed-Tools`, `X-Agent-System-Prompt` | Override, solo se abilitati |
| `X-Request-Id` | Identificativo riportato nei log e nella risposta |

Header di risposta: `X-Session-Id`, `X-Request-Id` e, nelle risposte non stream,
`X-Agent-Bridge-Turns`, `X-Agent-Bridge-Cost-USD`, `X-Agent-Bridge-Duration-Ms`.

Pagina di amministrazione (con `admin: true` o `AGENT_BRIDGE_ADMIN=1`):
`GET /admin` e `/admin/api/logins` per il login nelle CLI
([docs/docker.it.md](docs/docker.it.md)).

Comandi: `agent-bridge serve` (default), `check-config`, `doctor [-live]`,
`version`; `mcp-tools` è interno, avviato dalla CLI per le funzioni del client.
`doctor` controlla la CLI e la sua versione, la coerenza della modalità di
autenticazione e i permessi delle cartelle; con `-live` esegue un turno di
prova su Haiku (con i backend Codex e Kimi: il modello configurato nella CLI,
e per Codex `codex login status`).

## Limiti noti

- `temperature`, `top_p`, `seed`, `logprobs`, `presence_penalty`,
  `frequency_penalty` e `logit_bias` sono accettati e ignorati; `stop`, `n` e
  `max_tokens` sono emulati o parziali (vedi
  [Parametri di generazione](#parametri-di-generazione)).
- Function calling: `tool_choice` vincolante, `parallel_tool_calls: false` e gli
  schemi delle funzioni sono istruzioni al modello, non vincoli imposti.
- Parti di contenuto diverse da testo e `image_url` (audio, file) producono
  400 `unsupported_content`; il campo `detail` delle immagini è accettato e
  ignorato.
- Nessun supporto a `/v1/responses`, `/v1/embeddings`, generazione di
  immagini, audio, né ad altri provider.
- Prompt fino a 9 MB (la CLI limita stdin a 10 MB).
- In streaming gli header di risposta `X-Agent-Bridge-Turns`, `-Cost-USD` e
  `-Duration-Ms` non sono disponibili (gli header partono prima
  dell'esecuzione); costo e token sono nel log, in `/stats` e nel chunk `usage`
  se richiesto con `stream_options.include_usage`.
- In streaming l'avanzamento è inviato come commenti SSE di una riga, senza riga
  vuota successiva, perché alcuni SDK interpretano una riga vuota dopo un
  commento come un evento vuoto.
- Un turno può durare minuti: il modello può lavorare in più passi prima di
  rispondere.
- I valori esatti del campo `apiKeySource` dell'evento `init` non sono
  documentati: il server considera "nessuna API key" solo `none`.

## FAQ

**Supporta i tool?** Sì, di due tipi: le funzioni del client con il function
calling OpenAI, e i tool dell'agente sul server (file, comandi, ricerca, server
MCP) per gli agenti configurati. Vedi [Due modi di usarlo](#due-modi-di-usarlo).

**Perché `temperature` e `top_p` non funzionano?** Il bridge pilota la CLI di
Claude Code, che non ha opzioni per questi parametri, e chiamare direttamente
l'API del modello significherebbe aggirare la CLI (e, con l'abbonamento, i
suoi termini). Quello che la CLI offre è mappato: vedi
[Parametri di generazione](#parametri-di-generazione).

**Accetta immagini?** Sì, parti `image_url` con URL `data:` o `http(s)`, come
nell'API OpenAI. Vedi [Immagini](#immagini).

**Devo modificare il mio client OpenAI?** No: cambiare `base_url`
(`http://127.0.0.1:8787/v1`), `api_key` (la `AGENT_BRIDGE_API_KEY`, o un valore
qualsiasi se non impostata) e `model`.

**Devo creare un file di configurazione per ogni agente?** No, se gli agenti
sono definiti nel client: istruzioni in `system`, tool in `tools`, modello
`bridge/*`. Il file serve solo per gli agenti che usano i tool del server.

**Il contesto viene duplicato se invio tutta la cronologia?** Senza
`X-Session-Id` ogni richiesta riparte dalla cronologia completa, come
un'API senza stato. Con `X-Session-Id` la sessione viene ripresa e la CLI
riceve solo i messaggi nuovi.

**Cosa succede se cambio cartella di lavoro a metà conversazione?** Le sessioni
della CLI sono legate alla directory di progetto: il server ne avvia una nuova.

**Posso esporre il server in rete?** Solo esplicitamente (`allow_remote` più
`AGENT_BRIDGE_API_KEY`), dietro TLS, e in modalità `subscription` solo per il
titolare dell'account. Per più utenti usare la modalità `api`.

**Due richieste sulla stessa sessione?** Vengono serializzate (`on_busy: wait`)
o la seconda riceve 409 (`on_busy: reject`).

## Sviluppo

Struttura a livelli con interfacce e implementazioni sostituibili:
[docs/architecture.it.md](docs/architecture.it.md). Specifica tecnica completa,
aggiornata alla rev. 1.4: [docs/specification.it.md](docs/specification.it.md).

```sh
go test -race ./...          # usa un finto binario della CLI, nessuna credenziale
./scripts/coverage.sh        # copertura minima dei pacchetti principali
```

La CI non usa mai la CLI reale né credenziali. Le verifiche manuali prima di
ogni release sono in [docs/release-checklist.it.md](docs/release-checklist.it.md).

## Licenza

agent-bridge è software libero: puoi redistribuirlo e/o modificarlo secondo i
termini della GNU General Public License pubblicata dalla Free Software
Foundation, versione 3 o (a tua scelta) qualsiasi versione successiva. Vedi
[LICENSE](LICENSE).

La licenza riguarda solo il codice di questo progetto. Claude Code è un
prodotto separato con i propri termini, che installi e usi secondo il tuo
accordo con Anthropic.
