package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// Session search is designed to stay cheap even with many long conversations:
//   - JSONL sessions are streamed line by line; only one message is decoded at
//     a time, so memory stays bounded regardless of session length.
//   - Matching stops per session once a small number of hits is collected and
//     globally once enough hits are gathered, so early hits short-circuit the
//     scan.
//   - A small worker pool parallelizes file reads without thrashing disk.
//   - A deadline bounds the worst case (query matches nothing) and reports
//     partial results instead of scanning forever.
const (
	defaultSessionSearchLimit   = 20
	maxSessionSearchLimit       = 50
	maxSessionSearchOffset      = 200
	maxSessionSearchPerSession  = 3
	defaultSessionSearchWorkers = 4
	minSessionSearchQueryLen    = 2
	sessionSearchSnippetRadius  = 80
	sessionSearchTimeout        = 3 * time.Second
	sessionSearchCancelCheck    = 64
)

type sessionSearchResultItem struct {
	SessionID    string `json:"session_id"`
	Title        string `json:"title"`
	Role         string `json:"role"`
	Snippet      string `json:"snippet"`
	MatchStart   int    `json:"match_start"`
	MatchEnd     int    `json:"match_end"`
	MessageIndex int    `json:"message_index"`
	Updated      string `json:"updated"`
}

type sessionSearchResponse struct {
	Query     string                    `json:"query"`
	Results   []sessionSearchResultItem `json:"results"`
	Limit     int                       `json:"limit"`
	Offset    int                       `json:"offset"`
	HasMore   bool                      `json:"has_more"`
	Truncated bool                      `json:"truncated"`
}

type sessionSearchHit struct {
	Role         string
	Snippet      string
	MatchStart   int
	MatchEnd     int
	MessageIndex int
}

type sessionSearchCandidate struct {
	id      string
	key     string
	path    string
	legacy  bool
	skip    int
	updated time.Time
}

// sessionSearchScanner accumulates matches and the title preview while a single
// session is streamed.
type sessionSearchScanner struct {
	query           string
	queryLowerRunes []rune
	maxArgs         int
	perSessionLimit int
	detailIndex     int
	preview         string
	hits            []sessionSearchHit
}

func newSessionSearchScanner(query string, maxArgs, perSessionLimit int) *sessionSearchScanner {
	return &sessionSearchScanner{
		query:           query,
		queryLowerRunes: []rune(strings.ToLower(query)),
		maxArgs:         maxArgs,
		perSessionLimit: perSessionLimit,
		hits:            make([]sessionSearchHit, 0, perSessionLimit),
	}
}

func (s *sessionSearchScanner) full() bool {
	return len(s.hits) >= s.perSessionLimit
}

// feed processes one persisted message, advancing the visible transcript index
// exactly like handleGetSession does so returned indexes map to chat anchors.
func (s *sessionSearchScanner) feed(msg providers.Message) {
	if s.preview == "" && msg.Role == "user" {
		s.preview = truncateRunes(strings.TrimSpace(msg.Content), maxSessionTitleRunes)
	}
	if s.full() {
		return
	}

	entries := detailSessionMessages([]providers.Message{msg}, s.maxArgs)
	for j, entry := range entries {
		if s.full() {
			break
		}
		if entry.Role != "user" && entry.Role != "assistant" {
			continue
		}
		if entry.Kind != "" && entry.Kind != "normal" {
			continue
		}
		content := entry.Content
		if content == "" {
			continue
		}
		idx := indexFold(content, s.query, s.queryLowerRunes)
		if idx < 0 {
			continue
		}
		snippet, matchStart, matchEnd := buildSessionSearchSnippet(
			content,
			idx,
			len(s.queryLowerRunes),
		)
		s.hits = append(s.hits, sessionSearchHit{
			Role:         entry.Role,
			Snippet:      snippet,
			MatchStart:   matchStart,
			MatchEnd:     matchEnd,
			MessageIndex: s.detailIndex + j,
		})
	}
	s.detailIndex += len(entries)
}

// handleSearchSessions searches visible user/assistant message content across
// all conversations.
//
//	GET /api/sessions/search?q=<query>&limit=<n>&offset=<n>
func (h *Handler) handleSearchSessions(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))

	limit := defaultSessionSearchLimit
	if rawLimit := r.URL.Query().Get("limit"); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed < 1 || parsed > maxSessionSearchLimit {
			http.Error(w, "invalid search limit", http.StatusBadRequest)
			return
		}
		limit = parsed
	}

	offset := 0
	if rawOffset := r.URL.Query().Get("offset"); rawOffset != "" {
		parsed, err := strconv.Atoi(rawOffset)
		if err != nil || parsed < 0 || parsed > maxSessionSearchOffset {
			http.Error(w, "invalid search offset", http.StatusBadRequest)
			return
		}
		offset = parsed
	}

	writeResponse := func(results []sessionSearchResultItem, hasMore, truncated bool) {
		if results == nil {
			results = []sessionSearchResultItem{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(sessionSearchResponse{
			Query:     query,
			Results:   results,
			Limit:     limit,
			Offset:    offset,
			HasMore:   hasMore,
			Truncated: truncated,
		})
	}

	if utf8.RuneCountInString(query) < minSessionSearchQueryLen {
		writeResponse(nil, false, false)
		return
	}

	dir, maxArgs, err := h.sessionRuntimeSettings()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}
	if _, err := os.ReadDir(dir); err != nil {
		writeResponse(nil, false, false)
		return
	}

	candidates := h.sessionSearchCandidates(dir)
	if len(candidates) == 0 {
		writeResponse(nil, false, false)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), sessionSearchTimeout)
	defer cancel()

	need := offset + limit
	stopAt := need + 1

	results := make([]sessionSearchResultItem, 0, stopAt)
	truncated := false

	workers := defaultSessionSearchWorkers
	if workers > len(candidates) {
		workers = len(candidates)
	}
	if workers < 1 {
		workers = 1
	}

	for start := 0; start < len(candidates); start += workers {
		if len(results) >= stopAt {
			break
		}
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				truncated = true
			} else {
				return
			}
			break
		}

		end := start + workers
		if end > len(candidates) {
			end = len(candidates)
		}
		chunk := candidates[start:end]

		type chunkResult struct {
			hits    []sessionSearchHit
			preview string
		}
		chunkResults := make([]chunkResult, len(chunk))

		var wg sync.WaitGroup
		for i := range chunk {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				candidate := chunk[i]
				scanner := newSessionSearchScanner(query, maxArgs, maxSessionSearchPerSession)

				var scanErr error
				if candidate.legacy {
					scanErr = h.scanSessionFileLegacy(ctx, candidate.path, scanner)
				} else {
					scanErr = h.scanSessionFileJSONL(ctx, candidate.path, candidate.skip, scanner)
				}
				if scanErr != nil {
					return
				}
				chunkResults[i] = chunkResult{hits: scanner.hits, preview: scanner.preview}
			}(i)
		}
		wg.Wait()

		for i := range chunk {
			chunkResult := chunkResults[i]
			if len(chunkResult.hits) == 0 {
				continue
			}
			candidate := chunk[i]
			title := chunkResult.preview
			if title == "" {
				title = "(empty)"
			}
			for _, hit := range chunkResult.hits {
				results = append(results, sessionSearchResultItem{
					SessionID:    candidate.id,
					Title:        title,
					Role:         hit.Role,
					Snippet:      hit.Snippet,
					MatchStart:   hit.MatchStart,
					MatchEnd:     hit.MatchEnd,
					MessageIndex: hit.MessageIndex,
					Updated:      candidate.updated.Format(time.RFC3339),
				})
				if len(results) >= stopAt {
					break
				}
			}
			if len(results) >= stopAt {
				break
			}
		}
	}

	hasMore := len(results) > need
	if len(results) > need {
		results = results[:need]
	}
	if offset < len(results) {
		results = results[offset:]
	} else {
		results = []sessionSearchResultItem{}
	}

	writeResponse(results, hasMore, truncated)
}

func (h *Handler) sessionSearchCandidates(dir string) []sessionSearchCandidate {
	candidates := make([]sessionSearchCandidate, 0)
	seen := make(map[string]struct{})

	if refs, err := h.findPicoJSONLSessions(dir); err == nil {
		for _, ref := range refs {
			base := filepath.Join(dir, sanitizeSessionKey(ref.Key))
			path := base + ".jsonl"
			meta, _ := h.readSessionMeta(base+".meta.json", ref.Key)

			updated := meta.UpdatedAt
			if updated.IsZero() {
				if info, statErr := os.Stat(path); statErr == nil {
					updated = info.ModTime()
				}
			}

			seen[ref.ID] = struct{}{}
			candidates = append(candidates, sessionSearchCandidate{
				id:      ref.ID,
				key:     ref.Key,
				path:    path,
				skip:    meta.Skip,
				updated: updated,
			})
		}
	}

	if legacyRefs, err := h.findLegacyPicoSessions(dir); err == nil {
		for _, ref := range legacyRefs {
			if _, exists := seen[ref.ID]; exists {
				continue
			}
			updated := time.Time{}
			if info, statErr := os.Stat(ref.Path); statErr == nil {
				updated = info.ModTime()
			}
			seen[ref.ID] = struct{}{}
			candidates = append(candidates, sessionSearchCandidate{
				id:      ref.ID,
				path:    ref.Path,
				legacy:  true,
				updated: updated,
			})
		}
	}

	// Most recent conversations first so early termination returns relevant hits.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].updated.Equal(candidates[j].updated) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].updated.After(candidates[j].updated)
	})

	return candidates
}

func (h *Handler) scanSessionFileJSONL(
	ctx context.Context,
	path string,
	skip int,
	scanner *sessionSearchScanner,
) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	lineScanner := bufio.NewScanner(file)
	lineScanner.Buffer(make([]byte, 0, 64*1024), maxSessionJSONLLineSize)

	seen := 0
	processed := 0
	for lineScanner.Scan() {
		line := lineScanner.Bytes()
		if len(line) == 0 {
			continue
		}
		seen++
		if seen <= skip {
			continue
		}
		processed++
		if processed%sessionSearchCancelCheck == 0 && ctx.Err() != nil {
			return ctx.Err()
		}

		var msg providers.Message
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		scanner.feed(msg)
		if scanner.full() {
			return nil
		}
	}
	return lineScanner.Err()
}

func (h *Handler) scanSessionFileLegacy(
	ctx context.Context,
	path string,
	scanner *sessionSearchScanner,
) error {
	sess, err := h.readLegacySession(path)
	if err != nil {
		return err
	}
	for _, msg := range sess.Messages {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		scanner.feed(msg)
		if scanner.full() {
			return nil
		}
	}
	return nil
}

// indexFold finds query inside content case-insensitively, returning the byte
// offset in content. It avoids allocating a lowercased copy of the content,
// which matters because conversations can be large.
func indexFold(content, query string, queryLowerRunes []rune) int {
	if len(queryLowerRunes) == 0 {
		return -1
	}
	if idx := strings.Index(content, query); idx >= 0 {
		return idx
	}

	for start := 0; start < len(content); {
		matched := true
		i := start
		for k := 0; k < len(queryLowerRunes); k++ {
			if i >= len(content) {
				matched = false
				break
			}
			r, size := utf8.DecodeRuneInString(content[i:])
			if unicode.ToLower(r) != queryLowerRunes[k] {
				matched = false
				break
			}
			i += size
		}
		if matched {
			return start
		}
		_, size := utf8.DecodeRuneInString(content[start:])
		start += size
	}
	return -1
}

// buildSessionSearchSnippet returns a context window around the match plus the
// match offsets (in runes) relative to the returned snippet.
func buildSessionSearchSnippet(
	content string,
	byteIdx int,
	queryRuneLen int,
) (string, int, int) {
	runes := []rune(content)
	total := len(runes)

	matchStart := utf8.RuneCountInString(content[:min(byteIdx, len(content))])
	if matchStart > total {
		matchStart = total
	}
	matchEnd := matchStart + queryRuneLen
	if matchEnd > total {
		matchEnd = total
	}

	start := matchStart - sessionSearchSnippetRadius
	if start < 0 {
		start = 0
	}
	end := matchEnd + sessionSearchSnippetRadius
	if end > total {
		end = total
	}

	window := runes[start:end]
	for i, r := range window {
		if r == '\n' || r == '\r' || r == '\t' {
			window[i] = ' '
		}
	}

	prefix := ""
	if start > 0 {
		prefix = "…"
	}
	suffix := ""
	if end < total {
		suffix = "…"
	}

	snippet := prefix + string(window) + suffix
	offset := utf8.RuneCountInString(prefix)
	snippetMatchStart := offset + (matchStart - start)
	snippetMatchEnd := snippetMatchStart + (matchEnd - matchStart)
	return snippet, snippetMatchStart, snippetMatchEnd
}
