package cron

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const testReloadInterval = 20 * time.Millisecond

func newReloadService(t *testing.T, handler JobHandler) (*CronService, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := NewCronService(path, handler)
	cs.SetReloadInterval(testReloadInterval)
	return cs, path
}

// writeExternal replaces the store file the way a hand edit would.
func writeExternal(t *testing.T, path string, jobs ...CronJob) {
	t.Helper()
	data, err := json.MarshalIndent(&CronStore{Version: 1, Jobs: jobs}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func syncNow(cs *CronService) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.syncFromDiskUnsafe()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func everyJob(id string, ms int64) CronJob {
	return CronJob{
		ID:       id,
		Name:     id,
		Enabled:  true,
		Schedule: CronSchedule{Kind: "every", EveryMS: int64Ptr(ms)},
		Payload:  CronPayload{Message: "m", Channel: "cli", To: "direct"},
	}
}

func hasJob(cs *CronService, id string) bool {
	for _, j := range cs.ListJobs(true) {
		if j.ID == id {
			return true
		}
	}
	return false
}

func TestCronService_HotReloadPicksUpAndRemovesJobs(t *testing.T) {
	cs, path := newReloadService(t, nil)
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	defer cs.Stop()

	writeExternal(t, path, everyJob("a", 60_000))
	waitFor(t, "added job", func() bool { return hasJob(cs, "a") })

	j, _ := cs.GetJob("a")
	if j.State.NextRunAtMS == nil {
		t.Fatal("reloaded job has no next run")
	}

	writeExternal(t, path)
	waitFor(t, "removed job", func() bool { return !hasJob(cs, "a") })
}

func TestCronService_HotReloadIgnoresOwnWrites(t *testing.T) {
	cs, _ := newReloadService(t, nil)
	if err := cs.Start(); err != nil {
		t.Fatal(err)
	}
	defer cs.Stop()

	job, err := cs.AddJob("own", CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)}, "m", "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}
	cs.EnableJob(job.ID, false)

	cs.mu.RLock()
	_, changed := cs.storeChanged()
	cs.mu.RUnlock()
	if changed {
		t.Fatal("internal writes must not look like external edits")
	}
}

func TestCronService_HotReloadInvalidFileKeepsStore(t *testing.T) {
	cs, path := newReloadService(t, nil)
	if _, err := cs.AddJob(
		"keep",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(`{"jobs": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	syncNow(cs)
	if got := len(cs.ListJobs(true)); got != 1 {
		t.Fatalf("invalid file wiped the store: %d jobs", got)
	}

	// Reported once: the watermark advanced, so the same broken file is not retried.
	cs.mu.RLock()
	_, changed := cs.storeChanged()
	cs.mu.RUnlock()
	if changed {
		t.Fatal("broken file would be re-read on every tick")
	}

	// A later valid edit recovers.
	writeExternal(t, path, everyJob("fixed", 60_000))
	syncNow(cs)
	if !hasJob(cs, "fixed") || len(cs.ListJobs(true)) != 1 {
		t.Fatalf("valid edit after broken one not applied: %+v", cs.ListJobs(true))
	}
}

func TestCronService_HotReloadMissingFileKeepsStore(t *testing.T) {
	cs, path := newReloadService(t, nil)
	if _, err := cs.AddJob(
		"keep",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	syncNow(cs)
	if got := len(cs.ListJobs(true)); got != 1 {
		t.Fatalf("deleted file wiped the store: %d jobs", got)
	}
}

func TestCronService_HotReloadDisabled(t *testing.T) {
	cs, path := newReloadService(t, nil)
	cs.SetReloadInterval(0)
	if _, err := cs.AddJob(
		"mem",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatal(err)
	}

	writeExternal(t, path, everyJob("ext", 60_000))
	syncNow(cs)
	if hasJob(cs, "ext") || len(cs.ListJobs(true)) != 1 {
		t.Fatal("hot-reload ran while disabled")
	}
}

func TestCronService_HotReloadRecomputesNextRunOnScheduleChange(t *testing.T) {
	cs, path := newReloadService(t, nil)
	job := everyJob("tz", 0)
	job.Schedule = CronSchedule{Kind: "cron", Expr: "0 9 * * *", TZ: "UTC"}
	writeExternal(t, path, job)
	cs.mu.Lock()
	_ = cs.loadStore()
	cs.recomputeNextRuns()
	cs.mu.Unlock()

	job.Schedule.TZ = "Asia/Tokyo"
	writeExternal(t, path, job)
	syncNow(cs)

	got, _ := cs.GetJob("tz")
	want := cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
	if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != *want {
		t.Fatalf("next run = %v, want %v for new tz", got.State.NextRunAtMS, *want)
	}
}

func TestCronService_HotReloadKeepsNextRunOfUnchangedJobs(t *testing.T) {
	cs, path := newReloadService(t, nil)
	if _, err := cs.AddJob(
		"every",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(3_600_000)},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatal(err)
	}
	orig := cs.ListJobs(true)[0]
	// Distinct from anything recomputeNextRuns would produce.
	known := time.Now().UnixMilli() + 123_456
	cs.mu.Lock()
	cs.store.Jobs[0].State.NextRunAtMS = &known
	cs.mu.Unlock()

	writeExternal(t, path, orig, everyJob("added", 60_000))
	syncNow(cs)

	got, ok := cs.GetJob(orig.ID)
	if !ok || got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != known {
		t.Fatalf("unrelated edit moved next run: %+v", got)
	}
	if !hasJob(cs, "added") {
		t.Fatal("added job missing")
	}
}

func TestCronService_MutatorDoesNotClobberExternalEdit(t *testing.T) {
	cs, path := newReloadService(t, nil)
	writeExternal(t, path, everyJob("ext", 60_000))

	// No poll tick has run yet; the mutator must notice the edit itself.
	if _, err := cs.AddJob(
		"mine",
		CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)},
		"m",
		"cli",
		"direct",
	); err != nil {
		t.Fatal(err)
	}

	if !hasJob(cs, "ext") || len(cs.ListJobs(true)) != 2 {
		t.Fatalf("external edit lost: %+v", cs.ListJobs(true))
	}
	data, _ := os.ReadFile(path)
	var onDisk CronStore
	if err := json.Unmarshal(data, &onDisk); err != nil || len(onDisk.Jobs) != 2 {
		t.Fatalf("file lost the external job: err=%v jobs=%d", err, len(onDisk.Jobs))
	}
}

func TestCronService_JobRemovedFromFileWhileRunning(t *testing.T) {
	var path string
	cs, p := newReloadService(t, func(*CronJob) (string, error) {
		writeExternal(t, path) // user removes the job mid-run
		return "ok", nil
	})
	path = p
	job, err := cs.AddJob("gone", CronSchedule{Kind: "every", EveryMS: int64Ptr(60_000)}, "m", "cli", "direct")
	if err != nil {
		t.Fatal(err)
	}

	cs.executeJobByID(job.ID)

	if hasJob(cs, job.ID) {
		t.Fatal("job resurrected after external removal")
	}
	data, _ := os.ReadFile(path)
	var onDisk CronStore
	if err := json.Unmarshal(data, &onDisk); err != nil || len(onDisk.Jobs) != 0 {
		t.Fatalf("file should stay without the job: err=%v jobs=%d", err, len(onDisk.Jobs))
	}
}
