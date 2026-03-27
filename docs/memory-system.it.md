# Sistema di Memoria

Il Sistema di Memoria fornisce una memoria persistente e ricercabile per gli agenti PicoClaw. Indicizza i file markdown
del workspace e, opzionalmente, la cronologia delle sessioni in un database SQLite locale, supportando ricerca
full-text (BM25) e ricerca ibrida vettoriale+keyword con provider di embedding multipli.

## Panoramica

```
                     ┌──────────────┐
                     │  Agent Loop  │
                     └──────┬───────┘
                            │
               Search / Sync / ReadFile
                            │
                     ┌──────▼───────┐
                     │   Manager    │  singleton per (agent, workspace, config)
                     │  (pkg/memory │
                     │   /index)    │
                     └──────┬───────┘
                            │
              ┌─────────────┼─────────────┐
              │             │             │
        ┌─────▼─────┐ ┌────▼────┐ ┌──────▼──────┐
        │  SQLite   │ │Embedding│ │  File       │
        │  Store    │ │Provider │ │  Watcher    │
        │ (FTS5)    │ │(remoto/ │ │ (fsnotify)  │
        │           │ │ locale) │ │             │
        └───────────┘ └─────────┘ └─────────────┘
```

### Funzionalita Principali

- **Indicizzazione incrementale** dei file di memoria markdown con rilevamento modifiche via SHA-256
- **Ricerca full-text** tramite SQLite FTS5 con ranking BM25 ed espansione query
- **Ricerca ibrida** che combina similarita vettoriale (coseno) con matching keyword BM25
- **Embedding multi-provider**: OpenAI, Gemini, Voyage, Mistral, Ollama, con selezione automatica
- **Cache degli embedding** per evitare chiamate ridondanti al provider
- **Indicizzazione sessioni** (sperimentale): file JSONL con redazione dati sensibili
- **Decadimento temporale** per log giornalieri time-sensitive
- **Diversificazione MMR** per ridurre risultati ridondanti
- **Reindex atomico** con swap del database per cambi di configurazione sicuri
- **Watching dei file** per sync incrementale automatica su modifiche

## Avvio Rapido

### 1. Setup Base (solo FTS, nessuna configurazione)

Posiziona i file markdown nel workspace:

```
workspace/
├── MEMORY.md              # Memoria a lungo termine (evergreen)
└── memory/
    ├── note-progetto.md   # Note tematiche (evergreen)
    ├── 2026-03-27.md      # Log giornaliero (soggetto a decadimento)
    └── riferimento-api.md # Documentazione di riferimento (evergreen)
```

Il sistema di memoria li scopre e indicizza automaticamente.

### 2. Abilitare la Ricerca Ibrida (con embedding)

Aggiungi alla configurazione PicoClaw:

```json
{
  "memory": {
    "provider": {
      "provider": "openai",
      "model": "text-embedding-3-small",
      "remote": {
        "apiKey": "sk-la-tua-chiave"
      }
    }
  }
}
```

La modalita `auto` prova i provider in ordine (OpenAI, Gemini, Voyage, Mistral) e seleziona il primo disponibile. Ollama
non viene selezionato automaticamente.

## Configurazione

### Provider Supportati

| Provider  | Modello Predefinito      | Max Input |
|-----------|--------------------------|-----------|
| `openai`  | `text-embedding-3-small` | 8191      |
| `gemini`  | `gemini-embedding-001`   | 2048      |
| `voyage`  | `voyage-4-large`         | 16000     |
| `mistral` | `mistral-embed`          | 8192      |
| `ollama`  | `nomic-embed-text`       | 8192      |

### Ordine di Scoperta File

1. `MEMORY.md` nella root del workspace (primario)
2. `memory.md` nella root (fallback se MEMORY.md assente)
3. Tutti i file `.md` sotto `memory/` ricorsivamente
4. File/directory in `extraPaths`

Regole:

- I symlink sono silenziosamente ignorati
- I path reali duplicati sono deduplicati
- Solo file `.md` sono accettati
- Directory `.git`, `node_modules`, `.venv`, ecc. sono saltate

### Tipi di File e Comportamento Temporale

| Pattern File               | Tipo            | Decadimento             |
|----------------------------|-----------------|-------------------------|
| `MEMORY.md` / `memory.md`  | Evergreen       | No                      |
| `memory/*.md` (non datati) | Evergreen       | No                      |
| `memory/YYYY-MM-DD.md`     | Log giornaliero | Si (data dal nome file) |

## Pipeline di Ricerca

```
Query
  │
  ├─→ Ricerca FTS5 (BM25)
  │     tokenizza → rimuovi stop words → quota → unisci con AND
  │     normalizza rank a [0, 1]
  │
  ├─→ Ricerca Vettoriale (se embedding disponibili)
  │     genera embedding query → similarita coseno vs tutti i chunk
  │
  ├─→ Fusione Score
  │     scoreFinale = 0.7 * scoreVettoriale + 0.3 * scoreTesto
  │
  ├─→ Decadimento Temporale (opzionale)
  │     score * exp(-ln(2)/giorniDimezzamento * etaInGiorni)
  │     file evergreen esenti
  │
  ├─→ Re-ranking MMR (opzionale)
  │     similarita Jaccard tra snippet
  │     lambda * rilevanza - (1-lambda) * maxSimilarita
  │
  └─→ Top-K Risultati (default 6, score minimo 0.35)
```

## Memoria Sessione (Sperimentale)

Abilita aggiungendo `"sessions"` alle sorgenti:

```json
{
  "memory": {
    "sources": [
      "memory",
      "sessions"
    ]
  }
}
```

I file sessione (formato JSONL) vengono processati cosi:

1. Solo i messaggi con ruolo `user` e `assistant` vengono estratti
2. I messaggi sono normalizzati nel formato `User: <testo>` / `Assistant: <testo>`
3. I dati sensibili vengono redatti prima dell'indicizzazione
4. I numeri di riga dei chunk sono rimappati alle righe originali del JSONL

### Redazione Dati Sensibili

Il `SecretRedactor` predefinito rileva:

- Chiavi API: `sk-*`, `AKIA*`, `gsk_*`, `xoxb-*`, `ghp_*`, ecc.
- Token Bearer: `Bearer <token>` in qualsiasi contesto
- Assegnazioni password: `password=<valore>`, `secret=<valore>`, ecc.
- Stringhe ad alta entropia (>4.5 bit/char) vicino a identificatori di segreti

Formato sostituzione: `[REDACTED:API_KEY]`, `[REDACTED:BEARER_TOKEN]`, `[REDACTED:PASSWORD]`, `[REDACTED:SECRET]`

Redattori personalizzati possono essere forniti tramite l'interfaccia `Redactor`.

## Degradazione Graceful

- Nessun provider embedding configurato: modalita solo FTS (ricerca keyword BM25)
- Provider embedding non disponibile: fallback a solo FTS per quella ricerca
- FTS5 non disponibile: restituisce risultati vuoti
- Database readonly: recovery automatica (chiudi, riapri, reinizializza, riprova una volta)

## Prestazioni

| Metrica                                    | Obiettivo | Note                                    |
|--------------------------------------------|-----------|-----------------------------------------|
| Sync incrementale (100 file, 1 modificato) | < 2s      | Confronto hash SHA-256                  |
| Ricerca FTS (1000 chunk, top-6)            | < 100ms   | FTS5 BM25                               |
| Ricerca ibrida (1000 chunk, top-6)         | < 500ms   | FTS + similarita coseno in-process      |
| Hit rate cache embedding                   | > 90%     | Su sync ripetute di contenuto invariato |
| Memoria idle                               | < 50MB    | Per workspace tipico (fino a 500 file)  |
