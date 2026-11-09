package cron

import (
	"context"
	"time"
)

// Execution sources describe which path fired a job run.
const (
	ExecutionSourceScheduled = "scheduled"
	ExecutionSourceManual    = "manual"
)

// Execution identifies one run of a cron job. It is attached to the context
// passed to a ContextJobHandler and to agent hook metadata, so plugins and
// tools can tell a scheduled run from an interactive turn without trusting
// the model or the session key.
type Execution struct {
	ExecutionID    string    `json:"execution_id"`
	JobID          string    `json:"job_id"`
	JobName        string    `json:"job_name"`
	DeclarationKey string    `json:"declaration_key,omitempty"`
	Source         string    `json:"source"`
	ScheduledAtMS  *int64    `json:"scheduled_at_ms,omitempty"`
	StartedAt      time.Time `json:"started_at"`
}

type executionContextKey struct{}

// WithExecution returns a context carrying the cron execution identity.
func WithExecution(ctx context.Context, execution Execution) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, executionContextKey{}, execution)
}

// ExecutionFromContext returns the cron execution identity carried by ctx.
func ExecutionFromContext(ctx context.Context) (Execution, bool) {
	if ctx == nil {
		return Execution{}, false
	}
	execution, ok := ctx.Value(executionContextKey{}).(Execution)
	return execution, ok
}

// CloneExecution returns a deep copy safe to attach to hook metadata.
func CloneExecution(execution *Execution) *Execution {
	if execution == nil {
		return nil
	}
	cloned := *execution
	if execution.ScheduledAtMS != nil {
		value := *execution.ScheduledAtMS
		cloned.ScheduledAtMS = &value
	}
	return &cloned
}
