package line

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/line/line-bot-sdk-go/v8/linebot/messaging_api"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

func newLINETestChannel(t *testing.T, server *httptest.Server, allowFrom ...string) *LINEChannel {
	t.Helper()
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	ch, err := NewLINEChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.LINESettings{
			ChannelSecret:      *config.NewSecureString("secret"),
			ChannelAccessToken: *config.NewSecureString("token"),
		},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewLINEChannel() error = %v", err)
	}
	if server != nil {
		client, err := messaging_api.NewMessagingApiAPI(
			"token",
			messaging_api.WithEndpoint(server.URL),
			messaging_api.WithHTTPClient(server.Client()),
		)
		if err != nil {
			t.Fatalf("NewMessagingApiAPI() error = %v", err)
		}
		ch.client = client
	}
	ch.ctx = context.Background()
	ch.SetRunning(true)
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	return ch
}

func newLINETestBusChannel(t *testing.T, allowFrom ...string) (*LINEChannel, *bus.MessageBus) {
	t.Helper()
	messageBus := bus.NewMessageBus()
	t.Cleanup(messageBus.Close)
	ch, err := NewLINEChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.LINESettings{
			ChannelSecret:      *config.NewSecureString("secret"),
			ChannelAccessToken: *config.NewSecureString("token"),
		},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewLINEChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	return ch, messageBus
}

type lineTestRequest struct {
	Method        string
	Path          string
	Authorization string
	Body          json.RawMessage
}

func newLINETestAPIServer(
	t *testing.T,
	status int,
	responseBody string,
) (*httptest.Server, <-chan lineTestRequest) {
	t.Helper()
	requests := make(chan lineTestRequest, 16)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- lineTestRequest{
			Method:        r.Method,
			Path:          r.URL.Path,
			Authorization: r.Header.Get("Authorization"),
			Body:          body,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)
	return server, requests
}

func readLINETestRequest(t *testing.T, requests <-chan lineTestRequest) lineTestRequest {
	t.Helper()
	select {
	case req := <-requests:
		return req
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for LINE API request")
		return lineTestRequest{}
	}
}

func assertNoLINETestRequest(t *testing.T, requests <-chan lineTestRequest) {
	t.Helper()
	select {
	case req := <-requests:
		t.Fatalf("unexpected LINE API request: %+v", req)
	default:
	}
}

func waitLINETestInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for LINE inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoLINETestInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected LINE inbound message: %+v", inbound)
	default:
	}
}

func signedLINETestRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/webhook/line", strings.NewReader(body))
	mac := hmac.New(sha256.New, []byte("secret"))
	_, _ = mac.Write([]byte(body))
	req.Header.Set("X-Line-Signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}
