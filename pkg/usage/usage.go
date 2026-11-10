// Package usage aggregates provider-reported token usage and cost.
//
// PicoClaw stores the aggregate of all LLM calls in a turn on the terminal
// assistant message of that turn, so summing those messages yields session
// totals without double counting tool-call iterations.
package usage

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// UnknownModel labels messages persisted before model names were recorded.
const UnknownModel = "unknown"

// Totals aggregates provider-reported usage across one or more turns.
type Totals struct {
	// Requests is the number of LLM calls, including tool-call iterations.
	Requests int `json:"requests"`
	// Turns is the number of assistant messages that carried usage.
	Turns int `json:"turns"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	// CacheReadTokens and CacheCreationTokens are already included in
	// InputTokens; they break the prompt down by cache category.
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`

	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Empty reports whether no usage has been recorded.
func (t *Totals) Empty() bool {
	return t.Requests == 0 &&
		t.Turns == 0 &&
		t.InputTokens == 0 &&
		t.OutputTokens == 0 &&
		t.TotalTokens == 0 &&
		t.CacheReadTokens == 0 &&
		t.CacheCreationTokens == 0 &&
		t.CostUSD == 0
}

// FreshInputTokens returns the prompt tokens that were neither read from nor
// written to the provider cache.
func (t *Totals) FreshInputTokens() int {
	fresh := t.InputTokens - t.CacheReadTokens - t.CacheCreationTokens
	if fresh < 0 {
		return 0
	}
	return fresh
}

// InputBreakdown renders the input token count and, when the provider reported
// cache activity, the fresh/cache-read/cache-write split.
func (t *Totals) InputBreakdown() string {
	line := FormatTokens(t.InputTokens) + " tokens"
	if t.CacheReadTokens == 0 && t.CacheCreationTokens == 0 {
		return line
	}

	parts := []string{"fresh " + FormatTokens(t.FreshInputTokens())}
	if t.CacheReadTokens > 0 {
		parts = append(parts, "cache read "+FormatTokens(t.CacheReadTokens))
	}
	if t.CacheCreationTokens > 0 {
		parts = append(parts, "cache write "+FormatTokens(t.CacheCreationTokens))
	}
	return line + " (" + strings.Join(parts, " · ") + ")"
}

// Add accumulates one provider usage report and reports whether it contained
// any usage. Requests falls back to one call when the provider does not send
// the turn-level counter.
func (t *Totals) Add(u providers.UsageInfo) bool {
	if !hasUsage(u) {
		return false
	}
	t.add(u)
	return true
}

// Merge adds another totals snapshot. Both sides are already aggregated, so
// no per-message validation is needed.
func (t *Totals) Merge(other Totals) {
	t.Requests += other.Requests
	t.Turns += other.Turns
	t.InputTokens += other.InputTokens
	t.OutputTokens += other.OutputTokens
	t.TotalTokens += other.TotalTokens
	t.CacheReadTokens += other.CacheReadTokens
	t.CacheCreationTokens += other.CacheCreationTokens
	t.CostUSD += other.CostUSD
}

func (t *Totals) add(u providers.UsageInfo) {
	total := u.TotalTokens
	if total <= 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	requests := u.Requests
	if requests <= 0 {
		requests = 1
	}

	t.Requests += requests
	t.Turns++
	t.InputTokens += u.PromptTokens
	t.OutputTokens += u.CompletionTokens
	t.TotalTokens += total
	t.CacheReadTokens += u.CacheReadTokens
	t.CacheCreationTokens += u.CacheCreationTokens
	t.CostUSD += u.CostUSD
}

func hasUsage(u providers.UsageInfo) bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0 || u.CostUSD > 0
}

// ModelTotals breaks usage down by model name.
type ModelTotals struct {
	Model string `json:"model"`
	Totals
}

// Summary is the aggregated usage of a set of turns.
type Summary struct {
	Totals
	Models []ModelTotals `json:"models,omitempty"`
}

// Empty reports whether the summary contains no usage.
func (s *Summary) Empty() bool {
	return s.Totals.Empty()
}

// Add accumulates one usage report and attributes it to model. The model list
// is scanned linearly: sessions normally use one or two models, so a map would
// cost more than it saves.
func (s *Summary) Add(model string, u providers.UsageInfo) bool {
	if !hasUsage(u) {
		return false
	}

	s.Totals.add(u)

	model = strings.TrimSpace(model)
	if model == "" {
		model = UnknownModel
	}
	for i := range s.Models {
		if s.Models[i].Model == model {
			s.Models[i].Totals.add(u)
			return true
		}
	}
	var entry ModelTotals
	entry.Model = model
	entry.Totals.add(u)
	s.Models = append(s.Models, entry)
	return true
}

// SortModels orders models by total tokens so the largest consumers surface
// first; ties fall back to input tokens and then to the model name.
func (s *Summary) SortModels() {
	sort.SliceStable(s.Models, func(i, j int) bool {
		if s.Models[i].TotalTokens != s.Models[j].TotalTokens {
			return s.Models[i].TotalTokens > s.Models[j].TotalTokens
		}
		if s.Models[i].InputTokens != s.Models[j].InputTokens {
			return s.Models[i].InputTokens > s.Models[j].InputTokens
		}
		return s.Models[i].Model < s.Models[j].Model
	})
}

// FromMessages aggregates usage from assistant messages. Messages without
// provider-reported usage are ignored.
func FromMessages(messages []providers.Message) Summary {
	var summary Summary
	for i := range messages {
		if messages[i].Usage == nil {
			continue
		}
		summary.Add(messages[i].ModelName, *messages[i].Usage)
	}
	summary.SortModels()
	return summary
}

// FormatTokens renders a token count with thousands separators.
func FormatTokens(tokens int) string {
	sign := ""
	if tokens < 0 {
		sign = "-"
		tokens = -tokens
	}
	digits := strconv.Itoa(tokens)
	var out strings.Builder
	for i, digit := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			out.WriteByte(',')
		}
		out.WriteRune(digit)
	}
	return sign + out.String()
}

// FormatCostUSD renders a provider-reported cost in US dollars. Sub-cent
// amounts keep four decimals so small sessions do not look free.
func FormatCostUSD(cost float64) string {
	switch {
	case cost <= 0:
		return "$0"
	case cost < 0.0001:
		return "<$0.0001"
	case cost < 1:
		return "$" + strconv.FormatFloat(cost, 'f', 4, 64)
	default:
		return "$" + strconv.FormatFloat(cost, 'f', 2, 64)
	}
}
