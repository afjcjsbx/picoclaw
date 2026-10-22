package weixin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	basechannels "github.com/sipeed/picoclaw/pkg/channels"
)

// budgetTestChannel returns a running channel with a context token for
// "user-1". It records the text of every sendmessage request ("" for media)
// and answers request n (1-based) with the body returned by respond.
func budgetTestChannel(t *testing.T, respond func(n int) string) (*WeixinChannel, *[]string) {
	t.Helper()
	var texts []string
	ch := &WeixinChannel{
		BaseChannel: basechannels.NewBaseChannel("weixin", nil, nil, nil),
		api: &ApiClient{
			BaseURL: "https://ilinkai.weixin.qq.com/",
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/ilink/bot/sendmessage" {
					t.Fatalf("request path = %q, want /ilink/bot/sendmessage", r.URL.Path)
				}
				var req SendMessageReq
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode sendmessage req: %v", err)
				}
				text := ""
				if item := req.Msg.ItemList[0]; item.TextItem != nil {
					text = item.TextItem.Text
				}
				texts = append(texts, text)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte(respond(len(texts))))),
					Header:     make(http.Header),
				}, nil
			})},
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}
	ch.SetRunning(true)
	ch.contextTokens.Store("user-1", "ctx-1")
	return ch, &texts
}

func sendOK(int) string { return `{"ret":0,"errcode":0}` }

func toolFeedbackOutbound(content string) bus.OutboundMessage {
	return bus.OutboundMessage{
		ChatID:  "user-1",
		Context: bus.InboundContext{Raw: map[string]string{"message_kind": "tool_feedback"}},
		Content: content,
	}
}

func TestReplyBudgetNextToolFeedback(t *testing.T) {
	var b replyBudget
	want := []toolFeedbackAction{
		toolFeedbackSend, toolFeedbackSend, toolFeedbackSend, toolFeedbackSend, toolFeedbackSend, toolFeedbackSend,
		toolFeedbackSendNotice, toolFeedbackSkip, toolFeedbackSkip,
	}
	for sent, w := range want {
		if got := b.nextToolFeedback("user-1"); got != w {
			t.Fatalf("after %d sent: nextToolFeedback() = %v, want %v", sent, got, w)
		}
		b.recordSent("user-1")
	}
	if got := b.nextToolFeedback("user-2"); got != toolFeedbackSend {
		t.Fatalf("other user: nextToolFeedback() = %v, want send", got)
	}
	b.reset("user-1")
	if got := b.sentTo("user-1"); got != 0 {
		t.Fatalf("after reset: sentTo() = %d, want 0", got)
	}
}

func TestReplyBudgetConcurrentUse(t *testing.T) {
	var b replyBudget
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				b.recordSent("user-1")
				_ = b.nextToolFeedback("user-1")
			}
		}()
	}
	wg.Wait()
	if got := b.sentTo("user-1"); got != 800 {
		t.Fatalf("sentTo() = %d, want 800", got)
	}
}

func TestIsToolFeedbackIgnoresCaseAndSpace(t *testing.T) {
	tests := []struct {
		raw  map[string]string
		want bool
	}{
		{map[string]string{"message_kind": "tool_feedback"}, true},
		{map[string]string{"message_kind": " Tool_Feedback "}, true},
		{map[string]string{"message_kind": "thought"}, false},
		{nil, false},
	}
	for _, tt := range tests {
		msg := bus.OutboundMessage{Context: bus.InboundContext{Raw: tt.raw}}
		if got := isToolFeedback(msg); got != tt.want {
			t.Fatalf("isToolFeedback(%v) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestSendLimitsToolFeedbackAndKeepsFinalReply(t *testing.T) {
	ch, texts := budgetTestChannel(t, sendOK)
	for i := 1; i <= 8; i++ {
		if _, err := ch.Send(context.Background(), toolFeedbackOutbound(fmt.Sprintf("progress %d", i))); err != nil {
			t.Fatalf("Send(progress %d) error = %v", i, err)
		}
	}
	if _, err := ch.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "user-1", Content: "final answer"},
	); err != nil {
		t.Fatalf("Send(final answer) error = %v", err)
	}

	want := []string{
		"progress 1", "progress 2", "progress 3", "progress 4", "progress 5", "progress 6",
		toolFeedbackPausedNotice, "final answer",
	}
	if !reflect.DeepEqual(*texts, want) {
		t.Fatalf("sent texts = %q, want %q", *texts, want)
	}
}

func TestSendDoesNotLimitRegularMessages(t *testing.T) {
	ch, texts := budgetTestChannel(t, sendOK)
	for i := 1; i <= 9; i++ {
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{
			ChatID:  "user-1",
			Content: fmt.Sprintf("reply %d", i),
		}); err != nil {
			t.Fatalf("Send(reply %d) error = %v", i, err)
		}
	}
	if _, err := ch.Send(context.Background(), toolFeedbackOutbound("late progress")); err != nil {
		t.Fatalf("Send(late progress) error = %v", err)
	}
	if len(*texts) != 9 {
		t.Fatalf("sent %d messages, want the 9 regular replies only: %q", len(*texts), *texts)
	}
}

func TestSendRetriesPausedNoticeAfterRejection(t *testing.T) {
	ch, texts := budgetTestChannel(t, func(n int) string {
		if n == 7 {
			return `{"ret":-2,"errcode":0,"errmsg":"prepare failed"}`
		}
		return `{"ret":0,"errcode":0}`
	})
	for i := 1; i <= 8; i++ {
		_, err := ch.Send(context.Background(), toolFeedbackOutbound(fmt.Sprintf("progress %d", i)))
		if (err != nil) != (i == 7) {
			t.Fatalf("Send(progress %d) error = %v, want an error only for the rejected notice", i, err)
		}
	}

	want := []string{
		"progress 1", "progress 2", "progress 3", "progress 4", "progress 5", "progress 6",
		toolFeedbackPausedNotice, toolFeedbackPausedNotice,
	}
	if !reflect.DeepEqual(*texts, want) {
		t.Fatalf("sent texts = %q, want %q", *texts, want)
	}
	if got := ch.replies.sentTo("user-1"); got != 7 {
		t.Fatalf("sentTo() = %d, want 7 (the rejected notice must not count)", got)
	}
}

func TestHandleInboundMessageResetsReplyBudget(t *testing.T) {
	ch, _ := budgetTestChannel(t, sendOK)
	for range 7 {
		ch.replies.recordSent("user-1")
	}

	ch.handleInboundMessage(context.Background(), WeixinMessage{FromUserID: "user-1"})

	if got := ch.replies.sentTo("user-1"); got != 0 {
		t.Fatalf("sentTo() after inbound message = %d, want 0", got)
	}
}

func TestSendUploadedMediaCountsTowardReplyBudget(t *testing.T) {
	ch, texts := budgetTestChannel(t, sendOK)
	err := ch.sendUploadedMedia(
		context.Background(),
		"user-1",
		"ctx-1",
		"caption",
		UploadMediaTypeImage,
		&uploadedFileInfo{
			downloadParam: "download-token",
			aesKeyHex:     "31323334353637383930616263646566",
			fileSize:      11,
			cipherSize:    16,
			filename:      "photo.png",
		},
	)
	if err != nil {
		t.Fatalf("sendUploadedMedia() error = %v", err)
	}
	if len(*texts) != 2 {
		t.Fatalf("sent %d messages, want caption + image", len(*texts))
	}
	if got := ch.replies.sentTo("user-1"); got != 2 {
		t.Fatalf("sentTo() = %d, want 2", got)
	}
}
