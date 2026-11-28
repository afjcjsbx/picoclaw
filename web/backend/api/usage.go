package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/sipeed/picoclaw/pkg/memory"
	"github.com/sipeed/picoclaw/pkg/usage/usagereport"
)

const (
	defaultUsageDays = 30
	maxUsageDays     = 366
)

// registerUsageRoutes binds the token usage endpoint to the ServeMux.
func (h *Handler) registerUsageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/usage", h.handleGetUsage)
}

// handleGetUsage returns the token usage of every stored session, split by
// model and category, plus the daily history of the last days days.
//
//	GET /api/usage?days=30
func (h *Handler) handleGetUsage(w http.ResponseWriter, r *http.Request) {
	days := defaultUsageDays
	if raw := r.URL.Query().Get("days"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maxUsageDays {
			http.Error(w, "days must be between 1 and "+strconv.Itoa(maxUsageDays), http.StatusBadRequest)
			return
		}
		days = parsed
	}

	dir, err := h.sessionsDir()
	if err != nil {
		http.Error(w, "failed to resolve sessions directory", http.StatusInternalServerError)
		return
	}

	entries, err := memory.ReadSessionUsage(dir)
	if err != nil {
		http.Error(w, "failed to read session usage", http.StatusInternalServerError)
		return
	}
	report := usagereport.Overview(dir, entries)
	report.SessionsDir = "" // a local path means nothing in the browser
	if err := report.AddPeriod(dir, days, time.Now()); err != nil {
		http.Error(w, "failed to read usage history", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(report)
}
