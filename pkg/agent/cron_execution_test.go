package agent

import (
	"context"
	"sync"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
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

func TestTurnStateInheritCronExecution(t *testing.T) {
	ts := &turnState{
		agentID:       "main",
		cronExecution: &cron.Execution{JobID: "job-1", ExecutionID: "exec-1"},
	}
	msg := ts.inheritCronExecution(bus.InboundMessage{})
	if msg.AgentID != "main" || msg.CronExecution == nil ||
		msg.CronExecution.JobID != "job-1" {
		t.Fatalf("inherited message = %+v", msg)
	}
	if msg.CronExecution == ts.cronExecution {
		t.Fatal("follow-up shared the turn execution pointer")
	}

	existing := &cron.Execution{JobID: "job-2"}
	msg = ts.inheritCronExecution(bus.InboundMessage{
		AgentID:       "sales",
		CronExecution: existing,
	})
	if msg.AgentID != "sales" || msg.CronExecution != existing {
		t.Fatalf("existing message fields were overwritten: %+v", msg)
	}
}

type cronCaptureHook struct {
	mu   sync.Mutex
	seen []*cron.Execution
}

func (h *cronCaptureHook) BeforeLLM(
	_ context.Context,
	req *LLMHookRequest,
) (*LLMHookRequest, HookDecision, error) {
	h.mu.Lock()
	h.seen = append(h.seen, cron.CloneExecution(req.Meta.Cron))
	h.mu.Unlock()
	return req, HookDecision{Action: HookActionContinue}, nil
}

func (h *cronCaptureHook) AfterLLM(
	_ context.Context,
	resp *LLMHookResponse,
) (*LLMHookResponse, HookDecision, error) {
	return resp, HookDecision{Action: HookActionContinue}, nil
}

func (h *cronCaptureHook) executions() []*cron.Execution {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]*cron.Execution(nil), h.seen...)
}

func TestCronExecutionReachesDirectAndSystemTurns(t *testing.T) {
	al, cfg, msgBus, provider, cleanup := newTestAgentLoop(t)
	defer cleanup()
	defer al.Close()
	if cfg == nil || msgBus == nil || provider == nil {
		t.Fatal("incomplete test harness")
	}
	hook := &cronCaptureHook{}
	if err := al.MountHook(NamedHook("cron-capture", hook)); err != nil {
		t.Fatal(err)
	}

	execution := cron.Execution{
		ExecutionID: "exec-1",
		JobID:       "job-1",
		JobName:     "nightly",
		Source:      cron.ExecutionSourceScheduled,
	}
	if _, err := al.ProcessDirectWithChannel(
		cron.WithExecution(context.Background(), execution),
		"hello", "agent:cron-job-1", "cli", "direct",
	); err != nil {
		t.Fatalf("direct turn: %v", err)
	}

	msg := bus.InboundMessage{
		Context: bus.InboundContext{
			Channel:  "system",
			ChatID:   "telegram:123",
			SenderID: "async:tool",
		},
		SessionKey:    "agent:main:system",
		AgentID:       "main",
		Content:       "tool result",
		CronExecution: &execution,
	}
	if _, err := al.processMessage(context.Background(), msg); err != nil {
		t.Fatalf("system turn: %v", err)
	}

	seen := hook.executions()
	if len(seen) != 2 {
		t.Fatalf("captured %d executions, want 2", len(seen))
	}
	for _, got := range seen {
		if got == nil || got.JobID != "job-1" || got.ExecutionID != "exec-1" {
			t.Fatalf("unexpected execution: %+v", got)
		}
	}
}

func TestSubTurnProcessOptionsCarryCronExecution(t *testing.T) {
	parent := &turnState{
		agentID:       "main",
		cronExecution: &cron.Execution{JobID: "job-1", ExecutionID: "exec-1"},
	}
	opts := subTurnProcessOptions(
		parent,
		DispatchRequest{SessionKey: "agent:sub-1"},
		SubTurnConfig{},
	)
	if opts.CronExecution == nil || opts.CronExecution.JobID != "job-1" {
		t.Fatalf("sub-turn execution = %+v", opts.CronExecution)
	}
	if opts.CronExecution == parent.cronExecution {
		t.Fatal("sub-turn shared the parent execution pointer")
	}
}
