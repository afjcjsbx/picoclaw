package onebot

import (
	"encoding/json"
	"testing"
)

func TestHandleRawEvent_RoutesPrivateAndGroupMessages(t *testing.T) {
	ch, messageBus := newOneBotTestChannel(t)
	private := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		MessageID:   json.RawMessage(`"m1"`),
		UserID:      json.RawMessage(`"42"`),
		SelfID:      json.RawMessage(`7`),
		RawMessage:  "[CQ:at,qq=7] hello",
		Message:     json.RawMessage(`"hello"`),
		Sender:      json.RawMessage(`{"user_id":42,"nickname":"Alice"}`),
	}
	ch.handleRawEvent(private)

	inbound := waitOneBotTestInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "private:42" || inbound.Context.ChatType != "direct" {
		t.Fatalf("private inbound = %+v", inbound)
	}
	if !inbound.Context.Mentioned || inbound.Context.MessageID != "m1" || inbound.Context.Raw["nickname"] != "Alice" {
		t.Fatalf("private context = %+v", inbound.Context)
	}
	if inbound.Sender.CanonicalID != "onebot:42" || inbound.Sender.PlatformID != "42" {
		t.Fatalf("private sender = %+v", inbound.Sender)
	}

	group := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "group",
		MessageID:   json.RawMessage(`1002`),
		UserID:      json.RawMessage(`"42"`),
		GroupID:     json.RawMessage(`99`),
		Message: json.RawMessage(`[
			{"type":"at","data":{"qq":7}},
			{"type":"text","data":{"text":"group hello"}},
			{"type":"reply","data":{"id":"m0"}}
		]`),
		Sender: json.RawMessage(`{"user_id":"42","card":"Alice Card","nickname":"Alice"}`),
	}
	ch.handleRawEvent(group)
	inbound = waitOneBotTestInbound(t, messageBus)
	if inbound.Content != "group hello" || inbound.Context.ChatID != "group:99" || inbound.Context.ChatType != "group" {
		t.Fatalf("group inbound = %+v", inbound)
	}
	if inbound.Context.ReplyToMessageID != "m0" || inbound.Context.Raw["reply_to_message_id"] != "m0" {
		t.Fatalf("group reply context = %+v", inbound.Context)
	}
	if inbound.Context.Raw["sender_name"] != "Alice Card" || inbound.Context.Raw["sender_user_id"] != "42" {
		t.Fatalf("group sender metadata = %v", inbound.Context.Raw)
	}

	ch.handleRawEvent(group)
	assertNoOneBotTestInbound(t, messageBus)
}

func TestHandleRawEvent_RejectsInvalidAndDisallowedMessages(t *testing.T) {
	ch, messageBus := newOneBotTestChannel(t, "onebot:100")
	for _, raw := range []*oneBotRawEvent{
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`"not-a-number"`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`42`), MessageID: json.RawMessage(`1`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "unknown", UserID: json.RawMessage(`100`), MessageID: json.RawMessage(`2`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`100`), MessageID: json.RawMessage(`3`), Message: json.RawMessage(`""`)},
		{PostType: "message_sent"},
		{PostType: "meta_event", MetaEventType: "heartbeat"},
		{PostType: "notice", NoticeType: "group_recall", GroupID: json.RawMessage(`99`)},
		{PostType: "request", SubType: "friend"},
		{PostType: "future_event"},
		{},
	} {
		ch.handleRawEvent(raw)
	}
	assertNoOneBotTestInbound(t, messageBus)
}

func TestOneBotDedupRingEviction(t *testing.T) {
	ch := &OneBotChannel{
		dedup:     make(map[string]struct{}, 2),
		dedupRing: make([]string, 2),
	}
	if ch.isDuplicate("") || ch.isDuplicate("0") {
		t.Fatal("empty and zero message IDs must not be deduplicated")
	}
	if ch.isDuplicate("a") || ch.isDuplicate("b") {
		t.Fatal("new message IDs must not be deduplicated")
	}
	if !ch.isDuplicate("a") {
		t.Fatal("existing message ID must be deduplicated")
	}
	if ch.isDuplicate("c") {
		t.Fatal("new message ID must not be deduplicated")
	}
	if ch.isDuplicate("a") {
		t.Fatal("oldest message ID should have been evicted")
	}
}
