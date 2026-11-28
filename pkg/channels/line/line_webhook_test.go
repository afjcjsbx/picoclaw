package line

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestLINEWebhookRejectsOversizedBody(t *testing.T) {
	ch := &LINEChannel{config: &config.LINESettings{}}

	oversized := bytes.Repeat([]byte("A"), maxWebhookBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(oversized))
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status %d, got %d", http.StatusRequestEntityTooLarge, rec.Code)
	}
}

func TestLINEWebhookAcceptsMaxBodySize(t *testing.T) {
	ch := &LINEChannel{config: &config.LINESettings{}}

	body := bytes.Repeat([]byte("A"), maxWebhookBodySize)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, req)

	// Missing signature should be rejected, but the body size should not trigger 413.
	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestLINEWebhookRejectsOversizedBodyBeforeSignatureCheck(t *testing.T) {
	ch := &LINEChannel{config: &config.LINESettings{}}

	oversized := bytes.Repeat([]byte("A"), maxWebhookBodySize+1)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(oversized))
	req.Header.Set("X-Line-Signature", "invalidsignature")
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected status %d, got %d", http.StatusRequestEntityTooLarge, rec.Code)
	}
}

func TestLINEWebhookRejectsNonPostMethod(t *testing.T) {
	ch := &LINEChannel{config: &config.LINESettings{}}

	req := httptest.NewRequest(http.MethodGet, "/webhook", nil)
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status %d, got %d", http.StatusMethodNotAllowed, rec.Code)
	}
}

func TestLINEWebhookRejectsInvalidSignature(t *testing.T) {
	ch := &LINEChannel{
		config: &config.LINESettings{},
	}

	body := `{"events":[]}`
	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(body))
	req.Header.Set("X-Line-Signature", "invalidsignature")
	rec := httptest.NewRecorder()

	ch.webhookHandler(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected status %d, got %d", http.StatusForbidden, rec.Code)
	}
}

func TestLINEWebhookSignedMalformedAndEmptyPayloads(t *testing.T) {
	ch, _ := newLINETestBusChannel(t)

	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, signedLINETestRequest(t, `{"events":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed webhook status = %d, want 400", rec.Code)
	}

	rec = httptest.NewRecorder()
	ch.ServeHTTP(rec, signedLINETestRequest(t, `{"destination":"Ubot","events":[]}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("empty webhook status = %d, want 200", rec.Code)
	}
}

func TestLINEWebhookProcessesSignedTextEvent(t *testing.T) {
	ch, messageBus := newLINETestBusChannel(t)
	body := `{
		"destination":"Ubot",
		"events":[{
			"type":"message",
			"source":{"type":"user","userId":"U123"},
			"timestamp":1,
			"mode":"active",
			"webhookEventId":"event-1",
			"replyToken":"reply-1",
			"message":{"type":"text","id":"M1","text":"hello","quoteToken":"quote-1"}
		}]
	}`
	rec := httptest.NewRecorder()
	ch.ServeHTTP(rec, signedLINETestRequest(t, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("webhook status = %d, want 200", rec.Code)
	}

	inbound := waitLINETestInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "U123" || inbound.Context.ChatType != "direct" {
		t.Fatalf("inbound = %+v", inbound)
	}
	if inbound.Context.MessageID != "M1" || inbound.Context.Raw["source_type"] != "user" {
		t.Fatalf("inbound context = %+v", inbound.Context)
	}
	if inbound.Sender.CanonicalID != "line:U123" || inbound.Context.ReplyHandles["reply_token"] != "reply-1" ||
		inbound.Context.ReplyHandles["quote_token"] != "quote-1" {
		t.Fatalf("inbound sender/reply handles = %+v / %v", inbound.Sender, inbound.Context.ReplyHandles)
	}
	if entry, ok := ch.replyTokens.Load("U123"); !ok || entry.(replyTokenEntry).token != "reply-1" {
		t.Fatalf("cached reply token = %v, %v", entry, ok)
	}
	if token, ok := ch.quoteTokens.Load("U123"); !ok || token != "quote-1" {
		t.Fatalf("cached quote token = %v, %v", token, ok)
	}
}
