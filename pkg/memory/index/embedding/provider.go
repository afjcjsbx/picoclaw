package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
)

// Provider generates embedding vectors from text.
type Provider interface {
	Name() string
	Model() string
	MaxInput() int
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

// NormalizeL2 sanitizes non-finite values to 0, then L2-normalizes the vector.
// Returns the normalized vector (modifies in place).
func NormalizeL2(vec []float32) []float32 {
	// Sanitize non-finite values.
	for i, v := range vec {
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			vec[i] = 0
		}
	}

	// Compute L2 norm.
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	norm := math.Sqrt(sum)

	if norm == 0 {
		return vec
	}

	// Normalize in place.
	for i, v := range vec {
		vec[i] = float32(float64(v) / norm)
	}
	return vec
}

// ParseEmbeddingJSON parses a JSON array of float32 values from a string.
func ParseEmbeddingJSON(data string) ([]float32, error) {
	var vec []float32
	if err := json.Unmarshal([]byte(data), &vec); err != nil {
		return nil, fmt.Errorf("parse embedding JSON: %w", err)
	}
	return vec, nil
}

// SerializeEmbedding serializes a float32 slice to a JSON string.
func SerializeEmbedding(vec []float32) string {
	b, err := json.Marshal(vec)
	if err != nil {
		return "[]"
	}
	return string(b)
}
