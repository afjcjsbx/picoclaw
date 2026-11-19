package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sipeed/picoclaw/pkg/logger"
	fstools "github.com/sipeed/picoclaw/pkg/tools/fs"
	"github.com/sipeed/picoclaw/pkg/tools/shellguard"
)

// admitRun picks the working directory, runs the command guard against it and
// remembers an explicit cwd once the guard passed. It returns the directory to
// run in, or the rejection.
func (t *ExecTool) admitRun(
	ctx context.Context,
	req runRequest,
	plan runPlan,
	analysis shellguard.Analysis,
) (string, *ToolResult) {
	cwd, remembered := t.initialCwd(ctx, req.cwd)

	if req.cwd != "" && t.restrictToWorkspace && t.workingDir != "" {
		resolved, err := t.validateCwd(cwd)
		if err != nil {
			return "", t.blockedResult(ctx, plan.command, "Command blocked by safety guard ("+err.Error()+")")
		}
		cwd = resolved
	}

	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			cwd = wd
		}
	}

	// Remember the path that passed workspace validation, not the post-symlink
	// resolved path: on platforms where the workspace itself lives behind a
	// symlink (for example /var -> /private/var on macOS), storing the resolved
	// path would fail validation on the next call.
	stateCwd := cwd

	// The guard inspects the command as written, not the normalized form that
	// runs, so normalization cannot change which deny or allow rules apply.
	if guardErr := t.guardCommandWithAnalysis(req.command, cwd, analysis); guardErr != nil {
		return "", t.blockedResult(ctx, plan.command, guardErr.Error())
	}

	// Re-resolve symlinks immediately before execution to shrink the TOCTOU window
	// between validation and cmd.Dir assignment.
	if t.restrictToWorkspace && t.workingDir != "" && cwd != t.workingDir {
		resolved, err := t.validateCwd(cwd)
		if err != nil {
			if remembered {
				t.forgetRememberedCwd(ctx, cwd, err)
			}
			return "", t.blockedResult(ctx, plan.command, "Command blocked by safety guard ("+err.Error()+")")
		}
		cwd = resolved
	}

	// Persist only a cwd that passed validation and the command guard.
	if req.cwd != "" && stateCwd != "" {
		t.rememberCwd(ctx, stateCwd)
	}

	// Any allowed run breaks the blocked-command streak.
	t.blockBreaker.reset(ctx)
	return cwd, nil
}

// initialCwd is the explicit cwd, else the remembered one (revalidated), else
// the workspace. remembered reports that the remembered value was used.
func (t *ExecTool) initialCwd(ctx context.Context, requested string) (cwd string, remembered bool) {
	if requested != "" {
		return requested, false
	}
	if saved := t.state.load(ctx); saved != "" {
		if resolved, ok := t.rememberedCwd(ctx, saved); ok {
			return resolved, true
		}
	}
	return t.workingDir, false
}

// resolvedWorkspace returns the workspace with symlinks resolved, so paths
// produced by `pwd -P` compare correctly on systems where the workspace itself
// lives behind a symlink (for example /var -> /private/var on macOS).
func (t *ExecTool) resolvedWorkspace() string {
	workspace := t.workingDir
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	return workspace
}

// validateCwd applies the workspace and allow-path policy to a cwd. It is the
// single policy used by initial validation, the pre-execution re-check,
// observed-cwd acceptance and remembered-cwd validation. It tries the
// symlink-resolved form first so a workspace behind /var -> /private/var and a
// path reported by `pwd -P` compare correctly; the original pair is the
// fallback for operator allow patterns that name the unresolved path.
func (t *ExecTool) validateCwd(cwd string) (string, error) {
	if !t.restrictToWorkspace || t.workingDir == "" {
		return cwd, nil
	}
	candidate := cwd
	if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
		candidate = resolved
	}
	if validated, err := fstools.ValidatePathWithAllowPaths(
		candidate, t.resolvedWorkspace(), true, t.allowedPathPatterns,
	); err == nil {
		return validated, nil
	}
	return fstools.ValidatePathWithAllowPaths(cwd, t.workingDir, true, t.allowedPathPatterns)
}

// acceptObservedCwd reports whether the cwd observed after a command is a real
// directory that the workspace policy allows. The observed path is used for
// the result header even when it is rejected, but only accepted paths are
// remembered for the next call.
func (t *ExecTool) acceptObservedCwd(cwd string) bool {
	if cwd == "" || !filepath.IsAbs(cwd) {
		return false
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return false
	}
	_, err = t.validateCwd(cwd)
	return err == nil
}

// rememberedCwd revalidates a cwd stored for the conversation. It returns
// ok=false and forgets the value when the directory disappeared or no longer
// satisfies the workspace policy, so the caller can fall back to the default.
func (t *ExecTool) rememberedCwd(ctx context.Context, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	resolved := cwd
	if t.restrictToWorkspace && t.workingDir != "" {
		validated, err := t.validateCwd(cwd)
		if err != nil {
			t.forgetRememberedCwd(ctx, cwd, err)
			return "", false
		}
		resolved = validated
	}
	info, err := os.Stat(resolved)
	if err != nil {
		t.forgetRememberedCwd(ctx, cwd, err)
		return "", false
	}
	if !info.IsDir() {
		t.forgetRememberedCwd(ctx, cwd, fmt.Errorf("not a directory"))
		return "", false
	}
	if abs, err := filepath.Abs(resolved); err == nil {
		resolved = abs
	}
	return resolved, true
}

func (t *ExecTool) forgetRememberedCwd(ctx context.Context, cwd string, cause error) {
	if t.state != nil {
		t.state.clearCwd(ctx)
	}
	logger.WarnCF("shell", "forgetting unusable remembered cwd", map[string]any{
		"cwd":   cwd,
		"error": cause.Error(),
	})
}

func (t *ExecTool) rememberCwd(ctx context.Context, cwd string) {
	if t.state == nil {
		return
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	t.state.rememberCwd(ctx, cwd)
}
