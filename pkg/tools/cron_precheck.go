package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/isolation"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const (
	maxPrecheckOutputBytes  = 128 << 10
	maxPrecheckContextBytes = 64 << 10
	maxPrecheckErrorBytes   = 2 << 10
)

// precheckDecision is the JSON contract printed by a schedule's wake gate.
// Only wake_agent is mandatory; reason is recorded in the job state and
// context is appended to the scheduled prompt.
type precheckDecision struct {
	WakeAgent *bool  `json:"wake_agent"`
	Reason    string `json:"reason,omitempty"`
	Context   string `json:"context,omitempty"`
}

func (d precheckDecision) wakeAgent() bool {
	return d.WakeAgent != nil && *d.WakeAgent
}

// runPrecheck executes a job's wake gate and returns its decision. The gate
// receives the plugin environment resolved at load time plus read-only cron
// identity variables. It runs before the agent turn, through the same process
// isolation entry point as hooks and exec.
func (t *CronTool) runPrecheck(ctx context.Context, job *cron.CronJob) (precheckDecision, error) {
	var decision precheckDecision
	precheck := job.Payload.Precheck
	if precheck == nil {
		return decision, nil
	}
	if len(precheck.Command) == 0 || strings.TrimSpace(precheck.Command[0]) == "" {
		return decision, fmt.Errorf("precheck command is empty")
	}

	timeout := time.Duration(precheck.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = cron.DefaultPrecheckTimeout
	}
	if timeout > cron.MaxPrecheckTimeout {
		timeout = cron.MaxPrecheckTimeout
	}
	precheckCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(precheckCtx, precheck.Command[0], precheck.Command[1:]...)
	cmd.Dir = precheck.Dir
	cmd.Env = t.precheckEnv(ctx, job, precheck.Env)
	prepareCommandForTermination(cmd)
	cmd.Cancel = func() error { return terminateProcessTree(cmd) }
	cmd.WaitDelay = 2 * time.Second

	var stdout, stderr cappedBuffer
	stdout.limit = maxPrecheckOutputBytes
	stderr.limit = maxPrecheckErrorBytes
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := isolation.Start(cmd); err != nil {
		return decision, fmt.Errorf("start precheck: %w", err)
	}
	err := cmd.Wait()
	if stderr.String() != "" {
		logger.DebugCF("cron", "Job precheck stderr", map[string]any{
			"job_id": job.ID,
			"stderr": strings.TrimSpace(stderr.String()),
		})
	}
	if precheckCtx.Err() == context.DeadlineExceeded {
		return decision, fmt.Errorf("precheck timed out after %s", timeout)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return decision, fmt.Errorf("precheck canceled: %w", ctxErr)
	}
	if err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail != "" {
			return decision, fmt.Errorf("precheck exited: %w: %s", err, detail)
		}
		return decision, fmt.Errorf("precheck exited: %w", err)
	}
	if stdout.overflow {
		return decision, fmt.Errorf("precheck output exceeds %d bytes", maxPrecheckOutputBytes)
	}

	if err := json.Unmarshal(bytes.TrimSpace(stdout.buf.Bytes()), &decision); err != nil {
		return decision, fmt.Errorf("precheck output is not a JSON object: %w", err)
	}
	if decision.WakeAgent == nil {
		return decision, fmt.Errorf(`precheck output must contain a boolean "wake_agent"`)
	}
	if len(decision.Context) > maxPrecheckContextBytes {
		return decision, fmt.Errorf(
			"precheck context exceeds %d bytes",
			maxPrecheckContextBytes,
		)
	}
	return decision, nil
}

// precheckEnv builds the child environment. The plugin loader already removed
// ambient credentials; the host only adds paths and cron identity so a gate can
// locate the workspace and tell which job is running.
func (t *CronTool) precheckEnv(
	ctx context.Context,
	job *cron.CronJob,
	configured map[string]string,
) []string {
	values := make(map[string]string, len(configured)+8)
	for key, value := range configured {
		values[key] = value
	}
	if t.workspace != "" {
		workspace := t.workspace
		if absolute, err := filepath.Abs(workspace); err == nil {
			workspace = absolute
		}
		values["PICOCLAW_WORKSPACE"] = workspace
	}
	values["PICOCLAW_CRON_JOB_ID"] = job.ID
	values["PICOCLAW_CRON_JOB_NAME"] = job.Name
	if job.DeclarationKey != "" {
		values["PICOCLAW_CRON_DECLARATION_KEY"] = job.DeclarationKey
	}
	if job.AgentID != "" {
		values["PICOCLAW_AGENT_ID"] = job.AgentID
	}
	if execution, ok := cron.ExecutionFromContext(ctx); ok {
		if execution.ExecutionID != "" {
			values["PICOCLAW_CRON_EXECUTION_ID"] = execution.ExecutionID
		}
		if execution.Source != "" {
			values["PICOCLAW_CRON_SOURCE"] = execution.Source
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	env := make([]string, 0, len(keys))
	for _, key := range keys {
		env = append(env, key+"="+values[key])
	}
	return env
}

// cappedBuffer keeps at most limit bytes and remembers that it truncated. It
// never returns an error to the child, so a noisy gate cannot block on a full
// pipe; the caller rejects an overflowing precheck after the process exits.
type cappedBuffer struct {
	buf      bytes.Buffer
	limit    int
	overflow bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		b.limit = maxPrecheckOutputBytes
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
		} else {
			_, _ = b.buf.Write(p)
		}
	}
	if len(p) > remaining {
		b.overflow = true
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}
