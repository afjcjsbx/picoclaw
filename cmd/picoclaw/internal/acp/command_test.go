package acp

import (
	"errors"
	"testing"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
)

func TestNewACPCommand(t *testing.T) {
	command := NewACPCommand()
	if command.Use != "acp" {
		t.Fatalf("Use = %q, want acp", command.Use)
	}
	if command.Annotations[internal.StdioProtocolAnnotation] != "true" {
		t.Fatal("ACP command is not marked as a stdio protocol command")
	}
	for _, name := range []string{"cwd", "model", "debug", "max-sessions", "max-message-bytes"} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("missing flag %q", name)
		}
	}
}

func TestExitError(t *testing.T) {
	cause := errors.New("boom")
	err := &ExitError{Code: 2, Err: cause}
	if !errors.Is(err, cause) {
		t.Fatal("ExitError does not unwrap its cause")
	}
	if err.Error() != "boom" {
		t.Fatalf("Error() = %q, want boom", err.Error())
	}
}
