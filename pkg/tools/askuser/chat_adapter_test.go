package askuser

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func chatContext() context.Context {
	ctx := toolshared.WithToolContext(context.Background(), "test", "chat-1")
	ctx = toolshared.WithToolSessionContext(ctx, "agent", "session-1", nil)
	ctx = toolshared.WithToolSenderContext(ctx, "sender-1")
	return WithInteractiveSession(ctx)
}

func testBrokerKey() string {
	return BrokerKey("test", "chat-1", "agent", "sender-1", "session-1")
}

type adapterRun struct {
	result AdapterResult
	err    error
}

func runAdapter(t *testing.T, adapter *ChatAdapter, questions []Question) (<-chan adapterRun, <-chan string) {
	t.Helper()
	prompts := make(chan string, 8)
	done := make(chan adapterRun, 1)
	adapter.send = func(_ context.Context, channel, chatID, content string) error {
		if channel != "test" || chatID != "chat-1" {
			t.Errorf("send target = %s:%s", channel, chatID)
		}
		prompts <- content
		return nil
	}
	go func() {
		result, err := adapter.Collect(chatContext(), questions)
		done <- adapterRun{result: result, err: err}
	}()
	return done, prompts
}

func waitPrompt(t *testing.T, prompts <-chan string) string {
	t.Helper()
	select {
	case prompt := <-prompts:
		return prompt
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ask_user prompt")
		return ""
	}
}

func waitResult(t *testing.T, done <-chan adapterRun) adapterRun {
	t.Helper()
	select {
	case result := <-done:
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ask_user result")
		return adapterRun{}
	}
}

func TestChatAdapterSingleChoiceAndRecommendedMarker(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{{
		ID:      "q0",
		Text:    "Which format?",
		Choices: []string{"JSON", "YAML"},
	}}

	done, prompts := runAdapter(t, adapter, questions)
	prompt := waitPrompt(t, prompts)
	if !strings.Contains(prompt, "1. JSON"+RecommendedSuffix) {
		t.Fatalf("prompt missing recommended marker:\n%s", prompt)
	}
	if !strings.Contains(prompt, "2. YAML") {
		t.Fatalf("prompt missing second option:\n%s", prompt)
	}

	if !broker.Deliver(testBrokerKey(), "1") {
		t.Fatal("delivery failed")
	}
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeSubmitted {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
	if got := run.result.Answers["q0"]; got != "JSON" {
		t.Fatalf("answer = %#v", got)
	}
}

func TestChatAdapterOpenAndMultiAnswers(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{
		{ID: "q0", Text: "Describe the system"},
		{ID: "q1", Text: "Pick features", Choices: []string{"Auth", "Logging", "Caching"}, MultiSelect: true},
	}

	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)
	if !broker.Deliver(testBrokerKey(), "hello") {
		t.Fatal("delivery failed")
	}
	waitPrompt(t, prompts)
	if !broker.Deliver(testBrokerKey(), "1, 3") {
		t.Fatal("delivery failed")
	}

	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := run.result.Answers["q0"]; got != "hello" {
		t.Fatalf("q0 = %#v", got)
	}
	values, ok := run.result.Answers["q1"].([]string)
	if !ok || strings.Join(values, ",") != "Auth,Caching" {
		t.Fatalf("q1 = %#v", run.result.Answers["q1"])
	}
}

func TestChatAdapterMultiSelectSingleItemAndJSONArray(t *testing.T) {
	for _, test := range []struct {
		reply string
		want  string
	}{
		{"2", "Logging"},
		{`["Auth"]`, "Auth"},
	} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		questions := []Question{{
			ID: "q0", Text: "Pick", Choices: []string{"Auth", "Logging"}, MultiSelect: true,
		}}
		done, prompts := runAdapter(t, adapter, questions)
		waitPrompt(t, prompts)
		broker.Deliver(testBrokerKey(), test.reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		values := run.result.Answers["q0"].([]string)
		if len(values) != 1 || values[0] != test.want {
			t.Fatalf("reply=%q values=%#v, want %q", test.reply, values, test.want)
		}
	}
}

func TestChatAdapterMultiSelectFreeTextIsSingleEntry(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{{
		ID: "q0", Text: "Pick", Choices: []string{"Auth", "Logging"}, MultiSelect: true,
	}}
	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "custom answer, with comma")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	values := run.result.Answers["q0"].([]string)
	if len(values) != 1 || values[0] != "custom answer, with comma" {
		t.Fatalf("values = %#v", values)
	}
}

func TestChatAdapterSlashCommands(t *testing.T) {
	for _, test := range []struct {
		reply   string
		outcome Outcome
		skipped bool
	}{
		{"/skip", OutcomeSubmitted, true},
		{"/SKIP", OutcomeSubmitted, true},
		{"/cancel", OutcomeCancelled, false},
		{" /CANCEL ", OutcomeCancelled, false},
	} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		questions := []Question{{ID: "q0", Text: "Question"}}
		done, prompts := runAdapter(t, adapter, questions)
		waitPrompt(t, prompts)
		broker.Deliver(testBrokerKey(), test.reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		if run.result.Outcome != test.outcome {
			t.Fatalf("reply=%q outcome=%s", test.reply, run.result.Outcome)
		}
		if test.skipped {
			value, present := run.result.Answers["q0"]
			if !present || value != nil {
				t.Fatalf("skip answer = %#v present=%v", value, present)
			}
		}
	}
}

func TestChatAdapterBareControlWordsOnChoiceQuestions(t *testing.T) {
	for _, test := range []struct {
		reply   string
		outcome Outcome
	}{
		{"skip", OutcomeSubmitted},
		{"SKIP", OutcomeSubmitted},
		{"cancel", OutcomeCancelled},
	} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		questions := []Question{{ID: "q0", Text: "Question", Choices: []string{"A", "B"}}}
		done, prompts := runAdapter(t, adapter, questions)
		waitPrompt(t, prompts)
		broker.Deliver(testBrokerKey(), test.reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		if run.result.Outcome != test.outcome {
			t.Fatalf("reply=%q outcome=%s", test.reply, run.result.Outcome)
		}
	}
}

func TestChatAdapterOpenQuestionKeepsControlWordsAsAnswers(t *testing.T) {
	for _, reply := range []string{"skip", "cancel", "SKIP"} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		done, prompts := runAdapter(t, adapter, []Question{{ID: "q0", Text: "Open question"}})
		waitPrompt(t, prompts)
		broker.Deliver(testBrokerKey(), reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		if run.result.Outcome != OutcomeSubmitted {
			t.Fatalf("reply=%q outcome=%s, want submitted", reply, run.result.Outcome)
		}
		if got := run.result.Answers["q0"]; got != reply {
			t.Fatalf("reply=%q answer=%#v", reply, got)
		}
	}
}

func TestChatAdapterChoiceLabelWinsOverControlWord(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	done, prompts := runAdapter(t, adapter, []Question{{
		ID: "q0", Text: "Pick", Choices: []string{"skip", "cancel"},
	}})
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "skip")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeSubmitted {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
	if got := run.result.Answers["q0"]; got != "skip" {
		t.Fatalf("answer = %#v, want the option label", got)
	}
}

func TestChatAdapterNumericLabelsWinOverIndex(t *testing.T) {
	for _, test := range []struct {
		reply string
		want  string
	}{
		{"1", "1"},
		{"2", "2"},
	} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		done, prompts := runAdapter(t, adapter, []Question{{
			ID: "q0", Text: "Pick", Choices: []string{"2", "1"},
		}})
		waitPrompt(t, prompts)
		broker.Deliver(testBrokerKey(), test.reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		if got := run.result.Answers["q0"]; got != test.want {
			t.Fatalf("reply=%q answer=%#v, want %q", test.reply, got, test.want)
		}
	}

	// Non-numeric labels keep the positional interpretation.
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	done, prompts := runAdapter(t, adapter, []Question{{
		ID: "q0", Text: "Pick", Choices: []string{"A", "B"},
	}})
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "2")
	run := waitResult(t, done)
	if got := run.result.Answers["q0"]; got != "B" {
		t.Fatalf("answer = %#v, want B", got)
	}
}

func TestChatAdapterIgnoresEmptyReplies(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, 500*time.Millisecond)
	done, prompts := runAdapter(t, adapter, []Question{{ID: "q0", Text: "Open question"}})
	waitPrompt(t, prompts)
	deliver := func(content string) {
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if broker.Deliver(testBrokerKey(), content) {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("reply %q was not delivered", content)
	}
	deliver("")
	deliver("   ")
	deliver("real answer")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeSubmitted {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
	if got := run.result.Answers["q0"]; got != "real answer" {
		t.Fatalf("answer = %#v, want the non-empty reply", got)
	}
}

func TestChatAdapterEmptyRepliesDoNotSkip(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, 80*time.Millisecond)
	done, prompts := runAdapter(t, adapter, []Question{{ID: "q0", Text: "Open question"}})
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeTimedOut {
		t.Fatalf("outcome = %s, want timed_out", run.result.Outcome)
	}
	if _, present := run.result.Answers["q0"]; present {
		t.Fatalf("empty reply was recorded: %#v", run.result.Answers["q0"])
	}
}

func TestChatAdapterTimeoutPreservesPartialAnswers(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, 60*time.Millisecond)
	questions := []Question{
		{ID: "q0", Text: "First"},
		{ID: "q1", Text: "Second"},
	}
	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "saved")

	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeTimedOut {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
	if run.result.Answers["q0"] != "saved" {
		t.Fatalf("q0 = %#v", run.result.Answers["q0"])
	}
	if _, present := run.result.Answers["q1"]; present {
		t.Fatalf("q1 should be absent, got %#v", run.result.Answers["q1"])
	}
	if !strings.Contains(run.result.Notice, "[user did not respond within") {
		t.Fatalf("notice = %q", run.result.Notice)
	}
}

func TestChatAdapterUndelivered(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, func(context.Context, string, string, string) error {
		return errors.New("platform rejected the message")
	}, time.Second)
	done := make(chan adapterRun, 1)
	go func() {
		result, err := adapter.Collect(chatContext(), []Question{{ID: "q0", Text: "Question"}})
		done <- adapterRun{result: result, err: err}
	}()
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeUndelivered {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
	if !strings.Contains(run.result.Notice, "platform rejected the message") {
		t.Fatalf("notice = %q", run.result.Notice)
	}
}

func TestChatAdapterRequiresConversation(t *testing.T) {
	adapter := NewChatAdapter(NewBroker(), func(context.Context, string, string, string) error {
		return nil
	}, time.Second)
	run, err := adapter.Collect(context.Background(), []Question{{ID: "q0", Text: "Question"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome != OutcomeUndelivered {
		t.Fatalf("outcome = %s", run.Outcome)
	}
}

func TestChatAdapterRequiresInteractiveSession(t *testing.T) {
	adapter := NewChatAdapter(NewBroker(), func(context.Context, string, string, string) error {
		return nil
	}, time.Second)
	ctx := toolshared.WithToolContext(context.Background(), "test", "chat-1")
	run, err := adapter.Collect(ctx, []Question{{ID: "q0", Text: "Question"}})
	if err != nil {
		t.Fatal(err)
	}
	if run.Outcome != OutcomeUndelivered {
		t.Fatalf("outcome = %s", run.Outcome)
	}
	if !strings.Contains(run.Notice, "interactive") {
		t.Fatalf("notice = %q", run.Notice)
	}
}

func TestChatAdapterRejectsConcurrentRequest(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{{ID: "q0", Text: "Question"}}

	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)

	if _, err := adapter.Collect(chatContext(), questions); err == nil {
		t.Fatal("expected concurrent ask_user request to fail")
	}

	broker.Deliver(testBrokerKey(), "answer")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if run.result.Outcome != OutcomeSubmitted {
		t.Fatalf("outcome = %s", run.result.Outcome)
	}
}

func TestChatAdapterCancellationUnblocksWait(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Minute)
	questions := []Question{{ID: "q0", Text: "Question"}}

	prompts := make(chan string, 1)
	adapter.send = func(_ context.Context, _, _, content string) error {
		prompts <- content
		return nil
	}
	ctx, cancel := context.WithCancel(chatContext())
	done := make(chan adapterRun, 1)
	go func() {
		result, err := adapter.Collect(ctx, questions)
		done <- adapterRun{result: result, err: err}
	}()
	waitPrompt(t, prompts)
	cancel()

	run := waitResult(t, done)
	if !errors.Is(run.err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", run.err)
	}
}

func TestChatAdapterOtherAnswer(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{{
		ID: "q0", Text: "Pick", Choices: []string{"A", "B"},
	}}
	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)
	broker.Deliver(testBrokerKey(), "other: custom value")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := run.result.Answers["q0"]; got != "custom value" {
		t.Fatalf("answer = %#v", got)
	}
}

func TestTimeoutNoticeFormat(t *testing.T) {
	if got := timeoutNotice(5 * time.Minute); got != "[user did not respond within 5m]" {
		t.Fatalf("notice = %q", got)
	}
	if got := timeoutNotice(30 * time.Second); got != "[user did not respond within 30s]" {
		t.Fatalf("notice = %q", got)
	}
	if got := timeoutNotice(90 * time.Second); got != "[user did not respond within 1m30s]" {
		t.Fatalf("notice = %q", got)
	}
}

func TestBrokerKeyScopesRequesterAgentAndSession(t *testing.T) {
	base := BrokerKey("telegram", "chat-1", "agent-1", "user-1", "session-1")
	if base != BrokerKey(" telegram ", " chat-1 ", " agent-1 ", " user-1 ", " session-1 ") {
		t.Fatal("key is not stable under surrounding whitespace")
	}
	keys := map[string]bool{base: true}
	for name, key := range map[string]string{
		"other sender":  BrokerKey("telegram", "chat-1", "agent-1", "user-2", "session-1"),
		"other agent":   BrokerKey("telegram", "chat-1", "agent-2", "user-1", "session-1"),
		"other session": BrokerKey("telegram", "chat-1", "agent-1", "user-1", "session-2"),
		"other chat":    BrokerKey("telegram", "chat-2", "agent-1", "user-1", "session-1"),
	} {
		if keys[key] {
			t.Fatalf("%s produced a duplicate key", name)
		}
		keys[key] = true
	}
	if got := BrokerKey("", "", "", "user-1", " session "); got != "session" {
		t.Fatalf("session fallback = %q", got)
	}
}

func TestChatAdapterIgnoresRepliesFromOtherSenders(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, time.Second)
	questions := []Question{{ID: "q0", Text: "Who answers?", Choices: []string{"A", "B"}}}

	done, prompts := runAdapter(t, adapter, questions)
	waitPrompt(t, prompts)

	// A different participant in the same chat must not answer the question.
	other := BrokerKey("test", "chat-1", "agent", "sender-2", "session-1")
	if broker.Deliver(other, "2") {
		t.Fatal("reply from another sender was consumed")
	}
	if !broker.Deliver(testBrokerKey(), "1") {
		t.Fatal("reply from the requester was not delivered")
	}
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	if got := run.result.Answers["q0"]; got != "A" {
		t.Fatalf("answer = %#v, want A", got)
	}
}

func TestChatAdapterIgnoresRepliesForOtherAgentOrSession(t *testing.T) {
	cases := map[string]string{
		"other agent":   BrokerKey("test", "chat-1", "agent-2", "sender-1", "session-1"),
		"other session": BrokerKey("test", "chat-1", "agent", "sender-1", "session-2"),
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			broker := NewBroker()
			adapter := NewChatAdapter(broker, nil, 50*time.Millisecond)
			done, prompts := runAdapter(t, adapter, []Question{{ID: "q0", Text: "Q"}})
			waitPrompt(t, prompts)
			if broker.Deliver(key, "answer") {
				t.Fatal("reply with a different scope was consumed")
			}
			run := waitResult(t, done)
			if run.result.Outcome != OutcomeTimedOut {
				t.Fatalf("outcome = %s, want timed_out", run.result.Outcome)
			}
		})
	}
}

func TestChatAdapterRejectsOversizedPromptBeforeDelivery(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, 20*time.Millisecond)
	sent := 0
	adapter.send = func(context.Context, string, string, string) error {
		sent++
		return nil
	}
	questions := []Question{{
		ID:      "q0",
		Text:    "Pick",
		Choices: []string{strings.Repeat("a", 1000), strings.Repeat("b", 1000)},
	}}

	_, err := adapter.Collect(chatContext(), questions)
	if err == nil {
		t.Fatal("oversized prompt accepted")
	}
	if !strings.Contains(err.Error(), "prompt is too long") || !strings.Contains(err.Error(), "1900") {
		t.Fatalf("error = %v", err)
	}
	if sent != 0 {
		t.Fatalf("sent %d prompts before validation failed", sent)
	}

	// The failed interaction must not leave a waiter registered.
	result, err := adapter.Collect(chatContext(), []Question{{ID: "q0", Text: "Short"}})
	if err != nil {
		t.Fatalf("second interaction: %v", err)
	}
	if result.Outcome != OutcomeTimedOut {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
}

func TestChatAdapterMaxPromptRunesOverride(t *testing.T) {
	broker := NewBroker()
	adapter := NewChatAdapter(broker, nil, 20*time.Millisecond)
	adapter.SetMaxPromptRunes(5000)
	sent := 0
	adapter.send = func(context.Context, string, string, string) error {
		sent++
		return nil
	}
	result, err := adapter.Collect(chatContext(), []Question{{
		ID:      "q0",
		Text:    "Pick",
		Choices: []string{strings.Repeat("a", 1000), strings.Repeat("b", 1000)},
	}})
	if err != nil {
		t.Fatalf("override rejected the prompt: %v", err)
	}
	if result.Outcome != OutcomeTimedOut {
		t.Fatalf("outcome = %s", result.Outcome)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
}
