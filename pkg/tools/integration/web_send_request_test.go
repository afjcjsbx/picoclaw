package integrationtools

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// closeTrackingBody records that the search response body was closed.
type closeTrackingBody struct {
	io.Reader
	closed *bool
}

func (b closeTrackingBody) Close() error {
	*b.closed = true
	return nil
}

// failingBody returns an error on every read.
type failingBody struct{}

func (failingBody) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func (failingBody) Close() error { return nil }

func newSearchRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, "https://example.invalid/search", nil)
	if err != nil {
		t.Fatalf("NewRequest() error: %v", err)
	}
	return req
}

// TestSendSearchRequest_ReadsAndClosesBody verifies the shared send path returns
// the status and body of a search response with the body already closed.
func TestSendSearchRequest_ReadsAndClosesBody(t *testing.T) {
	closed := false
	client := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       closeTrackingBody{Reader: strings.NewReader(`{"ok":true}`), closed: &closed},
			}, nil
		}),
	}

	body, status, err := sendSearchRequest(client, newSearchRequest(t))
	if err != nil {
		t.Fatalf("sendSearchRequest() error: %v", err)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want %d", status, http.StatusOK)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q, want the response payload", body)
	}
	if !closed {
		t.Error("the response body was not closed")
	}
}

func TestSendSearchRequest_Errors(t *testing.T) {
	tests := []struct {
		name     string
		response *http.Response
		doErr    error
		wantErr  []string
	}{
		{
			name:    "transport failure",
			doErr:   errors.New("dial failed"),
			wantErr: []string{"request failed:", "dial failed"},
		},
		{
			name: "read failure",
			response: &http.Response{
				StatusCode: http.StatusOK,
				Body:       failingBody{},
			},
			wantErr: []string{"failed to read response: read failed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &http.Client{
				Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
					return tt.response, tt.doErr
				}),
			}

			_, _, err := sendSearchRequest(client, newSearchRequest(t))
			if err == nil {
				t.Fatalf("sendSearchRequest() error = nil, want %q", tt.wantErr)
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestRetryableSearchStatus(t *testing.T) {
	tests := []struct {
		status int
		want   bool
	}{
		{http.StatusOK, false},
		{http.StatusBadRequest, false},
		{http.StatusNotFound, false},
		{http.StatusTooManyRequests, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusInternalServerError, true},
		{http.StatusServiceUnavailable, true},
	}

	for _, tt := range tests {
		if got := retryableSearchStatus(tt.status); got != tt.want {
			t.Errorf("retryableSearchStatus(%d) = %t, want %t", tt.status, got, tt.want)
		}
	}
}
