package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

// TestPrecheckHelperProcess is not a real test: the precheck tests re-execute
// the test binary with GO_WANT_PRECHECK_HELPER=1 and read this process's
// stdout. It exits explicitly so the go test harness never appends its own
// output to the JSON contract.
func TestPrecheckHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_PRECHECK_HELPER") != "1" {
		return
	}
	switch os.Getenv("PRECHECK_MODE") {
	case "wake":
		fmt.Print(`{"wake_agent":true,"context":"digest"}`)
	case "skip":
		fmt.Print(`{"wake_agent":false,"reason":"idle"}`)
	case "badjson":
		fmt.Print("not json")
	case "missing":
		fmt.Print(`{"reason":"no decision"}`)
	case "fail":
		fmt.Fprintln(os.Stderr, "gate exploded")
		os.Exit(3)
	case "sleep":
		time.Sleep(5 * time.Second)
	case "huge":
		fmt.Print(strings.Repeat("x", maxPrecheckOutputBytes+1))
	case "env":
		fmt.Printf(
			`{"wake_agent":true,"context":"ws=%s job=%s name=%s decl=%s source=%s"}`,
			os.Getenv("PICOCLAW_WORKSPACE"),
			os.Getenv("PICOCLAW_CRON_JOB_ID"),
			os.Getenv("PICOCLAW_CRON_JOB_NAME"),
			os.Getenv("PICOCLAW_CRON_DECLARATION_KEY"),
			os.Getenv("PICOCLAW_CRON_SOURCE"),
		)
	default:
		fmt.Print(`{"wake_agent":false,"reason":"unknown helper mode"}`)
	}
	os.Exit(0)
}

func precheckTestJob(t *testing.T, mode string) *cron.CronJob {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"GO_WANT_PRECHECK_HELPER": "1",
		"PRECHECK_MODE":           mode,
	}
	for _, key := range []string{"PATH", "HOME", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR"} {
		if value, ok := os.LookupEnv(key); ok {
			env[key] = value
		}
	}
	return &cron.CronJob{
		ID:             "job-1",
		Name:           "nightly",
		AgentID:        "main",
		DeclarationKey: "plugin:dreaming:nightly",
		Payload: cron.CronPayload{
			Message: "nightly",
			Channel: "cli",
			To:      "direct",
			Precheck: &cron.JobPrecheck{
				Command:   []string{binary, "-test.run=TestPrecheckHelperProcess", "--"},
				Env:       env,
				TimeoutMS: 0,
			},
		},
	}
}

func TestCronPrecheckSkipDoesNotWakeAgent(t *testing.T) {
	executor := &stubJobExecutor{}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, nil)
	job := precheckTestJob(t, "skip")

	got, err := tool.ExecuteJob(context.Background(), job)
	if got != "skipped" {
		t.Fatalf("ExecuteJob() = %q, want skipped", got)
	}
	var skipErr *cron.SkipError
	if !errors.As(err, &skipErr) || skipErr.Reason != "idle" {
		t.Fatalf("ExecuteJob() error = %v, want SkipError{idle}", err)
	}
	if executor.lastPrompt != "" {
		t.Fatalf("agent was called with %q", executor.lastPrompt)
	}
}

func TestCronPrecheckWakeAppendsContext(t *testing.T) {
	executor := &stubJobExecutor{response: "done"}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, nil)
	job := precheckTestJob(t, "wake")

	got, err := tool.ExecuteJob(context.Background(), job)
	if err != nil {
		t.Fatalf("ExecuteJob() error = %v", err)
	}
	if got != "ok" {
		t.Fatalf("ExecuteJob() = %q, want ok", got)
	}
	if executor.lastPrompt != "nightly\n\ndigest" {
		t.Fatalf("prompt = %q", executor.lastPrompt)
	}
}

func TestCronPrecheckPassesWorkspaceAndExecutionIdentity(t *testing.T) {
	executor := &stubJobExecutor{response: "done"}
	tool := newTestCronToolWithExecutorAndConfig(t, executor, nil)
	job := precheckTestJob(t, "env")
	ctx := cron.WithExecution(context.Background(), cron.Execution{
		Source:         cron.ExecutionSourceScheduled,
		ExecutionID:    "exec-1",
		JobID:          job.ID,
		JobName:        job.Name,
		DeclarationKey: job.DeclarationKey,
	})

	if _, err := tool.ExecuteJob(ctx, job); err != nil {
		t.Fatalf("ExecuteJob() error = %v", err)
	}
	for _, want := range []string{
		"ws=" + tool.workspace,
		"job=" + job.ID,
		"name=" + job.Name,
		"decl=" + job.DeclarationKey,
		"source=" + cron.ExecutionSourceScheduled,
	} {
		if !strings.Contains(executor.lastPrompt, want) {
			t.Fatalf("prompt %q does not contain %q", executor.lastPrompt, want)
		}
	}
}

func TestCronPrecheckFailuresDoNotWakeAgent(t *testing.T) {
	cases := []struct {
		mode    string
		timeout int64
		want    string
	}{
		{"badjson", 0, "not a JSON object"},
		{"missing", 0, `must contain a boolean "wake_agent"`},
		{"fail", 0, "gate exploded"},
		{"sleep", 50, "timed out"},
		{"huge", 0, "exceeds"},
	}
	for _, test := range cases {
		t.Run(test.mode, func(t *testing.T) {
			executor := &stubJobExecutor{}
			tool := newTestCronToolWithExecutorAndConfig(t, executor, nil)
			job := precheckTestJob(t, test.mode)
			job.Payload.Precheck.TimeoutMS = test.timeout

			if _, err := tool.ExecuteJob(context.Background(), job); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if executor.lastPrompt != "" {
				t.Fatalf("agent was called with %q", executor.lastPrompt)
			}
		})
	}
}
