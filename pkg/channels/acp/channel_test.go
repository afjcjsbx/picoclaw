package acp

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
)

func TestSendMapsStructuredOutput(t *testing.T) {
	observer := &recordingObserver{}
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	channel := New(messageBus, observer)

	messages := []bus.OutboundMessage{
		{
			ChatID:  "s1",
			Content: "thinking",
			Context: bus.InboundContext{Raw: map[string]string{"message_kind": "thought"}},
		},
		{
			ChatID:  "s1",
			Content: `[{"id":"call-1"}]`,
			Context: bus.InboundContext{
				Raw: map[string]string{"message_kind": "tool_calls", "tool_calls": `[{"id":"call-1"}]`},
			},
		},
		{
			ChatID:  "s1",
			Content: "tool output",
			Context: bus.InboundContext{
				Raw: map[string]string{"message_kind": "tool_result", "tool_call_id": "call-1"},
			},
		},
		{
			ChatID:  "s1",
			Content: "approval",
			Context: bus.InboundContext{Raw: map[string]string{"message_kind": "approval"}},
		},
		{
			ChatID:  "s1",
			Content: "answer",
			Context: bus.InboundContext{Raw: map[string]string{"outbound_kind": "final"}},
		},
	}
	for _, message := range messages {
		if _, err := channel.Send(t.Context(), message); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}

	if len(observer.thoughts) != 1 || observer.thoughts[0] != "thinking" {
		t.Fatalf("thoughts = %#v", observer.thoughts)
	}
	if len(observer.toolCalls) != 1 || observer.toolCalls[0] != `[{"id":"call-1"}]` {
		t.Fatalf("tool calls = %#v", observer.toolCalls)
	}
	if len(observer.toolResults) != 1 || observer.toolResults[0].output != "tool output" {
		t.Fatalf("tool results = %#v", observer.toolResults)
	}
	if len(observer.text) != 0 {
		t.Fatalf("unexpected interim text = %#v", observer.text)
	}
	if len(observer.finals) != 1 || observer.finals[0] != "answer" {
		t.Fatalf("finals = %#v", observer.finals)
	}
}

func TestStreamerEmitsOnlyAppendedContent(t *testing.T) {
	observer := &recordingObserver{}
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	channel := New(messageBus, observer)

	streamer, err := channel.BeginStream(t.Context(), "s1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	if err := streamer.Update(t.Context(), "hel"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := streamer.Update(t.Context(), "hello"); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if err := streamer.Finalize(t.Context(), "hello world"); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	if len(observer.text) != 3 || observer.text[0] != "hel" || observer.text[1] != "lo" ||
		observer.text[2] != " world" {
		t.Fatalf("text deltas = %#v", observer.text)
	}
	if len(observer.finals) != 1 || observer.finals[0] != "hello world" {
		t.Fatalf("finals = %#v", observer.finals)
	}
}

func TestStreamerEmitsReasoningDeltas(t *testing.T) {
	observer := &recordingObserver{}
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	channel := New(messageBus, observer)

	streamer, err := channel.BeginStream(t.Context(), "s1")
	if err != nil {
		t.Fatalf("BeginStream() error = %v", err)
	}
	reasoning, ok := streamer.(bus.ReasoningStreamer)
	if !ok {
		t.Fatal("streamer does not implement ReasoningStreamer")
	}
	if err := reasoning.UpdateReasoning(t.Context(), "a"); err != nil {
		t.Fatalf("UpdateReasoning() error = %v", err)
	}
	if err := reasoning.FinalizeReasoning(t.Context(), "ab"); err != nil {
		t.Fatalf("FinalizeReasoning() error = %v", err)
	}
	if len(observer.thoughts) != 2 || observer.thoughts[0] != "a" || observer.thoughts[1] != "b" {
		t.Fatalf("thought deltas = %#v", observer.thoughts)
	}
}

type recordingObserver struct {
	text        []string
	thoughts    []string
	toolCalls   []string
	toolResults []recordedToolResult
	finals      []string
}

type recordedToolResult struct {
	toolCallID string
	output     string
	isError    bool
}

func (o *recordingObserver) OnText(_ context.Context, _, _, content string) error {
	o.text = append(o.text, content)
	return nil
}

func (o *recordingObserver) OnThought(_ context.Context, _, _, content string) error {
	o.thoughts = append(o.thoughts, content)
	return nil
}

func (o *recordingObserver) OnToolCalls(_ context.Context, _, _, rawCalls string) error {
	o.toolCalls = append(o.toolCalls, rawCalls)
	return nil
}

func (o *recordingObserver) OnToolResult(_ context.Context, _, _, toolCallID, output string, isError bool) error {
	o.toolResults = append(o.toolResults, recordedToolResult{toolCallID: toolCallID, output: output, isError: isError})
	return nil
}

func (o *recordingObserver) OnFinal(_ context.Context, _, _, content string, _ bus.OutboundMessage) error {
	o.finals = append(o.finals, content)
	return nil
}
