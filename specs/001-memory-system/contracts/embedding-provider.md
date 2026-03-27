# Contract: EmbeddingProvider Interface

**Package**: `pkg/memory/index/embedding`

## EmbeddingProvider

Interface that all embedding providers must implement.

```go
// Provider generates embedding vectors from text.
type Provider interface {
    // Name returns the provider identifier (e.g., "openai", "gemini", "local").
    Name() string

    // Model returns the active model name.
    Model() string

    // MaxInput returns the optional max input token limit (0 = unlimited).
    MaxInput() int

    // EmbedQuery generates an embedding for a single search query.
    EmbedQuery(ctx context.Context, text string) ([]float32, error)

    // EmbedBatch generates embeddings for a batch of texts.
    // Returns vectors in the same order as input texts.
    // Len(result) MUST equal len(texts).
    EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}
```

## ProviderConfig

```go
type ProviderConfig struct {
    Provider            string            // "openai"|"gemini"|"voyage"|"mistral"|"ollama"|"local"|"auto"
    Model               string            // Model name (uses provider default if empty)
    OutputDimensionality int              // Optional output dims override
    Fallback            string            // Fallback provider name ("none" default)
    Remote              *RemoteConfig     // Remote provider settings
    Local               *LocalConfig      // Local model settings
}

type RemoteConfig struct {
    BaseURL  string            // API base URL override
    APIKey   string            // API key (per-provider, config file only)
    Headers  map[string]string // Additional HTTP headers
    Batch    *BatchConfig      // Async batch settings
}

type LocalConfig struct {
    ModelPath    string // Path to GGUF model file
    ModelCacheDir string // Cache directory for downloaded models
}

type BatchConfig struct {
    Enabled        bool // Default: false
    Wait           bool // Default: true
    Concurrency    int  // Default: 2
    PollIntervalMs int  // Default: 2000
    TimeoutMinutes int  // Default: 60
}
```

## Provider Default Models

| Provider | Default Model              |
|----------|----------------------------|
| openai   | `text-embedding-3-small`   |
| gemini   | `gemini-embedding-001`     |
| voyage   | `voyage-4-large`           |
| mistral  | `mistral-embed`            |
| ollama   | `nomic-embed-text`         |
| local    | `embeddinggemma-300m-qat-Q8_0.gguf` |

## Auto-Selection Order

1. Local provider (if `modelPath` is valid)
2. OpenAI (if `apiKey` configured)
3. Gemini (if `apiKey` configured)
4. Voyage (if `apiKey` configured)
5. Mistral (if `apiKey` configured)

Ollama is NOT auto-selected.

## Retry Contract

On retryable errors (429, 5xx, rate limit, quota, transient):
- Max 3 attempts
- Base delay: 500ms
- Exponential backoff, max 8000ms
- Jitter: up to 20%

## Batching Contract

Chunks grouped by estimated byte size:
- Max batch size: 8000 bytes
- Text: UTF-8 byte count
- Structured input: serialized size
