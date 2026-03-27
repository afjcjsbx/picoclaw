// Package embedding provides embedding vector generation for the memory
// indexing system. It defines the [Provider] interface and ships implementations
// for OpenAI, Gemini, Voyage, Mistral, and Ollama.
//
// # Provider Interface
//
// All providers implement [Provider] with methods for single-query and batch
// embedding. Use [AutoSelect] to automatically choose the first available
// provider based on configured API keys.
//
// # Retry and Batching
//
// [WithRetry] wraps embedding calls with exponential backoff (500ms base,
// 8s max, 20% jitter, 3 attempts) for transient errors (429, 5xx).
//
// [BatchTexts] groups texts into batches where each batch does not exceed
// 8000 bytes (estimated by UTF-8 byte length).
//
// # Normalization
//
// [NormalizeL2] sanitizes non-finite values and L2-normalizes embedding
// vectors. Recommended for local model outputs.
package embedding
