package integrationtools

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
)

const defaultMultiSearchConcurrency = 3

type webSearchProvider struct {
	name       string
	provider   SearchProvider
	maxResults int
}

type multiSearchOutcome struct {
	result string
	err    error
}

// searchMulti races a randomized set of ready providers and returns the first
// non-empty result. Failed or empty providers are replaced by the next candidate
// until one succeeds or the pool is exhausted. Results are deliberately not
// merged or deduplicated.
func (t *WebSearchTool) searchMulti(
	ctx context.Context,
	query string,
	count int,
	rangeCode string,
) (string, error) {
	candidates := append([]webSearchProvider(nil), t.multiProviders...)
	if len(candidates) == 0 {
		return "", fmt.Errorf("search provider is not configured")
	}

	if t.shuffleProviders != nil {
		t.shuffleProviders(candidates)
	} else {
		rand.Shuffle(len(candidates), func(i, j int) {
			candidates[i], candidates[j] = candidates[j], candidates[i]
		})
	}

	concurrency := t.multiConcurrency
	if concurrency <= 0 {
		concurrency = defaultMultiSearchConcurrency
	}
	concurrency = min(concurrency, len(candidates))

	searchCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	outcomes := make(chan multiSearchOutcome, len(candidates))
	var wg sync.WaitGroup
	next := 0
	inFlight := 0
	launch := func() {
		candidate := candidates[next]
		next++
		inFlight++
		wg.Add(1)
		go func() {
			defer wg.Done()
			providerCount := count
			if candidate.maxResults > 0 {
				providerCount = min(providerCount, candidate.maxResults)
			}
			result, err := candidate.provider.Search(searchCtx, query, providerCount, rangeCode)
			if err != nil {
				err = fmt.Errorf("%s: %w", candidate.name, err)
			}
			outcomes <- multiSearchOutcome{result: result, err: err}
		}()
	}

	for next < len(candidates) && inFlight < concurrency {
		launch()
	}

	var lastErr error
	for inFlight > 0 {
		select {
		case outcome := <-outcomes:
			inFlight--
			if ctx.Err() != nil {
				cancel()
				wg.Wait()
				return "", ctx.Err()
			}
			if outcome.err == nil && !isEmptySearchResult(outcome.result) {
				cancel()
				wg.Wait()
				return outcome.result, nil
			}
			if outcome.err != nil {
				lastErr = outcome.err
			}
			if next < len(candidates) {
				launch()
			}
		case <-ctx.Done():
			cancel()
			wg.Wait()
			return "", ctx.Err()
		}
	}

	if lastErr != nil {
		return "", fmt.Errorf("all search providers failed, last error: %w", lastErr)
	}
	return fmt.Sprintf("No results for: %s", query), nil
}

func isEmptySearchResult(result string) bool {
	trimmed := strings.TrimSpace(result)
	if trimmed == "" {
		return true
	}
	return strings.HasPrefix(trimmed, "No results for:") ||
		strings.HasPrefix(trimmed, "No results found or extraction failed.") ||
		(strings.HasPrefix(trimmed, "Results for:") && !strings.Contains(trimmed, "\n"))
}
