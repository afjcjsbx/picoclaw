package cron

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func cronListCmd(ctx context.Context, store jobStore) error {
	jobs, err := store.List(ctx)
	if err != nil {
		return err
	}

	if len(jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return nil
	}

	fmt.Println("\nScheduled Jobs:")
	fmt.Println("----------------")
	for _, job := range jobs {
		var schedule string
		if job.Schedule.Kind == "every" && job.Schedule.EveryMS != nil {
			schedule = fmt.Sprintf("every %ds", *job.Schedule.EveryMS/1000)
		} else if job.Schedule.Kind == "cron" {
			schedule = job.Schedule.Expr
		} else {
			schedule = "one-time"
		}

		nextRun := "scheduled"
		if job.State.NextRunAtMS != nil {
			nextTime := time.UnixMilli(*job.State.NextRunAtMS)
			nextRun = nextTime.Format("2006-01-02 15:04")
		}

		status := "enabled"
		if !job.Enabled {
			status = "disabled"
		}

		fmt.Printf("  %s (%s)\n", job.Name, job.ID)
		fmt.Printf("    Schedule: %s\n", schedule)
		fmt.Printf("    Status: %s\n", status)
		fmt.Printf("    Next run: %s\n", nextRun)
	}
	return nil
}

func cronAddCmd(
	ctx context.Context,
	store jobStore,
	name string,
	schedule cron.CronSchedule,
	message, channel, to string,
) error {
	job, err := store.Add(ctx, name, schedule, message, channel, to)
	if err != nil {
		return fmt.Errorf("error adding job: %w", err)
	}

	fmt.Printf("✓ Added job '%s' (%s)\n", job.Name, job.ID)
	return nil
}

func cronRemoveCmd(ctx context.Context, store jobStore, jobID string) error {
	removed, err := store.Remove(ctx, jobID)
	if err != nil {
		return err
	}
	if removed {
		fmt.Printf("✓ Removed job %s\n", jobID)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
	return nil
}

func cronSetJobEnabled(ctx context.Context, store jobStore, jobID string, enabled bool) error {
	job, err := store.SetEnabled(ctx, jobID, enabled)
	if errors.Is(err, cron.ErrJobNotFound) {
		fmt.Printf("✗ Job %s not found\n", jobID)
		return nil
	}
	if err != nil {
		return err
	}

	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Printf("✓ Job '%s' %s\n", job.Name, state)
	return nil
}
