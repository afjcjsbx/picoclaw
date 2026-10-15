package cron

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSaveStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file permission bits are not enforced on Windows")
	}

	tmpDir := t.TempDir()
	storePath := filepath.Join(tmpDir, "cron", "jobs.json")

	cs := NewCronService(storePath, nil)

	_, err := cs.AddJob("test", CronSchedule{Kind: "every", EveryMS: int64Ptr(60000)}, "hello", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	info, err := os.Stat(storePath)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}

	perm := info.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("cron store has permission %04o, want 0600", perm)
	}
}

func int64Ptr(v int64) *int64 {
	return &v
}

func setupService(handler JobHandler) (*CronService, string) {
	tmpFile := fmt.Sprintf("test_cron_%d.json", time.Now().UnixNano())
	cs := NewCronService(tmpFile, handler)
	return cs, tmpFile
}

func TestCronService_CRUD(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	// Test AddJob
	at := time.Now().Add(time.Hour).UnixMilli()
	job, err := cs.AddJob("Task1", CronSchedule{Kind: "at", AtMS: &at}, "msg", "ch", "to")
	if err != nil || job.ID == "" {
		t.Fatalf("AddJob failed: %v", err)
	}

	// Test ListJobs
	if len(cs.ListJobs(true)) != 1 {
		t.Error("ListJobs should return 1 job")
	}

	// Test UpdateJob
	job.Name = "UpdatedName"
	err = cs.UpdateJob(job)
	if err != nil || cs.store.Jobs[0].Name != "UpdatedName" {
		t.Error("UpdateJob failed")
	}

	// Test EnableJob
	cs.EnableJob(job.ID, false)
	if cs.store.Jobs[0].Enabled != false || cs.store.Jobs[0].State.NextRunAtMS != nil {
		t.Error("EnableJob(false) failed to clear state")
	}

	// Test RemoveJob
	removed := cs.RemoveJob(job.ID)
	if !removed || len(cs.store.Jobs) != 0 {
		t.Error("RemoveJob failed")
	}
}

func TestCronService_GetJobReturnsCopy(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	everyMS := int64(60_000)
	job, err := cs.AddJob("Task1", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "ch", "to")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.State.NextRunAtMS == nil {
		t.Fatal("expected initial next run")
	}
	nextRun := *job.State.NextRunAtMS

	got, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("GetJob should find job")
	}
	got.Name = "mutated"
	got.Payload.Message = "changed"
	if got.Schedule.EveryMS != nil {
		*got.Schedule.EveryMS = 120_000
	}
	if got.State.NextRunAtMS != nil {
		*got.State.NextRunAtMS = time.Now().Add(3 * time.Hour).UnixMilli()
	}

	again, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("GetJob should still find job")
	}
	if again.Name != "Task1" || again.Payload.Message != "msg" {
		t.Fatalf("GetJob should return a copy, got %+v", again)
	}
	if again.Schedule.EveryMS == nil || *again.Schedule.EveryMS != everyMS {
		t.Fatalf("GetJob should not alias schedule pointers, got %+v", again.Schedule)
	}
	if again.State.NextRunAtMS == nil || *again.State.NextRunAtMS != nextRun {
		t.Fatalf("GetJob should not alias state pointers, got %+v", again.State)
	}
}

func TestCronService_UpdateJobRecomputesNextRunOnScheduleOrEnabledChange(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	at := time.Now().Add(time.Hour).UnixMilli()
	job, err := cs.AddJob("Task1", CronSchedule{Kind: "at", AtMS: &at}, "msg", "ch", "to")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if job.State.NextRunAtMS == nil {
		t.Fatal("expected initial next run")
	}
	initialNextRun := *job.State.NextRunAtMS

	everyMS := int64(30_000)
	job.Schedule = CronSchedule{Kind: "every", EveryMS: &everyMS}
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob schedule failed: %v", err)
	}
	updated, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("updated job not found")
	}
	if updated.State.NextRunAtMS == nil {
		t.Fatal("expected recomputed next run after schedule change")
	}
	if *updated.State.NextRunAtMS == initialNextRun {
		t.Fatalf("next run should be recomputed, still %d", initialNextRun)
	}

	if disabled := cs.EnableJob(job.ID, false); disabled == nil {
		t.Fatal("EnableJob(false) returned nil")
	}
	disabled, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("disabled job not found")
	}
	disabled.Enabled = true
	if err := cs.UpdateJob(disabled); err != nil {
		t.Fatalf("UpdateJob enabled failed: %v", err)
	}
	reenabled, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("reenabled job not found")
	}
	if !reenabled.Enabled || reenabled.State.NextRunAtMS == nil {
		t.Fatalf("expected enabled job with next run, got %+v", reenabled)
	}
}

func TestCronService_UpdateJobPreservesRunStateOnPayloadOnlyChange(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	everyMS := int64(60_000)
	job, err := cs.AddJob("Task1", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "ch", "to")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	lastRun := time.Now().Add(-time.Minute).UnixMilli()
	job.State.LastRunAtMS = &lastRun
	job.State.LastStatus = "ok"
	job.State.LastError = "previous"
	if job.State.NextRunAtMS == nil {
		t.Fatal("expected next run before update")
	}
	nextRun := *job.State.NextRunAtMS

	job.Payload.Message = "updated msg"
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob failed: %v", err)
	}

	updated, ok := cs.GetJob(job.ID)
	if !ok {
		t.Fatal("updated job not found")
	}
	if updated.State.LastRunAtMS == nil || *updated.State.LastRunAtMS != lastRun {
		t.Fatalf("last run changed: %+v", updated.State)
	}
	if updated.State.LastStatus != "ok" || updated.State.LastError != "previous" {
		t.Fatalf("last status changed: %+v", updated.State)
	}
	if updated.State.NextRunAtMS == nil || *updated.State.NextRunAtMS != nextRun {
		t.Fatalf("next run should be preserved: before=%d after=%+v", nextRun, updated.State.NextRunAtMS)
	}
}

// 2. Test Cron Expression Calculation Logic
func TestCronService_ComputeNextRun(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()

	tests := []struct {
		name     string
		schedule CronSchedule
		wantNil  bool
	}{
		{"Valid Cron", CronSchedule{Kind: "cron", Expr: "0 * * * *"}, false},
		{"Invalid Cron", CronSchedule{Kind: "cron", Expr: "invalid"}, true},
		{"Every MS", CronSchedule{Kind: "every", EveryMS: int64Ptr(5000)}, false},
		{"At Future", CronSchedule{Kind: "at", AtMS: int64Ptr(now + 1000)}, false},
		{"At Past", CronSchedule{Kind: "at", AtMS: int64Ptr(now - 1000)}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cs.computeNextRun(&tt.schedule, now)
			if (got == nil) != tt.wantNil {
				t.Errorf("%s: got %v, wantNil %v", tt.name, got, tt.wantNil)
			}
		})
	}
}

func TestCronService_AdvanceNextRunAnchorsToScheduledTime(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Now().UnixMilli()
	every := int64(1000)
	scheduled := now - 100 // job fired 100ms late
	job := &CronJob{
		Enabled:  true,
		Schedule: CronSchedule{Kind: "every", EveryMS: &every},
		State:    CronJobState{NextRunAtMS: &scheduled},
	}

	cs.advanceNextRun(job, now)

	if job.State.NextRunAtMS == nil {
		t.Fatal("expected next run")
	}
	want := scheduled + every
	if *job.State.NextRunAtMS != want {
		t.Fatalf(
			"next run = %d, want anchored %d (drift-free); now+every would be %d",
			*job.State.NextRunAtMS, want, now+every,
		)
	}
}

func TestCronService_AdvanceNextRunReanchorsAfterGap(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Now().UnixMilli()
	every := int64(1000)
	stale := now - 10_000 // far behind, e.g. after downtime
	job := &CronJob{
		Enabled:  true,
		Schedule: CronSchedule{Kind: "every", EveryMS: &every},
		State:    CronJobState{NextRunAtMS: &stale},
	}

	cs.advanceNextRun(job, now)

	if job.State.NextRunAtMS == nil {
		t.Fatal("expected next run")
	}
	if *job.State.NextRunAtMS <= now {
		t.Fatalf("expected re-anchored future run, got %d (now %d)", *job.State.NextRunAtMS, now)
	}
}

func TestCronService_ComputeNextRunTimezone(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC).UnixMilli()

	sched := CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "America/New_York"}
	got := cs.computeNextRun(&sched, now)
	if got == nil {
		t.Fatal("expected next run for valid timezone")
	}
	// 09:00 EST (UTC-5) on 2024-01-01 is 14:00 UTC.
	want := time.Date(2024, 1, 1, 14, 0, 0, 0, time.UTC).UnixMilli()
	if *got != want {
		t.Fatalf("next run = %s, want %s", time.UnixMilli(*got).UTC(), time.UnixMilli(want).UTC())
	}

	invalid := CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "Not/AZone"}
	if cs.computeNextRun(&invalid, now) != nil {
		t.Fatal("expected nil next run for invalid timezone")
	}
}

// 3. Test Execution Flow
func TestCronService_ExecutionFlow(t *testing.T) {
	var mu sync.Mutex
	executedJobs := make(map[string]bool)

	handler := func(job *CronJob) (string, error) {
		mu.Lock()
		executedJobs[job.ID] = true
		mu.Unlock()
		return "ok", nil
	}

	cs, path := setupService(handler)
	defer os.Remove(path)

	// Start the service
	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	// Add a job then runs 100ms from now
	target := time.Now().Add(100 * time.Millisecond).UnixMilli()
	job, _ := cs.AddJob("FastJob", CronSchedule{Kind: "at", AtMS: &target}, "", "", "")

	// Check for job execution with a timeout
	success := false
	for range 20 {
		mu.Lock()
		if executedJobs[job.ID] {
			success = true
			mu.Unlock()
			break
		}
		mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}

	if !success {
		t.Error("Job was not executed in time")
	}

	// check that the job is removed after execution (DeleteAfterRun = true)
	status := cs.Status()
	if status["jobs"].(int) != 0 {
		t.Errorf("Job should be deleted after run, got count: %v", status["jobs"])
	}
}

func TestCronService_NilHandlerMarksJobsSkipped(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	everyMS := int64(50)
	job, err := cs.AddJob("NoHandler", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	var skipped *CronJob
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if current, ok := cs.GetJob(job.ID); ok && current.State.LastStatus == "skipped" {
			skipped = current
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if skipped == nil {
		t.Fatal("job with nil handler was not marked skipped")
	}
	if skipped.State.LastError != "no job handler configured" {
		t.Fatalf("LastError = %q, want no handler reason", skipped.State.LastError)
	}
	if skipped.State.LastRunAtMS == nil {
		t.Fatal("expected LastRunAtMS to be recorded for skipped job")
	}
}

func TestCronService_PersistenceIntegrity(t *testing.T) {
	tmpFile := "persist_test.json"
	defer os.Remove(tmpFile)

	// write a job and persist
	cs1 := NewCronService(tmpFile, nil)
	at := int64(2000000000000)
	cs1.AddJob("PersistMe", CronSchedule{Kind: "at", AtMS: &at}, "payload", "ch1", "")

	// check file exists
	if _, err := os.Stat(tmpFile); os.IsNotExist(err) {
		t.Fatal("Store file was not created")
	}

	// reload and check data integrity
	cs2 := NewCronService(tmpFile, nil)
	if err := cs2.Load(); err != nil {
		t.Fatalf("Failed to load store: %v", err)
	}

	jobs := cs2.ListJobs(true)
	if len(jobs) != 1 || jobs[0].Name != "PersistMe" {
		t.Errorf("Data corruption after reload. Got: %+v", jobs)
	}

	// test loading invalid JSON
	os.WriteFile(tmpFile, []byte("{invalid json}"), 0o644)
	cs3 := NewCronService(tmpFile, nil)
	err := cs3.loadStore()
	if err == nil {
		t.Error("Should return error when loading invalid JSON")
	}
}

func TestCronService_SlowHandlerDoesNotBlockScheduler(t *testing.T) {
	const wantConcurrent = 3

	var (
		mu      sync.Mutex
		stopped bool
		wg      sync.WaitGroup
	)
	started := make(chan struct{}, wantConcurrent)
	release := make(chan struct{})
	releaseHandlers := sync.OnceFunc(func() { close(release) })
	defer releaseHandlers() // never leave handlers blocked if the test fails early

	handler := func(job *CronJob) (string, error) {
		// Runs spawned by a scheduler tick that raced with Stop are ignored so
		// wg.Add never overlaps wg.Wait.
		mu.Lock()
		if stopped {
			mu.Unlock()
			return "ok", nil
		}
		wg.Add(1)
		mu.Unlock()
		defer wg.Done()

		select {
		case started <- struct{}{}:
		default:
		}
		// Block until the test has seen enough concurrent runs. A scheduler
		// that ran due jobs synchronously (or serialized them behind the
		// handler) would stall here and never dispatch another run.
		<-release
		return "ok", nil
	}

	cs, path := setupService(handler)
	defer os.Remove(path)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	// A job never runs concurrently with itself, so concurrency is proven with
	// one job per expected run.
	everyMS := int64(20)
	for i := range wantConcurrent {
		if _, err := cs.AddJob(
			fmt.Sprintf("Slow%d", i),
			CronSchedule{Kind: "every", EveryMS: &everyMS},
			"msg",
			"cli",
			"direct",
		); err != nil {
			t.Fatalf("AddJob failed: %v", err)
		}
	}

	// Handlers stay blocked until released, so receiving wantConcurrent start
	// signals proves the scheduler keeps ticking while earlier runs are still in
	// flight. Waiting on events rather than sleeping makes this independent of
	// wall-clock timing on a loaded runner.
	for got := 0; got < wantConcurrent; got++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf(
				"slow handler blocked the scheduler: only %d concurrent runs (want >= %d)",
				got, wantConcurrent,
			)
		}
	}

	// Stop dispatching, then release and wait for every started handler so none
	// is still running when the test removes the store file.
	cs.Stop()
	mu.Lock()
	stopped = true
	mu.Unlock()
	releaseHandlers()
	wg.Wait()
}

func TestCronService_HandlerPanicKeepsScheduleAlive(t *testing.T) {
	handler := func(job *CronJob) (string, error) {
		panic("boom")
	}

	cs, path := setupService(handler)
	defer os.Remove(path)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	everyMS := int64(30)
	job, err := cs.AddJob("Panicky", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := cs.GetJob(job.ID)
		if ok && current.State.LastStatus == "error" {
			if current.State.NextRunAtMS == nil {
				t.Fatal("panic left recurring job with nil next run")
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("panicking job never recorded an error status")
}

func TestCronService_SetOnJobConcurrentWithExecution(t *testing.T) {
	cs, path := setupService(func(job *CronJob) (string, error) { return "", nil })
	defer os.Remove(path)

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	everyMS := int64(1)
	if _, err := cs.AddJob("Tick", CronSchedule{Kind: "every", EveryMS: &everyMS}, "m", "cli", "direct"); err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 200 {
			cs.SetOnJob(func(job *CronJob) (string, error) { return "ok", nil })
		}
	}()
	time.Sleep(500 * time.Millisecond)
	wg.Wait()
}

func TestCronService_StartupFinalizesExpiredOneTimeJobs(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	now := time.Now().UnixMilli()
	past := now - 3_600_000
	every := int64(60_000)

	expired, err := cs.AddJob("Expired", CronSchedule{Kind: "at", AtMS: &past}, "m", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	if _, err := cs.AddJob(
		"Recurring",
		CronSchedule{Kind: "every", EveryMS: &every},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	cs.recomputeNextRuns()

	jobs := cs.ListJobs(true)
	for _, job := range jobs {
		if job.ID == expired.ID {
			t.Fatalf("expired one-time job was neither dropped nor finalized: %+v", job)
		}
	}
	if len(jobs) != 1 || jobs[0].Name != "Recurring" || jobs[0].State.NextRunAtMS == nil {
		t.Fatalf("recurring job not preserved correctly: %+v", jobs)
	}
}

func TestCronService_ListJobsReturnsCopies(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	every := int64(60_000)
	cs.AddJob("a", CronSchedule{Kind: "every", EveryMS: &every}, "m", "cli", "d")
	cs.AddJob("b", CronSchedule{Kind: "every", EveryMS: &every}, "m", "cli", "d")

	jobs := cs.ListJobs(true)
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	jobs[0].Name = "mutated"
	if jobs[0].State.NextRunAtMS != nil {
		*jobs[0].State.NextRunAtMS = 1
	}
	if jobs[0].Schedule.EveryMS != nil {
		*jobs[0].Schedule.EveryMS = 1
	}

	again := cs.ListJobs(true)
	if again[0].Name == "mutated" {
		t.Fatal("ListJobs aliases the internal store (name mutation leaked)")
	}
	if again[0].State.NextRunAtMS != nil && *again[0].State.NextRunAtMS == 1 {
		t.Fatal("ListJobs aliases state pointers")
	}
	if again[0].Schedule.EveryMS != nil && *again[0].Schedule.EveryMS == 1 {
		t.Fatal("ListJobs aliases schedule pointers")
	}
}

func TestCronService_EnableJobReturnsCopy(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	every := int64(60_000)
	j, err := cs.AddJob("orig", CronSchedule{Kind: "every", EveryMS: &every}, "m", "cli", "d")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}

	ret := cs.EnableJob(j.ID, true)
	if ret == nil {
		t.Fatal("EnableJob returned nil")
	}
	ret.Name = "HACKED"

	got, ok := cs.GetJob(j.ID)
	if !ok {
		t.Fatal("job not found")
	}
	if got.Name == "HACKED" {
		t.Fatal("EnableJob returns a live internal pointer (mutation leaked into store)")
	}
}

func TestCronService_NotifyNotLostWithoutReceiver(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	// No scheduler loop is running, so notify's send must be buffered rather
	// than silently dropped.
	cs.notify()

	select {
	case <-cs.wakeChan:
	default:
		t.Fatal("notify was dropped because wakeChan was unbuffered")
	}
}

func TestCronService_ConcurrentAccess(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)

	cs.Start()
	defer cs.Stop()

	var wg sync.WaitGroup
	workers := 10
	iterations := 50

	wg.Add(workers * 2)

	// add jobs concurrently
	for i := range workers {
		go func(id int) {
			defer wg.Done()
			for j := range iterations {
				at := time.Now().Add(time.Hour).UnixMilli()
				cs.AddJob(fmt.Sprintf("Job-%d-%d", id, j), CronSchedule{Kind: "at", AtMS: &at}, "", "", "")
				time.Sleep(100 * time.Microsecond)
			}
		}(i)
	}

	// read and update jobs concurrently
	for range workers {
		go func() {
			defer wg.Done()
			for j := range iterations {
				jobs := cs.ListJobs(true)
				if len(jobs) > 0 {
					cs.EnableJob(jobs[0].ID, j%2 == 0)
				}
				time.Sleep(100 * time.Microsecond)
			}
		}()
	}

	wg.Wait()
}

// newBlockingService returns a started service whose handler counts calls and
// blocks until release is closed, so tests can hold a job "in flight".
func newBlockingService(t *testing.T) (cs *CronService, calls *atomic.Int32, started chan struct{}, release func()) {
	t.Helper()
	calls = &atomic.Int32{}
	started = make(chan struct{}, 16)
	gate := make(chan struct{})
	release = sync.OnceFunc(func() { close(gate) })
	t.Cleanup(release) // never leave handlers blocked if the test fails early

	cs, path := setupService(func(job *CronJob) (string, error) {
		calls.Add(1)
		started <- struct{}{}
		<-gate
		return "ok", nil
	})
	t.Cleanup(func() { os.Remove(path) })
	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(cs.Stop)
	return cs, calls, started, release
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("handler never started")
	}
}

func waitIdle(t *testing.T, cs *CronService, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cs.mu.RLock()
		_, busy := cs.inFlight[jobID]
		cs.mu.RUnlock()
		if !busy {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job never finished")
}

func TestCronService_RunJobExecutesWithoutTouchingSchedule(t *testing.T) {
	ran := make(chan string, 1)
	cs, path := setupService(func(job *CronJob) (string, error) {
		ran <- job.ID
		return "ok", nil
	})
	defer os.Remove(path)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	everyMS := time.Hour.Milliseconds()
	job, err := cs.AddJob("Hourly", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob failed: %v", err)
	}
	wantNext := *job.State.NextRunAtMS

	if err := cs.RunJob(job.ID); err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	select {
	case id := <-ran:
		if id != job.ID {
			t.Fatalf("ran job %s, want %s", id, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("manual run never executed")
	}
	waitIdle(t, cs, job.ID)

	got, _ := cs.GetJob(job.ID)
	if got.State.LastStatus != "ok" || got.State.LastRunAtMS == nil {
		t.Fatalf("manual run not recorded: %+v", got.State)
	}
	if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != wantNext {
		t.Fatalf("manual run changed next run: got %v, want %d", got.State.NextRunAtMS, wantNext)
	}
}

func TestCronService_RunJobRunsDisabledJob(t *testing.T) {
	cs, calls, started, release := newBlockingService(t)
	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Off", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")
	cs.EnableJob(job.ID, false)

	if err := cs.RunJob(job.ID); err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	waitStarted(t, started)
	release()
	waitIdle(t, cs, job.ID)

	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if got, _ := cs.GetJob(job.ID); got.Enabled || got.State.NextRunAtMS != nil {
		t.Fatalf("manual run re-enabled job: %+v", got)
	}
}

func TestCronService_RunJobErrors(t *testing.T) {
	cs, path := setupService(nil)
	defer os.Remove(path)
	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Idle", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")

	if err := cs.RunJob(job.ID); err == nil {
		t.Fatal("RunJob on a stopped service should fail")
	}

	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()
	if err := cs.RunJob(job.ID); err == nil {
		t.Fatal("RunJob without a job handler should fail instead of recording a skipped run")
	}
	cs.SetOnJob(func(*CronJob) (string, error) { return "ok", nil })
	if err := cs.RunJob("missing"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("RunJob on unknown job error = %v, want ErrJobNotFound", err)
	}
}

func TestCronService_RunJobRejectsConcurrentManualRun(t *testing.T) {
	cs, calls, started, release := newBlockingService(t)
	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Busy", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")

	if err := cs.RunJob(job.ID); err != nil {
		t.Fatalf("first RunJob failed: %v", err)
	}
	waitStarted(t, started)

	if err := cs.RunJob(job.ID); !errors.Is(err, ErrJobRunning) {
		t.Fatalf("second RunJob error = %v, want ErrJobRunning", err)
	}

	release()
	waitIdle(t, cs, job.ID)
	if err := cs.RunJob(job.ID); err != nil {
		t.Fatalf("RunJob after completion failed: %v", err)
	}
	waitIdle(t, cs, job.ID)
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
}

// A scheduler tick that finds a job due while a manual run is in flight must
// skip it (and still advance the schedule) instead of running it twice.
func TestCronService_SchedulerSkipsJobRunningManually(t *testing.T) {
	cs, calls, started, release := newBlockingService(t)
	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Raced", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")

	if err := cs.RunJob(job.ID); err != nil {
		t.Fatalf("RunJob failed: %v", err)
	}
	waitStarted(t, started)

	// Make the job due right now and run a scheduler tick by hand.
	cs.mu.Lock()
	past := time.Now().UnixMilli() - 1
	cs.store.Jobs[0].State.NextRunAtMS = &past
	cs.mu.Unlock()
	cs.checkJobs()

	got, _ := cs.GetJob(job.ID)
	if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS <= time.Now().UnixMilli() {
		t.Fatalf("skipped tick did not advance schedule: %v", got.State.NextRunAtMS)
	}

	release()
	waitIdle(t, cs, job.ID)
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1 (scheduler duplicated the manual run)", calls.Load())
	}
}

// And the other way round: a manual run requested while the scheduler already
// started the job is rejected.
func TestCronService_RunJobRejectedWhileScheduledRunInFlight(t *testing.T) {
	cs, _, started, release := newBlockingService(t)
	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Raced", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")

	cs.mu.Lock()
	past := time.Now().UnixMilli() - 1
	cs.store.Jobs[0].State.NextRunAtMS = &past
	cs.mu.Unlock()
	cs.checkJobs()
	waitStarted(t, started)

	if err := cs.RunJob(job.ID); !errors.Is(err, ErrJobRunning) {
		t.Fatalf("RunJob error = %v, want ErrJobRunning", err)
	}
	release()
	waitIdle(t, cs, job.ID)
}

// Hammer RunJob and scheduler ticks together (run with -race): the handler must
// never observe two overlapping executions of the same job.
func TestCronService_ManualAndScheduledRunsNeverOverlap(t *testing.T) {
	var active, maxActive, total atomic.Int32
	cs, path := setupService(func(job *CronJob) (string, error) {
		n := active.Add(1)
		for {
			m := maxActive.Load()
			if n <= m || maxActive.CompareAndSwap(m, n) {
				break
			}
		}
		total.Add(1)
		time.Sleep(2 * time.Millisecond)
		active.Add(-1)
		return "ok", nil
	})
	defer os.Remove(path)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer cs.Stop()

	everyMS := time.Hour.Milliseconds()
	job, _ := cs.AddJob("Hammered", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = cs.RunJob(job.ID)
				time.Sleep(time.Millisecond)
			}
		}()
		go func() {
			defer wg.Done()
			for range 20 {
				cs.mu.Lock()
				past := time.Now().UnixMilli() - 1
				cs.store.Jobs[0].State.NextRunAtMS = &past
				cs.mu.Unlock()
				cs.checkJobs()
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	waitIdle(t, cs, job.ID)

	if maxActive.Load() != 1 {
		t.Fatalf("max concurrent executions = %d, want 1 (total runs: %d)", maxActive.Load(), total.Load())
	}
	if total.Load() == 0 {
		t.Fatal("handler never ran")
	}
}
