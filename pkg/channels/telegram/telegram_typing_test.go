package telegram

import (
	"context"
	"sync"
	"testing"
	"time"

	ta "github.com/mymmrac/telego/telegoapi"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

// TestStartTypingKeepalive verifies that the typing keepalive can be disabled
// per channel (settings.typing_keepalive): when disabled only the immediate
// chat action is sent, while by default the action is re-sent until the stop
// function is called.
func TestStartTypingKeepalive(t *testing.T) {
	original := typingKeepaliveInterval
	typingKeepaliveInterval = 25 * time.Millisecond
	t.Cleanup(func() { typingKeepaliveInterval = original })

	newChannelWithCalls := func(keepalive *bool) (*TelegramChannel, func() int) {
		var mu sync.Mutex
		calls := 0
		caller := &stubCaller{
			callFn: func(_ context.Context, _ string, _ *ta.RequestData) (*ta.Response, error) {
				mu.Lock()
				calls++
				mu.Unlock()
				return &ta.Response{Ok: true, Result: []byte("true")}, nil
			},
		}
		ch := newTestChannel(t, caller)
		ch.tgCfg = &config.TelegramSettings{TypingKeepalive: keepalive}
		return ch, func() int {
			mu.Lock()
			defer mu.Unlock()
			return calls
		}
	}

	t.Run("disabled sends only the immediate action", func(t *testing.T) {
		disabled := false
		ch, calls := newChannelWithCalls(&disabled)

		stop, err := ch.StartTyping(context.Background(), "12345")
		require.NoError(t, err)
		require.NotNil(t, stop)

		// Several keepalive intervals must elapse without further calls.
		time.Sleep(150 * time.Millisecond)
		require.Equal(t, 1, calls())

		// The stop function is a safe no-op.
		stop()
		stop()
		require.Equal(t, 1, calls())
	})

	t.Run("enabled repeats the action until stopped", func(t *testing.T) {
		ch, calls := newChannelWithCalls(nil)

		stop, err := ch.StartTyping(context.Background(), "12345")
		require.NoError(t, err)
		require.NotNil(t, stop)

		require.Eventually(t, func() bool { return calls() >= 2 }, time.Second, 10*time.Millisecond)

		stop()
		afterStop := calls()
		time.Sleep(100 * time.Millisecond)
		// At most one already-scheduled tick may land after cancel.
		require.LessOrEqual(t, calls(), afterStop+1)
	})
}
