package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/usage/usagereport"
)

func getUsage(t *testing.T, h *Handler, query string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/usage"+query, nil))
	return rec
}

func TestHandleGetUsageReportsTotalsModelsAndDailyHistory(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	store, err := memory.NewJSONLStore(sessionsTestDir(t, configPath))
	if err != nil {
		t.Fatalf("NewJSONLStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	for _, key := range []string{"session-a", "session-b"} {
		err := store.AddFullMessage(t.Context(), key, providers.Message{
			Role:      "assistant",
			Content:   "reply",
			ModelName: "model-a",
			Usage: &providers.UsageInfo{
				PromptTokens: 1000, CompletionTokens: 100, TotalTokens: 1100,
				CacheReadTokens: 800, ReasoningTokens: 30, Requests: 1,
			},
		})
		if err != nil {
			t.Fatalf("AddFullMessage: %v", err)
		}
	}

	rec := getUsage(t, NewHandler(configPath), "?days=7")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var report usagereport.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if report.SessionCount != 2 || report.Totals.InputTokens != 2000 || report.Totals.CacheReadTokens != 1600 ||
		report.Totals.OutputTokens != 200 || report.Totals.ReasoningTokens != 60 {
		t.Errorf("report totals = %+v (%d sessions)", report.Totals, report.SessionCount)
	}
	if len(report.Models) != 1 || report.Models[0].Model != "model-a" {
		t.Errorf("models = %+v", report.Models)
	}
	if report.SessionsDir != "" {
		t.Errorf("sessions_dir = %q, must not leak the local path", report.SessionsDir)
	}
	p := report.Period
	if p == nil || p.Days != 7 || len(p.Daily) != 1 || p.Daily[0].Date != time.Now().Format("2006-01-02") ||
		p.Totals.TotalTokens != 2200 || p.TrackedSince == nil {
		t.Errorf("period = %+v", p)
	}
}

func TestHandleGetUsageEmptyWorkspaceListsAsArrays(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	rec := getUsage(t, NewHandler(configPath), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"models", "sessions"} {
		if list, ok := raw[key].([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %v, want []", key, raw[key])
		}
	}
	period, _ := raw["period"].(map[string]any)
	if period == nil || period["days"] != float64(defaultUsageDays) {
		t.Errorf("period = %v, want the default %d days", raw["period"], defaultUsageDays)
	}
}

func TestHandleGetUsageRejectsBadDays(t *testing.T) {
	configPath, cleanup := setupOAuthTestEnv(t)
	defer cleanup()

	for _, query := range []string{"?days=0", "?days=abc", "?days=367", "?days=-1"} {
		if rec := getUsage(t, NewHandler(configPath), query); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", query, rec.Code)
		}
	}
}
