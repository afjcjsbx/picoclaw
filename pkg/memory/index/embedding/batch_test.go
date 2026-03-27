package embedding

import (
	"strings"
	"testing"
)

func TestBatchTexts_SingleBatch(t *testing.T) {
	texts := []string{"hello", "world"}
	batches := BatchTexts(texts)
	if len(batches) != 1 {
		t.Errorf("expected 1 batch for small texts, got %d", len(batches))
	}
	if len(batches[0]) != 2 {
		t.Errorf("expected 2 texts in batch, got %d", len(batches[0]))
	}
}

func TestBatchTexts_MultipleBatches(t *testing.T) {
	// Create texts that exceed MaxBatchBytes
	longText := strings.Repeat("a", 5000)
	texts := []string{longText, longText, longText}
	batches := BatchTexts(texts)
	if len(batches) < 2 {
		t.Errorf("expected multiple batches for large texts, got %d", len(batches))
	}
}

func TestBatchTexts_Empty(t *testing.T) {
	batches := BatchTexts(nil)
	if len(batches) != 0 {
		t.Errorf("expected 0 batches for nil, got %d", len(batches))
	}
}

func TestBatchTexts_SingleLargeText(t *testing.T) {
	// Single text larger than MaxBatchBytes — should be its own batch
	hugeText := strings.Repeat("x", MaxBatchBytes+1000)
	batches := BatchTexts([]string{hugeText})
	if len(batches) != 1 {
		t.Errorf("expected 1 batch for single huge text, got %d", len(batches))
	}
}

func TestBatchTextsWithIndices(t *testing.T) {
	texts := []string{"a", "b", "c"}
	batches, indices := BatchTextsWithIndices(texts)
	if len(batches) != len(indices) {
		t.Fatalf("batches/indices length mismatch: %d vs %d", len(batches), len(indices))
	}
	// All indices should be present
	seen := make(map[int]bool)
	for _, idxBatch := range indices {
		for _, idx := range idxBatch {
			seen[idx] = true
		}
	}
	for i := range texts {
		if !seen[i] {
			t.Errorf("index %d not found in indices", i)
		}
	}
}
