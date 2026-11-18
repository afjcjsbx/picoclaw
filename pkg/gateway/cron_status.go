package gateway

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/health"
)

const (
	cronJobsPath   = "/internal/cron/jobs"
	cronJobsPrefix = "/internal/cron/jobs/"
)

type cronJobsResponse struct {
	Jobs             []cron.CronJob `json:"jobs"`
	RuntimeAvailable bool           `json:"runtime_available"`
}

type cronJobEnabledRequest struct {
	Enabled *bool `json:"enabled"`
}

func registerCronHandlers(server *health.Server, getCron func() *cron.CronService) {
	if server == nil {
		return
	}
	handler := cronHandler(getCron)
	// The gateway serves health and internal endpoints through a dynamic mux
	// that matches exact paths and trailing-slash prefixes only, so method and
	// wildcard patterns are dispatched inside the handler.
	server.RegisterProtectedHandler(cronJobsPath, handler)
	server.RegisterProtectedHandler(cronJobsPrefix, handler)
}

func cronHandler(getCron func() *cron.CronService) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == cronJobsPath {
			handleCronList(w, r, getCron)
			return
		}

		rest, ok := strings.CutPrefix(r.URL.Path, cronJobsPrefix)
		if !ok || rest == "" {
			writeCronError(w, http.StatusNotFound, "not found")
			return
		}
		if id, isRun := strings.CutSuffix(rest, "/run"); isRun {
			if id == "" || strings.Contains(id, "/") {
				writeCronError(w, http.StatusNotFound, "not found")
				return
			}
			handleCronRun(w, r, id, getCron)
			return
		}
		if strings.Contains(rest, "/") {
			writeCronError(w, http.StatusNotFound, "not found")
			return
		}
		handleCronJob(w, r, rest, getCron)
	})
}

func handleCronList(w http.ResponseWriter, r *http.Request, getCron func() *cron.CronService) {
	if r.Method != http.MethodGet {
		writeCronMethodNotAllowed(w, http.MethodGet)
		return
	}
	cs := getCron()
	if cs == nil {
		writeCronError(w, http.StatusServiceUnavailable, "cron service unavailable")
		return
	}
	jobs := cs.ListJobs(true)
	if jobs == nil {
		jobs = []cron.CronJob{}
	}
	writeCronJSON(w, http.StatusOK, cronJobsResponse{
		Jobs:             jobs,
		RuntimeAvailable: true,
	})
}

func handleCronRun(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	getCron func() *cron.CronService,
) {
	if r.Method != http.MethodPost {
		writeCronMethodNotAllowed(w, http.MethodPost)
		return
	}
	cs := getCron()
	if cs == nil {
		writeCronError(w, http.StatusServiceUnavailable, "cron service unavailable")
		return
	}
	switch err := cs.RunJob(id); {
	case err == nil:
		writeCronJSON(w, http.StatusAccepted, map[string]string{"status": "running"})
	case errors.Is(err, cron.ErrJobRunning):
		writeCronError(w, http.StatusConflict, err.Error())
	case errors.Is(err, cron.ErrJobNotFound):
		writeCronError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, cron.ErrServiceNotRunning):
		writeCronError(w, http.StatusServiceUnavailable, err.Error())
	default:
		writeCronError(w, http.StatusInternalServerError, err.Error())
	}
}

func handleCronJob(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	getCron func() *cron.CronService,
) {
	switch r.Method {
	case http.MethodPatch:
		handleCronUpdate(w, r, id, getCron)
	case http.MethodDelete:
		handleCronDelete(w, id, getCron)
	default:
		writeCronMethodNotAllowed(w, http.MethodPatch, http.MethodDelete)
	}
}

func handleCronUpdate(
	w http.ResponseWriter,
	r *http.Request,
	id string,
	getCron func() *cron.CronService,
) {
	cs := getCron()
	if cs == nil {
		writeCronError(w, http.StatusServiceUnavailable, "cron service unavailable")
		return
	}
	var request cronJobEnabledRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&request); err != nil {
		writeCronError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if request.Enabled == nil {
		writeCronError(w, http.StatusBadRequest, "enabled is required")
		return
	}
	job := cs.EnableJob(id, *request.Enabled)
	if job == nil {
		writeCronError(w, http.StatusNotFound, cron.ErrJobNotFound.Error())
		return
	}
	writeCronJSON(w, http.StatusOK, job)
}

func handleCronDelete(w http.ResponseWriter, id string, getCron func() *cron.CronService) {
	cs := getCron()
	if cs == nil {
		writeCronError(w, http.StatusServiceUnavailable, "cron service unavailable")
		return
	}
	if !cs.RemoveJob(id) {
		writeCronError(w, http.StatusNotFound, cron.ErrJobNotFound.Error())
		return
	}
	writeCronJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeCronMethodNotAllowed(w http.ResponseWriter, allowed ...string) {
	w.Header().Set("Allow", strings.Join(allowed, ", "))
	writeCronError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func writeCronJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeCronError(w http.ResponseWriter, status int, message string) {
	writeCronJSON(w, status, map[string]string{"error": message})
}
