package cron

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestReconcileDeclarationsConvergesAndPreservesOperatorPause(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	everyMS := int64(60_000)
	decl := CronJobDeclaration{
		DeclarationKey: "plugin:demo:nightly",
		Name:           "nightly",
		Schedule:       CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:        "run nightly work",
	}

	report, err := cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true)
	if err != nil {
		t.Fatalf("initial reconcile: %v", err)
	}
	if report.Created != 1 {
		t.Fatalf("created = %d, want 1", report.Created)
	}
	job, ok := cs.GetJobByDeclarationKey(decl.DeclarationKey)
	if !ok {
		t.Fatal("declared job not found")
	}
	originalID := job.ID
	if job.DeclarationKey != decl.DeclarationKey || job.Payload.Message != decl.Message {
		t.Fatalf("unexpected created job: %+v", job)
	}
	lastRun := time.Now().Add(-time.Minute).UnixMilli()
	job.State.LastRunAtMS = &lastRun
	job.State.LastStatus = "ok"
	job.State.RunHistory = []CronRunRecord{{RunAtMS: lastRun, Status: "ok", DurationMS: 5}}
	if updateErr := cs.UpdateJob(job); updateErr != nil {
		t.Fatalf("UpdateJob state: %v", updateErr)
	}

	// Operator pauses the job. Routine reconciliation omits Enabled, so the
	// pause must survive a reload.
	if updated := cs.EnableJob(originalID, false); updated == nil {
		t.Fatal("EnableJob returned nil")
	}
	report, err = cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true)
	if err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if report.Unchanged != 1 {
		t.Fatalf("unchanged = %d, want 1 (report %+v)", report.Unchanged, report)
	}
	job, _ = cs.GetJobByDeclarationKey(decl.DeclarationKey)
	if job.Enabled {
		t.Fatal("operator pause was overwritten by reconciliation")
	}
	if job.ID != originalID {
		t.Fatalf("job ID changed: %s -> %s", originalID, job.ID)
	}

	// An explicit declaration enable is the opt-in that overrides the pause.
	enabled := true
	decl.Enabled = &enabled
	report, err = cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true)
	if err != nil {
		t.Fatalf("explicit enable reconcile: %v", err)
	}
	if report.Updated != 1 {
		t.Fatalf("updated = %d, want 1", report.Updated)
	}
	job, _ = cs.GetJobByDeclarationKey(decl.DeclarationKey)
	if !job.Enabled || job.State.NextRunAtMS == nil {
		t.Fatalf("explicit enable did not arm job: %+v", job)
	}

	// A schedule change updates the existing row instead of creating a second.
	everyMS = 120_000
	decl.Schedule.EveryMS = &everyMS
	report, err = cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true)
	if err != nil {
		t.Fatalf("schedule reconcile: %v", err)
	}
	if report.Updated != 1 {
		t.Fatalf("updated = %d, want 1", report.Updated)
	}
	job, _ = cs.GetJobByDeclarationKey(decl.DeclarationKey)
	if job.ID != originalID {
		t.Fatalf("job ID changed after schedule update: %s", job.ID)
	}
	if job.Schedule.EveryMS == nil || *job.Schedule.EveryMS != everyMS {
		t.Fatalf("schedule not updated: %+v", job.Schedule)
	}
	if job.State.LastRunAtMS == nil || *job.State.LastRunAtMS != lastRun ||
		job.State.LastStatus != "ok" || len(job.State.RunHistory) != 1 {
		t.Fatalf("run state was not preserved: %+v", job.State)
	}
}

func TestReconcileDeclarationsPrunesOnlyOwnerPrefix(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	everyMS := int64(60_000)
	userJob, err := cs.AddJob(
		"user",
		CronSchedule{Kind: "every", EveryMS: &everyMS},
		"user work",
		"",
		"",
	)
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	one := CronJobDeclaration{
		DeclarationKey: "plugin:demo:one",
		Name:           "one",
		Schedule:       CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:        "one",
	}
	two := CronJobDeclaration{
		DeclarationKey: "plugin:demo:two",
		Name:           "two",
		Schedule:       CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:        "two",
	}
	if _, createErr := cs.ReconcileDeclarations(
		"plugin:",
		[]CronJobDeclaration{one, two},
		true,
	); createErr != nil {
		t.Fatalf("reconcile create: %v", createErr)
	}

	report, err := cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{two}, true)
	if err != nil {
		t.Fatalf("reconcile prune: %v", err)
	}
	if report.Removed != 1 || len(report.RemovedKeys) != 1 ||
		report.RemovedKeys[0] != one.DeclarationKey {
		t.Fatalf("unexpected prune report: %+v", report)
	}
	if _, ok := cs.GetJobByDeclarationKey(one.DeclarationKey); ok {
		t.Fatal("stale declaration was not removed")
	}
	if _, ok := cs.GetJobByDeclarationKey(two.DeclarationKey); !ok {
		t.Fatal("retained declaration was removed")
	}
	if _, ok := cs.GetJob(userJob.ID); !ok {
		t.Fatal("user job was touched by plugin reconciliation")
	}
}

func TestReconcileDeclarationsDoesNotRecreatePastOneShot(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	at := time.Now().Add(-time.Hour).UnixMilli()
	decl := CronJobDeclaration{
		DeclarationKey: "plugin:demo:once",
		Name:           "once",
		Schedule:       CronSchedule{Kind: "at", AtMS: &at},
		Message:        "run once",
	}
	report, err := cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if report.Created != 0 {
		t.Fatalf("created = %d, want 0", report.Created)
	}
	if _, ok := cs.GetJobByDeclarationKey(decl.DeclarationKey); ok {
		t.Fatal("past one-shot was re-created")
	}
}

func TestReconcileDeclarationsRejectsOwnerLeakAndDuplicates(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	everyMS := int64(60_000)
	decl := CronJobDeclaration{
		DeclarationKey: "other:plugin:job",
		Name:           "job",
		Schedule:       CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:        "job",
	}
	if _, err := cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl}, true); err == nil {
		t.Fatal("accepted a declaration outside the owner prefix")
	}
	decl.DeclarationKey = "plugin:demo:job"
	if _, err := cs.ReconcileDeclarations("plugin:", []CronJobDeclaration{decl, decl}, true); err == nil {
		t.Fatal("accepted duplicate declaration keys")
	}
}

func TestExecutionContextOnManualRun(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	t.Cleanup(func() { waitAllIdle(t, cs) })
	everyMS := int64(60_000)
	job, err := cs.AddJob("Job", CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	got := make(chan Execution, 1)
	cs.SetOnJobWithContext(func(ctx context.Context, job *CronJob) (string, error) {
		execution, ok := ExecutionFromContext(ctx)
		if !ok {
			t.Errorf("execution context missing")
		}
		got <- execution
		return "ok", nil
	})
	cs.executeJobByID(job.ID)

	select {
	case execution := <-got:
		if execution.Source != ExecutionSourceManual {
			t.Fatalf("source = %q, want manual", execution.Source)
		}
		if execution.JobID != job.ID || execution.ExecutionID == "" {
			t.Fatalf("incomplete execution: %+v", execution)
		}
		if execution.ScheduledAtMS != nil {
			t.Fatalf("manual execution has scheduled time: %v", *execution.ScheduledAtMS)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job did not run")
	}
}

func TestExecutionContextOnScheduledRun(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	t.Cleanup(func() { waitAllIdle(t, cs) })
	at := time.Now().Add(-time.Second).UnixMilli()
	job, err := cs.AddJob("Job", CronSchedule{Kind: "at", AtMS: &at}, "msg", "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}
	job.State.NextRunAtMS = &at
	if err := cs.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}

	got := make(chan Execution, 1)
	cs.SetOnJobWithContext(func(ctx context.Context, job *CronJob) (string, error) {
		execution, _ := ExecutionFromContext(ctx)
		got <- execution
		return "ok", nil
	})
	cs.mu.Lock()
	cs.running = true
	cs.mu.Unlock()
	cs.Tick()
	cs.mu.Lock()
	cs.running = false
	cs.mu.Unlock()

	select {
	case execution := <-got:
		if execution.Source != ExecutionSourceScheduled {
			t.Fatalf("source = %q, want scheduled", execution.Source)
		}
		if execution.JobID != job.ID || execution.ScheduledAtMS == nil ||
			*execution.ScheduledAtMS != at {
			t.Fatalf("unexpected scheduled execution: %+v", execution)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled job did not run")
	}
}
