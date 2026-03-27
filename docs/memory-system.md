# Memory System

The Memory System provides persistent, searchable memory for PicoClaw agents. It indexes workspace markdown files and
optional session history into a local SQLite database, supporting full-text search (BM25) and hybrid vector+keyword
search with multiple embedding providers.

## Overview

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
        │ (FTS5)    │ │(remote/ │ │ (fsnotify)  │
        │           │ │ local)  │ │             │
        └───────────┘ └─────────┘ └─────────────┘
```

### Key Capabilities

- **Incremental indexing** of markdown memory files with SHA-256 change detection
- **Full-text search** via SQLite FTS5 with BM25 ranking and query expansion
- **Hybrid search** combining vector similarity (cosine) with BM25 keyword matching
- **Multi-provider embeddings**: OpenAI, Gemini, Voyage, Mistral, Ollama, with auto-selection
- **Embedding cache** to avoid redundant provider calls
- **Session indexing** (experimental): JSONL session files with sensitive data redaction
- **Temporal decay** for time-sensitive daily logs
- **MMR diversification** to reduce redundant results
- **Atomic reindex** with database swap for safe configuration changes
- **File watching** for automatic incremental sync on changes

## Quick Start

### 1. Basic Setup (FTS-only, zero configuration)

Place markdown files in your workspace:

```
workspace/
├── MEMORY.md              # Long-term memory (evergreen)
└── memory/
    ├── project-notes.md   # Thematic notes (evergreen)
    ├── 2026-03-27.md      # Daily log (temporal decay eligible)
    └── api-reference.md   # Reference docs (evergreen)
```

The memory system discovers and indexes these automatically.

### 2. Enable Hybrid Search (with embeddings)

Add to your PicoClaw configuration:

```json
{
  "memory": {
    "provider": {
      "provider": "openai",
      "model": "text-embedding-3-small",
      "remote": {
        "apiKey": "sk-your-key-here"
      }
    }
  }
}
```

Other providers:

```json
{
  "memory": {
    "provider": {
      "provider": "gemini",
      "remote": {
        "apiKey": "your-key"
      }
    }
  }
}
{
  "memory": {
    "provider": {
      "provider": "voyage",
      "remote": {
        "apiKey": "your-key"
      }
    }
  }
}
{
  "memory": {
    "provider": {
      "provider": "mistral",
      "remote": {
        "apiKey": "your-key"
      }
    }
  }
}
{
  "memory": {
    "provider": {
      "provider": "ollama"
    }
  }
}
{
  "memory": {
    "provider": {
      "provider": "auto",
      "remote": {
        "apiKey": "your-key"
      }
    }
  }
}
```

The `auto` mode probes providers in order (OpenAI, Gemini, Voyage, Mistral) and selects the first available one. Ollama
is not auto-selected.

## Architecture

### Package Structure

```
pkg/memory/index/
├── types.go           # Core types: Chunk, FileRecord, SearchResult, Manager interface
├── config.go          # MemoryConfig with defaults and validation
├── store.go           # SQLite store (schema, CRUD, FTS5)
├── hash.go            # SHA-256 hashing (files, chunks, IDs)
├── discovery.go       # File discovery (MEMORY.md, memory/, extraPaths)
├── chunker.go         # Markdown line-based chunking with overlap
├── search.go          # FTS query building, BM25 normalization, cosine similarity,
│                      # score fusion, temporal decay, MMR
├── sync.go            # Incremental sync, atomic reindex, session indexing
├── session.go         # JSONL session extraction, line mapping
├── manager.go         # Singleton Manager (Search, Sync, ReadFile, Status, Close)
├── watcher.go         # fsnotify file watcher with debounce
├── embedding/
│   ├── provider.go    # Provider interface, L2 normalization, serialization
│   ├── openai.go      # OpenAI embeddings (/v1/embeddings)
│   ├── gemini.go      # Gemini embeddings (batchEmbedContents)
│   ├── voyage.go      # Voyage AI embeddings
│   ├── mistral.go     # Mistral embeddings
│   ├── ollama.go      # Ollama local embeddings (/api/embed)
│   ├── auto.go        # Auto-selection logic
│   ├── retry.go       # Exponential backoff retry wrapper
│   └── batch.go       # Byte-size-based batching
└── redact/
    ├── redactor.go    # Redactor interface, ChainRedactor
    └── secrets.go     # SecretRedactor (API keys, tokens, passwords, entropy)
```

### Database Schema

The system uses a single SQLite file at `~/.openclaw/memory/{agentId}.sqlite` with these tables:

| Table             | Purpose                                                      |
|-------------------|--------------------------------------------------------------|
| `meta`            | Key-value metadata (index config fingerprint)                |
| `files`           | File registry for change detection (path, hash, mtime, size) |
| `chunks`          | Indexed content chunks with optional embeddings              |
| `embedding_cache` | Persistent cache of computed embedding vectors               |
| `chunks_fts`      | FTS5 virtual table for full-text search                      |

### Search Pipeline

```
Query
  │
  ├─→ FTS5 Search (BM25)
  │     tokenize → remove stop words → quote → AND-join
  │     normalize rank to [0, 1]
  │
  ├─→ Vector Search (if embeddings available)
  │     embed query → cosine similarity vs all chunks
  │
  ├─→ Score Fusion
  │     finalScore = 0.7 * vectorScore + 0.3 * textScore
  │
  ├─→ Temporal Decay (optional)
  │     score * exp(-ln(2)/halfLifeDays * ageInDays)
  │     evergreen files exempt
  │
  ├─→ MMR Re-ranking (optional)
  │     Jaccard similarity between snippets
  │     lambda * relevance - (1-lambda) * maxSimilarity
  │
  └─→ Top-K Results (default 6, min score 0.35)
```

## Configuration Reference

### Full Configuration

```json
{
  "memory": {
    "provider": {
      "provider": "auto",
      "model": "",
      "outputDimensionality": 0,
      "fallback": "none",
      "remote": {
        "baseUrl": "",
        "apiKey": "",
        "headers": {}
      },
      "local": {
        "modelPath": "",
        "modelCacheDir": ""
      }
    },
    "storage": {
      "driver": "sqlite",
      "path": ""
    },
    "chunking": {
      "tokens": 400,
      "overlap": 80
    },
    "sync": {
      "onSessionStart": true,
      "onSearch": true,
      "watch": true,
      "watchDebounceMs": 1500,
      "intervalMinutes": 0,
      "sessions": {
        "deltaBytes": 100000,
        "deltaMessages": 50,
        "postCompactionForce": true
      }
    },
    "query": {
      "maxResults": 6,
      "minScore": 0.35,
      "hybrid": {
        "enabled": true,
        "vectorWeight": 0.7,
        "textWeight": 0.3,
        "candidateMultiplier": 4,
        "mmr": {
          "enabled": false,
          "lambda": 0.7
        },
        "temporalDecay": {
          "enabled": false,
          "halfLifeDays": 30
        }
      }
    },
    "cache": {
      "enabled": true,
      "maxEntries": 0
    },
    "sources": [
      "memory"
    ],
    "extraPaths": []
  }
}
```

### Provider Default Models

| Provider  | Default Model            | Max Input |
|-----------|--------------------------|-----------|
| `openai`  | `text-embedding-3-small` | 8191      |
| `gemini`  | `gemini-embedding-001`   | 2048      |
| `voyage`  | `voyage-4-large`         | 16000     |
| `mistral` | `mistral-embed`          | 8192      |
| `ollama`  | `nomic-embed-text`       | 8192      |

### File Discovery Order

1. `MEMORY.md` at workspace root (primary)
2. `memory.md` at workspace root (fallback if MEMORY.md absent)
3. All `.md` files under `memory/` recursively
4. Files/directories in `extraPaths`

Rules:

- Symlinks are silently ignored
- Duplicate real paths are deduplicated
- Only `.md` files are accepted
- Directories `.git`, `node_modules`, `.venv`, etc. are skipped

### File Types and Temporal Behavior

| File Pattern              | Type      | Temporal Decay           |
|---------------------------|-----------|--------------------------|
| `MEMORY.md` / `memory.md` | Evergreen | No                       |
| `memory/*.md` (non-dated) | Evergreen | No                       |
| `memory/YYYY-MM-DD.md`    | Daily log | Yes (date from filename) |

## Session Memory (Experimental)

Enable by adding `"sessions"` to sources:

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

Session files (JSONL format) are processed as follows:

1. Only `user` and `assistant` role messages are extracted
2. Messages are normalized to `User: <text>` / `Assistant: <text>` format
3. Sensitive data is redacted before indexing (API keys, tokens, passwords)
4. Chunk line numbers are remapped to original JSONL line numbers

### Sensitive Data Redaction

The default `SecretRedactor` detects:

- API keys: `sk-*`, `AKIA*`, `gsk_*`, `xoxb-*`, `ghp_*`, etc.
- Bearer tokens: `Bearer <token>` in any context
- Password assignments: `password=<value>`, `secret=<value>`, etc.
- High-entropy strings (>4.5 bits/char) near secret-like identifiers

Replacement format: `[REDACTED:API_KEY]`, `[REDACTED:BEARER_TOKEN]`, `[REDACTED:PASSWORD]`, `[REDACTED:SECRET]`

Custom redactors can be provided via the `Redactor` interface.

## Embedding Cache

The system caches embedding vectors keyed by `(provider, model, providerKey, contentHash)`. On subsequent syncs,
unchanged chunks reuse cached vectors without calling the provider.

Cache behavior:

- Enabled by default
- LRU eviction when `maxEntries` is set and exceeded
- Survives reindex (cache is seeded into the new database during atomic reindex)

## Retry and Error Handling

### Embedding Retries

Retryable errors (HTTP 429, 5xx, rate limits, transient network errors):

- Max 3 attempts
- Exponential backoff: 500ms, 1s, 2s (capped at 8s)
- 20% jitter

Non-retryable errors (400, 401, 403) fail immediately.

### Graceful Degradation

- No embedding provider configured: FTS-only mode (BM25 keyword search)
- Embedding provider unavailable: falls back to FTS-only for that search
- FTS5 unavailable: returns empty results (rare with modernc/sqlite)
- Readonly database: auto-recovery (close, reopen, reinitialize, retry once)

### Atomic Reindex

When configuration changes require a full reindex (provider, model, chunk size changes):

1. A temporary database is created
2. The embedding cache is seeded from the current database
3. All files are re-indexed into the temporary database
4. The temporary database atomically replaces the original (rename)
5. If the swap fails, the original database remains intact

## Performance

| Metric                                  | Target  | Notes                                   |
|-----------------------------------------|---------|-----------------------------------------|
| Incremental sync (100 files, 1 changed) | < 2s    | SHA-256 hash comparison                 |
| FTS search (1000 chunks, top-6)         | < 100ms | FTS5 BM25                               |
| Hybrid search (1000 chunks, top-6)      | < 500ms | FTS + in-process cosine similarity      |
| Embedding cache hit rate                | > 90%   | On repeated syncs of unchanged content  |
| Idle memory                             | < 50MB  | For typical workspace (up to 500 files) |
