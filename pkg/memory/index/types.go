package index

import "context"

// IndexMetadata holds global index configuration state stored in the meta table.
// Used to detect when a full reindex is needed.
type IndexMetadata struct {
	Model        string   `json:"model"`
	Provider     string   `json:"provider"`
	ProviderKey  string   `json:"providerKey"`
	Sources      []string `json:"sources"`
	ScopeHash    string   `json:"scopeHash"`
	ChunkTokens  int      `json:"chunkTokens"`
	ChunkOverlap int      `json:"chunkOverlap"`
	VectorDims   int      `json:"vectorDims"`
}

// FileRecord tracks an indexed file for incremental change detection.
type FileRecord struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Hash   string `json:"hash"`
	Mtime  int64  `json:"mtime"`
	Size   int64  `json:"size"`
}

// Chunk represents a segment of indexed content with an optional embedding.
type Chunk struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Source    string `json:"source"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Hash      string `json:"hash"`
	Model     string `json:"model"`
	Text      string `json:"text"`
	Embedding string `json:"embedding"`
	UpdatedAt int64  `json:"updated_at"`
}

// SearchOptions configures a search query.
type SearchOptions struct {
	Query      string  `json:"query"`
	MaxResults int     `json:"maxResults,omitempty"`
	MinScore   float64 `json:"minScore,omitempty"`
	SessionKey string  `json:"sessionKey,omitempty"`
}

// SearchResult represents a single result from a search query.
type SearchResult struct {
	Path      string  `json:"path"`
	StartLine int     `json:"startLine"`
	EndLine   int     `json:"endLine"`
	Score     float64 `json:"score"`
	Snippet   string  `json:"snippet"`
	Source    string  `json:"source"`
	Citation  string  `json:"citation,omitempty"`
}

// ReadFileOptions configures a file read request.
type ReadFileOptions struct {
	Path   string `json:"path"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// ReadFileResult contains the content read from a memory file.
type ReadFileResult struct {
	Text string `json:"text"`
	Path string `json:"path"`
}

// SyncOptions configures a sync operation.
type SyncOptions struct {
	Reason       string             `json:"reason,omitempty"`
	Force        bool               `json:"force,omitempty"`
	SessionFiles []string           `json:"sessionFiles,omitempty"`
	OnProgress   func(SyncProgress) `json:"-"`
}

// SyncProgress reports the progress of a sync operation.
type SyncProgress struct {
	Phase       string `json:"phase"`
	FilesTotal  int    `json:"filesTotal"`
	FilesDone   int    `json:"filesDone"`
	ChunksTotal int    `json:"chunksTotal"`
	ChunksDone  int    `json:"chunksDone"`
}

// StatusResult reports the current state of the memory backend.
type StatusResult struct {
	Backend          string            `json:"backend"`
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	FilesIndexed     int               `json:"filesIndexed"`
	ChunksIndexed    int               `json:"chunksIndexed"`
	Dirty            bool              `json:"dirty"`
	WorkspaceDir     string            `json:"workspaceDir"`
	DatabasePath     string            `json:"databasePath"`
	ExtraPaths       []string          `json:"extraPaths,omitempty"`
	Sources          []string          `json:"sources"`
	SourceCounts     map[string]int    `json:"sourceCounts"`
	CacheEnabled     bool              `json:"cacheEnabled"`
	FTSAvailable     bool              `json:"ftsAvailable"`
	FallbackProvider string            `json:"fallbackProvider,omitempty"`
	VectorAvailable  bool              `json:"vectorAvailable"`
	BatchEnabled     bool              `json:"batchEnabled"`
	Custom           map[string]string `json:"custom,omitempty"`
}

// EmbeddingCacheEntry stores a cached embedding vector.
type EmbeddingCacheEntry struct {
	Provider    string `json:"provider"`
	Model       string `json:"model"`
	ProviderKey string `json:"provider_key"`
	Hash        string `json:"hash"`
	Embedding   string `json:"embedding"`
	Dims        int    `json:"dims"`
	UpdatedAt   int64  `json:"updated_at"`
}

// Manager is the main entry point for memory indexing and search.
type Manager interface {
	Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error)
	ReadFile(ctx context.Context, opts ReadFileOptions) (ReadFileResult, error)
	Sync(ctx context.Context, opts SyncOptions) error
	Status(ctx context.Context) (StatusResult, error)
	ProbeEmbedding(ctx context.Context) (bool, error)
	ProbeVector(ctx context.Context) (bool, error)
	Close() error
}

// EmbeddingProvider generates embedding vectors from text.
// This interface is defined here to avoid circular imports with the embedding sub-package.
type EmbeddingProvider interface {
	Name() string
	Model() string
	MaxInput() int
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// metaKey is the key used to store IndexMetadata in the meta table.
const metaKey = "memory_index_meta_v1"
