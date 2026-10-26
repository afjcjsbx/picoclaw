package cron

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAddJobRejectsInvalidSchedules(t *testing.T) {
	cases := map[string]CronSchedule{
		"missing kind":            {},
		"unknown kind":            {Kind: "sometimes"},
		"every without interval":  {Kind: "every"},
		"every zero":              {Kind: "every", EveryMS: int64Ptr(0)},
		"every negative":          {Kind: "every", EveryMS: int64Ptr(-1)},
		"cron without expression": {Kind: "cron"},
		"cron blank expression":   {Kind: "cron", Expr: "   "},
		"cron malformed":          {Kind: "cron", Expr: "not a cron"},
		"cron bad timezone":       {Kind: "cron", Expr: "*/5 * * * *", TZ: "Mars/Olympus"},
		"at without timestamp":    {Kind: "at"},
	}

	for name, schedule := range cases {
		t.Run(name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "jobs.json")
			cs := NewCronService(storePath, nil)

			if _, err := cs.AddJob("bad", schedule, "msg", "", ""); err == nil {
				t.Fatalf("AddJob() accepted invalid schedule %+v", schedule)
			}
			if jobs := cs.ListJobs(true); len(jobs) != 0 {
				t.Fatalf("invalid job was stored: %+v", jobs)
			}
		})
	}
}

func TestAddJobAcceptsValidSchedules(t *testing.T) {
	every := int64(60_000)
	at := time.Now().Add(time.Hour).UnixMilli()
	cases := map[string]CronSchedule{
		"every":         {Kind: "every", EveryMS: &every},
		"cron":          {Kind: "cron", Expr: "*/5 * * * *"},
		"cron with tz":  {Kind: "cron", Expr: "0 9 * * *", TZ: "Europe/Rome"},
		"one time at":   {Kind: "at", AtMS: &at},
		"past one time": {Kind: "at", AtMS: int64Ptr(1)},
	}

	for name, schedule := range cases {
		t.Run(name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "jobs.json")
			cs := NewCronService(storePath, nil)

			job, err := cs.AddJob("ok", schedule, "msg", "", "")
			if err != nil {
				t.Fatalf("AddJob() error = %v", err)
			}
			if job == nil || job.ID == "" {
				t.Fatalf("AddJob() returned %+v, want a stored job", job)
			}
		})
	}
}

func TestAddJobInvalidCronErrorMentionsExpression(t *testing.T) {
	cs := NewCronService(filepath.Join(t.TempDir(), "jobs.json"), nil)

	_, err := cs.AddJob("bad", CronSchedule{Kind: "cron", Expr: "not a cron"}, "msg", "", "")
	if err == nil {
		t.Fatal("AddJob() accepted an invalid cron expression")
	}
	if !strings.Contains(err.Error(), "not a cron") {
		t.Fatalf("error %q does not mention the rejected expression", err)
	}
}
