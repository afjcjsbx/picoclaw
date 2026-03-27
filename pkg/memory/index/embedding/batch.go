package embedding

// MaxBatchBytes is the maximum estimated byte size for a single batch of texts.
const MaxBatchBytes = 8000

// BatchTexts groups texts into batches where each batch's estimated byte size
// does not exceed MaxBatchBytes. Uses UTF-8 byte length for estimation.
func BatchTexts(texts []string) [][]string {
	if len(texts) == 0 {
		return nil
	}

	var batches [][]string
	var currentBatch []string
	currentSize := 0

	for _, text := range texts {
		textSize := len(text) // len() on a string returns UTF-8 byte length in Go

		// If a single text exceeds MaxBatchBytes, put it in its own batch.
		if textSize > MaxBatchBytes {
			if len(currentBatch) > 0 {
				batches = append(batches, currentBatch)
				currentBatch = nil
				currentSize = 0
			}
			batches = append(batches, []string{text})
			continue
		}

		// If adding this text would exceed the limit, finalize the current batch.
		if currentSize+textSize > MaxBatchBytes && len(currentBatch) > 0 {
			batches = append(batches, currentBatch)
			currentBatch = nil
			currentSize = 0
		}

		currentBatch = append(currentBatch, text)
		currentSize += textSize
	}

	// Don't forget the last batch.
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
	}

	return batches
}

// BatchTextsWithIndices groups texts into batches and returns both the batched
// texts and their original indices for reassembly.
func BatchTextsWithIndices(texts []string) (batches [][]string, indices [][]int) {
	if len(texts) == 0 {
		return nil, nil
	}

	var currentBatch []string
	var currentIndices []int
	currentSize := 0

	for i, text := range texts {
		textSize := len(text)

		// If a single text exceeds MaxBatchBytes, put it in its own batch.
		if textSize > MaxBatchBytes {
			if len(currentBatch) > 0 {
				batches = append(batches, currentBatch)
				indices = append(indices, currentIndices)
				currentBatch = nil
				currentIndices = nil
				currentSize = 0
			}
			batches = append(batches, []string{text})
			indices = append(indices, []int{i})
			continue
		}

		// If adding this text would exceed the limit, finalize the current batch.
		if currentSize+textSize > MaxBatchBytes && len(currentBatch) > 0 {
			batches = append(batches, currentBatch)
			indices = append(indices, currentIndices)
			currentBatch = nil
			currentIndices = nil
			currentSize = 0
		}

		currentBatch = append(currentBatch, text)
		currentIndices = append(currentIndices, i)
		currentSize += textSize
	}

	// Don't forget the last batch.
	if len(currentBatch) > 0 {
		batches = append(batches, currentBatch)
		indices = append(indices, currentIndices)
	}

	return batches, indices
}
