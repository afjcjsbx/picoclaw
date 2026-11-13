package agent

import (
	"strings"
	"testing"
	"time"

	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestPublishRuntimeEventRedactsErrorPayload(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	redact.SetSecrets([]string{key})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	al := &AgentLoop{runtimeEvents: runtimeevents.NewBus()}
	t.Cleanup(func() { _ = al.runtimeEvents.Close() })

	ch, stop := subscribeRuntimeEventsForTest(t, al, 4, runtimeevents.KindAgentError)
	defer stop()

	al.publishRuntimeEvent(runtimeevents.Event{
		Kind: runtimeevents.KindAgentError,
		Payload: ErrorPayload{
			Stage:   "llm",
			Message: "API request failed: Incorrect API key provided: " + key,
		},
	})

	evt := waitForRuntimeEvent(t, ch, time.Second, func(runtimeevents.Event) bool { return true })
	payload, ok := evt.Payload.(ErrorPayload)
	if !ok {
		t.Fatalf("payload type = %T, want ErrorPayload", evt.Payload)
	}
	if strings.Contains(payload.Message, key) {
		t.Fatalf("credential leaked through the runtime event bus: %q", payload.Message)
	}
	if !strings.Contains(payload.Message, redact.Marker) {
		t.Fatalf("expected %s in payload: %q", redact.Marker, payload.Message)
	}
}

func subscribeRuntimeEventsForTest(
	t *testing.T,
	al *AgentLoop,
	buffer int,
	kinds ...runtimeevents.Kind,
) (<-chan runtimeevents.Event, func()) {
	t.Helper()

	if al == nil {
		t.Fatal("agent loop is nil")
	}
	channel := al.RuntimeEvents()
	if channel == nil {
		t.Fatal("runtime event channel is nil")
	}
	if len(kinds) > 0 {
		channel = channel.OfKind(kinds...)
	}
	sub, ch, err := channel.SubscribeChan(
		t.Context(),
		runtimeevents.SubscribeOptions{Name: "agent-runtime-test", Buffer: buffer},
	)
	if err != nil {
		t.Fatalf("SubscribeChan failed: %v", err)
	}
	return ch, func() {
		if err := sub.Close(); err != nil {
			t.Errorf("runtime subscription close failed: %v", err)
		}
	}
}

func waitForRuntimeEvent(
	t *testing.T,
	ch <-chan runtimeevents.Event,
	timeout time.Duration,
	match func(runtimeevents.Event) bool,
) runtimeevents.Event {
	t.Helper()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				t.Fatal("runtime event stream closed before expected event arrived")
			}
			if match(evt) {
				return evt
			}
		case <-timer.C:
			t.Fatal("timed out waiting for expected runtime event")
		}
	}
}

func collectRuntimeEventStream(ch <-chan runtimeevents.Event) []runtimeevents.Event {
	var events []runtimeevents.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
		default:
			return events
		}
	}
}

func findRuntimeEvent(
	events []runtimeevents.Event,
	kind runtimeevents.Kind,
) (runtimeevents.Event, bool) {
	for _, evt := range events {
		if evt.Kind == kind {
			return evt, true
		}
	}
	return runtimeevents.Event{}, false
}

func filterRuntimeEvents(events []runtimeevents.Event, kind runtimeevents.Kind) []runtimeevents.Event {
	var filtered []runtimeevents.Event
	for _, evt := range events {
		if evt.Kind == kind {
			filtered = append(filtered, evt)
		}
	}
	return filtered
}
