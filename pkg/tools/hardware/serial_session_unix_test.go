//go:build linux || darwin

package hardwaretools

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

func useLooseSerialPortNormalizer(t *testing.T) {
	t.Helper()

	previous := serialNormalizePort
	serialNormalizePort = func(port string) (string, error) {
		return strings.TrimSpace(port), nil
	}
	t.Cleanup(func() { serialNormalizePort = previous })
}

type ptyPair struct {
	master *os.File
	slave  *os.File
}

func openPTYPair(t *testing.T) ptyPair {
	t.Helper()

	master, slave, err := pty.Open()
	if err != nil {
		t.Fatalf("pty.Open() error = %v", err)
	}
	t.Cleanup(func() {
		master.Close()
		slave.Close()
	})
	return ptyPair{master: master, slave: slave}
}

func decodeResultJSON(t *testing.T, result *ToolResult) map[string]any {
	t.Helper()

	if result == nil {
		t.Fatal("nil tool result")
	}
	if result.IsError {
		t.Fatalf("tool error: %s", result.ForLLM)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &decoded); err != nil {
		t.Fatalf("result is not JSON: %v (%q)", err, result.ForLLM)
	}
	return decoded
}

func TestSerialSessionLifecycle(t *testing.T) {
	useLooseSerialPortNormalizer(t)

	pair := openPTYPair(t)
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(true, nil, false, 0),
	})
	tool.sessionTTL = 2 * time.Second
	t.Cleanup(tool.CloseAll)

	openResult := decodeResultJSON(t, tool.Execute(context.Background(), map[string]any{
		"action": "open",
		"port":   pair.slave.Name(),
	}))
	if openResult["session"] != "open" {
		t.Fatalf("open result = %#v", openResult)
	}

	go func() {
		time.Sleep(20 * time.Millisecond)
		_, _ = pair.master.Write([]byte("hello"))
	}()
	readResult := decodeResultJSON(t, tool.Execute(context.Background(), map[string]any{
		"action":     "read",
		"port":       pair.slave.Name(),
		"length":     float64(5),
		"timeout_ms": float64(1000),
	}))
	payload, _ := readResult["payload"].(map[string]any)
	if payload["text"] != "hello" {
		t.Fatalf("session read payload = %#v, want hello", payload)
	}

	written := make(chan string, 1)
	go func() {
		buf := make([]byte, 4)
		n, err := io.ReadFull(pair.master, buf)
		if err == nil {
			written <- string(buf[:n])
		}
	}()
	writeResult := decodeResultJSON(t, tool.Execute(context.Background(), map[string]any{
		"action":  "write",
		"port":    pair.slave.Name(),
		"text":    "ping",
		"confirm": true,
	}))
	if writeResult["written"] != float64(4) {
		t.Fatalf("write result = %#v", writeResult)
	}
	select {
	case got := <-written:
		if got != "ping" {
			t.Fatalf("master read %q, want ping", got)
		}
	case <-time.After(time.Second):
		t.Fatal("master did not receive the session write")
	}

	sessions := decodeResultJSON(t, tool.Execute(context.Background(), map[string]any{
		"action": "sessions",
	}))
	if sessions["count"] != float64(1) {
		t.Fatalf("sessions result = %#v, want one session", sessions)
	}

	closeResult := decodeResultJSON(t, tool.Execute(context.Background(), map[string]any{
		"action": "close",
		"port":   pair.slave.Name(),
	}))
	if closeResult["closed"] != true {
		t.Fatalf("close result = %#v, want closed", closeResult)
	}
	if got := tool.sessionCount(); got != 0 {
		t.Fatalf("session count after close = %d, want 0", got)
	}
}

func TestSerialSessionExpiresAfterIdleTTL(t *testing.T) {
	useLooseSerialPortNormalizer(t)

	pair := openPTYPair(t)
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(true, nil, false, 0),
	})
	tool.sessionTTL = 50 * time.Millisecond
	t.Cleanup(tool.CloseAll)

	if result := tool.Execute(context.Background(), map[string]any{
		"action": "open",
		"port":   pair.slave.Name(),
	}); result.IsError {
		t.Fatalf("open failed: %s", result.ForLLM)
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if tool.sessionCount() == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session did not expire after its idle TTL")
}

func TestSerialSessionConfigMismatchIsRejected(t *testing.T) {
	useLooseSerialPortNormalizer(t)

	pair := openPTYPair(t)
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(true, nil, false, 0),
	})
	t.Cleanup(tool.CloseAll)

	if result := tool.Execute(context.Background(), map[string]any{
		"action": "open",
		"port":   pair.slave.Name(),
		"baud":   float64(115200),
	}); result.IsError {
		t.Fatalf("open failed: %s", result.ForLLM)
	}

	result := tool.Execute(context.Background(), map[string]any{
		"action": "read",
		"port":   pair.slave.Name(),
		"baud":   float64(9600),
		"length": float64(1),
	})
	if result == nil || !result.IsError || !strings.Contains(result.ForLLM, "different settings") {
		t.Fatalf("read result = %#v, want a settings mismatch", result)
	}
}

func TestSerialClearSessionClosesOnlyOwnedSessions(t *testing.T) {
	useLooseSerialPortNormalizer(t)

	pairA := openPTYPair(t)
	pairB := openPTYPair(t)
	tool := NewSerialTool(SerialOptions{
		Policy: NewSerialWritePolicy(true, nil, false, 0),
	})
	t.Cleanup(tool.CloseAll)

	ctxA := toolshared.WithToolSessionContext(context.Background(), "agent-a", "session-a", nil)
	ctxB := toolshared.WithToolSessionContext(context.Background(), "agent-b", "session-b", nil)
	if result := tool.Execute(ctxA, map[string]any{"action": "open", "port": pairA.slave.Name()}); result.IsError {
		t.Fatalf("open A failed: %s", result.ForLLM)
	}
	if result := tool.Execute(ctxB, map[string]any{"action": "open", "port": pairB.slave.Name()}); result.IsError {
		t.Fatalf("open B failed: %s", result.ForLLM)
	}

	tool.ClearSession("agent-a", "session-a")
	if got := tool.sessionCount(); got != 1 {
		t.Fatalf("session count after clear = %d, want 1", got)
	}
}

func (t *SerialTool) sessionCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sessions)
}
