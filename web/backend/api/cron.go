package api

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"time"
)

const gatewayCronJobsPath = "/internal/cron/jobs"

var gatewayCronDo = (&http.Client{Timeout: 5 * time.Second}).Do

// registerCronRoutes exposes cron management to the dashboard. The gateway owns
// the scheduler and the job handler, so mutating endpoints always go through
// its protected internal API instead of editing jobs.json directly.
func (h *Handler) registerCronRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/cron/jobs", h.handleListCronJobs)
	mux.HandleFunc("POST /api/cron/jobs/{id}/run", h.handleRunCronJob)
	mux.HandleFunc("PATCH /api/cron/jobs/{id}", h.handleUpdateCronJob)
	mux.HandleFunc("DELETE /api/cron/jobs/{id}", h.handleDeleteCronJob)
}

func (h *Handler) handleListCronJobs(w http.ResponseWriter, r *http.Request) {
	h.serveGatewayCron(w, r, http.MethodGet, gatewayCronJobsPath, nil)
}

func (h *Handler) handleRunCronJob(w http.ResponseWriter, r *http.Request) {
	h.serveGatewayCron(
		w,
		r,
		http.MethodPost,
		gatewayCronJobsPath+"/"+url.PathEscape(r.PathValue("id"))+"/run",
		nil,
	)
}

func (h *Handler) handleUpdateCronJob(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<10))
	if err != nil {
		writeCronProxyError(w, http.StatusBadRequest, "failed to read request body")
		return
	}
	h.serveGatewayCron(
		w,
		r,
		http.MethodPatch,
		gatewayCronJobsPath+"/"+url.PathEscape(r.PathValue("id")),
		body,
	)
}

func (h *Handler) handleDeleteCronJob(w http.ResponseWriter, r *http.Request) {
	h.serveGatewayCron(
		w,
		r,
		http.MethodDelete,
		gatewayCronJobsPath+"/"+url.PathEscape(r.PathValue("id")),
		nil,
	)
}

func (h *Handler) serveGatewayCron(
	w http.ResponseWriter,
	r *http.Request,
	method string,
	gatewayPath string,
	body []byte,
) {
	if !h.gatewayAvailableForProxy() {
		writeCronProxyError(w, http.StatusServiceUnavailable, "gateway is not running")
		return
	}

	gateway.mu.Lock()
	pidData := gateway.pidData
	gateway.mu.Unlock()
	if pidData == nil || pidData.Token == "" {
		writeCronProxyError(w, http.StatusServiceUnavailable, "gateway is not running")
		return
	}

	target := h.gatewayProxyURL()
	target.Path = gatewayPath

	var requestBody io.Reader
	if body != nil {
		requestBody = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(r.Context(), method, target.String(), requestBody)
	if err != nil {
		writeCronProxyError(w, http.StatusInternalServerError, "failed to build gateway request")
		return
	}
	req.Header.Set("Authorization", "Bearer "+pidData.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := gatewayCronDo(req)
	if err != nil {
		writeCronProxyError(w, http.StatusBadGateway, "failed to reach gateway")
		return
	}
	defer resp.Body.Close()

	// A gateway-side auth failure must not bubble up as a dashboard 401:
	// launcherFetch treats that as an expired dashboard session and redirects.
	if resp.StatusCode == http.StatusUnauthorized {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
		writeCronProxyError(w, http.StatusBadGateway, "gateway rejected internal request")
		return
	}

	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 4<<20))
}

func writeCronProxyError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	writeErrorf(w, "%s", message)
}
