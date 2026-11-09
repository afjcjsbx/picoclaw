package agent

import (
	"testing"

	"github.com/sipeed/picoclaw/pkg/cron"
	runtimeevents "github.com/sipeed/picoclaw/pkg/events"
)

func TestCronExecutionHookMetaRoundTrip(t *testing.T) {
	scheduledAt := int64(123456)
	meta := HookMeta{
		AgentID: "main",
		Cron: &cron.Execution{
			ExecutionID:    "exec-1",
			JobID:          "job-1",
			JobName:        "nightly",
			DeclarationKey: "plugin:demo:nightly",
			Source:         cron.ExecutionSourceScheduled,
			ScheduledAtMS:  &scheduledAt,
		},
	}
	evt := runtimeevents.Event{
		Kind:  runtimeevents.KindAgentTurnStart,
		Attrs: runtimeAttrsFromHookMeta(meta),
	}
	got := hookMetaFromRuntimeEvent(evt)
	if got.Cron == nil {
		t.Fatal("cron execution was not reconstructed from event attrs")
	}
	if got.Cron.JobID != "job-1" || got.Cron.ExecutionID != "exec-1" ||
		got.Cron.DeclarationKey != "plugin:demo:nightly" ||
		got.Cron.Source != cron.ExecutionSourceScheduled ||
		got.Cron.ScheduledAtMS == nil || *got.Cron.ScheduledAtMS != scheduledAt {
		t.Fatalf("unexpected cron execution: %+v", got.Cron)
	}
}

func TestCloneHookMetaCopiesCronExecution(t *testing.T) {
	scheduledAt := int64(7)
	meta := HookMeta{Cron: &cron.Execution{
		JobID:         "job",
		ScheduledAtMS: &scheduledAt,
	}}
	cloned := cloneHookMeta(meta)
	cloned.Cron.JobID = "changed"
	*cloned.Cron.ScheduledAtMS = 9
	if meta.Cron.JobID != "job" || *meta.Cron.ScheduledAtMS != 7 {
		t.Fatalf("clone aliased cron execution: %+v", meta.Cron)
	}
}
