package index

import (
	"math"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"
)

// datePattern matches memory/YYYY-MM-DD.md filenames.
var datePattern = regexp.MustCompile(`^memory[/\\](\d{4}-\d{2}-\d{2})\.md$`)

// ParseFileDate extracts a date from a memory/YYYY-MM-DD.md filename.
// Returns zero time if the filename doesn't match the pattern.
func ParseFileDate(relPath string) (time.Time, bool) {
	matches := datePattern.FindStringSubmatch(relPath)
	if len(matches) < 2 {
		return time.Time{}, false
	}
	t, err := time.Parse("2006-01-02", matches[1])
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// IsEvergreenFile checks if a file is evergreen (not subject to temporal decay).
// Evergreen files: MEMORY.md, memory.md, memory/*.md (non-dated).
func IsEvergreenFile(relPath string) bool {
	base := filepath.Base(relPath)
	if base == "MEMORY.md" || base == "memory.md" {
		return true
	}
	if !IsMemoryPath(relPath) {
		return false
	}
	_, dated := ParseFileDate(relPath)
	return !dated
}

// stopWords is a set of common English stop words to filter during query expansion.
var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "is": true, "are": true,
	"was": true, "were": true, "be": true, "been": true, "being": true,
	"have": true, "has": true, "had": true, "do": true, "does": true,
	"did": true, "will": true, "would": true, "could": true, "should": true,
	"can": true, "may": true, "might": true, "must": true, "shall": true,
	"to": true, "of": true, "in": true, "for": true, "on": true,
	"at": true, "by": true, "with": true, "from": true, "as": true,
	"into": true, "about": true, "between": true, "through": true,
	"this": true, "that": true, "these": true, "those": true,
	"it": true, "its": true, "i": true, "me": true, "my": true,
	"we": true, "our": true, "you": true, "your": true, "he": true,
	"she": true, "they": true, "them": true, "their": true,
	"what": true, "which": true, "who": true, "whom": true, "how": true,
	"when": true, "where": true, "why": true,
	"and": true, "or": true, "but": true, "not": true, "no": true,
	"if": true, "then": true, "so": true, "than": true,
}

var tokenPattern = regexp.MustCompile(`[a-zA-Z0-9_]+`)

// BuildFTSQuery transforms a search query into an FTS5 match expression.
// Tokenizes on letters/numbers/underscore, removes stop words, deduplicates,
// quotes each token, and joins with AND.
func BuildFTSQuery(query string) string {
	tokens := tokenPattern.FindAllString(query, -1)
	seen := make(map[string]bool)
	var filtered []string
	for _, t := range tokens {
		lower := strings.ToLower(t)
		if stopWords[lower] || len(lower) < 2 {
			continue
		}
		if seen[lower] {
			continue
		}
		seen[lower] = true
		filtered = append(filtered, `"`+lower+`"`)
	}
	if len(filtered) == 0 {
		// Fallback: use all original tokens if everything was filtered
		for _, t := range tokens {
			lower := strings.ToLower(t)
			if !seen[lower] {
				seen[lower] = true
				filtered = append(filtered, `"`+lower+`"`)
			}
		}
	}
	return strings.Join(filtered, " AND ")
}

// NormalizeBM25Score converts an FTS5 BM25 rank to a [0,1] score.
// FTS5 rank is negative; more negative = more relevant.
func NormalizeBM25Score(rank float64) float64 {
	if rank < 0 {
		relevance := -rank
		return relevance / (1 + relevance)
	}
	return 1 / (1 + rank)
}

// TruncateSnippet truncates text to a maximum of maxUTF16 UTF-16 code units.
func TruncateSnippet(text string, maxUTF16 int) string {
	encoded := utf16.Encode([]rune(text))
	if len(encoded) <= maxUTF16 {
		return text
	}
	// Truncate at maxUTF16 and decode back
	truncated := encoded[:maxUTF16]
	return string(utf16.Decode(truncated))
}

// CosineSimilarity computes the cosine similarity between two vectors.
// Returns 0 if either vector is zero-length or has zero magnitude.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// FuseScores combines vector and text scores with given weights.
// Weights are normalized to sum to 1.0.
func FuseScores(vectorScore, textScore, vectorWeight, textWeight float64) float64 {
	total := vectorWeight + textWeight
	if total == 0 {
		return 0
	}
	return (vectorWeight*vectorScore + textWeight*textScore) / total
}

// ApplyTemporalDecay applies exponential decay to a score based on age.
// Formula: score * exp(-lambda * ageInDays) where lambda = ln(2) / halfLifeDays.
// Returns the original score if halfLifeDays <= 0.
func ApplyTemporalDecay(score float64, ageInDays float64, halfLifeDays int) float64 {
	if halfLifeDays <= 0 || ageInDays <= 0 {
		return score
	}
	lambda := math.Log(2) / float64(halfLifeDays)
	return score * math.Exp(-lambda*ageInDays)
}

// JaccardSimilarity computes the Jaccard similarity between two sets of tokens.
func JaccardSimilarity(a, b string) float64 {
	tokensA := tokenSet(a)
	tokensB := tokenSet(b)
	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 1.0
	}
	intersection := 0
	for t := range tokensA {
		if tokensB[t] {
			intersection++
		}
	}
	union := len(tokensA) + len(tokensB) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func tokenSet(text string) map[string]bool {
	tokens := tokenPattern.FindAllString(strings.ToLower(text), -1)
	set := make(map[string]bool, len(tokens))
	for _, t := range tokens {
		set[t] = true
	}
	return set
}

// ApplyMMR performs Maximal Marginal Relevance re-ranking.
// It selects results maximizing: lambda * relevance - (1-lambda) * maxSimilarityToSelected.
// Scores are normalized to [0,1] before MMR.
func ApplyMMR(results []SearchResult, lambda float64, maxResults int) []SearchResult {
	if len(results) <= 1 || maxResults <= 0 {
		return results
	}
	if maxResults > len(results) {
		maxResults = len(results)
	}

	// Normalize scores to [0,1]
	maxScore := 0.0
	for _, r := range results {
		if r.Score > maxScore {
			maxScore = r.Score
		}
	}
	if maxScore > 0 {
		for i := range results {
			results[i].Score /= maxScore
		}
	}

	selected := make([]SearchResult, 0, maxResults)
	remaining := make([]int, len(results))
	for i := range remaining {
		remaining[i] = i
	}

	for len(selected) < maxResults && len(remaining) > 0 {
		bestIdx := -1
		bestMMR := math.Inf(-1)

		for _, ri := range remaining {
			relevance := results[ri].Score
			maxSim := 0.0
			for _, sel := range selected {
				sim := JaccardSimilarity(results[ri].Snippet, sel.Snippet)
				if sim > maxSim {
					maxSim = sim
				}
			}
			mmr := lambda*relevance - (1-lambda)*maxSim
			if mmr > bestMMR {
				bestMMR = mmr
				bestIdx = ri
			}
		}

		if bestIdx < 0 {
			break
		}
		selected = append(selected, results[bestIdx])

		// Remove bestIdx from remaining
		newRemaining := make([]int, 0, len(remaining)-1)
		for _, ri := range remaining {
			if ri != bestIdx {
				newRemaining = append(newRemaining, ri)
			}
		}
		remaining = newRemaining
	}

	return selected
}
