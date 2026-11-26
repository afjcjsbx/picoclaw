package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleUpdateValidation(t *testing.T) {
	h := NewHandler("")
	called := false
	h.updateSelf = func(string, string, string, string) error {
		called = true
		return nil
	}
	mux := http.NewServeMux()
	h.registerUpdateRoutes(mux)

	tests := []struct {
		name   string
		method string
		body   string
		status int
	}{
		{name: "method not allowed", method: http.MethodGet, status: http.StatusMethodNotAllowed},
		{name: "invalid JSON", method: http.MethodPost, body: `{`, status: http.StatusBadRequest},
		{
			name:   "oversized body",
			method: http.MethodPost,
			body:   `{"url":"` + strings.Repeat("x", 1<<20) + `"}`,
			status: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, "/api/update", strings.NewReader(tt.body))
			mux.ServeHTTP(rec, req)
			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.status, rec.Body.String())
			}
			var response updateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != "error" {
				t.Fatalf("response status = %q, want error", response.Status)
			}
		})
	}
	if called {
		t.Fatal("updater called for invalid request")
	}
}

func TestHandleUpdateSuccess(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantURL    string
		wantBinary string
	}{
		{
			name:       "default binary",
			body:       `{"url":"https://example.test/release"}`,
			wantURL:    "https://example.test/release",
			wantBinary: "picoclaw-launcher",
		},
		{
			name:       "custom binary",
			body:       `{"url":"https://example.test/release","binary":"custom-launcher"}`,
			wantURL:    "https://example.test/release",
			wantBinary: "custom-launcher",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler("")
			calls := 0
			h.updateSelf = func(releaseURL, platform, arch, binary string) error {
				calls++
				if releaseURL != tt.wantURL || platform != "" || arch != "" || binary != tt.wantBinary {
					t.Fatalf("update args = %q, %q, %q, %q", releaseURL, platform, arch, binary)
				}
				return nil
			}
			mux := http.NewServeMux()
			h.registerUpdateRoutes(mux)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(tt.body))
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
			}
			if calls != 1 {
				t.Fatalf("updater calls = %d, want 1", calls)
			}
			var response updateResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Status != "ok" {
				t.Fatalf("response status = %q, want ok", response.Status)
			}
		})
	}
}

func TestHandleUpdateFailureDoesNotReportSuccess(t *testing.T) {
	h := NewHandler("")
	h.updateSelf = func(string, string, string, string) error {
		return errors.New("checksum mismatch")
	}
	mux := http.NewServeMux()
	h.registerUpdateRoutes(mux)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/update",
		strings.NewReader(`{"url":"https://example.test/release"}`),
	)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	var response updateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "error" || !strings.Contains(response.Message, "checksum mismatch") {
		t.Fatalf("response = %+v", response)
	}
}
