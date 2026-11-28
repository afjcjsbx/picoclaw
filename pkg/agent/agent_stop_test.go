package agent

import "testing"

func TestCancelSessionArmsPendingStop(t *testing.T) {
	loop := &AgentLoop{}
	if err := loop.CancelSession("queued-session"); err != nil {
		t.Fatalf("CancelSession() error = %v", err)
	}
	if loop.SessionIdle("queued-session") {
		t.Fatal("session with a pending stop is not idle")
	}
	if !loop.takePendingStop("queued-session") {
		t.Fatal("CancelSession() did not arm a pending stop")
	}
	if !loop.SessionIdle("queued-session") {
		t.Fatal("session is not idle after consuming the pending stop")
	}
}

func TestCancelSessionHandlesPendingPlaceholder(t *testing.T) {
	loop := &AgentLoop{}
	loop.activeTurnStates.Store("pending-session", &turnState{turnID: "pending-test"})
	if err := loop.CancelSession("pending-session"); err != nil {
		t.Fatalf("CancelSession() error = %v", err)
	}
	if !loop.takePendingStop("pending-session") {
		t.Fatal("CancelSession() did not stop a pending placeholder")
	}
}
