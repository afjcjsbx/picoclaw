package gateway

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSubagentsStatusHandler(t *testing.T) {
	handler := subagentsStatusHandler(nil)

	post := httptest.NewRequest(http.MethodPost, subagentsStatusPath, nil)
	postRecorder := httptest.NewRecorder()
	handler.ServeHTTP(postRecorder, post)
	if postRecorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST status = %d, want %d", postRecorder.Code, http.StatusMethodNotAllowed)
	}

	get := httptest.NewRequest(http.MethodGet, subagentsStatusPath, nil)
	getRecorder := httptest.NewRecorder()
	handler.ServeHTTP(getRecorder, get)
	if getRecorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET status = %d, want %d", getRecorder.Code, http.StatusServiceUnavailable)
	}
}

func TestBuildSubagentStatusResponseWithoutRuntime(t *testing.T) {
	response := buildSubagentStatusResponse(nil)
	if response.RuntimeAvailable {
		t.Fatal("RuntimeAvailable = true, want false")
	}
	if response.Active == nil {
		t.Fatal("Active = nil, want empty slice")
	}
	if len(response.Active) != 0 {
		t.Fatalf("Active length = %d, want 0", len(response.Active))
	}
}

func TestTrimSubagentTask(t *testing.T) {
	if got := trimSubagentTask("  hello  "); got != "hello" {
		t.Fatalf("trimSubagentTask() = %q, want %q", got, "hello")
	}

	long := strings.Repeat("a", 700)
	got := trimSubagentTask(long)
	if runes := []rune(got); len(runes) != 601 {
		t.Fatalf("trimmed rune length = %d, want 601", len(runes))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("trimmed task should end with ellipsis: %q", got)
	}
}

func TestFormatSubagentStart(t *testing.T) {
	if got := formatSubagentStart(time.Time{}); got != "" {
		t.Fatalf("zero time formatted as %q, want empty", got)
	}
	started := time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)
	if got, want := formatSubagentStart(started), "2026-11-03T12:00:00Z"; got != want {
		t.Fatalf("formatSubagentStart() = %q, want %q", got, want)
	}
}
