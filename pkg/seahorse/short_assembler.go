package seahorse

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// escapeXML escapes special characters for safe inclusion in XML content.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

// resolvedItem is a context item resolved to its full content with token count.
type resolvedItem struct {
	ordinal    int
	itemType   string // "message" or "summary"
	message    *Message
	summary    *Summary
	tokenCount int
}

// Assemble builds budget-constrained context from summaries + messages.
//
// Algorithm:
//  1. Fetch context_items, resolve to full content
//  2. Split into evictable prefix + protected fresh tail
//  3. If evictable fits in remaining budget → include all
//  4. Else walk evictable from newest to oldest, keep while fits
func (a *Assembler) Assemble(
	ctx context.Context,
	convID int64,
	input AssembleInput,
) (*AssembleResult, error) {
	items, err := a.store.GetContextItems(ctx, convID)
	if err != nil {
		return nil, fmt.Errorf("get context items: %w", err)
	}
	if len(items) == 0 {
		return &AssembleResult{}, nil
	}

	// Resolve all items
	resolved := make([]resolvedItem, len(items))
	for i, item := range items {
		r, err := a.resolveItem(ctx, item)
		if err != nil {
			return nil, err
		}
		resolved[i] = r
	}

	// Split into evictable prefix and protected fresh tail
	tailStart := len(resolved) - FreshTailCount
	if tailStart < 0 {
		tailStart = 0
	}
	evictable := resolved[:tailStart]
	freshTail := resolved[tailStart:]
	allTokens := 0
	for _, r := range resolved {
		allTokens += r.tokenCount
	}

	// Calculate fresh tail tokens. The tail has both an item-count limit and a
	// token limit: 32 small tool results are safe, 32 giant tool results are
	// not. Reserve most of the budget for the tail but keep the latest user turn
	// whole when it alone is larger than the normal tail share.
	freshTailTokens := 0
	for _, r := range freshTail {
		freshTailTokens += r.tokenCount
	}

	hardCap := input.Budget
	if hardCap < 1 {
		hardCap = 1
	}
	truncatedTail := false
	for i := range freshTail {
		if freshTail[i].tokenCount > hardCap {
			freshTailTokens -= freshTail[i].tokenCount
			freshTail[i] = truncateResolvedItem(freshTail[i], hardCap)
			freshTailTokens += freshTail[i].tokenCount
			truncatedTail = true
		}
	}

	tailBudget := input.Budget
	if tailBudget < 1 {
		tailBudget = 1
	}
	if idx := lastUserMessageIndex(freshTail); idx >= 0 {
		if latestTurnTokens := resolvedItemsTokenCount(freshTail[idx:]); latestTurnTokens > tailBudget {
			// Do not cut the active instruction merely to satisfy the tail
			// share; the hard overall budget still applies below.
			tailBudget = input.Budget
		}
	}

	if freshTailTokens > tailBudget {
		originalTailCount := len(freshTail)
		originalFreshTailTokens := freshTailTokens
		var preservedActiveTurn bool
		freshTail, freshTailTokens, preservedActiveTurn = trimFreshTailToSafeBudget(
			freshTail,
			tailBudget,
		)
		logFields := map[string]any{
			"budget":                input.Budget,
			"tail_budget":           tailBudget,
			"fresh_tail_tokens":     freshTailTokens,
			"fresh_tail_count":      len(freshTail),
			"trimmed_fresh_items":   originalTailCount - len(freshTail),
			"original_fresh_tokens": originalFreshTailTokens,
			"preserved_active_turn": preservedActiveTurn,
		}
		if preservedActiveTurn {
			logger.WarnCF("seahorse", "assemble: preserving active turn over budget", logFields)
		} else {
			logger.InfoCF("seahorse", "assemble: trimmed fresh tail to safe boundary", logFields)
		}
	}
	if freshTailTokens > input.Budget {
		// The active turn cannot be dropped. Shrink its largest non-user
		// items first, then fall back to an explicit error if even the
		// instruction itself cannot fit.
		freshTail, freshTailTokens = shrinkActiveTurnToBudget(
			freshTail,
			input.Budget,
			freshTailTokens,
		)
	}
	if freshTailTokens > input.Budget {
		return nil, fmt.Errorf("%w: fresh tail uses %d tokens, budget %d",
			ErrContextBudgetExceeded, freshTailTokens, input.Budget)
	}
	if truncatedTail {
		logger.InfoCF("seahorse", "assemble: truncated oversized tail item", map[string]any{
			"budget": input.Budget,
		})
	}
	remainingBudget := input.Budget - freshTailTokens
	if remainingBudget < 0 {
		remainingBudget = 0
	}

	var selected []resolvedItem
	evictableTokens := 0
	for _, r := range evictable {
		evictableTokens += r.tokenCount
	}

	gapCount := 0
	if evictableTokens <= remainingBudget {
		// All evictable fit
		selected = append(selected, evictable...)
	} else {
		// Walk from newest to oldest. A single oversized summary or tool result
		// must not hide every older item; skip it, record the gap, and continue.
		var kept []resolvedItem
		accum := 0
		for i := len(evictable) - 1; i >= 0; i-- {
			if accum+evictable[i].tokenCount <= remainingBudget {
				kept = append(kept, evictable[i])
				accum += evictable[i].tokenCount
			} else {
				gapCount++
			}
		}
		// Reverse to restore chronological order
		for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
			kept[i], kept[j] = kept[j], kept[i]
		}
		selected = append(selected, kept...)
	}

	// Combine: selected evictable + fresh tail
	final := append(selected, freshTail...)

	// Build result
	var messages []Message
	var summaries []Summary
	var sourceIDs []string
	totalTokens := 0
	maxDepth := 0
	condensedCount := 0
	for _, r := range final {
		totalTokens += r.tokenCount
		if r.itemType == "message" && r.message != nil {
			messages = append(messages, *r.message)
			sourceIDs = append(sourceIDs, fmt.Sprintf("msg:%d", r.message.ID))
		} else if r.itemType == "summary" && r.summary != nil {
			summaries = append(summaries, *r.summary)
			if r.summary.Depth > maxDepth {
				maxDepth = r.summary.Depth
			}
			if r.summary.Kind == SummaryKindCondensed {
				condensedCount++
			}
		}
	}

	// Build depth-aware system prompt addition
	systemPromptAddition := ""
	if len(summaries) > 0 {
		if maxDepth >= 2 || condensedCount >= 2 {
			systemPromptAddition = "Your context has been heavily compressed through multi-level summarization.\n" +
				"- Do NOT assert specific facts (commands, SHAs, paths, timestamps) from summaries without expanding.\n" +
				"- When uncertain, use expand to recover original detail before making claims.\n" +
				"- Tool escalation: grep \xe2\x86\x92 describe \xe2\x86\x92 expand"
		} else {
			systemPromptAddition = "Some earlier messages have been summarized. Use expand tools to recover details if needed."
		}
	}

	// Build Summary field: all XML summaries + system prompt addition
	var summaryParts []string
	for _, sum := range summaries {
		if sum.Content == "" {
			continue
		}
		// Load parent IDs for XML formatting
		parentSummaries, err := a.store.GetSummaryParents(ctx, sum.SummaryID)
		if err != nil {
			logger.WarnCF("seahorse", "assemble: get summary parents", map[string]any{
				"summary_id": sum.SummaryID,
				"error":      err.Error(),
			})
		}
		var parentIDs []string
		for _, ps := range parentSummaries {
			parentIDs = append(parentIDs, ps.SummaryID)
		}
		summaryParts = append(summaryParts, FormatSummaryXML(&sum, parentIDs))
	}
	summary := strings.Join(summaryParts, "\n\n")
	if systemPromptAddition != "" {
		if summary != "" {
			summary += "\n\n"
		}
		summary += systemPromptAddition
	}
	summaryTokens := EstimateMessageTokens(Message{Content: summary})

	return &AssembleResult{
		Messages:        messages,
		Summary:         summary,
		FreshTailTokens: freshTailTokens,
		SummaryTokens:   summaryTokens,
		TokensUsed:      totalTokens,
		TokensEvicted:   allTokens - totalTokens,
		GapCount:        gapCount,
	}, nil
}

func trimFreshTailToSafeBudget(tail []resolvedItem, budget int) ([]resolvedItem, int, bool) {
	tailTokens := resolvedItemsTokenCount(tail)
	if tailTokens <= budget {
		return tail, tailTokens, false
	}

	latestTurnStart := lastUserMessageIndex(tail)
	if latestTurnStart >= 0 {
		latestTurnTokens := resolvedItemsTokenCount(tail[latestTurnStart:])
		if latestTurnTokens > budget {
			return tail[latestTurnStart:], latestTurnTokens, true
		}
	}

	start := 0
	for tailTokens > budget && start < len(tail) {
		tailTokens -= tail[start].tokenCount
		start++
	}
	for start < len(tail) && !isProviderSafeHistoryStart(tail[start:]) {
		tailTokens -= tail[start].tokenCount
		start++
	}

	return tail[start:], tailTokens, false
}

func resolvedItemsTokenCount(items []resolvedItem) int {
	total := 0
	for _, item := range items {
		total += item.tokenCount
	}
	return total
}

// shrinkActiveTurnToBudget truncates the largest non-user items in the active
// turn until the turn fits. The user instruction is preserved whenever
// possible; if it alone exceeds the budget it is truncated last.
func shrinkActiveTurnToBudget(tail []resolvedItem, budget, current int) ([]resolvedItem, int) {
	if current <= budget {
		return tail, current
	}
	start := lastUserMessageIndex(tail)
	if start < 0 {
		start = 0
	}
	for current > budget {
		largest := -1
		for i := start; i < len(tail); i++ {
			if tail[i].message != nil && tail[i].message.Role == "user" {
				continue
			}
			if largest == -1 || tail[i].tokenCount > tail[largest].tokenCount {
				largest = i
			}
		}
		if largest == -1 {
			// Only user messages remain.
			for i := start; i < len(tail); i++ {
				if largest == -1 || tail[i].tokenCount > tail[largest].tokenCount {
					largest = i
				}
			}
		}
		if largest == -1 || tail[largest].tokenCount <= 1 {
			break
		}
		excess := current - budget
		target := tail[largest].tokenCount - excess
		if target < 1 {
			target = 1
		}
		before := tail[largest].tokenCount
		tail[largest] = truncateResolvedItem(tail[largest], target)
		if tail[largest].tokenCount >= before {
			// Fixed overhead (role framing, media) can dominate the estimate;
			// further truncation would not reduce it.
			break
		}
		current += tail[largest].tokenCount - before
	}
	return tail, current
}

func lastUserMessageIndex(items []resolvedItem) int {
	for i := len(items) - 1; i >= 0; i-- {
		if items[i].itemType != "message" || items[i].message == nil {
			continue
		}
		if items[i].message.Role == "user" {
			return i
		}
	}
	return -1
}

func isProviderSafeHistoryStart(items []resolvedItem) bool {
	for _, item := range items {
		if item.itemType != "message" || item.message == nil {
			continue
		}
		if item.message.Role == "tool" {
			return false
		}
		if item.message.Role == "assistant" && messageHasToolUse(item.message) {
			return false
		}
		return true
	}
	return true
}

func messageHasToolUse(msg *Message) bool {
	if msg == nil {
		return false
	}
	for _, part := range msg.Parts {
		if part.Type == "tool_use" {
			return true
		}
	}
	return false
}

// truncatedContentMarker tells the model that an item was shortened.
const truncatedContentMarker = "\n[truncated]"

// truncatedToolArguments is a valid JSON object used when a tool call's
// arguments cannot fit the per-item budget. Raw truncation would leave invalid
// JSON, which providers reject when replaying history.
const truncatedToolArguments = `{"_truncated":true}`

// truncateTextToTokens bounds text using the shared estimator's conservative
// 2-characters-per-token rate. It leaves room for the marker and message
// overhead so the estimate stays near the target instead of only being clamped
// afterwards.
func truncateTextToTokens(s string, maxTokens int) string {
	if maxTokens < 1 {
		maxTokens = 1
	}
	// Reserve a small amount for JSON/role overhead and the marker.
	usableTokens := maxTokens - 16
	if usableTokens < 1 {
		usableTokens = 1
	}
	limit := usableTokens * 2
	if len(s) <= limit {
		return s
	}
	return truncateUTF8Bytes(s, limit) + truncatedContentMarker
}

// truncateResolvedItem enforces the per-item hard cap. It keeps the structure
// (tool calls, tool-result IDs) but shortens every field that carries token
// weight: content, reasoning, text/tool-result parts, summary text, and tool
// arguments. The reported token count is the actual estimate of the shortened
// item, not a clamped value, so the assembler cannot silently overshoot the
// budget. The canonical rows in SQLite are not modified.
func truncateResolvedItem(item resolvedItem, maxTokens int) resolvedItem {
	if maxTokens < 1 {
		maxTokens = 1
	}
	if item.message != nil {
		msg := *item.message
		// Split the cap across every token-bearing field. Using the full cap
		// for content and again for reasoning can leave the item at twice the
		// requested size even after truncation.
		fields := messageTokenFieldCount(msg)
		fieldBudget := maxTokens
		if fields > 1 {
			fieldBudget = max(1, (maxTokens-16)/fields)
		}
		limit := fieldBudget * 2
		msg.Content = truncateTextToTokens(msg.Content, fieldBudget)
		msg.ReasoningContent = truncateTextToTokens(msg.ReasoningContent, fieldBudget)
		parts := make([]MessagePart, len(msg.Parts))
		copy(parts, msg.Parts)
		for i := range parts {
			switch parts[i].Type {
			case "text", "tool_result":
				parts[i].Text = truncateTextToTokens(parts[i].Text, fieldBudget)
			case "tool_use":
				if len(parts[i].Arguments) > limit {
					parts[i].Arguments = truncatedToolArguments
				}
			}
		}
		msg.Parts = parts
		msg.TokenCount = EstimateMessageTokens(msg)
		item.message = &msg
		item.tokenCount = msg.TokenCount
	}
	if item.summary != nil {
		sum := *item.summary
		sum.Content = truncateTextToTokens(sum.Content, maxTokens)
		sum.TokenCount = EstimateMessageTokens(Message{Content: sum.Content})
		item.summary = &sum
		item.tokenCount = sum.TokenCount
	}
	return item
}

func messageTokenFieldCount(msg Message) int {
	fields := 0
	if msg.Content != "" {
		fields++
	}
	if msg.ReasoningContent != "" {
		fields++
	}
	for _, part := range msg.Parts {
		switch part.Type {
		case "text", "tool_result":
			if part.Text != "" {
				fields++
			}
		case "tool_use":
			if part.Arguments != "" {
				fields++
			}
		}
	}
	if fields == 0 {
		fields = 1
	}
	return fields
}

// resolveItem loads the full message or summary for a context item.
func (a *Assembler) resolveItem(ctx context.Context, item ContextItem) (resolvedItem, error) {
	if item.ItemType == "message" {
		msg, err := a.store.GetMessageByID(ctx, item.MessageID)
		if err != nil {
			return resolvedItem{}, err
		}
		tokens := item.TokenCount
		if tokens == 0 {
			tokens = msg.TokenCount
		}
		return resolvedItem{
			ordinal:    item.Ordinal,
			itemType:   "message",
			message:    msg,
			tokenCount: tokens,
		}, nil
	}

	if item.ItemType == "summary" {
		sum, err := a.store.GetSummary(ctx, item.SummaryID)
		if err != nil {
			return resolvedItem{}, err
		}
		tokens := item.TokenCount
		if tokens == 0 {
			tokens = sum.TokenCount
		}
		return resolvedItem{
			ordinal:    item.Ordinal,
			itemType:   "summary",
			summary:    sum,
			tokenCount: tokens,
		}, nil
	}

	return resolvedItem{
		ordinal:    item.Ordinal,
		itemType:   item.ItemType,
		tokenCount: item.TokenCount,
	}, nil
}

// FormatSummaryXML formats a summary as XML for LLM context.
// This is exported so context managers can format summaries consistently.
func FormatSummaryXML(s *Summary, parentIDs []string) string {
	// Build time attributes if available
	var attrs string
	if s.EarliestAt != nil {
		attrs += fmt.Sprintf(` earliest_at="%s"`, s.EarliestAt.Format(time.RFC3339))
	}
	if s.LatestAt != nil {
		attrs += fmt.Sprintf(` latest_at="%s"`, s.LatestAt.Format(time.RFC3339))
	}

	var parentsSection string
	if s.Kind == SummaryKindCondensed && len(parentIDs) > 0 {
		parents := "<parents>\n"
		for _, pid := range parentIDs {
			parents += fmt.Sprintf("    <summary_ref id=\"%s\" />\n", pid)
		}
		parents += "  </parents>\n"
		parentsSection = parents
	}
	return fmt.Sprintf(
		"<summary id=\"%s\" kind=\"%s\" depth=\"%d\" descendant_count=\"%d\"%s>\n  <content>\n    %s\n  </content>\n%s</summary>",
		s.SummaryID,
		string(s.Kind),
		s.Depth,
		s.DescendantCount,
		attrs,
		escapeXML(s.Content),
		parentsSection,
	)
}
