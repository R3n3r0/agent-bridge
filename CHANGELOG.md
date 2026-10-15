# Changelog

## Unreleased

Prima implementazione completa delle specifiche v1.0:

- API compatibile OpenAI: `POST /v1/chat/completions` (JSON e SSE), `GET /v1/models`, `GET /v1/models/{id}`.
- Modelli built-in solo chat e agenti configurabili da file JSON.
- Sessioni multi-turno con ripresa, store in memoria o su file, TTL, LRU, serializzazione per sessione.
- Coda con limite di concorrenza, timeout, terminazione dell'albero dei processi su Unix e Windows.
- Modalità `subscription` e `api`, bind loopback di default, chiave Bearer, CORS, override per richiesta.
- Redazione dei segreti, request ID, log strutturati, file di utilizzo, `/stats`, `/metrics`, `/healthz`, `/readyz`.
- Comandi `serve`, `check-config`, `doctor`, `version`.
