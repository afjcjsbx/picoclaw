package commands

import (
	"fmt"
	"testing"
)

func TestCheckChannel_Success(t *testing.T) {
	rt := &Runtime{
		SwitchChannel: func(_ string) error {
			return nil
		},
	}

	reply := runCommand(t, rt, "/check channel telegram")

	want := "Channel 'telegram' is available and enabled"
	if reply != want {
		t.Fatalf("reply=%q, want=%q", reply, want)
	}
}

func TestCheckChannel_Error(t *testing.T) {
	rt := &Runtime{
		SwitchChannel: func(value string) error {
			return fmt.Errorf("channel '%s' not found", value)
		},
	}

	reply := runCommand(t, rt, "/check channel unknown")

	if reply != "channel 'unknown' not found" {
		t.Fatalf("reply=%q, want error message", reply)
	}
}

func TestCheckChannel_NilDep(t *testing.T) {
	reply := runCommand(t, &Runtime{}, "/check channel telegram")

	if reply != unavailableMsg {
		t.Fatalf("reply=%q, want %q", reply, unavailableMsg)
	}
}

func TestCheckChannel_MissingValue(t *testing.T) {
	rt := &Runtime{
		SwitchChannel: func(_ string) error {
			return nil
		},
	}

	reply := runCommand(t, rt, "/check channel")

	if reply != "Usage: /check channel <name>" {
		t.Fatalf("reply=%q, want usage message", reply)
	}
}
