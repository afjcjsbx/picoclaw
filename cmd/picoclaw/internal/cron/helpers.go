package cron

import (
	"fmt"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

func cronListCmd(storePath string) {
	cs := cron.NewCronService(storePath, nil)
	jobs := cs.ListJobs(true) // Show all jobs, including disabled

	if len(jobs) == 0 {
		fmt.Println("No scheduled jobs.")
		return
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

		nextRun := "not scheduled"
		switch {
		case !job.Enabled:
			nextRun = "disabled"
		case job.State.NextRunAtMS != nil:
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
		if job.DeclarationKey != "" {
			fmt.Printf("    Managed: %s\n", job.DeclarationKey)
		}
	}
}

func cronRemoveCmd(storePath, jobID string, force bool) {
	cs := cron.NewCronService(storePath, nil)
	job, ok := cs.GetJob(jobID)
	if !ok {
		fmt.Printf("✗ Job %s not found\n", jobID)
		return
	}
	if job.DeclarationKey != "" {
		if !force {
			fmt.Printf("✗ Job '%s' is managed by %s\n", job.Name, job.DeclarationKey)
			fmt.Println("  Disable the plugin or remove its declaration; use --force to remove the stored job anyway.")
			return
		}
		fmt.Printf("⚠ Removing managed job '%s' (%s); reconciliation may recreate it.\n", job.Name, job.DeclarationKey)
	}
	if cs.RemoveJob(jobID) {
		fmt.Printf("✓ Removed job %s\n", jobID)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
}

func cronSetJobEnabled(storePath, jobID string, enabled bool) {
	cs := cron.NewCronService(storePath, nil)
	job := cs.EnableJob(jobID, enabled)
	if job != nil {
		state := "enabled"
		if !enabled {
			state = "disabled"
		}
		fmt.Printf("✓ Job '%s' %s\n", job.Name, state)
	} else {
		fmt.Printf("✗ Job %s not found\n", jobID)
	}
}
