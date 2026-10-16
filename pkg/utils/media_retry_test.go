package utils

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDownloadFile_RetriesTransientServerError verifies that DownloadFile now
// reuses the shared HTTP retry policy (DoRequestWithRetry): a transient 5xx must
// be retried instead of being surfaced as an immediate failure.
func TestDownloadFile_RetriesTransientServerError(t *testing.T) {
	retryDelayUnit = time.Millisecond
	t.Cleanup(func() { retryDelayUnit = time.Second })

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("payload"))
	}))
	defer server.Close()

	path := DownloadFile(server.URL, "file.bin", DownloadOptions{Timeout: 5 * time.Second})
	require.NotEmpty(t, path, "download should recover after a transient server error")
	t.Cleanup(func() { _ = os.Remove(path) })
	require.Equal(t, 2, attempts)
}

// TestDownloadFile_DoesNotRetryPermanentStatus verifies that permanent client
// errors are not retried.
func TestDownloadFile_DoesNotRetryPermanentStatus(t *testing.T) {
	retryDelayUnit = time.Millisecond
	t.Cleanup(func() { retryDelayUnit = time.Second })

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	path := DownloadFile(server.URL, "missing.bin", DownloadOptions{Timeout: 5 * time.Second})
	require.Empty(t, path)
	require.Equal(t, 1, attempts)
}

// TestDownloadFile_RetriesShareTimeoutBudget verifies that the configured
// timeout bounds the whole download including retry backoff, instead of
// applying per attempt.
func TestDownloadFile_RetriesShareTimeoutBudget(t *testing.T) {
	retryDelayUnit = 10 * time.Second
	t.Cleanup(func() { retryDelayUnit = time.Second })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	start := time.Now()
	path := DownloadFile(server.URL, "file.bin", DownloadOptions{Timeout: 300 * time.Millisecond})

	require.Empty(t, path)
	require.Less(t, time.Since(start), 3*time.Second, "retry backoff must not outlive the download timeout")
}
