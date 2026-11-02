package askuser

import "context"

type interactiveContextKey struct{}

// WithInteractiveSession marks a turn as able to receive an inbound user
// reply while a tool is executing. The agent loop sets it for bus-driven chat
// turns; direct/cron/heartbeat invocations deliberately leave it unset.
func WithInteractiveSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, interactiveContextKey{}, true)
}

// IsInteractiveSession reports whether the current turn can suspend and wait
// for a user reply through the agent loop.
func IsInteractiveSession(ctx context.Context) bool {
	interactive, _ := ctx.Value(interactiveContextKey{}).(bool)
	return interactive
}
