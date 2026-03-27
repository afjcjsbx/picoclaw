package index

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ftsCandidate holds an FTS result with its text score.
type ftsCandidate struct {
	chunk     Chunk
	textScore float64
}

func sortResultsByScore(results []SearchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
}

func searchMode(hybrid bool) string {
	if hybrid {
		return "hybrid"
	}
	return "fts-only"
}

// managerCache stores singleton Manager instances.
var managerCache sync.Map

// managerCacheKey builds a cache key from the manager configuration.
func managerCacheKey(agentID, workspaceDir string, cfg MemoryConfig) string {
	key := agentID + ":" + workspaceDir + ":" + cfg.Provider.Provider + ":" + cfg.Provider.Model
	for _, s := range cfg.Sources {
		key += ":" + s
	}
	return HashChunkText(key)
}

// GetManager returns a singleton Manager for the given configuration.
func GetManager(agentID, workspaceDir string, cfg MemoryConfig) (Manager, error) {
	cacheKey := managerCacheKey(agentID, workspaceDir, cfg)
	if cached, ok := managerCache.Load(cacheKey); ok {
		return cached.(*memoryManager), nil
	}

	mgr, err := newManager(agentID, workspaceDir, cfg)
	if err != nil {
		return nil, err
	}

	actual, loaded := managerCache.LoadOrStore(cacheKey, mgr)
	if loaded {
		// Another goroutine created it first, close ours
		mgr.Close() //nolint:errcheck
		return actual.(*memoryManager), nil
	}
	return mgr, nil
}

// memoryManager implements the Manager interface.
type memoryManager struct {
	agentID      string
	workspaceDir string
	config       MemoryConfig
	store        *Store
	dbPath       string
	logger       *slog.Logger
	syncMu       sync.Mutex
	closed       bool
	embedder     EmbeddingProvider // nil if no embedding provider
	redactor     Redactor          // nil if no redactor configured
	watcher      *Watcher          // nil until first sync if watch enabled
	watcherOnce  sync.Once
}

// newManager creates a new memoryManager instance.
func newManager(agentID, workspaceDir string, cfg MemoryConfig) (*memoryManager, error) {
	// Apply defaults
	if cfg.Chunking.Tokens <= 0 {
		cfg.Chunking.Tokens = 400
	}
	if cfg.Chunking.Overlap < 0 {
		cfg.Chunking.Overlap = 0
	}
	if cfg.Query.MaxResults <= 0 {
		cfg.Query.MaxResults = 6
	}
	if cfg.Query.MinScore <= 0 {
		cfg.Query.MinScore = 0.35
	}

	// Resolve database path
	dbPath := cfg.Storage.Path
	if dbPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("get home dir: %w", err)
		}
		dbDir := filepath.Join(homeDir, ".openclaw", "memory")
		if err := os.MkdirAll(dbDir, 0755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
		dbPath = filepath.Join(dbDir, agentID+".sqlite")
	}

	store, err := OpenStore(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	logger := slog.Default().With("component", "memory", "agent", agentID)

	return &memoryManager{
		agentID:      agentID,
		workspaceDir: workspaceDir,
		config:       cfg,
		store:        store,
		dbPath:       dbPath,
		logger:       logger,
	}, nil
}

// SetEmbeddingProvider configures the embedding provider for hybrid search.
func (m *memoryManager) SetEmbeddingProvider(p EmbeddingProvider) {
	m.embedder = p
}

// SetRedactor configures the redactor for session file indexing.
func (m *memoryManager) SetRedactor(r Redactor) {
	m.redactor = r
}

// Search executes a search query.
func (m *memoryManager) Search(ctx context.Context, opts SearchOptions) ([]SearchResult, error) {
	if opts.Query == "" {
		return nil, nil
	}

	maxResults := opts.MaxResults
	if maxResults <= 0 {
		maxResults = m.config.Query.MaxResults
	}
	minScore := opts.MinScore
	if minScore <= 0 {
		minScore = m.config.Query.MinScore
	}

	startTime := time.Now()

	candidateLimit := maxResults * m.config.Query.Hybrid.CandidateMultiplier
	if candidateLimit <= 0 {
		candidateLimit = maxResults * 4
	}
	if candidateLimit > 200 {
		candidateLimit = 200
	}

	// Determine search mode: hybrid or FTS-only
	useHybrid := m.embedder != nil && m.config.Query.Hybrid.Enabled

	// FTS search
	ftsResults := make(map[string]ftsCandidate)
	if m.store.FTSAvailable() {
		ftsQuery := BuildFTSQuery(opts.Query)
		if ftsQuery != "" {
			chunks, ranks, err := m.store.SearchFTS(ftsQuery, candidateLimit)
			if err != nil {
				m.logger.Warn("FTS search failed", "error", err)
			} else {
				for i, c := range chunks {
					score := NormalizeBM25Score(ranks[i])
					// FTS matched this chunk so it is relevant. BM25 scores
					// can be extremely low with small corpora so we floor
					// any match at the configured minScore to avoid false
					// negatives from the score filter.
					if score < minScore {
						score = minScore
					}
					ftsResults[c.ID] = ftsCandidate{chunk: c, textScore: score}
				}
			}
		}
	}

	// Vector search (if hybrid mode)
	vectorResults := make(map[string]float64)
	if useHybrid {
		queryVec, err := m.embedder.EmbedQuery(ctx, opts.Query)
		if err != nil {
			m.logger.Warn("query embedding failed, falling back to FTS-only", "error", err)
		} else {
			allChunks, err := m.store.ListAllChunks()
			if err != nil {
				m.logger.Warn("failed to list chunks for vector search", "error", err)
			} else {
				for _, c := range allChunks {
					if c.Embedding == "" {
						continue
					}
					var chunkVec []float32
					if err := json.Unmarshal([]byte(c.Embedding), &chunkVec); err != nil {
						continue
					}
					sim := CosineSimilarity(queryVec, chunkVec)
					if sim > 0 {
						vectorResults[c.ID] = sim
					}
				}
			}
		}
	}

	// Fuse results
	vectorWeight := m.config.Query.Hybrid.VectorWeight
	textWeight := m.config.Query.Hybrid.TextWeight
	if vectorWeight <= 0 {
		vectorWeight = 0.7
	}
	if textWeight <= 0 {
		textWeight = 0.3
	}

	// Collect all candidate chunk IDs
	allCandidates := make(map[string]bool)
	for id := range ftsResults {
		allCandidates[id] = true
	}
	for id := range vectorResults {
		allCandidates[id] = true
	}

	var results []SearchResult
	for id := range allCandidates {
		var textScore, vecScore float64
		var chunk Chunk

		if fc, ok := ftsResults[id]; ok {
			textScore = fc.textScore
			chunk = fc.chunk
		}
		if vs, ok := vectorResults[id]; ok {
			vecScore = vs
		}

		var finalScore float64
		if useHybrid && len(vectorResults) > 0 {
			finalScore = FuseScores(vecScore, textScore, vectorWeight, textWeight)
		} else {
			finalScore = textScore
		}

		if finalScore < minScore {
			continue
		}

		// If we only have vector result, we need to fetch the chunk
		if chunk.ID == "" {
			chunks, err := m.store.ListAllChunks()
			if err == nil {
				for _, c := range chunks {
					if c.ID == id {
						chunk = c
						break
					}
				}
			}
		}
		if chunk.ID == "" {
			continue
		}

		results = append(results, SearchResult{
			Path:      chunk.Path,
			StartLine: chunk.StartLine,
			EndLine:   chunk.EndLine,
			Score:     finalScore,
			Snippet:   TruncateSnippet(chunk.Text, 700),
			Source:    chunk.Source,
		})
	}

	// Sort by score descending
	sortResultsByScore(results)

	// Limit results
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	// Apply relaxed min-score fallback (FR-018)
	if len(results) == 0 && len(ftsResults) > 0 {
		relaxedMin := minScore
		if textWeight < relaxedMin {
			relaxedMin = textWeight
		}
		for _, fc := range ftsResults {
			score := fc.textScore
			if score >= relaxedMin {
				results = append(results, SearchResult{
					Path:      fc.chunk.Path,
					StartLine: fc.chunk.StartLine,
					EndLine:   fc.chunk.EndLine,
					Score:     score,
					Snippet:   TruncateSnippet(fc.chunk.Text, 700),
					Source:    fc.chunk.Source,
				})
			}
		}
		sortResultsByScore(results)
		if len(results) > maxResults {
			results = results[:maxResults]
		}
	}

	elapsed := time.Since(startTime)
	m.logger.Debug("search completed",
		"query", opts.Query,
		"mode", searchMode(useHybrid),
		"results", len(results),
		"fts_candidates", len(ftsResults),
		"vector_candidates", len(vectorResults),
		"duration", elapsed,
	)

	return results, nil
}

// ReadFile reads content from an allowed memory file path.
func (m *memoryManager) ReadFile(ctx context.Context, opts ReadFileOptions) (ReadFileResult, error) {
	// Resolve the path
	absPath := opts.Path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(m.workspaceDir, absPath)
	}

	relPath, err := filepath.Rel(m.workspaceDir, absPath)
	if err != nil || strings.HasPrefix(relPath, "..") {
		// Path is outside workspace, check extraPaths
		allowed := false
		for _, ep := range m.config.ExtraPaths {
			epAbs := ep
			if !filepath.IsAbs(epAbs) {
				epAbs = filepath.Join(m.workspaceDir, epAbs)
			}
			if strings.HasPrefix(absPath, epAbs) {
				allowed = true
				break
			}
		}
		if !allowed {
			return ReadFileResult{}, fmt.Errorf("path %q is outside allowed boundaries", opts.Path)
		}
	} else {
		if !IsAllowedReadPath(relPath, m.config.ExtraPaths) {
			return ReadFileResult{}, fmt.Errorf("path %q is not in allowed memory paths", opts.Path)
		}
	}

	// Check for symlinks
	info, err := os.Lstat(absPath)
	if err != nil {
		// File doesn't exist — return empty, not error
		return ReadFileResult{Path: absPath}, nil
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return ReadFileResult{}, fmt.Errorf("symlinks are not allowed: %q", opts.Path)
	}

	// Only .md files
	if !strings.HasSuffix(strings.ToLower(absPath), ".md") {
		return ReadFileResult{}, fmt.Errorf("only .md files allowed: %q", opts.Path)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return ReadFileResult{Path: absPath}, nil
		}
		return ReadFileResult{}, fmt.Errorf("read file: %w", err)
	}

	text := string(content)

	// Apply offset and limit
	if opts.Offset > 0 || opts.Limit > 0 {
		lines := strings.Split(text, "\n")
		start := opts.Offset
		if start >= len(lines) {
			return ReadFileResult{Text: "", Path: absPath}, nil
		}
		end := len(lines)
		if opts.Limit > 0 && start+opts.Limit < end {
			end = start + opts.Limit
		}
		text = strings.Join(lines[start:end], "\n")
	}

	return ReadFileResult{Text: text, Path: absPath}, nil
}

// Sync synchronizes the index with the filesystem.
func (m *memoryManager) Sync(ctx context.Context, opts SyncOptions) error {
	m.syncMu.Lock()
	defer m.syncMu.Unlock()

	ss := &syncState{
		store:        m.store,
		workspaceDir: m.workspaceDir,
		config:       m.config,
		logger:       m.logger,
		embedder:     m.embedder,
		redactor:     m.redactor,
	}
	err := ss.runSync(opts)

	// Start watcher on first successful sync if enabled
	if err == nil && m.config.Sync.Watch {
		m.watcherOnce.Do(func() {
			w, watchErr := NewWatcher(context.Background(), WatcherConfig{
				WorkspaceDir:      m.workspaceDir,
				ExtraPaths:        m.config.ExtraPaths,
				DebounceMs:        m.config.Sync.WatchDebounceMs,
				SessionDebounceMs: 5000,
				Logger:            m.logger,
				OnSync: func() {
					// Trigger background sync on file changes
					go func() {
						syncErr := m.Sync(context.Background(), SyncOptions{Reason: "watcher"})
						if syncErr != nil {
							m.logger.Warn("watcher-triggered sync failed", "error", syncErr)
						}
					}()
				},
			})
			if watchErr != nil {
				m.logger.Warn("failed to start file watcher", "error", watchErr)
			} else {
				m.watcher = w
				m.logger.Info("file watcher started")
			}
		})
	}

	return err
}

// Status returns the current state of the memory backend.
func (m *memoryManager) Status(ctx context.Context) (StatusResult, error) {
	fileCount := 0
	files, err := m.store.ListFiles()
	if err == nil {
		fileCount = len(files)
	}

	chunkCount, _ := m.store.CountChunks()
	sourceCounts, _ := m.store.CountChunksBySource()

	return StatusResult{
		Backend:       "builtin",
		Provider:      m.config.Provider.Provider,
		Model:         m.config.Provider.Model,
		FilesIndexed:  fileCount,
		ChunksIndexed: chunkCount,
		WorkspaceDir:  m.workspaceDir,
		DatabasePath:  m.dbPath,
		ExtraPaths:    m.config.ExtraPaths,
		Sources:       m.config.Sources,
		SourceCounts:  sourceCounts,
		CacheEnabled:  m.config.Cache.Enabled,
		FTSAvailable:  m.store.FTSAvailable(),
	}, nil
}

// ProbeEmbedding checks if an embedding provider is available.
func (m *memoryManager) ProbeEmbedding(ctx context.Context) (bool, error) {
	// FTS-only mode for now; embedding providers added in US2
	return m.config.Provider.Provider != "", nil
}

// ProbeVector checks if vector search is available.
func (m *memoryManager) ProbeVector(ctx context.Context) (bool, error) {
	// In-process cosine similarity always available when embeddings are present
	return m.config.Provider.Provider != "", nil
}

// Close releases resources.
func (m *memoryManager) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	if m.watcher != nil {
		m.watcher.Close()
	}
	return m.store.Close()
}
