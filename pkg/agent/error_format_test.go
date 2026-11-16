package agent

import (
	"errors"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers/common"
	"github.com/sipeed/picoclaw/pkg/redact"
)

func TestFormatProcessingError_InvalidAPIKey(t *testing.T) {
	err := errors.New(
		`LLM call failed after retries: API request failed: Status: 401 Body: {"error":{"message":"Incorrect API key provided"}}`,
	)

	got := formatProcessingError(err)
	if !strings.Contains(got, "API key appears to be invalid") {
		t.Fatalf("formatted error missing friendly API key hint: %q", got)
	}
	if !strings.Contains(got, "Original error:") {
		t.Fatalf("formatted error missing original error label: %q", got)
	}
	if !strings.Contains(got, err.Error()) {
		t.Fatalf("formatted error missing original error: %q", got)
	}
}

func TestFormatProcessingError_GenericAuthHTTPError(t *testing.T) {
	err := &common.HTTPError{
		StatusCode:  401,
		BodyPreview: `{"error":"unauthorized"}`,
		ContentType: "application/json",
		APIBase:     "https://api.example.com",
	}

	got := formatProcessingError(err)
	if !strings.Contains(got, "check the API key, token, OAuth login, or provider permissions") {
		t.Fatalf("formatted error missing generic auth hint: %q", got)
	}
	if !strings.Contains(got, "Original error:") {
		t.Fatalf("formatted error missing original error: %q", got)
	}
}

func TestFormatProcessingError_NonAuth(t *testing.T) {
	err := errors.New("connection reset by peer")
	got := formatProcessingError(err)
	want := "Error processing message: connection reset by peer"
	if got != want {
		t.Fatalf("formatted error = %q, want %q", got, want)
	}
}

func TestFormatProcessingError_RedactsCredentials(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	redact.SetSecrets([]string{key})
	t.Cleanup(func() { redact.SetSecrets(nil) })

	err := errors.New(
		`LLM call failed after retries: API request failed: Status: 401 Body: ` +
			`{"error":{"message":"Incorrect API key provided: ` + key + `"}}`,
	)
	got := formatProcessingError(err)
	if strings.Contains(got, key) {
		t.Fatalf("API key leaked into the chat error notice: %q", got)
	}
	if !strings.Contains(got, redact.Marker) {
		t.Fatalf("expected %s in the chat error notice: %q", redact.Marker, got)
	}
}
