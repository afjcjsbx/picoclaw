// Package usage aggregates provider-reported token usage and cost.
//
// PicoClaw persists the aggregate of every LLM call made for a turn on the
// terminal assistant message of that turn, so summing one message per turn
// gives exact totals without counting tool-call iterations twice.
package usage

import (
	"sort"
	"strconv"
	"strings"

	"github.com/sipeed/picoclaw/pkg/providers"
)

// UnknownModel labels usage recorded before model names were persisted.
const UnknownModel = "unknown"

// Totals aggregates provider-reported usage.
type Totals struct {
	// Requests is the number of LLM calls, including tool-call iterations.
	Requests int `json:"requests"`
	// Turns is the number of assistant messages that carried usage.
	Turns int `json:"turns"`

	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`

	// CacheReadTokens and CacheCreationTokens are already part of InputTokens.
	CacheReadTokens     int `json:"cache_read_tokens,omitempty"`
	CacheCreationTokens int `json:"cache_creation_tokens,omitempty"`

	// CostUSD sums the costs providers reported. Calls without a reported cost
	// contribute nothing.
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Empty reports whether no usage has been recorded.
func (t Totals) Empty() bool {
	return t == (Totals{})
}

// FreshInputTokens returns the prompt tokens that were neither read from nor
// written to the provider cache.
func (t Totals) FreshInputTokens() int {
	return max(t.InputTokens-t.CacheReadTokens-t.CacheCreationTokens, 0)
}

// InputBreakdown renders the input token count and, when the provider reported
// cache activity, the fresh/cache-read/cache-write split.
func (t Totals) InputBreakdown() string {
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

// Merge adds other to t.
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

// addCall adds the counters of one provider call. Turns is left alone because
// a turn can span several calls.
func (t *Totals) addCall(u providers.UsageInfo) {
	total := u.TotalTokens
	if total <= 0 {
		total = u.PromptTokens + u.CompletionTokens
	}
	requests := u.Requests
	if requests <= 0 {
		requests = 1
	}
	t.Merge(Totals{
		Requests:            requests,
		InputTokens:         u.PromptTokens,
		OutputTokens:        u.CompletionTokens,
		TotalTokens:         total,
		CacheReadTokens:     u.CacheReadTokens,
		CacheCreationTokens: u.CacheCreationTokens,
		CostUSD:             u.CostUSD,
	})
}

// ModelTotals breaks usage down by model.
type ModelTotals struct {
	Model string `json:"model"`
	Totals
}

// Summary is the usage of a set of turns, split by model.
type Summary struct {
	Totals
	// Models holds one row per model that served a call. SortModels orders them.
	Models []ModelTotals `json:"models,omitempty"`
}

// AddMessage accumulates the usage persisted on one assistant message and
// reports whether it carried any. A message whose turn used several models
// carries a per-model split, which is attributed row by row; otherwise the
// whole report belongs to model.
func (s *Summary) AddMessage(model string, u providers.UsageInfo) bool {
	if len(u.ByModel) == 0 {
		if !u.HasUsage() {
			return false
		}
		s.Turns++
		s.addModel(model, u)
		return true
	}

	counted := false
	for _, part := range u.ByModel {
		if !part.Usage.HasUsage() {
			continue
		}
		if !counted {
			s.Turns++
			counted = true
		}
		s.addModel(part.Model, part.Usage)
	}
	return counted
}

// addModel adds one call to the totals and to the row of its model.
func (s *Summary) addModel(model string, u providers.UsageInfo) {
	s.Totals.addCall(u)
	row := s.row(model)
	row.addCall(u)
	row.Turns++
}

// row returns the entry for model, creating it on first use.
func (s *Summary) row(model string) *ModelTotals {
	model = strings.TrimSpace(model)
	if model == "" {
		model = UnknownModel
	}
	for i := range s.Models {
		if s.Models[i].Model == model {
			return &s.Models[i]
		}
	}
	s.Models = append(s.Models, ModelTotals{Model: model})
	return &s.Models[len(s.Models)-1]
}

// Merge adds other, including its per-model rows, to s.
func (s *Summary) Merge(other Summary) {
	s.Totals.Merge(other.Totals)
	for _, m := range other.Models {
		s.row(m.Model).Merge(m.Totals)
	}
}

// SortModels orders models by total tokens so the largest consumers come
// first. Ties fall back to input tokens and then to the model name.
func (s *Summary) SortModels() {
	sort.SliceStable(s.Models, func(i, j int) bool {
		a, b := s.Models[i], s.Models[j]
		if a.TotalTokens != b.TotalTokens {
			return a.TotalTokens > b.TotalTokens
		}
		if a.InputTokens != b.InputTokens {
			return a.InputTokens > b.InputTokens
		}
		return a.Model < b.Model
	})
}

// FromMessages aggregates the usage persisted on messages. It is the fallback
// for session stores that do not keep running totals.
func FromMessages(messages []providers.Message) Summary {
	var summary Summary
	for i := range messages {
		if messages[i].Usage != nil {
			summary.AddMessage(messages[i].ModelName, *messages[i].Usage)
		}
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

// FormatCost renders a provider-reported cost in US dollars. A zero cost means
// the provider reported none. Sub-cent amounts keep four decimals so small
// sessions do not look free.
func FormatCost(cost float64) string {
	switch {
	case cost <= 0:
		return "not reported"
	case cost < 0.0001:
		return "<$0.0001"
	case cost < 1:
		return "$" + strconv.FormatFloat(cost, 'f', 4, 64)
	default:
		return "$" + strconv.FormatFloat(cost, 'f', 2, 64)
	}
}
