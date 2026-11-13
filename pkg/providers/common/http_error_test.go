package common

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestHandleErrorResponse_ReturnsHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("HandleErrorResponse() error = %T, want *HTTPError", err)
	}
	if httpErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("StatusCode = %d, want %d", httpErr.StatusCode, http.StatusUnauthorized)
	}
	if httpErr.BodyPreview != `{"error":"unauthorized"}` {
		t.Fatalf("BodyPreview = %q", httpErr.BodyPreview)
	}
	if !strings.Contains(err.Error(), "Status: 401") {
		t.Fatalf("Error() should preserve status text, got %q", err.Error())
	}
}

func TestHTTPErrorRedactsCredentialsEchoedByProvider(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	redact.SetSecrets([]string{key})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"Incorrect API key provided: ` + key + `"}}`))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key) {
		t.Fatalf("API key leaked through HTTPError.Error(): %q", err.Error())
	}
	if !strings.Contains(err.Error(), redact.Marker) {
		t.Fatalf("expected %s in error: %q", redact.Marker, err.Error())
	}
}

func TestWrapHTMLResponseError_ReturnsHTTPError(t *testing.T) {
	err := WrapHTMLResponseError(
		http.StatusBadGateway,
		[]byte("<html>bad</html>"),
		"text/html",
		"https://api.example.com",
	)

	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("WrapHTMLResponseError() error = %T, want *HTTPError", err)
	}
	if !httpErr.IsHTML {
		t.Fatal("expected IsHTML")
	}
	if !strings.Contains(err.Error(), "HTML instead of JSON") {
		t.Fatalf("Error() should preserve HTML message, got %q", err.Error())
	}
}

func TestHandleErrorResponseRedactsBeforeTruncating(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	redact.SetSecrets([]string{key})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	// Place the key across the preview boundary: redaction must happen before
	// truncation, otherwise a partial credential would survive.
	body := strings.Repeat("x", 2040) + key
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	defer resp.Body.Close()

	err = HandleErrorResponse(resp, server.URL)
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), key[:12]) {
		t.Fatalf("partial credential leaked through truncation: %q", err.Error())
	}
}
