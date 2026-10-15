package gateway

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func newStartedCronService(t *testing.T, ran chan<- string) (*cron.CronService, string) {
	t.Helper()
	cs := cron.NewCronService(filepath.Join(t.TempDir(), "jobs.json"), func(job *cron.CronJob) (string, error) {
		ran <- job.ID
		return "ok", nil
	})
	if err := cs.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(cs.Stop)

	everyMS := time.Hour.Milliseconds()
	job, err := cs.AddJob("hourly", cron.CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "cli", "direct")
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	return cs, job.ID
}

func TestServices_RunCronJob(t *testing.T) {
	s := &services{}
	if err := s.runCronJob("x"); err == nil {
		t.Fatal("runCronJob without a cron service should fail")
	}

	ran := make(chan string, 1)
	cs, jobID := newStartedCronService(t, ran)
	s.setCronService(cs)

	if err := s.runCronJob(jobID); err != nil {
		t.Fatalf("runCronJob() error: %v", err)
	}
	select {
	case got := <-ran:
		if got != jobID {
			t.Fatalf("ran %s, want %s", got, jobID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job was not executed")
	}

	if err := s.runCronJob("missing"); !errors.Is(err, cron.ErrJobNotFound) {
		t.Fatalf("runCronJob(missing) error = %v, want ErrJobNotFound", err)
	}
	// Let the run finish recording its state before the temp dir is removed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := cs.GetJob(jobID); ok && job.State.LastStatus != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job never recorded its result")
}

// A config reload swaps the cron service while /cron/run may be reading it;
// run with -race.
func TestServices_RunCronJobConcurrentWithServiceSwap(t *testing.T) {
	s := &services{}
	ran := make(chan string, 1024)
	first, _ := newStartedCronService(t, ran)
	second, _ := newStartedCronService(t, ran)
	s.setCronService(first)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 200 {
			if i%2 == 0 {
				s.setCronService(second)
			} else {
				s.setCronService(first)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range 200 {
			_ = s.runCronJob("missing")
		}
	}()
	wg.Wait()
}
