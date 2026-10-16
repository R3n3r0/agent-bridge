# Docker e login dalla pagina di amministrazione

[English](docker.md) · **Italiano**

L'immagine contiene agent-bridge e le tre CLI supportate (la CLI
predefinita, Codex e Kimi Code). `compose.yaml` avvia un servizio per CLI,
tutti con lo stesso volume home, così ogni CLI fa il login una volta sola e
il login sopravvive a riavvii e aggiornamenti dell'immagine. I login si fanno
dalla pagina di amministrazione del bridge, che esegue il flusso di login
ufficiale di ogni CLI.

## Avvio rapido

Con make:

```sh
make up       # crea .env con una chiave API casuale, costruisce l'immagine, avvia i servizi
make admin    # apre la pagina di login e stampa la chiave API
make smoke    # invia "Reply with the single word OK." a ogni servizio
make help     # log, doctor, login da terminale, aggiornamento, reset…
```

Senza make:

```sh
cp .env.example .env            # imposta AGENT_BRIDGE_API_KEY a una stringa lunga e casuale
mkdir -p workspace
docker compose up -d            # oppure solo alcuni servizi: docker compose up -d codex
```

| Servizio | Backend | Indirizzo sull'host |
| --- | --- | --- |
| `claude` | CLI predefinita | `http://127.0.0.1:8787/v1` |
| `codex` | CLI Codex | `http://127.0.0.1:8788/v1` |
| `kimi` | CLI Kimi Code | `http://127.0.0.1:8789/v1` |

Le porte sono pubblicate solo sul loopback dell'host (`CLAUDE_PORT`,
`CODEX_PORT`, `KIMI_PORT` in `.env`). Dentro il container il bridge ascolta
su tutte le interfacce, quindi la chiave API è obbligatoria: i client la
inviano come `Authorization: Bearer <AGENT_BRIDGE_API_KEY>`.

Poi apri `http://127.0.0.1:8787/admin` (va bene uno qualsiasi dei tre
servizi: condividono i login), inserisci la chiave API e fai il login nelle
CLI che usi.

## Login dalla pagina di amministrazione

La pagina elenca ogni CLI con il suo stato (installata, login fatto o no) e
un pulsante **Log in** che avvia sul server il comando di login della CLI:

| CLI | Comando | Cosa fai |
| --- | --- | --- |
| CLI predefinita | `auth login` | Apri il link, accedi, copia il codice che la pagina di accesso mostra alla fine e incollalo in **Send code** |
| Codex | `login --device-auth` | Apri il link e inserisci il codice monouso mostrato |
| Kimi Code | `login --region global` | Apri il link su kimi.ai (il codice è già incluso) e conferma |

Kimi Code accede su **kimi.ai**, il servizio fuori dalla Cina continentale.
Imposta `AGENT_BRIDGE_KIMI_REGION=mainland-cn` in `.env` per un account su
kimi.com, poi ripeti il login.

Mentre un login è in corso la pagina si aggiorna ogni due secondi e ne
mostra l'esito; **Cancel** lo interrompe. Un login scade dopo 20 minuti.

Cosa fa e cosa non fa il bridge:

- esegue il comando di login ufficiale della CLI e inoltra ciò che la CLI
  stampa (link, codici monouso) e il codice che incolli;
- non legge, copia, salva né registra mai le credenziali: la CLI le scrive
  nella propria directory nel volume home;
- registra nei log solo l'avvio e l'esito di un login, mai il suo output né
  il codice incollato;
- lo stato arriva dai comandi delle CLI stesse (`auth status`,
  `login status`, `provider list`).

Gli stessi flussi si possono eseguire anche da terminale:
`docker compose exec codex codex login --device-auth`,
`docker compose exec kimi kimi login --region global`,
`docker compose exec -it claude claude auth login`.

## API di amministrazione

Si attiva con `admin: true` o `AGENT_BRIDGE_ADMIN=1` (l'immagine la imposta).

| Metodo e percorso | Effetto |
| --- | --- |
| `GET /admin` | La pagina (statica, senza dati) |
| `GET /admin/api/logins` | Stato di ogni CLI (in cache per 30 secondi, aggiornato quando un login inizia o finisce) e del suo login in corso o ultimo, con i link e i codici monouso stampati |
| `POST /admin/api/logins/{cli}` | Avvia il login di `claude`, `codex` o `kimi` (o restituisce quello in corso) |
| `POST /admin/api/logins/{cli}/input` | Invia `{"text": "..."}` (una riga) al login in corso |
| `DELETE /admin/api/logins/{cli}` | Annulla il login in corso |

Ogni chiamata richiede la chiave Bearer quando è impostata; quelle che
cambiano lo stato richiedono anche l'header `X-Agent-Bridge-Admin: 1`, che un
browser non può inviare da un altro sito senza un preflight CORS che il
bridge non concede mai.

## Volumi e configurazione

| Percorso | Volume | Contenuto |
| --- | --- | --- |
| `/home/bridge` | `home` | Login e impostazioni delle CLI (`.claude`, `.codex`, `.kimi-code`…) |
| `/work` | `./workspace` | Directory di lavoro degli agenti con strumenti |
| `/data` | `data` | Libero per un file di sessioni o un log dei consumi |

Per usare un file di configurazione, montalo e indicalo, per esempio in un
`compose.override.yaml`:

```yaml
services:
  codex:
    volumes:
      - ./config/codex.json:/config/codex.json:ro
    environment:
      AGENT_BRIDGE_CONFIG: /config/codex.json
```

Gli agenti con strumenti dovrebbero usare un `cwd` dentro `/work`. Con il
backend Codex il container è già una sandbox: gli agenti con strumenti
potrebbero richiedere `sandbox: "danger-full-access"` (con
`allow_bypass_permissions: true`) se la sandbox della CLI non riesce ad
avviarsi dentro il container.

## Comandi make

| Comando | Effetto |
| --- | --- |
| `make up` | Crea `.env` se manca (chiave casuale), avvia i servizi (`SERVICES=codex` per uno solo), stampa indirizzi e chiave |
| `make admin`, `make urls`, `make key` | Apre la pagina di login; stampa gli indirizzi; stampa la chiave |
| `make smoke` | Una richiesta di chat a ogni servizio |
| `make doctor` | `agent-bridge doctor` in ogni servizio |
| `make logs`, `make ps`, `make restart`, `make down` | Le solite operazioni di Compose |
| `make login-claude`, `make login-codex`, `make login-kimi` | Login da terminale invece che dalla pagina |
| `make shell` | Shell in un servizio |
| `make update` | Ricostruisce con le CLI più recenti e riavvia |
| `make reset` | Cancella i volumi, login compresi, dopo conferma |

## Build

`docker compose build` (oppure `docker build -t agent-bridge:local .`).
Argomenti di build: `CLAUDE_CODE_VERSION`, `CODEX_VERSION`,
`KIMI_CODE_VERSION` (predefinito `latest`), `GO_VERSION`, `NODE_VERSION`, e
`BASE_REGISTRY` per scaricare le immagini di base da un mirror delle immagini
ufficiali di Docker Hub (per esempio `mirror.gcr.io/library`). Ricostruisci
per aggiornare le CLI; i login restano nel volume.

## Uso consentito

Un container in modalità `subscription` resta un'istanza per un solo utente:
eseguilo sulla tua macchina per te, con le porte sul loopback. Le istanze
condivise devono usare la modalità `api` con chiavi API (`ANTHROPIC_API_KEY`,
`CODEX_API_KEY`, `KIMI_MODEL_NAME` e `KIMI_MODEL_API_KEY` in `.env`).
