package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func newCronTestService(t *testing.T) *cron.CronService {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := cron.NewCronService(storePath, nil)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(cs.Stop)
	return cs
}

func newCronTestMux(cs *cron.CronService) *http.ServeMux {
	getCron := func() *cron.CronService { return cs }
	handler := cronHandler(getCron)
	mux := http.NewServeMux()
	mux.Handle(cronJobsPath, handler)
	mux.Handle(cronJobsPrefix, handler)
	return mux
}

func TestCronStatusHandlersListJobs(t *testing.T) {
	cs := newCronTestService(t)
	everyMS := time.Hour.Milliseconds()
	job, err := cs.AddJob(
		"Hourly",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"msg",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("AddJob() error = %v", err)
	}
	if updated := cs.EnableJob(job.ID, false); updated == nil {
		t.Fatal("EnableJob() returned nil")
	}

	recorder := httptest.NewRecorder()
	newCronTestMux(cs).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, cronJobsPath, nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response cronJobsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if !response.RuntimeAvailable || len(response.Jobs) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.Jobs[0].ID != job.ID || response.Jobs[0].Enabled {
		t.Fatalf("job = %+v, want disabled %s", response.Jobs[0], job.ID)
	}
}

func TestCronStatusHandlersToggleAndDelete(t *testing.T) {
	cs := newCronTestService(t)
	everyMS := time.Hour.Milliseconds()
	job, err := cs.AddJob(
		"Toggle",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"msg",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("AddJob() error = %v", err)
	}
	mux := newCronTestMux(cs)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPatch,
			cronJobsPath+"/"+job.ID,
			strings.NewReader(`{"enabled":false}`),
		),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var updated cron.CronJob
	if err := json.Unmarshal(recorder.Body.Bytes(), &updated); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if updated.Enabled || updated.State.NextRunAtMS != nil {
		t.Fatalf("updated job = %+v, want paused", updated)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPatch, cronJobsPath+"/"+job.ID, strings.NewReader(`{}`)),
	)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("PATCH missing enabled status = %d, want 400", recorder.Code)
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodDelete, cronJobsPath+"/"+job.ID, nil),
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("DELETE status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if _, ok := cs.GetJob(job.ID); ok {
		t.Fatal("job still present after delete")
	}
}

func TestCronStatusHandlersRunJob(t *testing.T) {
	cs := newCronTestService(t)
	everyMS := time.Hour.Milliseconds()
	job, err := cs.AddJob(
		"Run",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"msg",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("AddJob() error = %v", err)
	}

	recorder := httptest.NewRecorder()
	newCronTestMux(cs).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPost, cronJobsPath+"/"+job.ID+"/run", nil),
	)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := cs.GetJob(job.ID)
		if ok && current.State.LastStatus != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("manual run was not recorded")
}

func TestCronStatusHandlersUnknownJob(t *testing.T) {
	cs := newCronTestService(t)
	mux := newCronTestMux(cs)

	tests := []struct {
		method string
		path   string
		body   string
	}{
		{method: http.MethodPost, path: cronJobsPath + "/missing/run"},
		{method: http.MethodPatch, path: cronJobsPath + "/missing", body: `{"enabled":true}`},
		{method: http.MethodDelete, path: cronJobsPath + "/missing"},
	}
	for _, tt := range tests {
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(
			recorder,
			httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body)),
		)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want 404 (body %s)", tt.method, tt.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestCronStatusHandlersAddJob(t *testing.T) {
	cs := newCronTestService(t)
	mux := newCronTestMux(cs)

	recorder := httptest.NewRecorder()
	mux.ServeHTTP(
		recorder,
		httptest.NewRequest(
			http.MethodPost,
			cronJobsPath,
			strings.NewReader(
				`{"name":"Nightly","message":"msg","schedule":{"kind":"every","everyMs":60000},"channel":"cli","to":"direct"}`,
			),
		),
	)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("POST status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var job cron.CronJob
	if err := json.Unmarshal(recorder.Body.Bytes(), &job); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if job.Name != "Nightly" || job.Schedule.Kind != "every" || !job.Enabled {
		t.Fatalf("job = %+v", job)
	}
	if _, ok := cs.GetJob(job.ID); !ok {
		t.Fatal("job was not added to the running service")
	}

	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{"name":`},
		{name: "missing name", body: `{"message":"msg","schedule":{"kind":"every","everyMs":60000}}`},
		{name: "missing message", body: `{"name":"job","schedule":{"kind":"every","everyMs":60000}}`},
		{name: "invalid schedule", body: `{"name":"job","message":"msg","schedule":{"kind":"cron","expr":"nope"}}`},
	}
	for _, tt := range tests {
		badRecorder := httptest.NewRecorder()
		mux.ServeHTTP(
			badRecorder,
			httptest.NewRequest(http.MethodPost, cronJobsPath, strings.NewReader(tt.body)),
		)
		if badRecorder.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, body = %s", tt.name, badRecorder.Code, badRecorder.Body.String())
		}
	}

	recorder = httptest.NewRecorder()
	mux.ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodPut, cronJobsPath, nil),
	)
	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("PUT status = %d, want 405", recorder.Code)
	}
	if allow := recorder.Header().Get("Allow"); allow != "GET, POST" {
		t.Fatalf("Allow = %q, want GET, POST", allow)
	}
}
