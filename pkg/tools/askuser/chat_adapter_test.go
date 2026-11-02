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
	return WithInteractiveSession(ctx)
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

	if !broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "1") {
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
	if !broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "hello") {
		t.Fatal("delivery failed")
	}
	waitPrompt(t, prompts)
	if !broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "1, 3") {
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
		broker.Deliver(BrokerKey("test", "chat-1", "session-1"), test.reply)
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
	broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "custom answer, with comma")
	run := waitResult(t, done)
	if run.err != nil {
		t.Fatal(run.err)
	}
	values := run.result.Answers["q0"].([]string)
	if len(values) != 1 || values[0] != "custom answer, with comma" {
		t.Fatalf("values = %#v", values)
	}
}

func TestChatAdapterSkipAndCancel(t *testing.T) {
	for _, test := range []struct {
		reply   string
		outcome Outcome
	}{
		{"skip", OutcomeSubmitted},
		{"SKIP", OutcomeSubmitted},
		{"cancel", OutcomeCancelled},
		{"/CANCEL", OutcomeCancelled},
	} {
		broker := NewBroker()
		adapter := NewChatAdapter(broker, nil, time.Second)
		questions := []Question{{ID: "q0", Text: "Question"}}
		done, prompts := runAdapter(t, adapter, questions)
		waitPrompt(t, prompts)
		broker.Deliver(BrokerKey("test", "chat-1", "session-1"), test.reply)
		run := waitResult(t, done)
		if run.err != nil {
			t.Fatal(run.err)
		}
		if run.result.Outcome != test.outcome {
			t.Fatalf("reply=%q outcome=%s", test.reply, run.result.Outcome)
		}
		if test.reply == "skip" || test.reply == "SKIP" {
			value, present := run.result.Answers["q0"]
			if !present || value != nil {
				t.Fatalf("skip answer = %#v present=%v", value, present)
			}
		}
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
	broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "saved")

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

	broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "answer")
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
	broker.Deliver(BrokerKey("test", "chat-1", "session-1"), "other: custom value")
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
