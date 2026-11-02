package askuser

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// DefaultTimeout is used when the adapter is configured without a timeout.
const DefaultTimeout = 5 * time.Minute

// SendFunc delivers one ask_user prompt to the user.
type SendFunc func(ctx context.Context, channel, chatID, content string) error

// Broker routes the next inbound message of a conversation to the ask_user
// interaction currently waiting on that conversation. The agent loop owns the
// inbound side and calls Deliver; the adapter owns the waiting side.
type Broker struct {
	mu      sync.Mutex
	waiters map[string]*waiter
}

type waiter struct {
	mu      sync.Mutex
	closed  bool
	replies chan string
}

func newWaiter() *waiter {
	return &waiter{replies: make(chan string, 1)}
}

// NewBroker creates an empty clarification broker.
func NewBroker() *Broker {
	return &Broker{waiters: make(map[string]*waiter)}
}

// BrokerKey identifies a conversation. channel+chatID is the routing key used
// by inbound messages; the session key is only a fallback for callers that do
// not provide chat context.
func BrokerKey(channel, chatID, sessionKey string) string {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	if channel != "" && chatID != "" {
		return channel + "\x00" + chatID
	}
	return strings.TrimSpace(sessionKey)
}

// Deliver hands an inbound user message to a waiting interaction. It returns
// false when no interaction is waiting for this conversation, so the caller
// can process the message normally.
func (b *Broker) Deliver(key, content string) bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	waiter := b.waiters[key]
	b.mu.Unlock()
	if waiter == nil {
		return false
	}
	return waiter.deliver(content)
}

func (b *Broker) register(key string) (*waiter, error) {
	if key == "" {
		return nil, errors.New("ask_user requires an active conversation")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.waiters[key]; exists {
		return nil, errors.New("another ask_user request is already pending for this conversation")
	}
	waiter := newWaiter()
	b.waiters[key] = waiter
	return waiter, nil
}

func (b *Broker) unregister(key string, candidate *waiter) {
	b.mu.Lock()
	if current, exists := b.waiters[key]; exists && current == candidate {
		delete(b.waiters, key)
	}
	b.mu.Unlock()
	candidate.close()
}

func (w *waiter) deliver(content string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return false
	}
	select {
	case w.replies <- content:
		return true
	default:
		return false
	}
}

func (w *waiter) close() {
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
}

func (w *waiter) next(ctx context.Context, timeout time.Duration) (string, bool, error) {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case reply := <-w.replies:
		return reply, true, nil
	case <-timer.C:
		return "", false, nil
	case <-ctx.Done():
		return "", false, ctx.Err()
	}
}

func (w *waiter) drain() {
	for {
		select {
		case <-w.replies:
		default:
			return
		}
	}
}

// ChatAdapter renders questions as chat messages and waits for textual replies
// through the broker. It reproduces the semantics of a numbered-option UI on
// platforms without cards or buttons.
type ChatAdapter struct {
	broker  *Broker
	send    SendFunc
	timeout time.Duration
}

// NewChatAdapter builds a text/chat adapter. A non-positive timeout falls back
// to DefaultTimeout.
func NewChatAdapter(broker *Broker, send SendFunc, timeout time.Duration) *ChatAdapter {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &ChatAdapter{broker: broker, send: send, timeout: timeout}
}

// Collect is the adapter entry point. It keeps one waiter registered for the
// whole batch, so the agent loop can route every inbound reply here.
func (a *ChatAdapter) Collect(ctx context.Context, questions []Question) (AdapterResult, error) {
	if a == nil || a.broker == nil {
		return AdapterResult{}, errors.New("ask_user adapter is not configured")
	}
	if a.send == nil {
		return AdapterResult{}, errors.New("ask_user send callback is not configured")
	}
	if !IsInteractiveSession(ctx) {
		return AdapterResult{
			Outcome: OutcomeUndelivered,
			Notice:  "ask_user requires an interactive chat turn; this invocation cannot receive a reply",
		}, nil
	}

	channel := toolshared.ToolChannel(ctx)
	chatID := toolshared.ToolChatID(ctx)
	if channel == "" || chatID == "" {
		return AdapterResult{
			Outcome: OutcomeUndelivered,
			Notice:  "ask_user requires an active chat conversation to deliver questions",
		}, nil
	}

	key := BrokerKey(channel, chatID, toolshared.ToolSessionKey(ctx))
	waiting, err := a.broker.register(key)
	if err != nil {
		return AdapterResult{}, err
	}
	defer a.broker.unregister(key, waiting)

	answers := make(map[string]any, len(questions))
	var deadline time.Time

	for index, question := range questions {
		if !deadline.IsZero() && time.Until(deadline) <= 0 {
			return timedOut(answers, a.timeout), nil
		}

		waiting.drain()
		if err := a.send(ctx, channel, chatID, formatPrompt(index, len(questions), question)); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return AdapterResult{}, ctxErr
			}
			return AdapterResult{
				Answers: answers,
				Outcome: OutcomeUndelivered,
				Notice:  "failed to deliver ask_user request: " + err.Error(),
			}, nil
		}
		if deadline.IsZero() {
			deadline = time.Now().Add(a.timeout)
		}

		reply, received, waitErr := waiting.next(ctx, time.Until(deadline))
		if waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) {
				return timedOut(answers, a.timeout), nil
			}
			return AdapterResult{}, waitErr
		}
		if !received {
			return timedOut(answers, a.timeout), nil
		}

		switch strings.ToLower(strings.TrimSpace(reply)) {
		case "cancel", "/cancel":
			return AdapterResult{Answers: answers, Outcome: OutcomeCancelled}, nil
		case "skip", "/skip":
			answers[question.ID] = nil
			continue
		}

		if question.MultiSelect {
			answers[question.ID] = parseMultiAnswer(reply, question)
		} else {
			answers[question.ID] = parseSingleAnswer(reply, question)
		}
	}

	return AdapterResult{Answers: answers, Outcome: OutcomeSubmitted}, nil
}

func timedOut(answers map[string]any, timeout time.Duration) AdapterResult {
	return AdapterResult{
		Answers: answers,
		Outcome: OutcomeTimedOut,
		Notice:  timeoutNotice(timeout),
	}
}

func timeoutNotice(timeout time.Duration) string {
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	timeout = timeout.Round(time.Second)
	minutes := int(timeout / time.Minute)
	seconds := int((timeout % time.Minute) / time.Second)
	var amount string
	switch {
	case minutes == 0:
		amount = fmt.Sprintf("%ds", seconds)
	case seconds == 0:
		amount = fmt.Sprintf("%dm", minutes)
	default:
		amount = fmt.Sprintf("%dm%ds", minutes, seconds)
	}
	return fmt.Sprintf("[user did not respond within %s]", amount)
}

func formatPrompt(index, total int, question Question) string {
	var builder strings.Builder
	if total > 1 {
		fmt.Fprintf(&builder, "Question %d/%d\n", index+1, total)
	}
	builder.WriteString(question.Text)

	if len(question.Choices) == 0 {
		builder.WriteString("\n\nType your answer.")
	} else {
		builder.WriteString("\n\n")
		for choiceIndex, choice := range question.DisplayChoices() {
			fmt.Fprintf(&builder, "%d. %s\n", choiceIndex+1, choice)
		}
		if question.MultiSelect {
			builder.WriteString(
				"\nReply with one or more numbers separated by commas, or type your own answer (Other).",
			)
		} else {
			builder.WriteString("\nReply with a number, or type your own answer (Other).")
		}
	}
	builder.WriteString("\nType `skip` to skip this question or `cancel` to cancel.")
	return builder.String()
}

func parseSingleAnswer(reply string, question Question) any {
	reply = strings.TrimSpace(reply)
	if index, ok := validChoiceIndex(reply, question); ok {
		return question.Choices[index-1]
	}
	for _, choice := range question.Choices {
		if strings.EqualFold(choice, reply) {
			return choice
		}
	}
	if custom, ok := otherAnswer(reply); ok {
		return custom
	}
	return reply
}

func parseMultiAnswer(reply string, question Question) []string {
	reply = strings.TrimSpace(reply)
	if reply == "" {
		return []string{}
	}
	if parsed, ok := parseJSONStringArray(reply); ok {
		answers := make([]string, 0, len(parsed))
		for _, item := range parsed {
			answers = appendSelectableAnswer(answers, item, question)
		}
		return answers
	}

	parts := splitMultiReply(reply)
	if len(parts) == 0 {
		return []string{}
	}

	// Free text containing commas is one custom answer unless at least one
	// token is a valid option number or an exact option label.
	hasSelectableToken := false
	for _, part := range parts {
		if _, ok := validChoiceIndex(part, question); ok {
			hasSelectableToken = true
			break
		}
		for _, choice := range question.Choices {
			if strings.EqualFold(choice, part) {
				hasSelectableToken = true
				break
			}
		}
		if hasSelectableToken {
			break
		}
	}
	if !hasSelectableToken {
		if custom, ok := otherAnswer(reply); ok {
			return []string{custom}
		}
		return []string{reply}
	}

	answers := make([]string, 0, len(parts))
	for _, part := range parts {
		answers = appendSelectableAnswer(answers, part, question)
	}
	return answers
}

func appendSelectableAnswer(answers []string, raw string, question Question) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return answers
	}
	if index, ok := validChoiceIndex(raw, question); ok {
		return append(answers, question.Choices[index-1])
	}
	for _, choice := range question.Choices {
		if strings.EqualFold(choice, raw) {
			return append(answers, choice)
		}
	}
	if custom, ok := otherAnswer(raw); ok {
		if custom == "" {
			return answers
		}
		return append(answers, custom)
	}
	return append(answers, raw)
}

func splitMultiReply(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if isNumberList(value) {
		return strings.Fields(value)
	}
	return strings.FieldsFunc(value, func(r rune) bool {
		switch r {
		case ',', ';', '\n', '\r':
			return true
		default:
			return false
		}
	})
}

func isNumberList(value string) bool {
	fields := strings.Fields(value)
	if len(fields) < 2 {
		return false
	}
	for _, field := range fields {
		if _, err := strconv.Atoi(field); err != nil {
			return false
		}
	}
	return true
}

func choiceIndex(value string) (int, bool) {
	index, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return index, true
}

func validChoiceIndex(value string, question Question) (int, bool) {
	index, ok := choiceIndex(value)
	if !ok || index < 1 || index > len(question.Choices) {
		return 0, false
	}
	return index, true
}

func otherAnswer(value string) (string, bool) {
	lower := strings.ToLower(value)
	const prefix = "other:"
	if strings.HasPrefix(lower, prefix) {
		return strings.TrimSpace(value[len(prefix):]), true
	}
	return "", false
}
