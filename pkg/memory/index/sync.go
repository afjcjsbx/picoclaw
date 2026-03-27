package index

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Redactor masks sensitive data in text before indexing.
type Redactor interface {
	Redact(text string) string
}

// syncState holds the incremental sync state.
type syncState struct {
	store        *Store
	workspaceDir string
	config       MemoryConfig
	logger       *slog.Logger
	embedder     EmbeddingProvider // nil if no embedding provider available
	redactor     Redactor          // nil if no redactor configured
}

// runSync performs an incremental sync of memory files.
// It discovers files, diffs against the file registry, chunks changed files,
// upserts/deletes as needed, and updates metadata.
func (ss *syncState) runSync(opts SyncOptions) error {
	startTime := time.Now()
	ss.logger.Info("sync started", "reason", opts.Reason, "force", opts.Force)

	// Check if full reindex is needed
	if opts.Force {
		ss.logger.Info("forced full reindex requested")
		return ss.fullReindex(opts)
	}

	needsReindex, err := ss.checkReindexNeeded()
	if err != nil {
		return fmt.Errorf("check reindex: %w", err)
	}
	if needsReindex {
		ss.logger.Info("reindex triggered due to configuration change")
		return ss.fullReindex(opts)
	}

	// Discover files
	files, err := DiscoverMemoryFiles(ss.workspaceDir, ss.config.ExtraPaths)
	if err != nil {
		return fmt.Errorf("discover files: %w", err)
	}

	if opts.OnProgress != nil {
		opts.OnProgress(SyncProgress{Phase: "discover", FilesTotal: len(files)})
	}

	// Load existing file registry
	existingFiles, err := ss.store.ListFiles()
	if err != nil {
		return fmt.Errorf("list files: %w", err)
	}
	existingMap := make(map[string]FileRecord, len(existingFiles))
	for _, f := range existingFiles {
		existingMap[f.Path] = f
	}

	// Build set of discovered paths
	discoveredPaths := make(map[string]bool, len(files))
	for _, f := range files {
		discoveredPaths[f.Path] = true
	}

	// Detect changes
	var added, changed []DiscoveredFile
	for _, f := range files {
		existing, ok := existingMap[f.Path]
		if !ok {
			added = append(added, f)
			continue
		}
		// Check hash
		absPath := filepath.Join(ss.workspaceDir, f.Path)
		hash, err := HashFile(absPath)
		if err != nil {
			ss.logger.Warn("failed to hash file", "path", f.Path, "error", err)
			continue
		}
		if hash != existing.Hash {
			changed = append(changed, f)
		}
	}

	// Detect deletions
	var deleted []string
	for _, f := range existingFiles {
		if f.Source == "memory" && !discoveredPaths[f.Path] {
			deleted = append(deleted, f.Path)
		}
	}

	ss.logger.Info("sync diff",
		"added", len(added),
		"changed", len(changed),
		"deleted", len(deleted),
		"unchanged", len(files)-len(added)-len(changed),
	)

	// Process additions and changes
	totalToProcess := len(added) + len(changed)
	processed := 0
	for _, f := range append(added, changed...) {
		if err := ss.indexFile(f); err != nil {
			ss.logger.Warn("failed to index file", "path", f.Path, "error", err)
			continue
		}
		processed++
		if opts.OnProgress != nil {
			opts.OnProgress(SyncProgress{
				Phase:      "index",
				FilesTotal: totalToProcess,
				FilesDone:  processed,
			})
		}
	}

	// Process session files if sessions source is enabled
	sessionsEnabled := false
	for _, s := range ss.config.Sources {
		if s == "sessions" {
			sessionsEnabled = true
			break
		}
	}
	if sessionsEnabled {
		sessionFiles := opts.SessionFiles
		if len(sessionFiles) > 0 {
			for _, sf := range sessionFiles {
				if err := ss.indexSessionFile(sf); err != nil {
					ss.logger.Warn("failed to index session file", "path", sf, "error", err)
				}
			}
		}
	}

	// Process deletions
	for _, path := range deleted {
		if err := ss.store.DeleteChunksByPath(path); err != nil {
			ss.logger.Warn("failed to delete chunks", "path", path, "error", err)
		}
		if err := ss.store.DeleteFile(path); err != nil {
			ss.logger.Warn("failed to delete file record", "path", path, "error", err)
		}
	}

	// Update metadata
	if err := ss.saveMetadata(); err != nil {
		ss.logger.Warn("failed to save metadata", "error", err)
	}

	elapsed := time.Since(startTime)
	chunkCount, _ := ss.store.CountChunks()
	fileCount := len(files) - len(deleted)
	ss.logger.Info("sync completed",
		"duration", elapsed,
		"files", fileCount,
		"chunks", chunkCount,
	)

	return nil
}

// indexFile reads, chunks, and indexes a single file.
func (ss *syncState) indexFile(f DiscoveredFile) error {
	absPath := filepath.Join(ss.workspaceDir, f.Path)

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read file %q: %w", f.Path, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat file %q: %w", f.Path, err)
	}

	fileHash := HashContent(content)

	// Chunk the content
	chunkResults := ChunkMarkdown(string(content), ss.config.Chunking.Tokens, ss.config.Chunking.Overlap)

	now := time.Now().UnixMilli()
	model := ""
	if ss.embedder != nil {
		model = ss.embedder.Model()
	}

	chunks := make([]Chunk, len(chunkResults))
	for i, cr := range chunkResults {
		chunks[i] = Chunk{
			ID:        GenerateChunkID(f.Source, f.Path, cr.StartLine, cr.EndLine, cr.Hash, model),
			Path:      f.Path,
			Source:    f.Source,
			StartLine: cr.StartLine,
			EndLine:   cr.EndLine,
			Hash:      cr.Hash,
			Model:     model,
			Text:      cr.Text,
			Embedding: "",
			UpdatedAt: now,
		}
	}

	// Generate embeddings if provider is available
	if ss.embedder != nil && len(chunks) > 0 {
		ss.embedChunks(chunks)
	}

	// Delete old chunks for this file, then insert new ones
	if err := ss.store.DeleteChunksByPath(f.Path); err != nil {
		return fmt.Errorf("delete old chunks: %w", err)
	}
	if err := ss.store.UpsertChunks(chunks); err != nil {
		return fmt.Errorf("upsert chunks: %w", err)
	}

	// Update file record
	fr := FileRecord{
		Path:   f.Path,
		Source: f.Source,
		Hash:   fileHash,
		Mtime:  info.ModTime().UnixMilli(),
		Size:   info.Size(),
	}
	if err := ss.store.UpsertFile(fr); err != nil {
		return fmt.Errorf("upsert file record: %w", err)
	}

	return nil
}

// indexSessionFile extracts, redacts, chunks, and indexes a JSONL session file.
func (ss *syncState) indexSessionFile(sessionPath string) error {
	absPath := sessionPath
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(ss.workspaceDir, absPath)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("read session file %q: %w", sessionPath, err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("stat session file %q: %w", sessionPath, err)
	}

	// Extract messages
	extraction, err := ExtractSessionMessages(string(content))
	if err != nil {
		return fmt.Errorf("extract session messages %q: %w", sessionPath, err)
	}

	if extraction.Content == "" {
		return nil
	}

	// Apply redaction
	normalizedContent := extraction.Content
	if ss.redactor != nil {
		normalizedContent = ss.redactor.Redact(normalizedContent)
	}

	// Compute hash including lineMap
	fileHash := HashSessionFile(normalizedContent, extraction.LineMap)

	// Check if already indexed with same hash
	existing, err := ss.store.GetFile(sessionPath)
	if err == nil && existing != nil && existing.Hash == fileHash {
		return nil // No changes
	}

	// Chunk the normalized content
	chunkResults := ChunkMarkdown(normalizedContent, ss.config.Chunking.Tokens, ss.config.Chunking.Overlap)

	now := time.Now().UnixMilli()
	model := ""
	if ss.embedder != nil {
		model = ss.embedder.Model()
	}

	chunks := make([]Chunk, len(chunkResults))
	for i, cr := range chunkResults {
		// Remap line numbers back to original JSONL lines
		mappedStart, mappedEnd := RemapChunkLines(cr.StartLine, cr.EndLine, extraction.LineMap)

		chunks[i] = Chunk{
			ID:        GenerateChunkID("sessions", sessionPath, mappedStart, mappedEnd, cr.Hash, model),
			Path:      sessionPath,
			Source:    "sessions",
			StartLine: mappedStart,
			EndLine:   mappedEnd,
			Hash:      cr.Hash,
			Model:     model,
			Text:      cr.Text,
			Embedding: "",
			UpdatedAt: now,
		}
	}

	// Generate embeddings if available
	if ss.embedder != nil && len(chunks) > 0 {
		ss.embedChunks(chunks)
	}

	// Delete old chunks and insert new
	if err := ss.store.DeleteChunksByPath(sessionPath); err != nil {
		return fmt.Errorf("delete old session chunks: %w", err)
	}
	if err := ss.store.UpsertChunks(chunks); err != nil {
		return fmt.Errorf("upsert session chunks: %w", err)
	}

	// Update file record
	fr := FileRecord{
		Path:   sessionPath,
		Source: "sessions",
		Hash:   fileHash,
		Mtime:  info.ModTime().UnixMilli(),
		Size:   info.Size(),
	}
	if err := ss.store.UpsertFile(fr); err != nil {
		return fmt.Errorf("upsert session file record: %w", err)
	}

	ss.logger.Info("indexed session file", "path", sessionPath, "chunks", len(chunks))
	return nil
}

// embedChunks generates embeddings for chunks, using the cache when possible.
func (ss *syncState) embedChunks(chunks []Chunk) {
	if ss.embedder == nil {
		return
	}

	provider := ss.embedder.Name()
	model := ss.embedder.Model()
	pKey := providerKey(ss.config.Provider)

	// Collect hashes for cache lookup
	hashes := make([]string, len(chunks))
	for i, c := range chunks {
		hashes[i] = c.Hash
	}

	// Batch cache lookup
	cached, err := ss.store.GetCachedEmbeddings(provider, model, pKey, hashes)
	if err != nil {
		ss.logger.Warn("failed to lookup embedding cache", "error", err)
		cached = make(map[string]EmbeddingCacheEntry)
	}

	// Find chunks that need embedding
	var needEmbed []int
	for i, c := range chunks {
		if entry, ok := cached[c.Hash]; ok {
			chunks[i].Embedding = entry.Embedding
		} else {
			needEmbed = append(needEmbed, i)
		}
	}

	ss.logger.Debug("embedding status",
		"total", len(chunks),
		"cached", len(chunks)-len(needEmbed),
		"need_embed", len(needEmbed),
	)

	if len(needEmbed) == 0 {
		return
	}

	// Batch embed the missing chunks
	texts := make([]string, len(needEmbed))
	for i, idx := range needEmbed {
		texts[i] = chunks[idx].Text
	}

	ctx := context.Background()
	vectors, err := ss.embedder.EmbedBatch(ctx, texts)
	if err != nil {
		ss.logger.Warn("embedding failed, continuing without embeddings", "error", err)
		return
	}

	if len(vectors) != len(texts) {
		ss.logger.Warn("embedding count mismatch", "expected", len(texts), "got", len(vectors))
		return
	}

	// Store embeddings and update cache
	now := time.Now().UnixMilli()
	var cacheEntries []EmbeddingCacheEntry
	for i, idx := range needEmbed {
		vec := vectors[i]
		embJSON, _ := json.Marshal(vec)
		embStr := string(embJSON)
		chunks[idx].Embedding = embStr

		cacheEntries = append(cacheEntries, EmbeddingCacheEntry{
			Provider:    provider,
			Model:       model,
			ProviderKey: pKey,
			Hash:        chunks[idx].Hash,
			Embedding:   embStr,
			Dims:        len(vec),
			UpdatedAt:   now,
		})
	}

	if err := ss.store.SetCachedEmbeddings(cacheEntries); err != nil {
		ss.logger.Warn("failed to cache embeddings", "error", err)
	}
}

// checkReindexNeeded compares current config with stored metadata.
func (ss *syncState) checkReindexNeeded() (bool, error) {
	raw, err := ss.store.GetMeta(metaKey)
	if err != nil {
		return false, err
	}
	if raw == "" {
		// First sync, no reindex needed — just index everything
		return false, nil
	}

	var stored IndexMetadata
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		// Corrupted metadata, reindex
		return true, nil
	}

	current := ss.buildMetadata()
	if stored.Provider != current.Provider ||
		stored.Model != current.Model ||
		stored.ProviderKey != current.ProviderKey ||
		stored.ChunkTokens != current.ChunkTokens ||
		stored.ChunkOverlap != current.ChunkOverlap {
		return true, nil
	}

	// Check sources
	if len(stored.Sources) != len(current.Sources) {
		return true, nil
	}
	storedSources := make(map[string]bool, len(stored.Sources))
	for _, s := range stored.Sources {
		storedSources[s] = true
	}
	for _, s := range current.Sources {
		if !storedSources[s] {
			return true, nil
		}
	}

	return false, nil
}

// fullReindex performs an atomic reindex by building the index in a temp DB
// and swapping it with the original on success.
func (ss *syncState) fullReindex(opts SyncOptions) error {
	ss.logger.Info("starting atomic full reindex")

	dbPath := ss.store.Path()
	tempPath := dbPath + ".reindex.tmp"

	// Clean up any leftover temp DB
	for _, ext := range []string{"", "-wal", "-shm"} {
		os.Remove(tempPath + ext)
	}

	// Create temp DB
	tempStore, err := OpenStore(tempPath)
	if err != nil {
		return fmt.Errorf("create temp store: %w", err)
	}

	// Seed embedding cache from current DB
	currentEntries, err := ss.store.ListAllCacheEntries()
	if err == nil && len(currentEntries) > 0 {
		if err := tempStore.SetCachedEmbeddings(currentEntries); err != nil {
			ss.logger.Warn("failed to seed cache into temp DB", "error", err)
		}
	}

	// Build index in temp DB using a temp syncState
	tempSS := &syncState{
		store:        tempStore,
		workspaceDir: ss.workspaceDir,
		config:       ss.config,
		logger:       ss.logger,
		embedder:     ss.embedder,
		redactor:     ss.redactor,
	}

	files, err := DiscoverMemoryFiles(ss.workspaceDir, ss.config.ExtraPaths)
	if err != nil {
		tempStore.Close()
		os.Remove(tempPath)
		return fmt.Errorf("discover files for reindex: %w", err)
	}

	for i, f := range files {
		if err := tempSS.indexFile(f); err != nil {
			ss.logger.Warn("failed to index file during reindex", "path", f.Path, "error", err)
		}
		if opts.OnProgress != nil {
			opts.OnProgress(SyncProgress{
				Phase:      "reindex",
				FilesTotal: len(files),
				FilesDone:  i + 1,
			})
		}
	}

	// Save metadata in temp DB
	if err := tempSS.saveMetadata(); err != nil {
		tempStore.Close()
		os.Remove(tempPath)
		return fmt.Errorf("save metadata in temp DB: %w", err)
	}

	// Close both DBs
	tempStore.Close()
	ss.store.Close()

	// Atomic swap: rename temp DB files over original
	for _, ext := range []string{"", "-wal", "-shm"} {
		os.Remove(dbPath + ext)
	}
	if err := os.Rename(tempPath, dbPath); err != nil {
		// Recovery: try to reopen original
		ss.store.Reopen() //nolint:errcheck
		return fmt.Errorf("atomic swap failed: %w", err)
	}
	// Clean up temp sidecar files
	for _, ext := range []string{"-wal", "-shm"} {
		os.Remove(tempPath + ext)
	}

	// Reopen the store with the new DB
	if err := ss.store.Reopen(); err != nil {
		return fmt.Errorf("reopen after reindex: %w", err)
	}

	ss.logger.Info("atomic full reindex completed", "files", len(files))
	return nil
}

// buildMetadata creates an IndexMetadata from the current config.
func (ss *syncState) buildMetadata() IndexMetadata {
	return IndexMetadata{
		Provider:     ss.config.Provider.Provider,
		Model:        ss.config.Provider.Model,
		ProviderKey:  providerKey(ss.config.Provider),
		Sources:      ss.config.Sources,
		ScopeHash:    scopeHash(ss.workspaceDir, ss.config.ExtraPaths),
		ChunkTokens:  ss.config.Chunking.Tokens,
		ChunkOverlap: ss.config.Chunking.Overlap,
	}
}

// saveMetadata persists the current IndexMetadata.
func (ss *syncState) saveMetadata() error {
	meta := ss.buildMetadata()

	// Update chunk count for vector dims tracking
	chunkCount, _ := ss.store.CountChunks()
	_ = chunkCount // vector dims will be set when embeddings are added

	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal metadata: %w", err)
	}
	return ss.store.SetMeta(metaKey, string(data))
}

// providerKey generates a stable fingerprint for the provider configuration.
func providerKey(cfg ProviderConfig) string {
	key := cfg.Provider + ":" + cfg.Model
	if cfg.Remote != nil && cfg.Remote.BaseURL != "" {
		key += ":" + cfg.Remote.BaseURL
	}
	return HashChunkText(key)
}

// scopeHash hashes the workspace dir and extra paths configuration.
func scopeHash(workspaceDir string, extraPaths []string) string {
	input := workspaceDir
	for _, p := range extraPaths {
		input += "\n" + p
	}
	return HashChunkText(input)
}
