package line

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/media"
)

func TestLINESendReplyPushAndQuoteToken(t *testing.T) {
	t.Run("reply", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		ch.replyTokens.Store("U123", replyTokenEntry{token: "reply-1", timestamp: time.Now()})
		ch.quoteTokens.Store("U123", "quote-1")
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		req := readLINETestRequest(t, requests)
		if req.Path != "/v2/bot/message/reply" || req.Authorization != "Bearer token" {
			t.Fatalf("reply request = %+v", req)
		}
		var body struct {
			ReplyToken string `json:"replyToken"`
			Messages   []struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				QuoteToken string `json:"quoteToken"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(req.Body, &body); err != nil {
			t.Fatalf("json.Unmarshal(request) error = %v", err)
		}
		if body.ReplyToken != "reply-1" || len(body.Messages) != 1 || body.Messages[0].Text != "hello" ||
			body.Messages[0].QuoteToken != "quote-1" {
			t.Fatalf("reply body = %+v", body)
		}
		if _, ok := ch.replyTokens.Load("U123"); ok {
			t.Fatal("reply token should be consumed")
		}
		if _, ok := ch.quoteTokens.Load("U123"); ok {
			t.Fatal("quote token should be consumed")
		}
		assertNoLINETestRequest(t, requests)
	})

	t.Run("expired reply token uses push", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		ch.replyTokens.Store(
			"U123",
			replyTokenEntry{token: "old", timestamp: time.Now().Add(-lineReplyTokenMaxAge - time.Second)},
		)
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		req := readLINETestRequest(t, requests)
		if req.Path != "/v2/bot/message/push" || !strings.Contains(string(req.Body), `"to":"U123"`) {
			t.Fatalf("push request = %+v", req)
		}
	})

	t.Run("reply error falls back to push", func(t *testing.T) {
		requests := make(chan lineTestRequest, 4)
		var replyCount atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			requests <- lineTestRequest{Method: r.Method, Path: r.URL.Path, Body: body}
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path == "/v2/bot/message/reply" {
				replyCount.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"message":"reply failed"}`))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		ch := newLINETestChannel(t, server)
		ch.replyTokens.Store("U123", replyTokenEntry{token: "reply-1", timestamp: time.Now()})
		ch.quoteTokens.Store("U123", "quote-1")
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); err != nil {
			t.Fatalf("Send() error = %v", err)
		}
		if got := readLINETestRequest(t, requests).Path; got != "/v2/bot/message/reply" {
			t.Fatalf("first path = %q", got)
		}
		push := readLINETestRequest(t, requests)
		if push.Path != "/v2/bot/message/push" || replyCount.Load() != 1 ||
			!strings.Contains(string(push.Body), `"quoteToken":"quote-1"`) {
			t.Fatalf("fallback push = %+v, reply count %d", push, replyCount.Load())
		}
	})
}

func TestLINESendPushErrorsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		want   error
	}{
		{name: "bad request", status: http.StatusBadRequest, want: channels.ErrSendFailed},
		{name: "rate limited", status: http.StatusTooManyRequests, want: channels.ErrRateLimit},
		{name: "server error", status: http.StatusInternalServerError, want: channels.ErrTemporary},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, requests := newLINETestAPIServer(t, tc.status, `{"message":"failed"}`)
			ch := newLINETestChannel(t, server)
			_, err := ch.Send(context.Background(), bus.OutboundMessage{
				ChatID: "U123", Content: "hello",
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("Send() error = %v, want %v", err, tc.want)
			}
			if got := readLINETestRequest(t, requests).Path; got != "/v2/bot/message/push" {
				t.Fatalf("request path = %q", got)
			}
		})
	}

	t.Run("not running", func(t *testing.T) {
		ch, _ := newLINETestBusChannel(t)
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); !errors.Is(err, channels.ErrNotRunning) {
			t.Fatalf("Send() error = %v, want ErrNotRunning", err)
		}
		if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
			err,
			channels.ErrNotRunning,
		) {
			t.Fatalf("SendMedia() error = %v, want ErrNotRunning", err)
		}
	})

	t.Run("network error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		ch := newLINETestChannel(t, server)
		server.Close()
		if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "U123", Content: "hello"}); !errors.Is(
			err,
			channels.ErrTemporary,
		) {
			t.Fatalf("Send() network error = %v, want ErrTemporary", err)
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := ch.Send(ctx, bus.OutboundMessage{ChatID: "U123", Content: "hello"}); !errors.Is(
			err,
			context.Canceled,
		) {
			t.Fatalf("Send() canceled error = %v", err)
		}
		assertNoLINETestRequest(t, requests)
	})
}

func TestLINESendMediaCaptionFallbackAndErrors(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		ch.SetMediaStore(media.NewFileMediaStore())
		msg := bus.OutboundMediaMessage{ChatID: "U123", Parts: []bus.MediaPart{
			{Type: "image", Filename: "picture.png", Caption: "picture caption"},
			{Type: "video", Filename: "clip.mp4"},
		}}
		if _, err := ch.SendMedia(context.Background(), msg); err != nil {
			t.Fatalf("SendMedia() error = %v", err)
		}
		for i, want := range []string{"picture caption", "[video: clip.mp4]"} {
			req := readLINETestRequest(t, requests)
			if req.Path != "/v2/bot/message/push" || !strings.Contains(string(req.Body), `"text":"`+want+`"`) {
				t.Fatalf("request %d = %+v, want text %q", i, req, want)
			}
		}
		assertNoLINETestRequest(t, requests)
	})

	t.Run("missing store", func(t *testing.T) {
		ch, _ := newLINETestBusChannel(t)
		ch.SetRunning(true)
		if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
			err,
			channels.ErrSendFailed,
		) {
			t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
		}
	})

	t.Run("API error stops remaining parts", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusBadRequest, `{"message":"failed"}`)
		ch := newLINETestChannel(t, server)
		ch.SetMediaStore(media.NewFileMediaStore())
		_, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{Parts: []bus.MediaPart{
			{Type: "image", Caption: "first"},
			{Type: "video", Caption: "second"},
		}})
		if !errors.Is(err, channels.ErrSendFailed) {
			t.Fatalf("SendMedia() error = %v, want ErrSendFailed", err)
		}
		_ = readLINETestRequest(t, requests)
		assertNoLINETestRequest(t, requests)
	})
}
