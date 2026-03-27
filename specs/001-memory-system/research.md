# Research: Memory System

**Branch**: `001-memory-system` | **Date**: 2026-03-27

## R1: SQLite Driver Selection

**Decision**: Use `modernc.org/sqlite` (pure-Go, already in `go.mod`).

**Rationale**: Already a direct dependency used by `pkg/channels/whatsapp_native` and `pkg/channels/matrix`. Pure-Go means no CGo build complexity, cross-compilation works out of the box, and the driver supports FTS5 natively.

**Alternatives considered**:
- `github.com/mattn/go-sqlite3` (indirect dependency): Requires CGo, complicates cross-compilation. No benefit over modernc for this use case.
- `crawshaw.io/sqlite`: Less popular, fewer maintainers, no clear advantage.

## R2: FTS5 Availability

**Decision**: FTS5 is available in `modernc.org/sqlite` by default.

**Rationale**: The modernc SQLite build includes FTS5 as a compile-time extension. No additional configuration needed. Verified by examining the modernc/sqlite build tags and documentation.

**Alternatives considered**:
- External FTS library (Bleve, Tantivy): Over-engineered for this use case. SQLite FTS5 provides BM25 ranking with minimal overhead and zero extra dependencies.

## R3: Vector Search Extension

**Decision**: Implement in-process cosine similarity as the primary vector search mechanism. Optional `sqlite-vec` extension support can be added later.

**Rationale**: No Go-native SQLite vector extension is mature and easily distributable. In-process cosine similarity over the `chunks` table (JSON-serialized embeddings) is simple, correct, and sufficient for the expected scale (< 10,000 chunks per workspace). The spec already requires graceful degradation when vector extension is unavailable.

**Alternatives considered**:
- `sqlite-vec` (CGo): Would require CGo, contradicting the pure-Go approach. Can be added as an optional build tag later.
- `github.com/asg017/sqlite-vss`: Depends on Faiss, heavy dependency, not suitable for a "pico" framework.
- Dedicated vector DB (Qdrant, Milvus): Overkill for local single-user agent memory.

## R4: Embedding Provider HTTP Client

**Decision**: Use Go standard library `net/http` with custom retry wrapper.

**Rationale**: All embedding provider APIs (OpenAI, Gemini, Voyage, Mistral) use simple REST endpoints. PicoClaw already has `pkg/utils/http.go` with retry utilities. No need for provider-specific SDKs.

**Alternatives considered**:
- OpenAI Go SDK (`github.com/openai/openai-go`): Adds a dependency for a single API call (embeddings). The HTTP contract is trivial.
- Generic SDK wrappers: Add abstraction without value for simple POST requests returning JSON arrays.

## R5: Local Embedding Model (GGUF)

**Decision**: Defer local GGUF model support to a future iteration. Focus on remote providers first.

**Rationale**: Running GGUF models locally requires either CGo bindings (llama.cpp) or an external process (ollama). Both add significant complexity. The `auto` provider mode will try remote providers first; Ollama support covers the "local" use case for users who have it installed. A native GGUF runner can be added later behind a build tag.

**Alternatives considered**:
- Bundle llama.cpp via CGo: Contradicts pure-Go approach, bloats binary, complicates CI.
- Always require Ollama for local: Viable but adds setup friction. Document Ollama as the recommended local option.

## R6: File System Watcher

**Decision**: Use `github.com/fsnotify/fsnotify`.

**Rationale**: De facto standard Go file watcher. Already battle-tested, supports macOS (kqueue/FSEvents), Linux (inotify), Windows (ReadDirectoryChangesW). Minimal API surface. Check if already in `go.mod` as a transitive dependency; if not, it's a small, well-justified addition.

**Alternatives considered**:
- Polling: Simpler but wastes CPU and has higher latency (spec requires 1500ms debounce, implying event-driven).
- `github.com/rjeczalik/notify`: More features but less maintained than fsnotify.

## R7: Package Organization

**Decision**: Create `pkg/memory/index/` as the new package for the memory indexing system, separate from the existing `pkg/memory/` (JSONL session store).

**Rationale**: The existing `pkg/memory/` package handles JSONL session persistence (a different concern). The new memory indexing system is conceptually distinct: it manages a SQLite-backed search index over markdown/session files. Placing it under `pkg/memory/index/` signals that it's part of the memory domain but a separate subsystem.

**Alternatives considered**:
- `pkg/memoryindex/`: Flat but long package name, breaks convention used elsewhere.
- `pkg/search/`: Too generic; this is specifically memory search, not general search.
- Merge into existing `pkg/memory/`: Would conflate two different concerns (JSONL persistence vs. search indexing).

## R8: Configuration Integration

**Decision**: Add a `Memory` field to the existing `Config` struct in `pkg/config/config.go` with a new `MemoryConfig` type.

**Rationale**: PicoClaw configuration is centralized in `pkg/config/`. The memory system configuration (provider, storage, chunking, sync, query) fits naturally as a top-level config section. The `MemoryConfig` struct will mirror the configuration hierarchy from the spec (sections 19.1-19.10).

**Alternatives considered**:
- Separate config file: Adds discovery complexity. All other PicoClaw config is in one place.
- Environment variables: Rejected during clarification (config file `apiKey` only).

## R9: Sensitive Data Redaction

**Decision**: Define a `Redactor` interface in `pkg/memory/index/redact/`. Ship a default `SecretRedactor` implementation.

**Rationale**: Spec requires pluggable redaction (clarification Q1). An interface with a single method (`Redact(text string) string`) keeps it simple. The default implementation uses regex patterns for API key formats (`sk-*`, `Bearer *`, `AKIA*`, etc.) and entropy-based detection for high-entropy strings in secret-like contexts.

**Alternatives considered**:
- Use an external library (e.g., `github.com/presidio-research/presidio`): Heavy, Python-based, doesn't fit Go codebase.
- No interface, just a function: Doesn't allow custom implementations as spec requires.

## R10: Singleton Manager Pattern

**Decision**: Use a package-level `sync.Map` keyed by `(agentId, workspaceDir, configHash)` to cache `MemoryManager` instances.

**Rationale**: Spec requires singleton per configuration tuple (FR-023). A `sync.Map` provides concurrent-safe caching without a global mutex bottleneck. The config hash ensures that configuration changes produce a new manager instance (and trigger reindex).

**Alternatives considered**:
- Global mutex + regular map: Simpler but creates contention if multiple agents initialize concurrently.
- Per-agent lazy initialization in `AgentInstance`: Would scatter lifecycle management across the codebase.
