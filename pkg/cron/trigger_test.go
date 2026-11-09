package cron

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type testTrigger struct {
	run func(context.Context, TriggerScheduler)
}

func (t testTrigger) Run(ctx context.Context, scheduler TriggerScheduler) {
	t.run(ctx, scheduler)
}

func TestCronServiceUsesRegisteredTriggerForExecution(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "jobs.json")
	cs := NewCronService(storePath, nil)
	t.Cleanup(func() {
		cs.Stop()
		waitAllIdle(t, cs)
	})

	ran := make(chan Execution, 1)
	cs.SetOnJobWithContext(func(ctx context.Context, _ *CronJob) (string, error) {
		execution, _ := ExecutionFromContext(ctx)
		ran <- execution
		return "ok", nil
	})

	at := time.Now().Add(100 * time.Millisecond).UnixMilli()
	job, err := cs.AddJob("triggered", CronSchedule{Kind: "at", AtMS: &at}, "msg", "", "")
	if err != nil {
		t.Fatalf("AddJob: %v", err)
	}

	var once sync.Once
	trigger := testTrigger{run: func(ctx context.Context, scheduler TriggerScheduler) {
		// The trigger only decides when to tick; execution stays in CronService.
		go func() {
			time.Sleep(200 * time.Millisecond)
			once.Do(scheduler.Tick)
		}()
		<-ctx.Done()
	}}
	if err := cs.SetTrigger(trigger); err != nil {
		t.Fatalf("SetTrigger: %v", err)
	}
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	select {
	case execution := <-ran:
		if execution.JobID != job.ID || execution.Source != ExecutionSourceScheduled {
			t.Fatalf("unexpected execution: %+v", execution)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("registered trigger did not execute the job")
	}
}

func TestNewTrigger(t *testing.T) {
	trigger, err := NewTrigger("")
	if err != nil || trigger == nil {
		t.Fatalf("default trigger: %v", err)
	}
	if _, err := NewTrigger("missing"); !errors.Is(err, ErrUnknownTrigger) {
		t.Fatalf("missing trigger error = %v, want ErrUnknownTrigger", err)
	}
	if names := TriggerNames(); len(names) == 0 || names[0] != "inprocess" {
		t.Fatalf("unexpected trigger names: %v", names)
	}
}

func TestSetTriggerRejectedWhileRunning(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)
	if err := cs.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(cs.Stop)
	if err := cs.SetTrigger(testTrigger{run: func(context.Context, TriggerScheduler) {}}); err == nil {
		t.Fatal("SetTrigger accepted a change while running")
	}
}
