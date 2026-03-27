# Data Model: Memory System

**Branch**: `001-memory-system` | **Date**: 2026-03-27

## Entities

### IndexMetadata

Global index configuration state stored as a JSON blob in the `meta` table.

| Field          | Type     | Description                                     |
|----------------|----------|-------------------------------------------------|
| model          | string   | Embedding model name (e.g., `text-embedding-3-small`) |
| provider       | string   | Embedding provider identifier                    |
| providerKey    | string   | Stable fingerprint of provider configuration     |
| sources        | []string | Enabled sources (e.g., `["memory"]`, `["memory","sessions"]`) |
| scopeHash      | string   | Hash of the scope/path configuration             |
| chunkTokens    | int      | Chunk size in tokens                             |
| chunkOverlap   | int      | Chunk overlap in tokens                          |
| vectorDims     | int      | Embedding vector dimensionality (0 if unknown)   |

**Identity**: Singleton per database. Stored under key `memory_index_meta_v1`.

**Lifecycle**: Created on first sync. Compared on every subsequent sync to detect reindex triggers. Updated after successful sync/reindex.

**Reindex triggers**: Any field change requires full reindex.

---

### FileRecord

Tracks each indexed file for incremental change detection.

| Field  | Type   | Description                                      |
|--------|--------|--------------------------------------------------|
| path   | string | Relative path to workspace (primary key)          |
| source | string | Logical source: `memory` or `sessions`            |
| hash   | string | SHA-256 of file content                           |
| mtime  | int64  | Last modified timestamp in milliseconds           |
| size   | int64  | File size in bytes                                |

**Identity**: Unique by `path`.

**Lifecycle**: Created when file is first indexed. Updated when file content changes (hash mismatch). Deleted when file is removed from disk.

**State transitions**: `discovered` → `indexed` → `updated` (on change) → `deleted` (on removal).

---

### Chunk

A segment of indexed content with optional embedding.

| Field      | Type     | Description                                       |
|------------|----------|---------------------------------------------------|
| id         | string   | Stable ID: `sha256("{source}:{path}:{startLine}:{endLine}:{hash}:{model}")` |
| path       | string   | Source file path (FK to FileRecord)                |
| source     | string   | Logical source: `memory` or `sessions`             |
| start_line | int      | Start line in source file                          |
| end_line   | int      | End line in source file                            |
| hash       | string   | SHA-256 of chunk text                              |
| model      | string   | Embedding model used (empty if no embeddings)      |
| text       | string   | Chunk text content                                 |
| embedding  | string   | JSON-serialized `[]float32` (empty if unavailable) |
| updated_at | int64    | Timestamp in milliseconds                          |

**Identity**: Unique by `id` (content-addressable).

**Lifecycle**: Created during sync. Replaced when chunk content or model changes (new ID generated). Deleted when parent file is removed or chunks are re-partitioned.

**Indexes**: On `path` (for file-level operations), on `source` (for source-level queries).

---

### EmbeddingCacheEntry

Persistent cache of computed embeddings to avoid redundant provider calls.

| Field        | Type   | Description                                     |
|--------------|--------|-------------------------------------------------|
| provider     | string | Provider identifier (composite PK part 1)        |
| model        | string | Model name (composite PK part 2)                 |
| provider_key | string | Provider config fingerprint (composite PK part 3) |
| hash         | string | SHA-256 of source text (composite PK part 4)     |
| embedding    | string | JSON-serialized `[]float32`                      |
| dims         | int    | Vector dimensionality                            |
| updated_at   | int64  | Timestamp for LRU eviction                       |

**Identity**: Unique by `(provider, model, provider_key, hash)`.

**Lifecycle**: Created when embedding is first computed. Reused across syncs when chunk hash matches. Evicted by LRU when `maxEntries` exceeded.

---

### SearchResult (runtime, not persisted)

Returned from search queries.

| Field      | Type    | Description                                  |
|------------|---------|----------------------------------------------|
| path       | string  | Source file path                              |
| startLine  | int     | Start line in source file                     |
| endLine    | int     | End line in source file                       |
| score      | float64 | Normalized score in [0, 1]                    |
| snippet    | string  | Truncated text (max 700 UTF-16 chars)         |
| source     | string  | Logical source label                          |
| citation   | string  | Optional citation reference                   |

---

### MemoryConfig (runtime configuration)

| Field            | Type              | Description                               |
|------------------|-------------------|-------------------------------------------|
| Provider         | ProviderConfig    | Embedding provider settings                |
| Storage          | StorageConfig     | Database path and vector settings          |
| Chunking         | ChunkingConfig    | Token size and overlap                     |
| Sync             | SyncConfig        | Watch, debounce, session delta thresholds  |
| Query            | QueryConfig       | Max results, min score, hybrid settings    |
| Cache            | CacheConfig       | Embedding cache enabled, max entries       |
| Sources          | []string          | Enabled sources (default: `["memory"]`)    |
| ExtraPaths       | []string          | Additional paths to index                  |

---

## Relationships

```text
IndexMetadata (1) ←──── meta table ────→ singleton per database

FileRecord (1) ←───── path ─────→ (*) Chunk
  │                                     │
  │  source: memory|sessions            │  source: memory|sessions
  │                                     │
  └─── discovered via file discovery    └─── created via chunking

Chunk.hash ←────── cache lookup ──────→ EmbeddingCacheEntry
  (provider + model + provider_key + hash)

SearchResult ←── computed from ──→ Chunk (at query time)
  (score fusion, decay, MMR applied)

MemoryManager (singleton)
  ├── owns → SQLite connection
  ├── owns → file watcher
  ├── owns → sync mutex
  ├── uses → EmbeddingProvider (interface)
  ├── uses → Redactor (interface)
  └── exposes → Search, ReadFile, Sync, Status, Close
```

## SQLite Schema

```sql
-- Metadata key-value store
CREATE TABLE IF NOT EXISTS meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- File registry for change detection
CREATE TABLE IF NOT EXISTS files (
    path   TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    hash   TEXT NOT NULL,
    mtime  INTEGER NOT NULL,
    size   INTEGER NOT NULL
);

-- Chunk store with embeddings
CREATE TABLE IF NOT EXISTS chunks (
    id         TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    source     TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line   INTEGER NOT NULL,
    hash       TEXT NOT NULL,
    model      TEXT NOT NULL DEFAULT '',
    text       TEXT NOT NULL,
    embedding  TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_chunks_path ON chunks(path);
CREATE INDEX IF NOT EXISTS idx_chunks_source ON chunks(source);

-- Embedding cache
CREATE TABLE IF NOT EXISTS embedding_cache (
    provider     TEXT NOT NULL,
    model        TEXT NOT NULL,
    provider_key TEXT NOT NULL,
    hash         TEXT NOT NULL,
    embedding    TEXT NOT NULL,
    dims         INTEGER NOT NULL,
    updated_at   INTEGER NOT NULL,
    PRIMARY KEY (provider, model, provider_key, hash)
);

-- FTS5 full-text index (created when FTS available)
CREATE VIRTUAL TABLE IF NOT EXISTS chunks_fts USING fts5(
    text,
    id UNINDEXED,
    path UNINDEXED,
    source UNINDEXED,
    model UNINDEXED,
    start_line UNINDEXED,
    end_line UNINDEXED,
    content='chunks',
    content_rowid='rowid'
);

-- Triggers to keep FTS in sync with chunks table
CREATE TRIGGER IF NOT EXISTS chunks_ai AFTER INSERT ON chunks BEGIN
    INSERT INTO chunks_fts(rowid, text, id, path, source, model, start_line, end_line)
    VALUES (new.rowid, new.text, new.id, new.path, new.source, new.model, new.start_line, new.end_line);
END;

CREATE TRIGGER IF NOT EXISTS chunks_ad AFTER DELETE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text, id, path, source, model, start_line, end_line)
    VALUES ('delete', old.rowid, old.text, old.id, old.path, old.source, old.model, old.start_line, old.end_line);
END;

CREATE TRIGGER IF NOT EXISTS chunks_au AFTER UPDATE ON chunks BEGIN
    INSERT INTO chunks_fts(chunks_fts, rowid, text, id, path, source, model, start_line, end_line)
    VALUES ('delete', old.rowid, old.text, old.id, old.path, old.source, old.model, old.start_line, old.end_line);
    INSERT INTO chunks_fts(rowid, text, id, path, source, model, start_line, end_line)
    VALUES (new.rowid, new.text, new.id, new.path, new.source, new.model, new.start_line, new.end_line);
END;

-- Vector index (created dynamically when dims known)
-- CREATE VIRTUAL TABLE chunks_vec USING vec0(id TEXT PRIMARY KEY, embedding float[{dims}]);
```

## Data Volume Assumptions

| Metric                       | Typical   | Upper Bound |
|------------------------------|-----------|-------------|
| Memory files per workspace   | 10-50     | 500         |
| Average file size            | 2-10 KB   | 100 KB      |
| Chunks per file              | 1-5       | 50          |
| Total chunks                 | 50-250    | 10,000      |
| Embedding dimensions         | 256-1536  | 3072        |
| Embedding cache entries      | 50-250    | 100,000     |
| Session files (if enabled)   | 0-10      | 100         |
