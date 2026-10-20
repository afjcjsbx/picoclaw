package tools

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// ChildTracker lets a turn count the async children it spawns so it can join
// them before finishing. Begin is called synchronously, before the child
// goroutine starts; the returned func is called exactly once with the child's
// final result (errors included) and takes over delivering it to the turn.
type ChildTracker interface {
	Begin() (done func(*ToolResult))
}

type childTrackerKey struct{}

// WithChildTracker returns a context whose async spawns are tracked by t.
func WithChildTracker(ctx context.Context, t ChildTracker) context.Context {
	return context.WithValue(ctx, childTrackerKey{}, t)
}

// beginChild returns nil when ctx has no tracker (untracked, fire-and-forget).
func beginChild(ctx context.Context) func(*ToolResult) {
	if t, ok := ctx.Value(childTrackerKey{}).(ChildTracker); ok {
		return t.Begin()
	}
	return nil
}

type SpawnTool struct {
	spawner        SubTurnSpawner
	defaultModel   string
	maxTokens      int
	temperature    float64
	allowlistCheck func(targetAgentID string) bool
}

// Compile-time check: SpawnTool implements AsyncExecutor.
var _ AsyncExecutor = (*SpawnTool)(nil)

func NewSpawnTool(manager *SubagentManager) *SpawnTool {
	if manager == nil {
		return &SpawnTool{}
	}
	return &SpawnTool{
		defaultModel: manager.defaultModel,
		maxTokens:    manager.maxTokens,
		temperature:  manager.temperature,
	}
}

// SetSpawner sets the SubTurnSpawner for direct sub-turn execution.
func (t *SpawnTool) SetSpawner(spawner SubTurnSpawner) {
	t.spawner = spawner
}

func (t *SpawnTool) Name() string {
	return "spawn"
}

func (t *SpawnTool) Description() string {
	return "Spawn a subagent to handle a task in the background. Use this for complex or time-consuming tasks that can run independently. " +
		"Call it several times in one step to run subagents in parallel. " +
		"The result arrives automatically when the subagent is done; never poll or sleep to wait for it."
}

func (t *SpawnTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The task for subagent to complete",
			},
			"label": map[string]any{
				"type":        "string",
				"description": "Optional short label for the task (for display)",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Optional target agent ID to delegate the task to",
			},
		},
		"required": []string{"task"},
	}
}

func (t *SpawnTool) SetAllowlistChecker(check func(targetAgentID string) bool) {
	t.allowlistCheck = check
}

func (t *SpawnTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	return t.execute(ctx, args, nil)
}

// ExecuteAsync implements AsyncExecutor. The callback is passed through to the
// subagent manager as a call parameter — never stored on the SpawnTool instance.
func (t *SpawnTool) ExecuteAsync(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	return t.execute(ctx, args, cb)
}

func (t *SpawnTool) execute(
	ctx context.Context,
	args map[string]any,
	cb AsyncCallback,
) *ToolResult {
	task, ok := args["task"].(string)
	if !ok || strings.TrimSpace(task) == "" {
		return ErrorResult("task is required and must be a non-empty string")
	}

	label := stringArg(args, "label")
	targetAgentID := strings.TrimSpace(stringArg(args, "agent_id"))

	// Check allowlist if targeting a specific agent
	if targetAgentID != "" && t.allowlistCheck != nil {
		if !t.allowlistCheck(targetAgentID) {
			return ErrorResult(fmt.Sprintf("not allowed to spawn agent '%s'", targetAgentID))
		}
	}

	// Build system prompt for spawned subagent
	role := "You are a spawned subagent running in the background"
	if label != "" {
		role = fmt.Sprintf("You are a spawned subagent labeled %q running in the background", label)
	}
	systemPrompt := subagentSystemPrompt(
		role,
		"Complete the given task independently and report back when done.",
		task,
	)

	// Use spawner if available (direct SpawnSubTurn call)
	if t.spawner != nil {
		// Tracked children (sub-turn parents) are joined before the parent
		// finishes and get their result through the tracker; untracked ones
		// (root turns) deliver via the callback/bus as before.
		track := beginChild(ctx)
		go func() {
			// Recover panics in this goroutine: it outlives the registry's
			// synchronous recover, so an unrecovered panic would crash the
			// whole process instead of failing the spawned task.
			result := func() (spawnResult *ToolResult) {
				defer func() {
					if r := recover(); r != nil {
						logger.ErrorCF("spawn", "spawn goroutine panic recovered",
							map[string]any{
								"panic": fmt.Sprintf("%v", r),
								"stack": string(debug.Stack()),
							})
						spawnResult = ErrorResult("spawn failed unexpectedly").
							WithError(fmt.Errorf("spawn panic: %v", r))
					}
				}()

				res, err := t.spawner.SpawnSubTurn(ctx, SubTurnConfig{
					Model:         t.defaultModel,
					Tools:         nil, // Will inherit from parent via context
					SystemPrompt:  systemPrompt,
					MaxTokens:     t.maxTokens,
					Temperature:   t.temperature,
					Async:         track == nil, // tracker delivers the result itself
					Critical:      true,         // Background spawn should survive parent turn completion
					TargetAgentID: targetAgentID,
				})
				if err != nil {
					return ErrorResult(fmt.Sprintf("Spawn failed: %v", err)).WithError(err)
				}
				if res == nil {
					return ErrorResult("spawn returned no result")
				}
				return res
			}()

			if result != nil && result.ForLLM != "" {
				// Several children report back to the same parent: say who this is.
				// Copy: with Async=true the same pointer was already delivered to the
				// parent's pendingResults, so mutating it would race with the parent.
				tagged := *result
				tagged.ForLLM = fmt.Sprintf(
					"[Result from subagent %s]\n%s",
					spawnSource(label, targetAgentID),
					result.ForLLM,
				)
				result = &tagged
			}

			if track != nil {
				track(result)
				return
			}

			// Call callback if provided
			if cb != nil {
				cb(ctx, result)
			}
		}()

		// Return immediate acknowledgment, saying what happens next so the model
		// does not invent its own wait (exec sleep, polling).
		next := "You will receive its result as a follow-up message; do not poll, sleep or use exec to wait."
		if track != nil {
			next = "Its result is delivered to you automatically before you finish; " +
				"do not poll, sleep or use exec to wait."
		}
		if label != "" {
			return AsyncResult(fmt.Sprintf("Spawned subagent '%s' for task: %s\n%s", label, task, next))
		}
		return AsyncResult(fmt.Sprintf("Spawned subagent for task: %s\n%s", task, next))
	}

	// Fallback: spawner not configured
	return ErrorResult("Subagent manager not configured")
}

// spawnSource names a spawned subagent for result attribution.
func spawnSource(label, agentID string) string {
	switch {
	case label != "" && agentID != "" && label != agentID:
		return fmt.Sprintf("%q (agent %s)", label, agentID)
	case label != "":
		return fmt.Sprintf("%q", label)
	case agentID != "":
		return fmt.Sprintf("%q", agentID)
	}
	return "(unnamed)"
}
