package health

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

type Server struct {
	server      *http.Server
	mu          sync.RWMutex
	ready       bool
	checks      map[string]Check
	startTime   time.Time
	reloadFunc  func() error
	cronRunFunc func(jobID string) error
	authToken   string // optional bearer token for protected endpoints
}

type Check struct {
	Name      string    `json:"name"`
	Status    string    `json:"status"`
	Message   string    `json:"message,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

type StatusResponse struct {
	Status string           `json:"status"`
	Uptime string           `json:"uptime"`
	PID    int              `json:"pid,omitempty"`
	Checks map[string]Check `json:"checks,omitempty"`
}

func NewServer(host string, port int, token string) *Server {
	mux := http.NewServeMux()
	s := &Server{
		ready:     false,
		checks:    make(map[string]Check),
		startTime: time.Now(),
		authToken: token,
	}

	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/ready", s.readyHandler)
	mux.HandleFunc("/reload", s.reloadHandler)
	mux.HandleFunc("/cron/run", s.cronRunHandler)

	addr := net.JoinHostPort(host, strconv.Itoa(port))
	s.server = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
	}

	return s
}

func (s *Server) Start() error {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return s.server.ListenAndServe()
}

func (s *Server) StartContext(ctx context.Context) error {
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.server.Shutdown(context.Background())
	}
}

func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	s.ready = false
	s.mu.Unlock()
	return s.server.Shutdown(ctx)
}

func (s *Server) SetReady(ready bool) {
	s.mu.Lock()
	s.ready = ready
	s.mu.Unlock()
}

func (s *Server) RegisterCheck(name string, checkFn func() (bool, string)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	status, msg := checkFn()
	s.checks[name] = Check{
		Name:      name,
		Status:    statusString(status),
		Message:   msg,
		Timestamp: time.Now(),
	}
}

// SetReloadFunc sets the callback function for config reload.
func (s *Server) SetReloadFunc(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadFunc = fn
}

func (s *Server) reloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(map[string]string{"error": "method not allowed, use POST"})
		return
	}

	// Token check
	s.mu.RLock()
	requiredToken := s.authToken
	s.mu.RUnlock()

	if requiredToken != "" {
		given := extractBearerToken(r.Header.Get("Authorization"))
		if given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(requiredToken)) != 1 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
	}

	s.mu.Lock()
	reloadFunc := s.reloadFunc
	s.mu.Unlock()

	if reloadFunc == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(map[string]string{"error": "reload not configured"})
		return
	}

	if err := reloadFunc(); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	// The reload itself runs asynchronously on the gateway loop, so the request
	// is accepted for processing (202) rather than completed (200).
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "reload scheduled",
		"detail": "the reload runs asynchronously; check the logs or gateway.reload.* runtime events for the outcome",
	})
}

// SetCronRunFunc sets the callback that starts a cron job on demand. It must
// return promptly (cron.CronService.RunJob does: the job runs detached).
func (s *Server) SetCronRunFunc(fn func(jobID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cronRunFunc = fn
}

// cronRunHandler triggers a cron job on demand: POST {"job_id":"..."}.
// Running a job can execute its shell command, so unlike /reload it fails
// closed when no auth token is configured.
func (s *Server) cronRunHandler(w http.ResponseWriter, r *http.Request) {
	writeError := func(status int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
	}

	if r.Method != http.MethodPost {
		writeError(http.StatusMethodNotAllowed, "method not allowed, use POST")
		return
	}

	s.mu.RLock()
	requiredToken := s.authToken
	runFunc := s.cronRunFunc
	s.mu.RUnlock()

	given := extractBearerToken(r.Header.Get("Authorization"))
	if requiredToken == "" || given == "" || subtle.ConstantTimeCompare([]byte(given), []byte(requiredToken)) != 1 {
		writeError(http.StatusUnauthorized, "unauthorized")
		return
	}
	if runFunc == nil {
		writeError(http.StatusServiceUnavailable, "cron run not configured")
		return
	}

	var req struct {
		JobID string `json:"job_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil || req.JobID == "" {
		writeError(http.StatusBadRequest, `body must be JSON like {"job_id":"<id>"}`)
		return
	}

	switch err := runFunc(req.JobID); {
	case err == nil:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered", "job_id": req.JobID})
	case errors.Is(err, cron.ErrJobNotFound):
		writeError(http.StatusNotFound, err.Error())
	case errors.Is(err, cron.ErrJobRunning):
		writeError(http.StatusConflict, err.Error())
	default:
		writeError(http.StatusServiceUnavailable, err.Error())
	}
}

func (s *Server) healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	uptime := time.Since(s.startTime)
	resp := StatusResponse{
		Status: "ok",
		Uptime: uptime.String(),
		PID:    os.Getpid(),
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) readyHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	s.mu.RLock()
	ready := s.ready
	checks := make(map[string]Check)
	maps.Copy(checks, s.checks)
	s.mu.RUnlock()

	if !ready {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(StatusResponse{
			Status: "not ready",
			Checks: checks,
		})
		return
	}

	for _, check := range checks {
		if check.Status == "fail" {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(StatusResponse{
				Status: "not ready",
				Checks: checks,
			})
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	uptime := time.Since(s.startTime)
	_ = json.NewEncoder(w).Encode(StatusResponse{
		Status: "ready",
		Uptime: uptime.String(),
		Checks: checks,
	})
}

// HandlerMux is the interface for registering HTTP handlers, used by
// RegisterOnMux so that callers can pass any mux implementation
// (e.g. *http.ServeMux or a custom dynamic mux).
type HandlerMux interface {
	Handle(pattern string, handler http.Handler)
	HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request))
}

// RegisterOnMux registers /health, /ready, /reload and /cron/run handlers onto the given mux.
// This allows the health endpoints to be served by a shared HTTP server.
func (s *Server) RegisterOnMux(mux HandlerMux) {
	mux.HandleFunc("/health", s.healthHandler)
	mux.HandleFunc("/ready", s.readyHandler)
	mux.HandleFunc("/reload", s.reloadHandler)
	mux.HandleFunc("/cron/run", s.cronRunHandler)
}

func statusString(ok bool) string {
	if ok {
		return "ok"
	}
	return "fail"
}

// extractBearerToken returns the token from an "Authorization: Bearer <t>" header,
// or the empty string if the header is missing or malformed.
func extractBearerToken(header string) string {
	const prefix = "Bearer "
	if len(header) < len(prefix) {
		return ""
	}
	if header[:len(prefix)] != prefix {
		return ""
	}
	return header[len(prefix):]
}
