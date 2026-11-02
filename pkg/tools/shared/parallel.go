package toolshared

// ParallelSafety describes whether one specific tool call can run
// concurrently with its neighbors in the same LLM response.
type ParallelSafety struct {
	// Safe is false for a call that must run alone, in order (a barrier).
	Safe bool
	// Paths is the filesystem footprint of the call. Two calls conflict when
	// their paths overlap and at least one of them writes.
	Paths []PathAccess
}

// PathAccess is one filesystem access of a tool call.
type PathAccess struct {
	// Path must be absolute, cleaned and symlink-resolved.
	Path  string
	Write bool
}

// ParallelAware is implemented by tools that can declare per-call parallel
// safety. Tools that do not implement it are always treated as barriers.
type ParallelAware interface {
	Tool
	ParallelSafety(args map[string]any) ParallelSafety
}
