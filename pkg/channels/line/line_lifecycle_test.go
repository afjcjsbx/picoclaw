package line

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestLINENewChannelValidationAndDefaults(t *testing.T) {
	for _, tc := range []struct {
		name   string
		secret string
		token  string
	}{
		{name: "missing secret", token: "token"},
		{name: "missing token", secret: "secret"},
		{name: "missing both"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewLINEChannel(&config.Channel{}, &config.LINESettings{
				ChannelSecret:      *config.NewSecureString(tc.secret),
				ChannelAccessToken: *config.NewSecureString(tc.token),
			}, bus.NewMessageBus())
			if err == nil {
				t.Fatal("NewLINEChannel() should reject missing credentials")
			}
		})
	}

	ch, _ := newLINETestBusChannel(t)
	if got := ch.WebhookPath(); got != "/webhook/line" {
		t.Fatalf("WebhookPath() = %q", got)
	}
	ch.config.WebhookPath = "/custom/line"
	if got := ch.WebhookPath(); got != "/custom/line" {
		t.Fatalf("WebhookPath() = %q", got)
	}
	if got := ch.MaxMessageLength(); got != 5000 {
		t.Fatalf("MaxMessageLength() = %d, want 5000", got)
	}
	if got := ch.VoiceCapabilities(); !got.ASR || !got.TTS {
		t.Fatalf("VoiceCapabilities() = %+v", got)
	}
}

func TestLINEStartFetchesBotInfoAndToleratesAPIError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server, requests := newLINETestAPIServer(
			t,
			http.StatusOK,
			`{"userId":"Ubot","basicId":"@bot","displayName":"Pico"}`,
		)
		ch := newLINETestChannel(t, server)
		if err := ch.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !ch.IsRunning() || ch.botUserID != "Ubot" || ch.botBasicID != "@bot" || ch.botDisplayName != "Pico" {
			t.Fatalf(
				"channel state after Start = running:%v user:%q basic:%q name:%q",
				ch.IsRunning(),
				ch.botUserID,
				ch.botBasicID,
				ch.botDisplayName,
			)
		}
		req := readLINETestRequest(t, requests)
		if req.Method != http.MethodGet || req.Path != "/v2/bot/info" || req.Authorization != "Bearer token" {
			t.Fatalf("GetBotInfo request = %+v", req)
		}
		if err := ch.Stop(context.Background()); err != nil {
			t.Fatalf("Stop() error = %v", err)
		}
		if ch.IsRunning() {
			t.Fatal("channel should be stopped")
		}
		select {
		case <-ch.ctx.Done():
		default:
			t.Fatal("Stop should cancel the channel context")
		}
	})

	t.Run("API error", func(t *testing.T) {
		server, _ := newLINETestAPIServer(t, http.StatusInternalServerError, `{"message":"unavailable"}`)
		ch := newLINETestChannel(t, server)
		if err := ch.Start(context.Background()); err != nil {
			t.Fatalf("Start() error = %v", err)
		}
		if !ch.IsRunning() || ch.botUserID != "" || ch.botDisplayName != "" {
			t.Fatalf("channel should run without bot info: %+v", ch)
		}
	})
}

func TestLINEStartTypingLocalAPIAndNoOpChats(t *testing.T) {
	t.Run("no-op", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		for _, chatID := range []string{"", "C123", "R123"} {
			stop, err := ch.StartTyping(context.Background(), chatID)
			if err != nil || stop == nil {
				t.Fatalf("StartTyping(%q) returned nil stop=%v, err=%v", chatID, stop == nil, err)
			}
			stop()
		}
		assertNoLINETestRequest(t, requests)
	})

	t.Run("user chat", func(t *testing.T) {
		server, requests := newLINETestAPIServer(t, http.StatusOK, `{}`)
		ch := newLINETestChannel(t, server)
		stop, err := ch.StartTyping(context.Background(), "U123")
		if err != nil {
			t.Fatalf("StartTyping() error = %v", err)
		}
		req := readLINETestRequest(t, requests)
		if req.Path != "/v2/bot/chat/loading/start" || !strings.Contains(string(req.Body), `"chatId":"U123"`) ||
			!strings.Contains(string(req.Body), `"loadingSeconds":60`) {
			t.Fatalf("loading request = %+v", req)
		}
		stop()
		stop()
	})

	t.Run("API error", func(t *testing.T) {
		server, _ := newLINETestAPIServer(t, http.StatusInternalServerError, `{"message":"failed"}`)
		ch := newLINETestChannel(t, server)
		stop, err := ch.StartTyping(context.Background(), "U123")
		if !errors.Is(err, channels.ErrTemporary) || stop == nil {
			t.Fatalf("StartTyping() returned nil stop=%v, err=%v", stop == nil, err)
		}
		stop()
	})
}

func TestLINEClassifySDKError(t *testing.T) {
	if err := classifySDKError(nil, nil); err != nil {
		t.Fatalf("classifySDKError(nil, nil) = %v", err)
	}
	if err := classifySDKError(nil, errors.New("network")); !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("network error = %v, want ErrTemporary", err)
	}
	for _, tc := range []struct {
		status int
		want   error
	}{
		{status: http.StatusBadRequest, want: channels.ErrSendFailed},
		{status: http.StatusTooManyRequests, want: channels.ErrRateLimit},
		{status: http.StatusServiceUnavailable, want: channels.ErrTemporary},
	} {
		resp := &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader("body"))}
		if err := classifySDKError(resp, errors.New("api")); !errors.Is(err, tc.want) {
			t.Errorf("classifySDKError(%d) = %v, want %v", tc.status, err, tc.want)
		}
	}
}
