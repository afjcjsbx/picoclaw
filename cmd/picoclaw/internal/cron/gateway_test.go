package cron

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/sipeed/picoclaw/pkg/cron"
	picopid "github.com/sipeed/picoclaw/pkg/pid"
)

func TestGatewayStoreOperations(t *testing.T) {
	everyMS := int64(60_000)
	job := cron.CronJob{
		ID:       "job-1",
		Name:     "nightly",
		Enabled:  true,
		Schedule: cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/internal/cron/jobs":
			_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []cron.CronJob{job}})
		case r.Method == http.MethodPost && r.URL.Path == "/internal/cron/jobs":
			var request gatewayAddRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode add request: %v", err)
			}
			if request.Name != "nightly" || request.Message != "msg" ||
				request.Schedule.Kind != "every" {
				t.Errorf("add request = %+v", request)
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(job)
		case r.Method == http.MethodPatch && r.URL.Path == "/internal/cron/jobs/job-1":
			var request struct {
				Enabled *bool `json:"enabled"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Errorf("decode enable request: %v", err)
			}
			if request.Enabled == nil || *request.Enabled {
				t.Errorf("enabled = %v, want false", request.Enabled)
			}
			job.Enabled = false
			_ = json.NewEncoder(w).Encode(job)
		case r.Method == http.MethodPatch && r.URL.Path == "/internal/cron/jobs/missing":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
		case r.Method == http.MethodDelete && r.URL.Path == "/internal/cron/jobs/job-1":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})
		case r.Method == http.MethodDelete && r.URL.Path == "/internal/cron/jobs/missing":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "job not found"})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store := gatewayStore{client: &gatewayClient{
		baseURL: server.URL,
		token:   "test-token",
		http:    server.Client(),
	}}
	ctx := context.Background()

	jobs, err := store.List(ctx)
	if err != nil || len(jobs) != 1 || jobs[0].ID != "job-1" {
		t.Fatalf("List() = %+v, err = %v", jobs, err)
	}

	added, err := store.Add(
		ctx,
		"nightly",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"msg",
		"cli",
		"direct",
	)
	if err != nil || added.ID != "job-1" {
		t.Fatalf("Add() = %+v, err = %v", added, err)
	}

	disabledJob, err := store.SetEnabled(ctx, "job-1", false)
	if err != nil || disabledJob.Enabled {
		t.Fatalf("SetEnabled() = %+v, err = %v", disabledJob, err)
	}
	if _, enableErr := store.SetEnabled(ctx, "missing", false); !errors.Is(enableErr, cron.ErrJobNotFound) {
		t.Fatalf("SetEnabled(missing) error = %v, want ErrJobNotFound", enableErr)
	}
	removed, err := store.Remove(ctx, "job-1")
	if err != nil || !removed {
		t.Fatalf("Remove() = %v, err = %v", removed, err)
	}
	removed, err = store.Remove(ctx, "missing")
	if err != nil || removed {
		t.Fatalf("Remove(missing) = %v, err = %v", removed, err)
	}
}

func TestNormalizeGatewayHost(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0", "::", "[::]"} {
		if got := normalizeGatewayHost(host); got == "" || got == host {
			t.Errorf("normalizeGatewayHost(%q) = %q, want a loopback host", host, got)
		}
	}
	if got := normalizeGatewayHost("192.0.2.10"); got != "192.0.2.10" {
		t.Errorf("normalizeGatewayHost(192.0.2.10) = %q", got)
	}
}

func TestNewGatewayStoreDiscoversPidFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jobs": []cron.CronJob{{ID: "job-1"}}})
	}))
	defer server.Close()

	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portString, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portString)
	if err != nil {
		t.Fatal(err)
	}

	home := t.TempDir()
	data, err := picopid.WritePidFile(home, host, port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { picopid.RemovePidFileIfPID(home, data.PID) })

	store := newGatewayStore(home)
	if store == nil {
		t.Fatal("newGatewayStore() = nil with a live PID file")
	}
	jobs, err := store.List(context.Background())
	if err != nil || len(jobs) != 1 || jobs[0].ID != "job-1" {
		t.Fatalf("List() = %+v, err = %v", jobs, err)
	}
}
