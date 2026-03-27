# Contract: MemoryManager Interface

**Package**: `pkg/memory/index`

## MemoryManager

The primary public interface for the memory indexing system.

```go
// Manager is the main entry point for memory indexing and search.
// It is a singleton per (agentId, workspaceDir, resolvedConfig) tuple.
type Manager interface {
    // Search executes a hybrid or FTS-only search and returns ranked results.
    Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error)

    // ReadFile reads content from an allowed memory file path.
    // Returns empty string (not error) if file does not exist.
    // Rejects paths outside the allowed boundary.
    ReadFile(ctx context.Context, opts ReadFileOptions) (ReadFileResult, error)

    // Sync synchronizes the index with the filesystem.
    // Serialized: only one sync runs at a time; concurrent requests queue.
    Sync(ctx context.Context, opts SyncOptions) error

    // Status returns the current state of the memory backend.
    Status(ctx context.Context) (StatusResult, error)

    // ProbeEmbedding checks if an embedding provider is available.
    ProbeEmbedding(ctx context.Context) (bool, error)

    // ProbeVector checks if vector search is available.
    ProbeVector(ctx context.Context) (bool, error)

    // Close releases resources (DB connection, watcher).
    Close() error
}
```

## SearchOptions

```go
type SearchOptions struct {
    Query      string  // Required: search text
    MaxResults int     // Optional: default 6
    MinScore   float64 // Optional: default 0.35
    SessionKey string  // Optional: for session-scoped search
}
```

## SearchResult

```go
type SearchResult struct {
    Path      string  // Source file relative path
    StartLine int     // Start line in source file
    EndLine   int     // End line in source file
    Score     float64 // Normalized score [0, 1]
    Snippet   string  // Truncated text (max 700 UTF-16 chars)
    Source    string  // Logical source: "memory" or "sessions"
    Citation  string  // Optional citation reference
}
```

## ReadFileOptions

```go
type ReadFileOptions struct {
    Path   string // Relative or absolute path
    Offset int    // Optional: line offset (0-based)
    Limit  int    // Optional: number of lines (0 = all)
}
```

## ReadFileResult

```go
type ReadFileResult struct {
    Text string // File content (empty if not found)
    Path string // Resolved absolute path
}
```

## SyncOptions

```go
type SyncOptions struct {
    Reason       string   // Optional: reason for sync (for logging)
    Force        bool     // Optional: force full reindex
    SessionFiles []string // Optional: specific session files to sync
    OnProgress   func(SyncProgress) // Optional: progress callback
}

type SyncProgress struct {
    Phase       string // "discover", "chunk", "embed", "index"
    FilesTotal  int
    FilesDone   int
    ChunksTotal int
    ChunksDone  int
}
```

## StatusResult

```go
type StatusResult struct {
    Backend         string            // "builtin"
    Provider        string            // Active embedding provider name
    Model           string            // Active embedding model name
    FilesIndexed    int               // Number of indexed files
    ChunksIndexed   int               // Number of indexed chunks
    Dirty           bool              // True if sync needed
    WorkspaceDir    string            // Workspace root path
    DatabasePath    string            // SQLite database path
    ExtraPaths      []string          // Configured extra paths
    Sources         []string          // Enabled sources
    SourceCounts    map[string]int    // Chunk count per source
    CacheEnabled    bool              // Embedding cache status
    FTSAvailable    bool              // FTS5 availability
    FallbackProvider string           // Fallback provider name
    VectorAvailable bool              // Vector search availability
    BatchEnabled    bool              // Batch embedding status
    Custom          map[string]string // Additional metadata
}
```
