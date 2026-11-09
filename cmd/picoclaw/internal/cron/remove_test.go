package cron

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cronpkg "github.com/sipeed/picoclaw/pkg/cron"
)

func TestNewRemoveSubcommand(t *testing.T) {
	fn := func() string { return "" }
	cmd := newRemoveCommand(fn)

	require.NotNil(t, cmd)

	assert.Equal(t, "Remove a job by ID", cmd.Short)

	assert.True(t, cmd.HasExample())
	assert.NotNil(t, cmd.Flags().Lookup("force"))
}

func TestCronRemoveManagedJobRequiresForce(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "cron", "jobs.json")
	cs := cronpkg.NewCronService(storePath, nil)
	everyMS := int64(60_000)
	decl := cronpkg.CronJobDeclaration{
		DeclarationKey: "plugin:demo:nightly",
		Name:           "nightly",
		Schedule:       cronpkg.CronSchedule{Kind: "every", EveryMS: &everyMS},
		Message:        "Run",
	}
	if _, err := cs.ReconcileDeclarations("plugin:", []cronpkg.CronJobDeclaration{decl}, true); err != nil {
		t.Fatalf("ReconcileDeclarations() error = %v", err)
	}
	job, ok := cs.GetJobByDeclarationKey(decl.DeclarationKey)
	if !ok {
		t.Fatal("managed job missing")
	}

	out := captureStdout(t, func() {
		cronRemoveCmd(storePath, job.ID, false)
	})
	if !strings.Contains(out, "managed by plugin:demo:nightly") {
		t.Fatalf("remove output = %q, want managed warning", out)
	}
	if _, ok := cs.GetJob(job.ID); !ok {
		t.Fatal("managed job was removed without --force")
	}

	out = captureStdout(t, func() {
		cronRemoveCmd(storePath, job.ID, true)
	})
	if !strings.Contains(out, "reconciliation may recreate it") {
		t.Fatalf("forced remove output = %q, want recreate warning", out)
	}
	fresh := cronpkg.NewCronService(storePath, nil)
	if _, ok := fresh.GetJob(job.ID); ok {
		t.Fatal("managed job was not removed with --force")
	}
}
