package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mymmrac/telego"
	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func countAPICalls(caller *stubCaller, needle string) int {
	count := 0
	for _, call := range caller.calls {
		if strings.Contains(call.URL, needle) {
			count++
		}
	}
	return count
}

func getFileResponse(t *testing.T) *ta.Response {
	t.Helper()
	return &ta.Response{
		Ok:     true,
		Result: []byte(`{"file_id":"f1","file_unique_id":"u1","file_path":"voice/file_1.oga"}`),
	}
}

func TestIsTransientTelegramGetFileError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "real dns i/o timeout",
			err: errors.New(
				"telego: getFile: internal execution: request call: fasthttp do request: lookup api.telegram.org: i/o timeout",
			),
			want: true,
		},
		{name: "connection reset", err: errors.New("read tcp 1.2.3.4: connection reset by peer"), want: true},
		{name: "nil", err: nil, want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "permanent bad request", err: errors.New(`api: 400 "Bad Request: file not found"`), want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTransientTelegramGetFileError(tc.err))
		})
	}
}

func TestGetFileWithRetry_RetriesTransientErrors(t *testing.T) {
	calls := 0
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			require.Contains(t, url, "getFile")
			calls++
			if calls < telegramGetFileMaxAttempts {
				return nil, errors.New("telego: getFile: fasthttp do request: lookup api.telegram.org: i/o timeout")
			}
			return getFileResponse(t), nil
		},
	}
	ch := newTestChannel(t, caller)

	file := ch.getFileWithRetry(context.Background(), "f1")

	require.NotNil(t, file)
	require.Equal(t, telegramGetFileMaxAttempts, calls)
}

func TestGetFileWithRetry_DoesNotRetryPermanentError(t *testing.T) {
	calls := 0
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			calls++
			return nil, errors.New(`api: 400 "Bad Request: file not found"`)
		},
	}
	ch := newTestChannel(t, caller)

	file := ch.getFileWithRetry(context.Background(), "f1")

	require.Nil(t, file)
	require.Equal(t, 1, calls)
}

func TestGetFileWithRetry_StopsOnContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	caller := &stubCaller{
		callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
			calls++
			cancel()
			return nil, errors.New("lookup api.telegram.org: i/o timeout")
		},
	}
	ch := newTestChannel(t, caller)

	file := ch.getFileWithRetry(ctx, "f1")

	require.Nil(t, file)
	require.Equal(t, 1, calls)
}

// TestDownloadFile_RetriesTransientGetFileFailure exercises the full downloadFile
// path against a local Telegram-compatible server: the first getFile call fails
// with a transient error and the retry must succeed.
func TestDownloadFile_RetriesTransientGetFileFailure(t *testing.T) {
	var getFileCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			getFileCalls++
			w.Header().Set("Content-Type", "application/json")
			if getFileCalls == 1 {
				_, _ = w.Write([]byte(`{"ok":false,"error_code":500,"description":"timeout contacting file server"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"ok": true,
				"result": map[string]any{
					"file_id":        "voice-file",
					"file_unique_id": "uniq",
					"file_path":      "voice/file_1.oga",
				},
			})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("audio-bytes"))
	}))
	defer server.Close()

	ch, err := NewTelegramChannel(
		&config.Channel{Type: config.ChannelTelegram, Enabled: true},
		&config.TelegramSettings{
			Token:   *config.NewSecureString(testToken),
			BaseURL: server.URL,
		},
		nil,
	)
	require.NoError(t, err)

	path := ch.downloadFile(context.Background(), "voice-file", ".ogg")
	require.NotEmpty(t, path, "download should recover after a transient getFile failure")
	t.Cleanup(func() { _ = os.Remove(path) })
	require.GreaterOrEqual(t, getFileCalls, 2)
}

// TestHandleMessage_NotifiesUserWhenVoiceDownloadFails pins the silent-drop bug:
// when a voice message cannot be downloaded, the user must receive a visible
// notice instead of the message being ignored.
func TestHandleMessage_NotifiesUserWhenVoiceDownloadFails(t *testing.T) {
	caller := &stubCaller{
		callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
			switch {
			case strings.Contains(url, "getFile"):
				return nil, errors.New("telego: getFile: lookup api.telegram.org: i/o timeout")
			case strings.Contains(url, "sendMessage"):
				return successResponse(t), nil
			default:
				t.Fatalf("unexpected API call: %s", url)
				return nil, nil
			}
		},
	}
	ch := newTestChannel(t, caller)
	ch.ctx = context.Background()

	msg := &telego.Message{
		MessageID: 7,
		Chat:      telego.Chat{ID: 42, Type: "private"},
		From:      &telego.User{ID: 99, FirstName: "Alice"},
		Voice:     &telego.Voice{FileID: "voice-file"},
	}

	require.NoError(t, ch.handleMessage(context.Background(), msg))

	require.Equal(t, telegramGetFileMaxAttempts, countAPICalls(caller, "getFile"))
	require.Equal(t, 1, countAPICalls(caller, "sendMessage"), "user must be told the audio was dropped")
}

// TestHandleMessage_GroupDownloadFailureRespectsGroupTrigger ensures the failure
// notice is subject to the group trigger filter: a mention-only bot must stay
// silent in a group when it was not addressed, even if the download failed.
func TestHandleMessage_GroupDownloadFailureRespectsGroupTrigger(t *testing.T) {
	tests := []struct {
		name        string
		mentionOnly bool
		wantNotices int
	}{
		{name: "mention-only group stays silent", mentionOnly: true, wantNotices: 0},
		{name: "permissive group is notified", mentionOnly: false, wantNotices: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			caller := &stubCaller{
				callFn: func(_ context.Context, url string, _ *ta.RequestData) (*ta.Response, error) {
					switch {
					case strings.Contains(url, "getFile"):
						return nil, errors.New(`api: 400 "Bad Request: file not found"`)
					case strings.Contains(url, "sendMessage"):
						return successResponse(t), nil
					default:
						t.Fatalf("unexpected API call: %s", url)
						return nil, nil
					}
				},
			}
			ch := newTestChannel(t, caller)
			ch.ctx = context.Background()
			ch.BaseChannel = channels.NewBaseChannel("telegram", nil, nil, nil,
				channels.WithMaxMessageLength(4000),
				channels.WithGroupTrigger(config.GroupTriggerConfig{MentionOnly: tc.mentionOnly}),
			)
			ch.SetRunning(true)

			msg := &telego.Message{
				MessageID: 7,
				Chat:      telego.Chat{ID: -42, Type: "supergroup"},
				From:      &telego.User{ID: 99, FirstName: "Alice"},
				Voice:     &telego.Voice{FileID: "voice-file"},
			}

			require.NoError(t, ch.handleMessage(context.Background(), msg))

			require.Equal(t, tc.wantNotices, countAPICalls(caller, "sendMessage"))
		})
	}
}
