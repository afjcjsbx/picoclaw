package index

import "fmt"

// MemoryConfig holds all configuration for the memory indexing system.
type MemoryConfig struct {
	Provider   ProviderConfig `json:"provider,omitempty"`
	Storage    StorageConfig  `json:"storage,omitempty"`
	Chunking   ChunkingConfig `json:"chunking,omitempty"`
	Sync       SyncConfig     `json:"sync,omitempty"`
	Query      QueryConfig    `json:"query,omitempty"`
	Cache      CacheConfig    `json:"cache,omitempty"`
	Sources    []string       `json:"sources,omitempty"`
	ExtraPaths []string       `json:"extraPaths,omitempty"`
}

// ProviderConfig configures the embedding provider.
type ProviderConfig struct {
	Provider             string        `json:"provider,omitempty"`
	Model                string        `json:"model,omitempty"`
	OutputDimensionality int           `json:"outputDimensionality,omitempty"`
	Fallback             string        `json:"fallback,omitempty"`
	Remote               *RemoteConfig `json:"remote,omitempty"`
	Local                *LocalConfig  `json:"local,omitempty"`
}

// RemoteConfig holds settings for remote embedding providers.
type RemoteConfig struct {
	BaseURL string            `json:"baseUrl,omitempty"`
	APIKey  string            `json:"apiKey,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	Batch   *BatchConfig      `json:"batch,omitempty"`
}

// LocalConfig holds settings for local embedding models.
type LocalConfig struct {
	ModelPath     string `json:"modelPath,omitempty"`
	ModelCacheDir string `json:"modelCacheDir,omitempty"`
}

// BatchConfig configures async batch embedding.
type BatchConfig struct {
	Enabled        bool `json:"enabled,omitempty"`
	Wait           bool `json:"wait,omitempty"`
	Concurrency    int  `json:"concurrency,omitempty"`
	PollIntervalMs int  `json:"pollIntervalMs,omitempty"`
	TimeoutMinutes int  `json:"timeoutMinutes,omitempty"`
}

// StorageConfig configures the SQLite storage backend.
type StorageConfig struct {
	Driver string       `json:"driver,omitempty"`
	Path   string       `json:"path,omitempty"`
	Vector VectorConfig `json:"vector,omitempty"`
}

// VectorConfig configures the optional vector index.
type VectorConfig struct {
	Enabled       bool   `json:"enabled,omitempty"`
	ExtensionPath string `json:"extensionPath,omitempty"`
}

// ChunkingConfig controls text chunking behavior.
type ChunkingConfig struct {
	Tokens  int `json:"tokens,omitempty"`
	Overlap int `json:"overlap,omitempty"`
}

// SyncConfig configures synchronization behavior.
type SyncConfig struct {
	OnSessionStart  bool           `json:"onSessionStart,omitempty"`
	OnSearch        bool           `json:"onSearch,omitempty"`
	Watch           bool           `json:"watch,omitempty"`
	WatchDebounceMs int            `json:"watchDebounceMs,omitempty"`
	IntervalMinutes int            `json:"intervalMinutes,omitempty"`
	Sessions        SessionsConfig `json:"sessions,omitempty"`
}

// SessionsConfig configures session-specific sync behavior.
type SessionsConfig struct {
	DeltaBytes          int  `json:"deltaBytes,omitempty"`
	DeltaMessages       int  `json:"deltaMessages,omitempty"`
	PostCompactionForce bool `json:"postCompactionForce,omitempty"`
}

// QueryConfig configures search query behavior.
type QueryConfig struct {
	MaxResults int          `json:"maxResults,omitempty"`
	MinScore   float64      `json:"minScore,omitempty"`
	Hybrid     HybridConfig `json:"hybrid,omitempty"`
}

// HybridConfig configures hybrid search parameters.
type HybridConfig struct {
	Enabled             bool        `json:"enabled,omitempty"`
	VectorWeight        float64     `json:"vectorWeight,omitempty"`
	TextWeight          float64     `json:"textWeight,omitempty"`
	CandidateMultiplier int         `json:"candidateMultiplier,omitempty"`
	MMR                 MMRConfig   `json:"mmr,omitempty"`
	TemporalDecay       DecayConfig `json:"temporalDecay,omitempty"`
}

// MMRConfig configures Maximal Marginal Relevance re-ranking.
type MMRConfig struct {
	Enabled bool    `json:"enabled,omitempty"`
	Lambda  float64 `json:"lambda,omitempty"`
}

// DecayConfig configures temporal decay scoring.
type DecayConfig struct {
	Enabled      bool `json:"enabled,omitempty"`
	HalfLifeDays int  `json:"halfLifeDays,omitempty"`
}

// CacheConfig configures the embedding cache.
type CacheConfig struct {
	Enabled    bool `json:"enabled,omitempty"`
	MaxEntries int  `json:"maxEntries,omitempty"`
}

// validProviders lists the accepted provider names.
var validProviders = map[string]bool{
	"":        true, // empty = no provider (FTS-only)
	"openai":  true,
	"gemini":  true,
	"voyage":  true,
	"mistral": true,
	"ollama":  true,
	"local":   true,
	"auto":    true,
}

// ValidateConfig checks the MemoryConfig for invalid values and returns
// actionable error messages.
func ValidateConfig(cfg MemoryConfig) error {
	// Validate provider name
	if !validProviders[cfg.Provider.Provider] {
		return fmt.Errorf("invalid memory provider %q: accepted values are openai, gemini, voyage, mistral, ollama, local, auto", cfg.Provider.Provider)
	}

	// Validate API key for remote providers
	remoteProviders := map[string]bool{"openai": true, "gemini": true, "voyage": true, "mistral": true}
	if remoteProviders[cfg.Provider.Provider] {
		if cfg.Provider.Remote == nil || cfg.Provider.Remote.APIKey == "" {
			return fmt.Errorf("memory provider %q requires remote.apiKey to be configured", cfg.Provider.Provider)
		}
	}

	// Validate local provider model path
	if cfg.Provider.Provider == "local" {
		if cfg.Provider.Local == nil || cfg.Provider.Local.ModelPath == "" {
			return fmt.Errorf("memory provider 'local' requires local.modelPath to be configured")
		}
	}

	// Validate chunking
	if cfg.Chunking.Tokens < 0 {
		return fmt.Errorf("memory chunking.tokens must be non-negative, got %d", cfg.Chunking.Tokens)
	}
	if cfg.Chunking.Overlap < 0 {
		return fmt.Errorf("memory chunking.overlap must be non-negative, got %d", cfg.Chunking.Overlap)
	}

	// Validate sources
	validSources := map[string]bool{"memory": true, "sessions": true}
	for _, s := range cfg.Sources {
		if !validSources[s] {
			return fmt.Errorf("invalid memory source %q: accepted values are memory, sessions", s)
		}
	}

	return nil
}

// DefaultMemoryConfig returns a MemoryConfig with all defaults applied.
func DefaultMemoryConfig() MemoryConfig {
	return MemoryConfig{
		Provider: ProviderConfig{
			Fallback: "none",
		},
		Storage: StorageConfig{
			Driver: "sqlite",
		},
		Chunking: ChunkingConfig{
			Tokens:  400,
			Overlap: 80,
		},
		Sync: SyncConfig{
			OnSessionStart:  true,
			OnSearch:        true,
			Watch:           true,
			WatchDebounceMs: 1500,
			IntervalMinutes: 0,
			Sessions: SessionsConfig{
				DeltaBytes:          100000,
				DeltaMessages:       50,
				PostCompactionForce: true,
			},
		},
		Query: QueryConfig{
			MaxResults: 6,
			MinScore:   0.35,
			Hybrid: HybridConfig{
				Enabled:             true,
				VectorWeight:        0.7,
				TextWeight:          0.3,
				CandidateMultiplier: 4,
				MMR: MMRConfig{
					Enabled: false,
					Lambda:  0.7,
				},
				TemporalDecay: DecayConfig{
					Enabled:      false,
					HalfLifeDays: 30,
				},
			},
		},
		Cache: CacheConfig{
			Enabled: true,
		},
		Sources: []string{"memory"},
	}
}
