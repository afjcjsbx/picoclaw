package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/logger"
)

type stubJobExecutor struct {
	response        string
	err             error
	alreadySent     bool // simulate message tool having already sent in this round
	lastPrompt      string
	lastKey         string
	lastChan        string
	lastChatID      string
	publishedResp   string
	publishedChan   string
	publishedChatID string
	publishedKey    string
}

func (s *stubJobExecutor) ProcessDirectWithChannel(
	_ context.Context,
	content, sessionKey, channel, chatID string,
) (string, error) {
	s.lastPrompt = content
	s.lastKey = sessionKey
	s.lastChan = channel
	s.lastChatID = chatID
	return s.response, s.err
}

func (s *stubJobExecutor) PublishResponseIfNeeded(
	_ context.Context,
	channel, chatID, sessionKey, response string,
) {
	if s.alreadySent {
		return
	}
	s.publishedResp = response
	s.publishedChan = channel
	s.publishedChatID = chatID
	s.publishedKey = sessionKey
}

func newTestCronToolWithExecutorAndConfig(t *testing.T, executor JobExecutor, cfg *config.Config) *CronTool {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "cron.json")
	cronService := cron.NewCronService(storePath, nil)
	msgBus := bus.NewMessageBus()
	tool, err := NewCronTool(cronService, executor, msgBus, t.TempDir(), true, 0, cfg)
	if err != nil {
		t.Fatalf("NewCronTool() error: %v", err)
	}
	return tool
}

func newTestCronToolWithConfig(t *testing.T, cfg *config.Config) *CronTool {
	t.Helper()
	return newTestCronToolWithExecutorAndConfig(t, nil, cfg)
}

func newTestCronTool(t *testing.T) *CronTool {
	t.Helper()
	return newTestCronToolWithConfig(t, config.DefaultConfig())
}

// mustExecuteJobOK runs a job that is expected to succeed.
func mustExecuteJobOK(t *testing.T, tool *CronTool, job *cron.CronJob) string {
	t.Helper()
	got, err := tool.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("ExecuteJob() error = %v, want nil", err)
	}
	if got != "ok" {
		t.Fatalf("ExecuteJob() = %q, want ok", got)
	}
	return got
}

func parseCronJobResult(t *testing.T, result *ToolResult) cron.CronJob {
	t.Helper()
	text := result.ForLLM
	if idx := strings.Index(text, "{"); idx >= 0 {
		text = text[idx:]
	}
	var job cron.CronJob
	if err := json.Unmarshal([]byte(text), &job); err != nil {
		t.Fatalf("failed to parse cron job JSON %q: %v", result.ForLLM, err)
	}
	return job
}

func addTestCronJob(t *testing.T, tool *CronTool, name, channel, chatID, command string) *cron.CronJob {
	t.Helper()
	everyMS := int64(60_000)
	job, err := tool.cronService.AddJob(
		name,
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		name+" message",
		channel,
		chatID,
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	if command != "" {
		job.Payload.Command = command
		if err := tool.cronService.UpdateJob(job); err != nil {
			t.Fatalf("UpdateJob() error: %v", err)
		}
	}
	return job
}

// TestCronTool_CommandBlockedFromRemoteChannel verifies command scheduling is restricted by default.
func TestCronTool_CommandBlockedFromRemoteChannel(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "check disk",
		"command":         "df -h",
		"command_confirm": true,
		"at_seconds":      float64(60),
	})

	if !result.IsError {
		t.Fatal("expected command scheduling to be blocked from remote channel")
	}
	if !strings.Contains(result.ForLLM, "restricted to internal channels or configured remote channels") {
		t.Errorf("expected remote restriction message, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedFromRemoteChannelAllowlist(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram"}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if result.IsError {
		t.Fatalf("expected command scheduling from allowed remote channel to succeed, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedFromRemoteChatIDAllowlist(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{" telegram:1234567890 "}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "1234567890")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if result.IsError {
		t.Fatalf("expected command scheduling from allowed remote chat to succeed, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedFromRemoteWildcardAllowlist(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"*"}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if result.IsError {
		t.Fatalf("expected wildcard allowlist to allow remote command scheduling, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedRemoteWildcardRequiresNonEmptyChannel(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"*"}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if !result.IsError {
		t.Fatal("expected missing channel to remain blocked even with wildcard allowlist")
	}
	if !strings.Contains(result.ForLLM, "no session context") {
		t.Errorf("expected session context error, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandBlockedFromDifferentRemoteChatID(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram:1234567890"}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "other-chat")
	result := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "check disk",
		"command":         "df -h",
		"command_confirm": true,
		"at_seconds":      float64(60),
	})

	if !result.IsError {
		t.Fatal("expected command scheduling from non-allowlisted remote chat to fail")
	}
	if !strings.Contains(result.ForLLM, "restricted to internal channels or configured remote channels") {
		t.Errorf("expected remote restriction message, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedRemoteRequiresConfirmWhenAllowCommandDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.AllowCommand = false
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram"}

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if !result.IsError {
		t.Fatal("expected allowlisted remote command scheduling to require confirm when allow_command is disabled")
	}
	if !strings.Contains(result.ForLLM, "command_confirm=true") {
		t.Errorf("expected command_confirm requirement message, got: %s", result.ForLLM)
	}
}

func TestCronTool_AllowCommandDoesNotBypassRemoteAllowlist(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.AllowCommand = true

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if !result.IsError {
		t.Fatal("expected allow_command=true not to bypass remote allowlist")
	}
	if !strings.Contains(result.ForLLM, "restricted to internal channels or configured remote channels") {
		t.Errorf("expected remote restriction message, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandDoesNotRequireConfirmByDefault(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if result.IsError {
		t.Fatalf("expected command scheduling without confirm to succeed by default, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Cron job added") {
		t.Errorf("expected 'Cron job added', got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandRequiresConfirmWhenAllowCommandDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.AllowCommand = false

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "check disk",
		"command":    "df -h",
		"at_seconds": float64(60),
	})

	if !result.IsError {
		t.Fatal("expected command scheduling to require confirm when allow_command is disabled")
	}
	if !strings.Contains(result.ForLLM, "command_confirm=true") {
		t.Errorf("expected command_confirm requirement message, got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandAllowedWithConfirmWhenAllowCommandDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.AllowCommand = false

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "check disk",
		"command":         "df -h",
		"command_confirm": true,
		"at_seconds":      float64(60),
	})

	if result.IsError {
		t.Fatalf(
			"expected command scheduling with confirm to succeed when allow_command is disabled, got: %s",
			result.ForLLM,
		)
	}
	if !strings.Contains(result.ForLLM, "Cron job added") {
		t.Errorf("expected 'Cron job added', got: %s", result.ForLLM)
	}
}

func TestCronTool_CommandBlockedWhenExecDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Exec.Enabled = false

	tool := newTestCronToolWithConfig(t, cfg)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "check disk",
		"command":         "df -h",
		"command_confirm": true,
		"at_seconds":      float64(60),
	})

	if !result.IsError {
		t.Fatal("expected command scheduling to be blocked when exec is disabled")
	}
	if !strings.Contains(result.ForLLM, "command execution is disabled") {
		t.Errorf("expected exec disabled message, got: %s", result.ForLLM)
	}
}

// TestCronTool_CommandAllowedFromInternalChannel verifies command scheduling works from internal channels
func TestCronTool_CommandAllowedFromInternalChannel(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	result := tool.Execute(ctx, map[string]any{
		"action":          "add",
		"message":         "check disk",
		"command":         "df -h",
		"command_confirm": true,
		"at_seconds":      float64(60),
	})

	if result.IsError {
		t.Fatalf("expected command scheduling to succeed from internal channel, got: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, "Cron job added") {
		t.Errorf("expected 'Cron job added', got: %s", result.ForLLM)
	}
}

// TestCronTool_AddJobRequiresSessionContext verifies fail-closed when channel/chatID missing
func TestCronTool_AddJobRequiresSessionContext(t *testing.T) {
	tool := newTestCronTool(t)
	result := tool.Execute(context.Background(), map[string]any{
		"action":     "add",
		"message":    "reminder",
		"at_seconds": float64(60),
	})

	if !result.IsError {
		t.Fatal("expected error when session context is missing")
	}
	if !strings.Contains(result.ForLLM, "no session context") {
		t.Errorf("expected 'no session context' message, got: %s", result.ForLLM)
	}
}

// TestCronTool_NonCommandJobAllowedFromRemoteChannel verifies regular reminders work from any channel
func TestCronTool_NonCommandJobAllowedFromRemoteChannel(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	result := tool.Execute(ctx, map[string]any{
		"action":     "add",
		"message":    "time to stretch",
		"at_seconds": float64(600),
	})

	if result.IsError {
		t.Fatalf("expected non-command reminder to succeed from remote channel, got: %s", result.ForLLM)
	}
}

func TestCronTool_GetReturnsFullJobPayload(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	everyMS := int64(60_000)
	message := strings.Repeat("daily briefing details ", 8)
	job, err := tool.cronService.AddJob(
		"daily",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		message,
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action": "get",
		"job_id": job.ID,
	})

	if result.IsError {
		t.Fatalf("get failed: %s", result.ForLLM)
	}
	got := parseCronJobResult(t, result)
	if got.ID != job.ID || got.Payload.Message != message || got.Payload.Channel != "telegram" ||
		got.Payload.To != "chat-1" {
		t.Fatalf("get returned wrong payload: %+v", got)
	}
	if got.Schedule.Kind != "every" || got.Schedule.EveryMS == nil || *got.Schedule.EveryMS != everyMS {
		t.Fatalf("get returned wrong schedule: %+v", got.Schedule)
	}
	if got.State.NextRunAtMS == nil {
		t.Fatal("get should include next run state")
	}
}

func TestCronTool_UpdateSchedulePreservesPayload(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	original, err := tool.cronService.AddJob(
		"AI daily",
		cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
		"fetch RSS, include source links",
		"weixin",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{
		"action":    "update",
		"job_id":    original.ID,
		"cron_expr": "30 10 * * *",
	})

	if result.IsError {
		t.Fatalf("update failed: %s", result.ForLLM)
	}
	updated, ok := tool.cronService.GetJob(original.ID)
	if !ok {
		t.Fatal("updated job not found")
	}
	if updated.ID != original.ID || updated.CreatedAtMS != original.CreatedAtMS {
		t.Fatalf("identity changed after update: before=%+v after=%+v", original, updated)
	}
	if updated.Payload.Message != original.Payload.Message || updated.Payload.Channel != original.Payload.Channel ||
		updated.Payload.To != original.Payload.To {
		t.Fatalf("payload was not preserved: %+v", updated.Payload)
	}
	if updated.Schedule.Kind != "cron" || updated.Schedule.Expr != "30 10 * * *" {
		t.Fatalf("schedule not updated: %+v", updated.Schedule)
	}
	if updated.DeleteAfterRun {
		t.Fatal("cron schedule should not delete after run")
	}
}

func TestCronTool_UpdateMessagePreservesScheduleAndNextRun(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	everyMS := int64(120_000)
	original, err := tool.cronService.AddJob(
		"reminder",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"old message",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	if original.State.NextRunAtMS == nil {
		t.Fatal("expected original next run")
	}
	nextRunBefore := *original.State.NextRunAtMS

	result := tool.Execute(ctx, map[string]any{
		"action":  "update",
		"job_id":  original.ID,
		"message": "new message",
	})

	if result.IsError {
		t.Fatalf("update failed: %s", result.ForLLM)
	}
	updated, _ := tool.cronService.GetJob(original.ID)
	if updated.Payload.Message != "new message" {
		t.Fatalf("message not updated: %+v", updated.Payload)
	}
	if updated.Name != "reminder" {
		t.Fatalf("name should be preserved, got %q", updated.Name)
	}
	if updated.Schedule.Kind != "every" || updated.Schedule.EveryMS == nil || *updated.Schedule.EveryMS != everyMS {
		t.Fatalf("schedule should be preserved: %+v", updated.Schedule)
	}
	if updated.State.NextRunAtMS == nil || *updated.State.NextRunAtMS != nextRunBefore {
		t.Fatalf("next run should be preserved: before=%d after=%v", nextRunBefore, updated.State.NextRunAtMS)
	}
}

func TestCronTool_UpdateValidationErrors(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	job, err := tool.cronService.AddJob(
		"job",
		cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
		"message",
		"cli",
		"direct",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{
			name: "invalid job id",
			args: map[string]any{"action": "update", "job_id": "missing", "message": "new"},
			want: "not found",
		},
		{
			name: "missing patch",
			args: map[string]any{"action": "update", "job_id": job.ID},
			want: "at least one update field",
		},
		{
			name: "multiple schedule fields",
			args: map[string]any{
				"action":        "update",
				"job_id":        job.ID,
				"every_seconds": float64(60),
				"cron_expr":     "0 9 * * *",
			},
			want: "only one of",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(ctx, tt.args)
			if !result.IsError {
				t.Fatalf("expected error, got: %s", result.ForLLM)
			}
			if !strings.Contains(result.ForLLM, tt.want) {
				t.Fatalf("error = %q, want substring %q", result.ForLLM, tt.want)
			}
		})
	}
}

func TestCronTool_ListFiltersJobsForRemoteChannel(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")
	everyMS := int64(60_000)

	ownJob, err := tool.cronService.AddJob(
		"own",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"visible",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	otherChatJob, err := tool.cronService.AddJob(
		"other-chat",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"hidden",
		"telegram",
		"chat-2",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	otherChannelJob, err := tool.cronService.AddJob(
		"other-channel",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"hidden",
		"feishu",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	commandJob, err := tool.cronService.AddJob(
		"command",
		cron.CronSchedule{Kind: "every", EveryMS: &everyMS},
		"hidden command",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	commandJob.Payload.Command = "df -h"
	if err := tool.cronService.UpdateJob(commandJob); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}

	result := tool.Execute(ctx, map[string]any{"action": "list"})

	if result.IsError {
		t.Fatalf("list failed: %s", result.ForLLM)
	}
	if !strings.Contains(result.ForLLM, ownJob.ID) {
		t.Fatalf("list should include own job %s, got: %s", ownJob.ID, result.ForLLM)
	}
	for _, hiddenID := range []string{otherChatJob.ID, otherChannelJob.ID, commandJob.ID} {
		if strings.Contains(result.ForLLM, hiddenID) {
			t.Fatalf("list should not include hidden job %s, got: %s", hiddenID, result.ForLLM)
		}
	}
}

func TestCronTool_RemoteCannotAccessOtherChatJob(t *testing.T) {
	tool := newTestCronTool(t)
	job, err := tool.cronService.AddJob(
		"private",
		cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
		"secret",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	ctx := WithToolContext(context.Background(), "telegram", "chat-2")

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if !getResult.IsError || !strings.Contains(getResult.ForLLM, "not accessible") {
		t.Fatalf("expected inaccessible get, got: %+v", getResult)
	}

	updateResult := tool.Execute(ctx, map[string]any{"action": "update", "job_id": job.ID, "message": "changed"})
	if !updateResult.IsError || !strings.Contains(updateResult.ForLLM, "not accessible") {
		t.Fatalf("expected inaccessible update, got: %+v", updateResult)
	}
	unchanged, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job should still exist")
	}
	if unchanged.Payload.Message != "secret" {
		t.Fatalf("unauthorized update mutated job: %+v", unchanged.Payload)
	}
}

func TestCronTool_RemoteCannotAccessCommandJob(t *testing.T) {
	tool := newTestCronTool(t)
	job, err := tool.cronService.AddJob(
		"command",
		cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
		"run command",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	job.Payload.Command = "df -h"
	if err := tool.cronService.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if !getResult.IsError || !strings.Contains(getResult.ForLLM, "not accessible") {
		t.Fatalf("expected inaccessible get, got: %+v", getResult)
	}

	updateResult := tool.Execute(ctx, map[string]any{"action": "update", "job_id": job.ID, "message": "changed"})
	if !updateResult.IsError || !strings.Contains(updateResult.ForLLM, "not accessible") {
		t.Fatalf("expected inaccessible update, got: %+v", updateResult)
	}
	unchanged, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job should still exist")
	}
	if unchanged.Payload.Message != "run command" || unchanged.Payload.Command != "df -h" {
		t.Fatalf("unauthorized update mutated command job: %+v", unchanged.Payload)
	}
}

func TestCronTool_AllowlistedRemoteCanAccessOwnCommandJob(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram:chat-1"}
	tool := newTestCronToolWithConfig(t, cfg)
	job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError || !strings.Contains(listResult.ForLLM, job.ID) {
		t.Fatalf("expected list to include own command job, got: %+v", listResult)
	}

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if getResult.IsError {
		t.Fatalf("expected get to access own command job, got: %s", getResult.ForLLM)
	}
	got := parseCronJobResult(t, getResult)
	if got.ID != job.ID || got.Payload.Command != "df -h" {
		t.Fatalf("get returned wrong command job: %+v", got)
	}

	updateResult := tool.Execute(ctx, map[string]any{
		"action":  "update",
		"job_id":  job.ID,
		"message": "updated command description",
	})
	if updateResult.IsError {
		t.Fatalf("expected update to access own command job, got: %s", updateResult.ForLLM)
	}
	updated, _ := tool.cronService.GetJob(job.ID)
	if updated.Payload.Message != "updated command description" || updated.Payload.Command != "df -h" {
		t.Fatalf("update returned wrong command payload: %+v", updated.Payload)
	}
}

func TestCronTool_AllowlistedRemoteCannotAccessOtherChatCommandJob(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram"}
	tool := newTestCronToolWithConfig(t, cfg)
	job := addTestCronJob(t, tool, "command", "telegram", "chat-2", "df -h")
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError || strings.Contains(listResult.ForLLM, job.ID) {
		t.Fatalf("expected list to hide other chat command job, got: %+v", listResult)
	}

	for _, action := range []string{"get", "update"} {
		args := map[string]any{"action": action, "job_id": job.ID}
		if action == "update" {
			args["message"] = "changed"
		}
		result := tool.Execute(ctx, args)
		if !result.IsError || !strings.Contains(result.ForLLM, "not accessible") {
			t.Fatalf("expected %s to reject other chat command job, got: %+v", action, result)
		}
	}
}

// TestCronTool_PicoJobsAreVisibleAcrossChats verifies job access on the Pico
// channel is scoped to the whole channel: it has a single owner (one shared
// token and the fixed pico-user sender), so every WebUI chat belongs to the
// same owner and a job created in one chat stays manageable from another.
func TestCronTool_PicoJobsAreVisibleAcrossChats(t *testing.T) {
	tool := newTestCronTool(t)
	picoJob := addTestCronJob(t, tool, "daily", "pico", "pico:chat-a", "")
	telegramJob := addTestCronJob(t, tool, "other", "telegram", "chat-1", "")
	ctx := WithToolContext(context.Background(), "pico", "pico:chat-b")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError {
		t.Fatalf("list failed: %s", listResult.ForLLM)
	}
	if !strings.Contains(listResult.ForLLM, picoJob.ID) {
		t.Fatalf("list should include pico job from another chat, got: %s", listResult.ForLLM)
	}
	if strings.Contains(listResult.ForLLM, telegramJob.ID) {
		t.Fatalf("list should not include jobs of other channels, got: %s", listResult.ForLLM)
	}

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": picoJob.ID})
	if getResult.IsError {
		t.Fatalf("get from another pico chat failed: %s", getResult.ForLLM)
	}

	updateResult := tool.Execute(ctx, map[string]any{"action": "update", "job_id": picoJob.ID, "message": "updated"})
	if updateResult.IsError {
		t.Fatalf("update from another pico chat failed: %s", updateResult.ForLLM)
	}
	updated, ok := tool.cronService.GetJob(picoJob.ID)
	if !ok {
		t.Fatal("job should still exist")
	}
	if updated.Payload.Message != "updated" {
		t.Fatalf("update did not apply: %+v", updated.Payload)
	}

	removeResult := tool.Execute(ctx, map[string]any{"action": "remove", "job_id": picoJob.ID})
	if removeResult.IsError {
		t.Fatalf("remove from another pico chat failed: %s", removeResult.ForLLM)
	}
	if _, ok := tool.cronService.GetJob(picoJob.ID); ok {
		t.Fatal("job should have been removed")
	}
}

// TestCronTool_PicoCommandJobsStillRequireAllowlist verifies the
// command_allowed_remotes gate still applies to command jobs on the
// single-owner Pico channel, even though their visibility spans chats.
func TestCronTool_PicoCommandJobsStillRequireAllowlist(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "command", "pico", "pico:chat-a", "df -h")
	ctx := WithToolContext(context.Background(), "pico", "pico:chat-b")

	for _, action := range []string{"get", "update", "run", "remove"} {
		args := map[string]any{"action": action, "job_id": job.ID}
		if action == "update" {
			args["message"] = "changed"
		}
		result := tool.Execute(ctx, args)
		if !result.IsError || !strings.Contains(result.ForLLM, "not accessible") {
			t.Fatalf("expected %s to reject non-allowlisted pico command job, got: %+v", action, result)
		}
	}
	unchanged, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("rejected management must not remove the job")
	}
	if unchanged.Payload.Message != "command message" || unchanged.Payload.Command != "df -h" {
		t.Fatalf("rejected management mutated the job: %+v", unchanged.Payload)
	}

	allowedCfg := config.DefaultConfig()
	allowedCfg.Tools.Cron.CommandAllowedRemotes = []string{"pico"}
	allowedTool := newTestCronToolWithConfig(t, allowedCfg)
	allowedJob := addTestCronJob(t, allowedTool, "command", "pico", "pico:chat-a", "df -h")

	getResult := allowedTool.Execute(ctx, map[string]any{"action": "get", "job_id": allowedJob.ID})
	if getResult.IsError {
		t.Fatalf("allowlisted pico command job should be visible from another chat, got: %s", getResult.ForLLM)
	}
}

// TestCronTool_RunPicoJobFromAnotherChat verifies on-demand runs honor the
// channel-wide access of the single-owner Pico channel.
func TestCronTool_RunPicoJobFromAnotherChat(t *testing.T) {
	tool, ran := startRunnableCronTool(t, config.DefaultConfig())
	job := addTestCronJob(t, tool, "hourly", "pico", "pico:chat-a", "")
	ctx := WithToolContext(context.Background(), "pico", "pico:chat-b")

	result := tool.Execute(ctx, map[string]any{"action": "run", "job_id": job.ID})
	if result.IsError {
		t.Fatalf("run from another pico chat failed: %s", result.ForLLM)
	}
	select {
	case id := <-ran:
		if id != job.ID {
			t.Fatalf("ran %s, want %s", id, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job was not executed")
	}
	waitCronJobDone(t, tool, job.ID)
}

func TestCronTool_NonAllowlistedRemoteCannotAccessOwnCommandJob(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError || strings.Contains(listResult.ForLLM, job.ID) {
		t.Fatalf("expected list to hide non-allowlisted command job, got: %+v", listResult)
	}

	for _, action := range []string{"get", "update"} {
		args := map[string]any{"action": action, "job_id": job.ID}
		if action == "update" {
			args["message"] = "changed"
		}
		result := tool.Execute(ctx, args)
		if !result.IsError || !strings.Contains(result.ForLLM, "not accessible") {
			t.Fatalf("expected %s to reject non-allowlisted command job, got: %+v", action, result)
		}
	}
}

func TestCronTool_WildcardRemoteCanAccessOwnCommandJob(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Cron.CommandAllowedRemotes = []string{"*"}
	tool := newTestCronToolWithConfig(t, cfg)
	job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
	other := addTestCronJob(t, tool, "other", "telegram", "chat-2", "uptime")
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError || !strings.Contains(listResult.ForLLM, job.ID) {
		t.Fatalf("expected wildcard list to include own command job, got: %+v", listResult)
	}
	if strings.Contains(listResult.ForLLM, other.ID) {
		t.Fatalf("wildcard list should still hide other chat job, got: %s", listResult.ForLLM)
	}

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if getResult.IsError {
		t.Fatalf("expected wildcard get to access own command job, got: %s", getResult.ForLLM)
	}
}

func TestCronTool_InternalChannelCanAccessAllCommandJobs(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
	ctx := WithToolContext(context.Background(), "cli", "direct")

	listResult := tool.Execute(ctx, map[string]any{"action": "list"})
	if listResult.IsError || !strings.Contains(listResult.ForLLM, job.ID) {
		t.Fatalf("expected internal list to include command job, got: %+v", listResult)
	}

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if getResult.IsError {
		t.Fatalf("expected internal get to access command job, got: %s", getResult.ForLLM)
	}

	updateResult := tool.Execute(ctx, map[string]any{
		"action":  "update",
		"job_id":  job.ID,
		"message": "internal update",
	})
	if updateResult.IsError {
		t.Fatalf("expected internal update to access command job, got: %s", updateResult.ForLLM)
	}
}

func TestCronTool_AllowlistedRemoteCanManageOwnCommandJob(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram:chat-1"}
			tool := newTestCronToolWithConfig(t, cfg)
			job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
			if action == "enable" {
				tool.cronService.EnableJob(job.ID, false)
			}
			ctx := WithToolContext(context.Background(), "telegram", "chat-1")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if result.IsError {
				t.Fatalf("expected %s to access own command job, got: %s", action, result.ForLLM)
			}

			saved, ok := tool.cronService.GetJob(job.ID)
			switch action {
			case "remove":
				if ok {
					t.Fatalf("remove should delete own command job: %+v", saved)
				}
			case "enable":
				if !ok || !saved.Enabled {
					t.Fatalf("enable should enable own command job: %+v", saved)
				}
			case "disable":
				if !ok || saved.Enabled {
					t.Fatalf("disable should disable own command job: %+v", saved)
				}
			}
		})
	}
}

func TestCronTool_RemoteCannotManageOtherChatJob(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Tools.Cron.CommandAllowedRemotes = []string{"telegram"}
			tool := newTestCronToolWithConfig(t, cfg)
			job := addTestCronJob(t, tool, "command", "telegram", "chat-2", "df -h")
			ctx := WithToolContext(context.Background(), "telegram", "chat-1")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if !result.IsError || !strings.Contains(result.ForLLM, "not accessible") {
				t.Fatalf("expected %s to reject other chat job, got: %+v", action, result)
			}

			saved, ok := tool.cronService.GetJob(job.ID)
			if !ok {
				t.Fatalf("%s should not remove other chat job", action)
			}
			if !saved.Enabled {
				t.Fatalf("%s should not disable other chat job: %+v", action, saved)
			}
		})
	}
}

func TestCronTool_RemoteCannotManageCommandJobUnlessAllowlisted(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			tool := newTestCronTool(t)
			job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
			ctx := WithToolContext(context.Background(), "telegram", "chat-1")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if !result.IsError || !strings.Contains(result.ForLLM, "not accessible") {
				t.Fatalf("expected %s to reject non-allowlisted command job, got: %+v", action, result)
			}

			saved, ok := tool.cronService.GetJob(job.ID)
			if !ok {
				t.Fatalf("%s should not remove non-allowlisted command job", action)
			}
			if !saved.Enabled {
				t.Fatalf("%s should not disable non-allowlisted command job: %+v", action, saved)
			}
		})
	}
}

func TestCronTool_InternalChannelCanManageAllJobs(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			tool := newTestCronTool(t)
			job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
			if action == "enable" {
				tool.cronService.EnableJob(job.ID, false)
			}
			ctx := WithToolContext(context.Background(), "cli", "direct")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if result.IsError {
				t.Fatalf("expected internal %s to access command job, got: %s", action, result.ForLLM)
			}

			saved, ok := tool.cronService.GetJob(job.ID)
			switch action {
			case "remove":
				if ok {
					t.Fatalf("internal remove should delete command job: %+v", saved)
				}
			case "enable":
				if !ok || !saved.Enabled {
					t.Fatalf("internal enable should enable command job: %+v", saved)
				}
			case "disable":
				if !ok || saved.Enabled {
					t.Fatalf("internal disable should disable command job: %+v", saved)
				}
			}
		})
	}
}

func TestCronTool_RemoteCanManageOwnNonCommandJob(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			tool := newTestCronTool(t)
			job := addTestCronJob(t, tool, "reminder", "telegram", "chat-1", "")
			if action == "enable" {
				tool.cronService.EnableJob(job.ID, false)
			}
			ctx := WithToolContext(context.Background(), "telegram", "chat-1")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if result.IsError {
				t.Fatalf("expected %s to access own non-command job, got: %s", action, result.ForLLM)
			}
		})
	}
}

func TestCronTool_WildcardRemoteCanManageOwnCommandJob(t *testing.T) {
	for _, action := range []string{"remove", "enable", "disable"} {
		t.Run(action, func(t *testing.T) {
			cfg := config.DefaultConfig()
			cfg.Tools.Cron.CommandAllowedRemotes = []string{"*"}
			tool := newTestCronToolWithConfig(t, cfg)
			job := addTestCronJob(t, tool, "command", "telegram", "chat-1", "df -h")
			if action == "enable" {
				tool.cronService.EnableJob(job.ID, false)
			}
			other := addTestCronJob(t, tool, "other", "telegram", "chat-2", "uptime")
			ctx := WithToolContext(context.Background(), "telegram", "chat-1")

			result := tool.Execute(ctx, map[string]any{"action": action, "job_id": job.ID})
			if result.IsError {
				t.Fatalf("expected wildcard %s to access own command job, got: %s", action, result.ForLLM)
			}

			otherResult := tool.Execute(ctx, map[string]any{"action": action, "job_id": other.ID})
			if !otherResult.IsError || !strings.Contains(otherResult.ForLLM, "not accessible") {
				t.Fatalf("wildcard %s should still reject other chat job, got: %+v", action, otherResult)
			}
		})
	}
}

func TestCronTool_CommandUpdateSafetyGates(t *testing.T) {
	t.Run("exec disabled", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Tools.Exec.Enabled = false
		tool := newTestCronToolWithConfig(t, cfg)
		ctx := WithToolContext(context.Background(), "cli", "direct")
		job, err := tool.cronService.AddJob(
			"job",
			cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
			"message",
			"cli",
			"direct",
		)
		if err != nil {
			t.Fatalf("AddJob() error: %v", err)
		}

		result := tool.Execute(ctx, map[string]any{
			"action":          "update",
			"job_id":          job.ID,
			"command":         "df -h",
			"command_confirm": true,
		})

		if !result.IsError || !strings.Contains(result.ForLLM, "command execution is disabled") {
			t.Fatalf("expected exec disabled error, got: %+v", result)
		}
	})

	t.Run("confirm required", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Tools.Cron.AllowCommand = false
		tool := newTestCronToolWithConfig(t, cfg)
		ctx := WithToolContext(context.Background(), "cli", "direct")
		job, err := tool.cronService.AddJob(
			"job",
			cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
			"message",
			"cli",
			"direct",
		)
		if err != nil {
			t.Fatalf("AddJob() error: %v", err)
		}

		result := tool.Execute(ctx, map[string]any{
			"action":  "update",
			"job_id":  job.ID,
			"command": "df -h",
		})

		if !result.IsError || !strings.Contains(result.ForLLM, "command_confirm=true") {
			t.Fatalf("expected confirm error, got: %+v", result)
		}

		result = tool.Execute(ctx, map[string]any{
			"action":          "update",
			"job_id":          job.ID,
			"command":         "df -h",
			"command_confirm": true,
		})

		if result.IsError {
			t.Fatalf("expected confirmed command update to succeed, got: %s", result.ForLLM)
		}
		updated, _ := tool.cronService.GetJob(job.ID)
		if updated.Payload.Command != "df -h" {
			t.Fatalf("command not updated: %+v", updated.Payload)
		}

		result = tool.Execute(ctx, map[string]any{
			"action":          "update",
			"job_id":          job.ID,
			"command":         "",
			"command_confirm": true,
		})

		if result.IsError {
			t.Fatalf("expected empty command update to clear command, got: %s", result.ForLLM)
		}
		updated, _ = tool.cronService.GetJob(job.ID)
		if updated.Payload.Command != "" {
			t.Fatalf("command not cleared: %+v", updated.Payload)
		}
	})
	t.Run("clearing does not require confirm", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Tools.Cron.AllowCommand = false
		tool := newTestCronToolWithConfig(t, cfg)
		ctx := WithToolContext(context.Background(), "cli", "direct")
		job, err := tool.cronService.AddJob(
			"job",
			cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
			"message",
			"cli",
			"direct",
		)
		if err != nil {
			t.Fatalf("AddJob() error: %v", err)
		}
		job.Payload.Command = "df -h"
		if err := tool.cronService.UpdateJob(job); err != nil {
			t.Fatalf("UpdateJob() error: %v", err)
		}

		result := tool.Execute(ctx, map[string]any{
			"action":  "update",
			"job_id":  job.ID,
			"command": "",
		})
		if result.IsError {
			t.Fatalf("expected clearing command without confirm to succeed, got: %s", result.ForLLM)
		}
		updated, _ := tool.cronService.GetJob(job.ID)
		if updated.Payload.Command != "" {
			t.Fatalf("command not cleared: %+v", updated.Payload)
		}
	})
}

func TestCronTool_InternalCanAccessCommandJobFromAnyChannel(t *testing.T) {
	tool := newTestCronTool(t)
	ctx := WithToolContext(context.Background(), "cli", "direct")
	job, err := tool.cronService.AddJob(
		"command",
		cron.CronSchedule{Kind: "cron", Expr: "0 8 * * *"},
		"run command",
		"telegram",
		"chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	job.Payload.Command = "df -h"
	if err := tool.cronService.UpdateJob(job); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}

	getResult := tool.Execute(ctx, map[string]any{"action": "get", "job_id": job.ID})
	if getResult.IsError {
		t.Fatalf("get failed: %s", getResult.ForLLM)
	}
	got := parseCronJobResult(t, getResult)
	if got.Payload.Command != "df -h" || got.Payload.Channel != "telegram" || got.Payload.To != "chat-1" {
		t.Fatalf("get returned wrong command job: %+v", got.Payload)
	}

	updateResult := tool.Execute(ctx, map[string]any{
		"action":    "update",
		"job_id":    job.ID,
		"cron_expr": "30 10 * * *",
	})
	if updateResult.IsError {
		t.Fatalf("update failed: %s", updateResult.ForLLM)
	}
	updated, _ := tool.cronService.GetJob(job.ID)
	if updated.Payload.Command != "df -h" {
		t.Fatalf("command should be preserved: %+v", updated.Payload)
	}
	if updated.Schedule.Kind != "cron" || updated.Schedule.Expr != "30 10 * * *" {
		t.Fatalf("schedule not updated: %+v", updated.Schedule)
	}
}

func TestCronTool_ExecuteJobPublishesErrorWhenExecDisabled(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Tools.Exec.Enabled = false

	tool := newTestCronToolWithConfig(t, cfg)
	job := &cron.CronJob{}
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"
	job.Payload.Command = "df -h"

	got, err := tool.ExecuteJob(context.Background(), job)
	if err == nil {
		t.Fatalf("ExecuteJob() error = nil, want error when command execution is disabled (result %q)", got)
	}
	if !strings.Contains(err.Error(), "command execution is disabled") {
		t.Fatalf("ExecuteJob() error = %v, want mention of disabled command execution", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var msg bus.OutboundMessage
	select {
	case msg = <-tool.msgBus.OutboundChan():
		// got message
	case <-ctx.Done():
		t.Fatal("timeout waiting for outbound message")
	}
	if !strings.Contains(msg.Content, "command execution is disabled") {
		t.Fatalf("expected exec disabled message, got: %s", msg.Content)
	}
}

func TestCronTool_ExecuteJobPublishesAgentResponse(t *testing.T) {
	executor := &stubJobExecutor{response: "generated reply"}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, config.DefaultConfig())

	job := &cron.CronJob{ID: "job-1"}
	job.Payload.Channel = "telegram"
	job.Payload.To = "chat-1"
	job.Payload.Message = "send me a poem"

	mustExecuteJobOK(t, tool, job)

	if !strings.HasPrefix(executor.lastKey, "agent:cron-job-1-") {
		t.Fatalf("sessionKey = %q, want agent:cron-job-1-{uuid}", executor.lastKey)
	}
	if executor.lastChan != "telegram" || executor.lastChatID != "chat-1" {
		t.Fatalf("executor target = %s/%s, want telegram/chat-1", executor.lastChan, executor.lastChatID)
	}
	if executor.lastPrompt != "send me a poem" {
		t.Fatalf("prompt = %q, want original message", executor.lastPrompt)
	}
	if executor.publishedResp != "generated reply" {
		t.Fatalf("published response = %q, want generated reply", executor.publishedResp)
	}
	if executor.publishedKey != executor.lastKey {
		t.Fatalf("published sessionKey = %q, want %q", executor.publishedKey, executor.lastKey)
	}
	if executor.publishedChan != "telegram" || executor.publishedChatID != "chat-1" {
		t.Fatalf("published target = %s/%s, want telegram/chat-1", executor.publishedChan, executor.publishedChatID)
	}
}

func TestCronTool_ExecuteJobSkipsEmptyAgentResponse(t *testing.T) {
	executor := &stubJobExecutor{}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, config.DefaultConfig())

	job := &cron.CronJob{ID: "job-empty"}
	job.Payload.Channel = "telegram"
	job.Payload.To = "chat-1"
	job.Payload.Message = "say nothing"

	mustExecuteJobOK(t, tool, job)

	if executor.publishedResp != "" {
		t.Fatalf("unexpected published response: %q", executor.publishedResp)
	}
}

func TestCronTool_ExecuteJobSkipsWhenMessageToolAlreadySent(t *testing.T) {
	executor := &stubJobExecutor{response: "Sent.", alreadySent: true}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, config.DefaultConfig())

	job := &cron.CronJob{ID: "job-msg-sent"}
	job.Payload.Channel = "telegram"
	job.Payload.To = "chat-1"
	job.Payload.Message = "send weather"

	mustExecuteJobOK(t, tool, job)

	if executor.publishedResp != "" {
		t.Fatalf("expected no published response when message tool already sent, got: %q", executor.publishedResp)
	}
}

func TestCronTool_ExecuteJobRunsCommand(t *testing.T) {
	tool := newTestCronToolWithConfig(t, config.DefaultConfig())
	job := &cron.CronJob{}
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"
	job.Payload.Command = "echo cron-test-ok"

	mustExecuteJobOK(t, tool, job)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var msg bus.OutboundMessage
	select {
	case msg = <-tool.msgBus.OutboundChan():
	case <-ctx.Done():
		t.Fatal("timeout waiting for outbound message")
	}
	if !strings.Contains(msg.Content, "cron-test-ok") {
		t.Fatalf("expected command output containing 'cron-test-ok', got: %s", msg.Content)
	}
}

// TestCronTool_ExecuteJobLogsDroppedOutput pins that a job whose outcome cannot
// be published still completes, and that the dropped publish is reported
// instead of vanishing.
func TestCronTool_ExecuteJobLogsDroppedOutput(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "cron.log")
	if err := logger.EnableFileLogging(logPath); err != nil {
		t.Fatalf("EnableFileLogging() error: %v", err)
	}
	t.Cleanup(logger.DisableFileLogging)

	tool := newTestCronToolWithConfig(t, config.DefaultConfig())
	job := &cron.CronJob{}
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"
	job.Payload.Command = "echo dropped-output"

	// A closed bus rejects the publish; the command itself already ran.
	tool.msgBus.Close()

	mustExecuteJobOK(t, tool, job)

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	if !strings.Contains(string(data), "Failed to publish job output") {
		t.Fatalf("log does not mention the dropped publish: %s", data)
	}
}

func TestCronTool_ExecuteJobReturnsErrorWithoutPublish(t *testing.T) {
	executor := &stubJobExecutor{
		response: "this response must not be published",
		err:      fmt.Errorf("agent failure"),
	}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, config.DefaultConfig())

	job := &cron.CronJob{ID: "job-err"}
	job.Payload.Channel = "telegram"
	job.Payload.To = "chat-1"
	job.Payload.Message = "do something"

	got, err := tool.ExecuteJob(context.Background(), job)
	if err == nil {
		t.Fatalf("ExecuteJob() error = nil, want error (result %q)", got)
	}
	if !strings.Contains(err.Error(), "agent failure") {
		t.Fatalf("ExecuteJob() error = %v, want wrapped agent failure", err)
	}

	if executor.publishedResp != "" {
		t.Fatalf("unexpected publish on error path: %q", executor.publishedResp)
	}
}

func TestCronTool_ExecuteJobFailureRecordedInJobState(t *testing.T) {
	executor := &stubJobExecutor{err: fmt.Errorf("agent failure")}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, config.DefaultConfig())

	// Same wiring as the gateway: the handler's error must reach the cron
	// service so the job state records the failed run instead of "ok".
	tool.cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		return tool.ExecuteJob(context.Background(), job)
	})
	if err := tool.cronService.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(tool.cronService.Stop)

	everyMS := time.Hour.Milliseconds()
	job, err := tool.cronService.AddJob(
		"failing", cron.CronSchedule{Kind: "every", EveryMS: &everyMS}, "do it", "telegram", "chat-1",
	)
	if err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}
	if err := tool.cronService.RunJob(job.ID); err != nil {
		t.Fatalf("RunJob() error: %v", err)
	}
	waitCronJobDone(t, tool, job.ID)

	current, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job disappeared")
	}
	if current.State.LastStatus != "error" {
		t.Fatalf("lastStatus = %q, want error", current.State.LastStatus)
	}
	if !strings.Contains(current.State.LastError, "agent failure") {
		t.Fatalf("lastError = %q, want mention of agent failure", current.State.LastError)
	}
}

func TestCronTool_ExecuteJobReportsCommandFailure(t *testing.T) {
	tool := newTestCronToolWithConfig(t, config.DefaultConfig())
	job := &cron.CronJob{}
	job.Payload.Channel = "cli"
	job.Payload.To = "direct"
	job.Payload.Command = "exit 3"

	got, err := tool.ExecuteJob(context.Background(), job)
	if err == nil {
		t.Fatalf("ExecuteJob() error = nil, want error for failing command (result %q)", got)
	}
	if !strings.Contains(err.Error(), "scheduled command failed") {
		t.Fatalf("ExecuteJob() error = %v, want scheduled command failure", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var msg bus.OutboundMessage
	select {
	case msg = <-tool.msgBus.OutboundChan():
	case <-ctx.Done():
		t.Fatal("timeout waiting for outbound message")
	}
	if !strings.Contains(msg.Content, "Error executing scheduled command") {
		t.Fatalf("expected failure message, got: %s", msg.Content)
	}
}

// startRunnableCronTool starts the tool's service with a handler that reports
// every executed job ID, so run requests can be observed.
func startRunnableCronTool(t *testing.T, cfg *config.Config) (*CronTool, chan string) {
	t.Helper()
	tool := newTestCronToolWithConfig(t, cfg)
	ran := make(chan string, 4)
	tool.cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		ran <- job.ID
		return "ok", nil
	})
	if err := tool.cronService.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(tool.cronService.Stop)
	return tool, ran
}

// waitCronJobDone blocks until a triggered job has recorded its result, so the
// service's final store write cannot race the test's temp dir cleanup.
func waitCronJobDone(t *testing.T, tool *CronTool, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if job, ok := tool.cronService.GetJob(jobID); ok && job.State.LastStatus != "" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never finished", jobID)
}

func TestCronTool_RunTriggersJobNow(t *testing.T) {
	tool, ran := startRunnableCronTool(t, config.DefaultConfig())
	job := addTestCronJob(t, tool, "hourly", "cli", "direct", "")
	ctx := WithToolContext(context.Background(), "cli", "direct")

	result := tool.Execute(ctx, map[string]any{"action": "run", "job_id": job.ID})
	if result.IsError {
		t.Fatalf("run failed: %s", result.ForLLM)
	}
	select {
	case id := <-ran:
		if id != job.ID {
			t.Fatalf("ran %s, want %s", id, job.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("job was not executed")
	}
	waitCronJobDone(t, tool, job.ID)
}

func TestCronTool_RunValidationAndAccess(t *testing.T) {
	tool, ran := startRunnableCronTool(t, config.DefaultConfig())
	own := addTestCronJob(t, tool, "own", "telegram", "chat-1", "")
	other := addTestCronJob(t, tool, "other", "telegram", "chat-2", "")
	command := addTestCronJob(t, tool, "cmd", "telegram", "chat-1", "df -h")
	ctx := WithToolContext(context.Background(), "telegram", "chat-1")

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"missing id", map[string]any{"action": "run"}, "job_id is required for run"},
		{"unknown job", map[string]any{"action": "run", "job_id": "nope"}, "not found"},
		{"other chat", map[string]any{"action": "run", "job_id": other.ID}, "not accessible"},
		{
			"command job, remote not allowlisted",
			map[string]any{"action": "run", "job_id": command.ID},
			"not accessible",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tool.Execute(ctx, tt.args)
			if !result.IsError || !strings.Contains(result.ForLLM, tt.want) {
				t.Fatalf("want error containing %q, got: %+v", tt.want, result)
			}
		})
	}

	// Rejected requests must not have started anything.
	select {
	case id := <-ran:
		t.Fatalf("rejected run executed job %s", id)
	case <-time.After(100 * time.Millisecond):
	}

	if result := tool.Execute(ctx, map[string]any{"action": "run", "job_id": own.ID}); result.IsError {
		t.Fatalf("run of own job failed: %s", result.ForLLM)
	}
	waitCronJobDone(t, tool, own.ID)
}

func TestCronTool_RunReportsAlreadyRunning(t *testing.T) {
	tool := newTestCronTool(t)
	gate := make(chan struct{})
	started := make(chan struct{}, 1)
	tool.cronService.SetOnJob(func(job *cron.CronJob) (string, error) {
		started <- struct{}{}
		<-gate
		return "ok", nil
	})
	if err := tool.cronService.Start(); err != nil {
		t.Fatalf("Start() error: %v", err)
	}
	t.Cleanup(tool.cronService.Stop)

	job := addTestCronJob(t, tool, "slow", "cli", "direct", "")
	t.Cleanup(func() {
		close(gate)
		waitCronJobDone(t, tool, job.ID)
	})
	ctx := WithToolContext(context.Background(), "cli", "direct")
	args := map[string]any{"action": "run", "job_id": job.ID}

	if result := tool.Execute(ctx, args); result.IsError {
		t.Fatalf("first run failed: %s", result.ForLLM)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not start")
	}

	result := tool.Execute(ctx, args)
	if !result.IsError || !strings.Contains(result.ForLLM, "already running") {
		t.Fatalf("want already-running error, got: %+v", result)
	}
}

// TestCronTool_ListShowsLocalRunTimes verifies list output renders last and
// next run as local times next to the raw job metadata, so agents can report
// schedules without shelling out to a date command.
func TestCronTool_ListShowsLocalRunTimes(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "timed", "cli", "direct", "")

	last := time.Now().Add(-2 * time.Hour).UnixMilli()
	next := time.Now().Add(2 * time.Hour).UnixMilli()
	current, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}
	current.State.LastRunAtMS = &last
	current.State.LastStatus = "ok"
	current.State.NextRunAtMS = &next
	if err := tool.cronService.UpdateJob(current); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}

	result := tool.Execute(
		WithToolContext(context.Background(), "cli", "direct"),
		map[string]any{"action": "list"},
	)
	if result.IsError {
		t.Fatalf("list failed: %s", result.ForLLM)
	}

	wantLast := "last run: " + time.UnixMilli(last).Format(cronLocalTimeLayout) + " (ok)"
	if !strings.Contains(result.ForLLM, wantLast) {
		t.Fatalf("list should show local last run %q, got: %s", wantLast, result.ForLLM)
	}
	wantNext := "next run: " + time.UnixMilli(next).Format(cronLocalTimeLayout)
	if !strings.Contains(result.ForLLM, wantNext) {
		t.Fatalf("list should show local next run %q, got: %s", wantNext, result.ForLLM)
	}
}

// TestCronTool_ListShowsOneTimeScheduleInLocalTime verifies one-time jobs show
// their target time in the schedule summary without duplicating it as next run.
func TestCronTool_ListShowsOneTimeScheduleInLocalTime(t *testing.T) {
	tool := newTestCronTool(t)
	atMS := time.Now().Add(45 * time.Minute).UnixMilli()
	if _, err := tool.cronService.AddJob(
		"once",
		cron.CronSchedule{Kind: "at", AtMS: &atMS},
		"once message",
		"cli",
		"direct",
	); err != nil {
		t.Fatalf("AddJob() error: %v", err)
	}

	result := tool.Execute(
		WithToolContext(context.Background(), "cli", "direct"),
		map[string]any{"action": "list"},
	)
	if result.IsError {
		t.Fatalf("list failed: %s", result.ForLLM)
	}

	local := time.UnixMilli(atMS).Format(cronLocalTimeLayout)
	if want := "one-time at " + local; !strings.Contains(result.ForLLM, want) {
		t.Fatalf("list should show %q, got: %s", want, result.ForLLM)
	}
	if dup := "next run: " + local; strings.Contains(result.ForLLM, dup) {
		t.Fatalf("one-time job should not repeat its target as next run, got: %s", result.ForLLM)
	}
}

// TestFormatLocalTimeMS_ISO8601 verifies rendered times are ISO 8601
// (RFC 3339) with an offset and round-trip to the original instant.
func TestFormatLocalTimeMS_ISO8601(t *testing.T) {
	ms := int64(1792393200000)
	got := formatLocalTimeMS(&ms)
	if want := time.UnixMilli(ms).Format(time.RFC3339); got != want {
		t.Fatalf("formatLocalTimeMS() = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("rendered time %q is not ISO 8601: %v", got, err)
	}
	if !parsed.Equal(time.UnixMilli(ms)) {
		t.Fatalf("rendered time %q does not round-trip the instant", got)
	}
	if empty := formatLocalTimeMS(nil); empty != "" {
		t.Fatalf("nil timestamp should render empty, got %q", empty)
	}
}

// TestCronTool_GetIncludesLocalTimesJSON verifies get keeps the raw epoch
// fields and adds derived local times under localTimes.
func TestCronTool_GetIncludesLocalTimesJSON(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "timed", "pico", "pico:chat-a", "")

	last := time.Now().Add(-90 * time.Minute).UnixMilli()
	next := time.Now().Add(30 * time.Minute).UnixMilli()
	current, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}
	current.State.LastRunAtMS = &last
	current.State.LastStatus = "ok"
	current.State.NextRunAtMS = &next
	if err := tool.cronService.UpdateJob(current); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}

	result := tool.Execute(
		WithToolContext(context.Background(), "pico", "pico:chat-b"),
		map[string]any{"action": "get", "job_id": job.ID},
	)
	if result.IsError {
		t.Fatalf("get failed: %s", result.ForLLM)
	}

	// Existing consumers still parse the payload as a plain cron job.
	got := parseCronJobResult(t, result)
	if got.ID != job.ID {
		t.Fatalf("get returned wrong job: %+v", got)
	}
	if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != next {
		t.Fatalf("get payload lost nextRunAtMs: %+v", got.State)
	}

	var envelope struct {
		LocalTimes map[string]string `json:"localTimes"`
	}
	text := result.ForLLM
	if idx := strings.Index(text, "{"); idx >= 0 {
		text = text[idx:]
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("failed to parse get JSON %q: %v", result.ForLLM, err)
	}
	if want := time.UnixMilli(last).Format(cronLocalTimeLayout); envelope.LocalTimes["lastRun"] != want {
		t.Fatalf("localTimes.lastRun = %q, want %q", envelope.LocalTimes["lastRun"], want)
	}
	if want := time.UnixMilli(next).Format(cronLocalTimeLayout); envelope.LocalTimes["nextRun"] != want {
		t.Fatalf("localTimes.nextRun = %q, want %q", envelope.LocalTimes["nextRun"], want)
	}
}

// TestCronTool_UpdateIncludesLocalTimesJSON verifies update keeps the raw epoch
// fields and adds derived local times under localTimes.
func TestCronTool_UpdateIncludesLocalTimesJSON(t *testing.T) {
	tool := newTestCronTool(t)
	job := addTestCronJob(t, tool, "timed", "cli", "direct", "")

	last := time.Now().Add(-45 * time.Minute).UnixMilli()
	next := time.Now().Add(15 * time.Minute).UnixMilli()
	current, ok := tool.cronService.GetJob(job.ID)
	if !ok {
		t.Fatal("job not found")
	}
	current.State.LastRunAtMS = &last
	current.State.LastStatus = "ok"
	current.State.NextRunAtMS = &next
	if err := tool.cronService.UpdateJob(current); err != nil {
		t.Fatalf("UpdateJob() error: %v", err)
	}

	result := tool.Execute(
		WithToolContext(context.Background(), "cli", "direct"),
		map[string]any{"action": "update", "job_id": job.ID, "message": "updated message"},
	)
	if result.IsError {
		t.Fatalf("update failed: %s", result.ForLLM)
	}

	got := parseCronJobResult(t, result)
	if got.Payload.Message != "updated message" {
		t.Fatalf("update returned wrong message: %+v", got.Payload)
	}
	if got.State.NextRunAtMS == nil || *got.State.NextRunAtMS != next {
		t.Fatalf("update payload lost nextRunAtMs: %+v", got.State)
	}

	var envelope struct {
		LocalTimes *cronJobLocalTimes `json:"localTimes"`
	}
	text := result.ForLLM
	if idx := strings.Index(text, "{"); idx >= 0 {
		text = text[idx:]
	}
	if err := json.Unmarshal([]byte(text), &envelope); err != nil {
		t.Fatalf("failed to parse update JSON %q: %v", result.ForLLM, err)
	}
	if envelope.LocalTimes == nil {
		t.Fatalf("update payload is missing localTimes: %s", result.ForLLM)
	}
	if want := time.UnixMilli(last).Format(cronLocalTimeLayout); envelope.LocalTimes.LastRun != want {
		t.Fatalf("localTimes.lastRun = %q, want %q", envelope.LocalTimes.LastRun, want)
	}
	if want := time.UnixMilli(next).Format(cronLocalTimeLayout); envelope.LocalTimes.NextRun != want {
		t.Fatalf("localTimes.nextRun = %q, want %q", envelope.LocalTimes.NextRun, want)
	}
}

// TestFormatTimeMSInLocation verifies the rendered offset is RFC 3339: UTC
// renders as Z, while non-UTC zones carry a numeric offset.
func TestFormatTimeMSInLocation(t *testing.T) {
	ms := int64(1792393200000)
	tests := []struct {
		name string
		loc  *time.Location
		want string
	}{
		{"UTC", time.UTC, "2026-10-19T07:00:00Z"},
		{"fixed offset", time.FixedZone("CEST", 2*60*60), "2026-10-19T09:00:00+02:00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTimeMSInLocation(&ms, tt.loc); got != tt.want {
				t.Fatalf("formatTimeMSInLocation() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestFormatTimeMSInLocation_TruncatesMilliseconds documents that RFC 3339
// output is second-granular even though the stored epoch value has milliseconds.
func TestFormatTimeMSInLocation_TruncatesMilliseconds(t *testing.T) {
	ms := int64(1792393200123)
	got := formatTimeMSInLocation(&ms, time.UTC)
	if want := "2026-10-19T07:00:00Z"; got != want {
		t.Fatalf("formatTimeMSInLocation() = %q, want %q", got, want)
	}
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("rendered time %q is not ISO 8601: %v", got, err)
	}
	if want := int64(1792393200000); parsed.UnixMilli() != want {
		t.Fatalf("rendered time %q resolves to %d ms, want %d ms", got, parsed.UnixMilli(), want)
	}
}
