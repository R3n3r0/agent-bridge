# agent-bridge

`agent-bridge` è un server HTTP scritto in Go che espone un'API compatibile OpenAI
(`/v1/chat/completions`, `/v1/models`) e soddisfa ogni richiesta eseguendo la CLI
ufficiale di Claude Code in modalità headless
(`claude -p --output-format stream-json`).

Qualsiasi client o framework che parla OpenAI può così usare Claude Code come
motore di esecuzione (lettura e modifica di file, comandi, ricerca nel codice)
cambiando solo `base_url`, `api_key` e `model`, e assegnare a ogni agente un
modello, un system prompt, dei tool e una cartella di lavoro diversi, lasciando
il controllo del flusso al proprio orchestratore.

> **Progetto indipendente.** agent-bridge non è sviluppato, approvato né
> supportato da Anthropic. Esegue il binario ufficiale `claude` installato
> sulla macchina, senza modificarlo, e non accede mai alle sue credenziali.

- Binario singolo, sola libreria standard Go a runtime.
- Linux, macOS e Windows (amd64 e arm64).
- Stessa build per sviluppo (login con abbonamento) e produzione (API key).

## Avvio rapido

Prerequisito: la CLI `claude` installata, versione **2.1.128 o successiva**, e un
login fatto una volta in modo interattivo (`claude`).

```sh
go install github.com/r3n3r0/agent-bridge/cmd/agent-bridge@latest
AGENT_BRIDGE_AUTH_MODE=subscription agent-bridge
curl -s http://127.0.0.1:8787/v1/chat/completions \
  -d '{"model":"claude-code/haiku","messages":[{"role":"user","content":"Ciao!"}]}'
```

Senza file di configurazione il server ascolta su `127.0.0.1:8787` ed espone i
modelli `claude-code/opus`, `claude-code/sonnet` e `claude-code/haiku` in modalità
solo chat (nessun tool, cartella di lavoro temporanea vuota).

In alternativa scaricare un binario dalla pagina delle release (archivi e
`checksums.txt` per ogni piattaforma).

## Agenti configurati

Un file JSON associa un nome di modello a un agente: modello, system prompt,
tool, permessi e cartella di lavoro.

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

Le conversazioni multi-turno usano le sessioni della CLI: il client invia la
cronologia completa come con OpenAI, il server riprende la sessione con
`--resume` e passa solo i nuovi messaggi utente. La chiave di sessione è
l'header `X-Session-Id`, oppure il campo `user`, oppure una chiave generata
restituita nell'header `X-Session-Id` della risposta.

Riferimento completo: [docs/configuration.md](docs/configuration.md). Esempio
commentato: [examples/agents.example.json](examples/agents.example.json).

## Function calling (tools)

Le funzioni definite dal client con `tools` funzionano come con l'API OpenAI:
il modello risponde con `tool_calls` e `finish_reason: "tool_calls"`, il client
esegue le funzioni e rimanda i risultati con messaggi `role: "tool"`, e il
modello prosegue. Il ciclo standard degli SDK funziona senza modifiche, anche
in streaming (`delta.tool_calls`) e senza header di sessione.

```python
r = client.chat.completions.create(model="coder", messages=messages, tools=tools)
if r.choices[0].finish_reason == "tool_calls":
    messages.append(r.choices[0].message)
    for call in r.choices[0].message.tool_calls:
        messages.append({"role": "tool", "tool_call_id": call.id,
                         "content": run(call.function.name, call.function.arguments)})
    r = client.chat.completions.create(model="coder", messages=messages, tools=tools)
```

Come funziona: le funzioni del client vengono offerte alla CLI come tool di un
server MCP standard (il bridge stesso, sottocomando `mcp-tools`). Quando il
modello ne chiama una, il bridge restituisce la chiamata al client e tiene in
attesa il processo; la richiesta successiva viene riconosciuta dai
`tool_call_id` e il processo riprende esattamente da dove si era fermato. Le
funzioni vengono eseguite dal client, mai sul server, quindi sono disponibili
anche per gli agenti solo chat e i modelli built-in.

Supportati: `tools` (tipo `function`), `tool_choice` (`auto`, `none`,
`required`, funzione specifica), `parallel_tool_calls` (chiamate parallele
restituite insieme) e il formato deprecato `functions`/`function_call`.

Differenze da conoscere:

- `tool_choice` diverso da `auto`/`none` e `parallel_tool_calls: false` sono
  resi come istruzioni al modello: il modello li rispetta, ma non sono vincoli
  rigidi come nell'API OpenAI. Anche gli schemi `parameters` (e `strict`)
  guidano il modello ma non vengono imposti.
- Mentre attende i risultati, il processo occupa uno slot di concorrenza. Se i
  risultati non arrivano entro `client_tools.wait_timeout_seconds` (default
  600) il processo viene fermato; una richiesta successiva con i risultati
  riparte da un processo nuovo con l'intera cronologia, quindi funziona
  comunque, solo più lentamente.
- Con il formato deprecato `functions` i risultati non hanno id: ogni chiamata
  chiude il processo e la richiesta successiva riparte dalla cronologia.

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

Un agente con tool abilitati può leggere, scrivere ed eseguire comandi: chi
raggiunge il server ha l'equivalente di una shell limitata. Le impostazioni di
default sono chiuse e ogni apertura è esplicita.

- **Rete.** Ascolto di default su `127.0.0.1:8787`. Un indirizzo non loopback
  richiede `allow_remote: true` (o `-allow-remote`) **e** `AGENT_BRIDGE_API_KEY`.
  Non c'è TLS integrato: per l'accesso remoto usare un reverse proxy con TLS, un
  tunnel SSH o una VPN.
- **Autenticazione dei client.** Con `AGENT_BRIDGE_API_KEY` impostata, `/v1/*`,
  `/stats` e `/metrics` richiedono `Authorization: Bearer <chiave>` (confronto a
  tempo costante). `/healthz` e `/readyz` restano aperti.
- **CORS** disattivato; si abilita con una lista esatta di origini
  (`cors_origins`), senza caratteri jolly.
- **Override per richiesta** (`X-Agent-Cwd`, `X-Agent-Allowed-Tools`,
  `X-Agent-System-Prompt`) rifiutati con 403 se non abilitati in `overrides`;
  possono solo restringere i tool, la cartella deve stare dentro una radice
  ammessa (anche dopo la risoluzione dei symlink), il system prompt viene solo
  aggiunto. Mai applicabili ai modelli built-in.
- **`bypassPermissions`** è rifiutato a meno di `allow_bypass_permissions: true`,
  e in quel caso all'avvio compare un avviso per ogni agente interessato.
- **Log.** A livello `info` e superiori non vengono mai registrati prompt o
  risposte; a `debug` sono troncati a 500 caratteri. Chiavi `sk-ant-…` e la
  chiave del bridge vengono sostituite con `[REDACTED]` in log ed errori.
- **Processi.** Ogni turno è un processo separato, avviato senza shell, con il
  prompt su stdin. Timeout, disconnessione del client o arresto del server
  terminano l'intero albero dei processi (process group su Unix, `taskkill /T`
  su Windows).

## Esempi di client

- Go con l'SDK ufficiale: [examples/go](examples/go/main.go)
- Python con l'SDK ufficiale: [examples/python/client.py](examples/python/client.py)
- curl: [examples/curl.sh](examples/curl.sh)

Endpoint disponibili:

| Metodo e percorso | Scopo |
| --- | --- |
| `POST /v1/chat/completions` | Esegue un turno di un agente (JSON o SSE) |
| `GET /v1/models`, `GET /v1/models/{id}` | Elenca agenti e modelli built-in |
| `GET /stats` | Statistiche per agente; `?reset=true` le azzera dopo averle restituite |
| `GET /metrics` | Metriche Prometheus (con `metrics: true`) |
| `GET /healthz`, `GET /readyz` | Liveness e readiness |

Comandi: `agent-bridge serve` (default), `check-config`, `doctor [-live]`,
`version`. `doctor` controlla la CLI e la sua versione, la coerenza della
modalità di autenticazione e i permessi delle cartelle; con `-live` esegue un
turno di prova su Haiku.

## Limiti noti

- I parametri OpenAI `response_format`, `temperature`, `top_p`, `n`,
  `logprobs`, `max_tokens` sono accettati e ignorati.
- Il function calling è supportato con le differenze descritte nella sezione
  dedicata (vincoli di `tool_choice` e degli schemi espressi come istruzioni).
- Solo contenuto testuale: immagini e audio producono 400 `unsupported_content`.
- Nessun supporto a `/v1/responses`, `/v1/embeddings`, immagini, audio, né ad
  altri provider.
- Prompt fino a 9 MB (la CLI limita stdin a 10 MB).
- In streaming gli header `X-Agent-Bridge-*` non sono disponibili (gli header
  partono prima dell'esecuzione); costo e token sono nel log, in `/stats` e nel
  chunk `usage` se richiesto con `stream_options.include_usage`.
- In streaming l'avanzamento è inviato come commenti SSE di una riga, senza riga
  vuota successiva, perché alcuni SDK interpretano una riga vuota dopo un
  commento come un evento vuoto.
- I messaggi `system` vengono usati solo al primo turno di una sessione; nelle
  sessioni riprese il ruolo stabile dell'agente sta nel suo `system_prompt`.
- I valori esatti del campo `apiKeySource` dell'evento `init` non sono
  documentati: il server considera "nessuna API key" solo `none`.

## FAQ

**Supporta il function calling di OpenAI (`tools`, `tool_calls`)?** Sì, con lo
stesso formato e lo stesso ciclo richiesta/risultato dell'API OpenAI (vedi
"Function calling"). In più l'agente può usare i propri tool sul server
(file, comandi, ricerca, server MCP in `mcp_config`) secondo i permessi
configurati: quelli restano interni e il client ne vede solo l'effetto.

**Funziona esattamente come l'API OpenAI?** Per chat, streaming e function
calling gli SDK funzionano senza modifiche (stessi formati di richiesta,
risposta, chunk, errori e `usage`). Non ci sono `temperature`, `n`,
`logprobs`, output strutturato, immagini o audio, e un turno può durare minuti
perché l'agente lavora in più passi.

**Devo modificare il mio client OpenAI?** No: cambiare `base_url`
(`http://127.0.0.1:8787/v1`), `api_key` (la `AGENT_BRIDGE_API_KEY`, o un valore
qualsiasi se non impostata) e `model` (nome dell'agente).

**Il contesto viene duplicato se invio tutta la cronologia?** No. Dal secondo
turno la sessione viene ripresa e alla CLI arrivano solo i messaggi utente
successivi all'ultima risposta dell'assistente.

**Cosa succede se cambio cartella di lavoro a metà conversazione?** Le sessioni
della CLI sono legate alla directory di progetto: il server ne avvia una nuova.

**Posso esporre il server in rete?** Solo esplicitamente (`allow_remote` più
`AGENT_BRIDGE_API_KEY`), dietro TLS, e in modalità `subscription` solo per il
titolare dell'account. Per più utenti usare la modalità `api`.

**Due richieste sulla stessa sessione?** Vengono serializzate (`on_busy: wait`)
o la seconda riceve 409 (`on_busy: reject`).

## Sviluppo

Struttura a livelli con interfacce e implementazioni sostituibili:
[docs/architecture.md](docs/architecture.md).

```sh
go test -race ./...          # usa un finto binario della CLI, nessuna credenziale
./scripts/coverage.sh        # copertura minima dei pacchetti principali
```

La CI non usa mai la CLI reale né credenziali. Le verifiche manuali prima di
ogni release sono in [docs/release-checklist.md](docs/release-checklist.md).
