package cron

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cronpkg "github.com/sipeed/picoclaw/pkg/cron"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe() error = %v", err)
	}
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = oldStdout
	defer r.Close()

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("io.Copy() error = %v", err)
	}
	return buf.String()
}

func TestCronSetJobEnabledReportsActualState(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := cronpkg.NewCronService(storePath, nil)
	everyMS := int64(60_000)
	job, err := cs.AddJob("nightly", cronpkg.CronSchedule{Kind: "every", EveryMS: &everyMS}, "msg", "", "")
	if err != nil {
		t.Fatalf("AddJob() error = %v", err)
	}

	disabled := captureStdout(t, func() {
		if err := cronSetJobEnabled(context.Background(), fileStore{path: storePath}, job.ID, false); err != nil {
			t.Fatalf("disable error = %v", err)
		}
	})
	if !strings.Contains(disabled, "disabled") {
		t.Fatalf("disable output = %q, want it to mention disabled", disabled)
	}
	if strings.Contains(disabled, "enabled") {
		t.Fatalf("disable output = %q, must not claim the job is enabled", disabled)
	}

	enabled := captureStdout(t, func() {
		if err := cronSetJobEnabled(context.Background(), fileStore{path: storePath}, job.ID, true); err != nil {
			t.Fatalf("enable error = %v", err)
		}
	})
	if !strings.Contains(enabled, "enabled") {
		t.Fatalf("enable output = %q, want it to mention enabled", enabled)
	}
}
