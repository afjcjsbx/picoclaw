package telegram

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewTelegramHTTPClientBoundsStalledRequests guards the fix for a Telegram
// channel that stops receiving messages forever: getUpdates is a long poll and
// its connection can silently stop delivering data (half-open TCP, dead proxy
// tunnel), so the transport must bound the request without aborting healthy
// polls.
func TestNewTelegramHTTPClientBoundsStalledRequests(t *testing.T) {
	client := newTelegramHTTPClient(nil)
	require.NotNil(t, client)

	// An overall client timeout would abort every long poll at that mark.
	assert.Zero(t, client.Timeout, "client-wide timeout would cut healthy long polls")

	transport, ok := client.Transport.(*http.Transport)
	require.Truef(t, ok, "transport type = %T, want *http.Transport", client.Transport)

	assert.NotNil(t, transport.DialContext, "dial must be bounded so a dead proxy or host fails instead of hanging")
	assert.Positive(t, transport.TLSHandshakeTimeout)
	assert.Positive(t, transport.IdleConnTimeout)
	assert.Positive(t, transport.ResponseHeaderTimeout)
	assert.Greater(t, transport.ResponseHeaderTimeout,
		time.Duration(telegramLongPollTimeoutSeconds)*time.Second,
		"response header timeout must exceed the long-poll timeout so healthy polls are never cut off")
}

// TestNewTelegramHTTPClientRoutesThroughConfiguredProxy verifies that an
// explicit proxy (for example socks5://127.0.0.1:1080) is still honored after
// the client construction was centralized in newTelegramHTTPClient.
func TestNewTelegramHTTPClientRoutesThroughConfiguredProxy(t *testing.T) {
	proxyURL, err := url.Parse("socks5://127.0.0.1:1080")
	require.NoError(t, err)

	client := newTelegramHTTPClient(http.ProxyURL(proxyURL))
	transport, ok := client.Transport.(*http.Transport)
	require.Truef(t, ok, "transport type = %T, want *http.Transport", client.Transport)

	req, err := http.NewRequest(http.MethodPost, "https://api.telegram.org/botTOKEN/getUpdates", nil)
	require.NoError(t, err)

	got, err := transport.Proxy(req)
	require.NoError(t, err)
	assert.Equal(t, proxyURL, got)
}

// TestNewTelegramHTTPClientDirectConnectionHasNoProxy verifies that without an
// explicit or environment proxy the client connects directly while still
// carrying the transport timeouts.
func TestNewTelegramHTTPClientDirectConnectionHasNoProxy(t *testing.T) {
	client := newTelegramHTTPClient(nil)
	transport, ok := client.Transport.(*http.Transport)
	require.Truef(t, ok, "transport type = %T, want *http.Transport", client.Transport)

	assert.Nil(t, transport.Proxy)
	assert.NotNil(t, transport.DialContext)
	assert.Positive(t, transport.ResponseHeaderTimeout)
}
