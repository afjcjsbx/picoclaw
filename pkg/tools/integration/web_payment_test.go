package integrationtools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/x402"
)

type paymentTestEnvelope struct {
	URL             string          `json:"url"`
	Status          int             `json:"status"`
	PaymentRequired *x402.Challenge `json:"payment_required"`
	PaymentError    string          `json:"payment_details_error"`
	Message         string          `json:"message"`
	Retryable       bool            `json:"retryable"`
	Body            string          `json:"body"`
}

const paymentTestV2Challenge = `{
  "x402Version": 2,
  "error": "PAYMENT-SIGNATURE header is required",
  "resource": {
    "url": "https://api.example.com/premium-data",
    "description": "Access to premium market data",
    "mimeType": "application/json"
  },
  "accepts": [
    {
      "scheme": "exact",
      "network": "eip155:84532",
      "amount": "10000",
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "payTo": "0x209693Bc6afc0C5328bA36FaF03C514EF312287C",
      "maxTimeoutSeconds": 60,
      "extra": {"name": "USDC", "version": "2"}
    }
  ]
}`

const paymentTestV1Challenge = `{
  "x402Version": 1,
  "error": "X-PAYMENT header is required",
  "accepts": [
    {
      "scheme": "exact",
      "network": "base-sepolia",
      "maxAmountRequired": "2500",
      "resource": "https://api.example.com/v1/data",
      "description": "v1 protected resource",
      "payTo": "0x1111111111111111111111111111111111111111",
      "maxTimeoutSeconds": 30,
      "asset": "0x2222222222222222222222222222222222222222"
    }
  ]
}`

func newPaymentTestTool(t *testing.T) *WebFetchTool {
	t.Helper()
	tool, err := NewWebFetchTool(50000, format, testFetchLimit)
	if err != nil {
		t.Fatalf("NewWebFetchTool() error = %v", err)
	}
	return tool
}

func decodePaymentTestEnvelope(t *testing.T, result *ToolResult) paymentTestEnvelope {
	t.Helper()
	var envelope paymentTestEnvelope
	if err := json.Unmarshal([]byte(result.ForLLM), &envelope); err != nil {
		t.Fatalf("expected a JSON payment envelope, got %q: %v", result.ForLLM, err)
	}
	return envelope
}

func TestWebFetchPaymentRequiredV2Header(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	var receivedPaymentHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPaymentHeaders = append(
			receivedPaymentHeaders,
			r.Header.Get("Payment-Signature"),
			r.Header.Get("X-Payment"),
		)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString([]byte(paymentTestV2Challenge)))
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"error":"payment required"}`))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	if result.IsError {
		t.Fatalf("expected a structured non-error result, got %q", result.ForLLM)
	}

	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.URL != server.URL {
		t.Errorf("URL = %q, want %q", envelope.URL, server.URL)
	}
	if envelope.Status != http.StatusPaymentRequired {
		t.Errorf("Status = %d, want 402", envelope.Status)
	}
	if envelope.PaymentRequired == nil {
		t.Fatalf("PaymentRequired = nil, want parsed challenge (error: %q)", envelope.PaymentError)
	}
	if envelope.PaymentRequired.Version != 2 {
		t.Errorf("Version = %d, want 2", envelope.PaymentRequired.Version)
	}
	if len(envelope.PaymentRequired.Accepts) != 1 {
		t.Fatalf("Accepts = %d, want 1", len(envelope.PaymentRequired.Accepts))
	}
	option := envelope.PaymentRequired.Accepts[0]
	if option.Amount != "10000" || option.Network != "eip155:84532" ||
		option.PayTo != "0x209693Bc6afc0C5328bA36FaF03C514EF312287C" {
		t.Errorf("unexpected payment option: %+v", option)
	}
	if option.AssetLabel() != "USDC" {
		t.Errorf("AssetLabel() = %q, want USDC", option.AssetLabel())
	}
	if envelope.PaymentError != "" {
		t.Errorf("PaymentError = %q, want empty", envelope.PaymentError)
	}
	if envelope.Retryable {
		t.Error("Retryable = true, want false")
	}
	if !strings.Contains(envelope.Message, "does not send payments") {
		t.Errorf("Message = %q", envelope.Message)
	}
	if !strings.Contains(envelope.Body, "payment required") {
		t.Errorf("Body = %q, want upstream body snippet", envelope.Body)
	}

	if !strings.Contains(result.ForUser, "Payment required (HTTP 402)") ||
		!strings.Contains(result.ForUser, "10000 atomic units of USDC") ||
		!strings.Contains(result.ForUser, "eip155:84532") ||
		!strings.Contains(result.ForUser, option.Asset) ||
		!strings.Contains(result.ForUser, option.PayTo) ||
		!strings.Contains(result.ForUser, "did not send a payment") {
		t.Errorf("ForUser is missing payment details: %q", result.ForUser)
	}

	for _, value := range receivedPaymentHeaders {
		if value != "" {
			t.Fatalf("web_fetch sent a payment header %q, want none", value)
		}
	}
}

func TestWebFetchPaymentRequiredV1Body(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(paymentTestV1Challenge))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.PaymentRequired == nil {
		t.Fatalf("PaymentRequired = nil, want body challenge (error: %q)", envelope.PaymentError)
	}
	if envelope.PaymentRequired.Version != 1 {
		t.Errorf("Version = %d, want 1", envelope.PaymentRequired.Version)
	}
	if envelope.PaymentRequired.Accepts[0].Amount != "2500" {
		t.Errorf("Amount = %q, want 2500", envelope.PaymentRequired.Accepts[0].Amount)
	}
	if !strings.Contains(result.ForUser, "2500 atomic units") {
		t.Errorf("ForUser = %q, want the v1 amount", result.ForUser)
	}
}

func TestWebFetchPaymentRequiredMalformedHeader(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Payment-Required", "!!!not-base64!!!")
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte("payment required"))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.PaymentRequired != nil {
		t.Fatalf("PaymentRequired = %+v, want nil", envelope.PaymentRequired)
	}
	if envelope.PaymentError == "" {
		t.Fatal("PaymentError is empty, want the parse failure")
	}
	if !strings.Contains(result.ForUser, "could not be parsed") {
		t.Errorf("ForUser = %q, want a parse failure explanation", result.ForUser)
	}
}

func TestWebFetchPaymentRequiredLargeBodyKeepsHeaderDetails(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString([]byte(paymentTestV2Challenge)))
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(strings.Repeat("x", x402.MaxChallengeBytes+1)))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	if result.IsError {
		t.Fatalf("expected a payment result instead of a body-size error, got %q", result.ForLLM)
	}

	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.PaymentRequired == nil {
		t.Fatalf("PaymentRequired = nil, want header challenge (error: %q)", envelope.PaymentError)
	}
	if envelope.PaymentRequired.Accepts[0].Amount != "10000" {
		t.Errorf("Amount = %q, want header details despite oversized body", envelope.PaymentRequired.Accepts[0].Amount)
	}
}

func TestWebFetchPaymentRequiredAfterCloudflareRetry(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests == 1 {
			w.Header().Set("Cf-Mitigated", "challenge")
			w.Header().Set("Content-Type", "text/html")
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("<html>challenge</html>"))
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString([]byte(paymentTestV2Challenge)))
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(strings.Repeat("x", x402.MaxChallengeBytes+1)))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	if requests != 2 {
		t.Fatalf("requests = %d, want 2", requests)
	}

	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.PaymentRequired == nil {
		t.Fatalf("PaymentRequired = nil, want the retry challenge (error: %q)", envelope.PaymentError)
	}
	if envelope.PaymentRequired.Accepts[0].Amount != "10000" {
		t.Errorf("Amount = %q, want 10000", envelope.PaymentRequired.Accepts[0].Amount)
	}
}

func TestWebFetchPaymentRequiredWithoutDetails(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte("pay up"))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	envelope := decodePaymentTestEnvelope(t, result)
	if envelope.PaymentRequired != nil {
		t.Fatalf("PaymentRequired = %+v, want nil", envelope.PaymentRequired)
	}
	if envelope.PaymentError != "" {
		t.Errorf("PaymentError = %q, want empty", envelope.PaymentError)
	}
	if !strings.Contains(result.ForUser, "did not provide x402 payment details") {
		t.Errorf("ForUser = %q, want the missing-details notice", result.ForUser)
	}
}

func TestWebFetchNonPaymentStatusIgnoresPaymentHeader(t *testing.T) {
	withPrivateWebFetchHostsAllowed(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Payment-Required", base64.StdEncoding.EncodeToString([]byte(paymentTestV2Challenge)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("normal body"))
	}))
	defer server.Close()

	result := newPaymentTestTool(t).Execute(context.Background(), map[string]any{"url": server.URL})
	if result.IsError {
		t.Fatalf("expected success, got %q", result.ForLLM)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.ForLLM), &envelope); err != nil {
		t.Fatalf("expected the regular web_fetch envelope, got %q: %v", result.ForLLM, err)
	}
	if _, ok := envelope["payment_required"]; ok {
		t.Fatalf("unexpected payment_required field on a 200 response: %v", envelope)
	}
}
