// Package index implements a persistent memory indexing system for PicoClaw.
//
// It provides incremental file indexing, text chunking, embedding generation
// with caching, and hybrid search (vector + full-text) over workspace memory
// files and optional session history. The storage backend is SQLite with FTS5.
//
// # Architecture
//
// The main entry point is the [Manager] interface, obtained via [GetManager].
// Each manager is a singleton per (agentId, workspaceDir, config) tuple.
//
// The manager orchestrates:
//   - File discovery: [DiscoverMemoryFiles] scans MEMORY.md, memory/, extraPaths
//   - Chunking: [ChunkMarkdown] splits content into overlapping token-sized chunks
//   - Indexing: [Store] persists chunks, file records, and embeddings in SQLite
//   - Search: FTS5 (BM25) and/or cosine similarity, with score fusion
//   - Sync: Incremental change detection via SHA-256 hashing
//
// # Search Modes
//
// FTS-only mode (no embedding provider): BM25 keyword search with query
// expansion (stop-word removal, token deduplication).
//
// Hybrid mode (embedding provider configured): Combines vector similarity
// (weight 0.7) with BM25 text score (weight 0.3). Falls back to FTS-only
// if the embedding provider is unavailable.
//
// # Optional Features
//
// Temporal decay: Reduces scores for dated daily logs (memory/YYYY-MM-DD.md)
// while preserving evergreen files. Disabled by default.
//
// MMR diversification: Re-ranks results to reduce near-duplicate snippets
// using Jaccard similarity. Disabled by default.
//
// Session indexing: Extracts user/assistant messages from JSONL session files
// with sensitive data redaction. Requires "sessions" in sources config.
//
// File watching: Monitors memory files via fsnotify and triggers incremental
// sync after a configurable debounce period.
package index
