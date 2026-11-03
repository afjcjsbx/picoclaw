package agent

import (
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/isolation"
)

func TestBuildIsolationPromptPathBoundary(t *testing.T) {
	cb := NewContextBuilder("/tmp/workspace")
	cb.restrictToWorkspace = true
	cb.allowReadOutsideWorkspace = false
	cb.isolationBoundary = &isolation.Boundary{
		Backend:         "Linux bubblewrap",
		Root:            "/tmp/picoclaw",
		RootWritable:    true,
		Workspace:       "/tmp/workspace",
		Scratch:         "/tmp/picoclaw/runtime-user-env/tmp",
		ScratchWritable: true,
		Writable:        []string{"/data/rw"},
		ReadOnly:        []string{"/data/ro"},
		Network:         true,
		PathBoundary:    true,
	}

	prompt := cb.buildIsolationPrompt()
	for _, want := range []string{
		"## Sandbox Boundaries",
		"Linux bubblewrap",
		"Workspace: `/tmp/workspace`",
		"PicoClaw instance root: `/tmp/picoclaw` (writable",
		"Scratch: `/tmp/picoclaw/runtime-user-env/tmp` (`$TMPDIR`, writable)",
		"Additional writable paths (expose_paths): `/data/rw`",
		"Additional read-only paths (expose_paths): `/data/ro`",
		"Network access remains available",
		"restrict_to_workspace=true",
		"allow_read_outside_workspace=false",
		"sandbox boundary, not a broken host",
		"isolation.expose_paths",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("isolation prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildIsolationPromptGenericBackend(t *testing.T) {
	cb := NewContextBuilder("/tmp/workspace")
	cb.isolationBoundary = &isolation.Boundary{
		Backend:      "Windows restricted token and low integrity",
		Workspace:    "/tmp/workspace",
		Network:      true,
		PathBoundary: false,
	}

	prompt := cb.buildIsolationPrompt()
	for _, want := range []string{
		"## Sandbox Boundaries",
		"Windows restricted token and low integrity",
		"exact filesystem map is platform-defined",
		"sandbox boundary, not a broken host",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("isolation prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Additional writable paths") {
		t.Fatalf("generic isolation prompt must not claim a path-level boundary:\n%s", prompt)
	}
}

func TestBuildIsolationPromptDisabled(t *testing.T) {
	cb := NewContextBuilder(t.TempDir())
	if prompt := cb.buildIsolationPrompt(); prompt != "" {
		t.Fatalf("buildIsolationPrompt() = %q, want empty", prompt)
	}
	if prompt := cb.BuildSystemPrompt(); strings.Contains(prompt, "## Sandbox Boundaries") {
		t.Fatalf("system prompt contains sandbox boundaries while isolation is disabled:\n%s", prompt)
	}
}

func TestWithIsolationInvalidatesSystemPromptCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	workspace := t.TempDir()
	cb := NewContextBuilder(workspace)
	before := cb.BuildSystemPromptWithCache()
	if strings.Contains(before, "## Sandbox Boundaries") {
		t.Fatalf("system prompt contains sandbox boundaries before isolation is enabled:\n%s", before)
	}

	cb.WithIsolation(config.IsolationConfig{Enabled: true}, true, false)
	after := cb.BuildSystemPromptWithCache()
	if !strings.Contains(after, "## Sandbox Boundaries") {
		t.Fatalf("system prompt missing sandbox boundaries after WithIsolation:\n%s", after)
	}
}

func TestFormatBoundaryPathList(t *testing.T) {
	paths := []string{
		"/p1", "/p2", "/p3", "/p4", "/p5", "/p6", "/p7", "/p8", "/p9",
	}
	got := formatBoundaryPathList(paths)
	if !strings.Contains(got, "`/p8` (+1 more)") {
		t.Fatalf("formatBoundaryPathList() = %q, want capped list", got)
	}
	if strings.Contains(got, "`/p9`") {
		t.Fatalf("formatBoundaryPathList() = %q, want ninth path omitted", got)
	}
}
