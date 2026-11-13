package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/plugins/evolution/internal/evolution"
)

func TestServer_RecordsCompletedTurn(t *testing.T) {
	dataDir := t.TempDir()
	srv := New(Options{DataDir: dataDir, Config: evolution.DefaultConfig()})
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(inR, io.Discard) }()

	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"config":{"enabled":"true","mode":"observe"}}}`,
	)
	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":`+completedTurnEvent(
			"turn-1",
			"/tmp/workspace-a",
			"hello",
			"world",
		)+`}`,
	)

	recordPath := waitForFile(t, dataDir, "task-records.jsonl")
	body, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(body), `"ID":"turn-1`) && !strings.Contains(string(body), `"id":"turn-1`) {
		t.Fatalf("record does not contain turn id: %s", body)
	}
	if !strings.Contains(string(body), "hello") {
		t.Fatalf("record does not contain user message: %s", body)
	}

	_ = inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}

func TestServer_DisabledWritesNothing(t *testing.T) {
	dataDir := t.TempDir()
	srv := New(Options{DataDir: dataDir, Config: evolution.DefaultConfig()})
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(inR, io.Discard) }()

	writeLine(t, inW, `{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"config":{"enabled":"false"}}}`)
	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":`+completedTurnEvent(
			"turn-1",
			"/tmp/workspace-a",
			"hello",
			"world",
		)+`}`,
	)
	time.Sleep(200 * time.Millisecond)

	if matches, _ := filepath.Glob(filepath.Join(dataDir, "state", "*", "task-records.jsonl")); len(matches) > 0 {
		t.Fatalf("disabled evolution wrote state: %v", matches)
	}

	_ = inW.Close()
	<-done
}

func TestServer_HelloRequiresDataDir(t *testing.T) {
	srv := New(Options{Config: evolution.DefaultConfig()})
	inR, inW := io.Pipe()
	out := &safeBuffer{}
	done := make(chan error, 1)
	go func() { done <- srv.Run(inR, out) }()

	writeLine(t, inW, `{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{}}`)
	_ = inW.Close()
	<-done

	if !strings.Contains(out.String(), `"error"`) {
		t.Fatalf("expected hello error, got %q", out.String())
	}
}

func TestServer_IgnoresHeartbeatAndNonTurnEnd(t *testing.T) {
	dataDir := t.TempDir()
	srv := New(Options{DataDir: dataDir, Config: evolution.DefaultConfig()})
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(inR, io.Discard) }()

	writeLine(t, inW, `{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"config":{}}}`)
	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":`+completedTurnEvent(
			"turn-1",
			"/tmp/workspace-a",
			"hello",
			"world",
		)+`}`,
	)
	writeLine(t, inW, `{"kind":"agent.turn.start","scope":{"session_key":"heartbeat"},"payload":{}}`)
	writeLine(
		t,
		inW,
		`{"kind":"agent.turn.end","scope":{"session_key":"heartbeat"},"payload":{"Status":"completed","Workspace":"/tmp/workspace-a"}}`,
	)
	time.Sleep(300 * time.Millisecond)

	matches, _ := filepath.Glob(filepath.Join(dataDir, "state", "*", "task-records.jsonl"))
	if len(matches) != 1 {
		t.Fatalf("state files = %v, want exactly one", matches)
	}
	body, _ := os.ReadFile(matches[0])
	if strings.Contains(string(body), "heartbeat") {
		t.Fatalf("heartbeat turn was recorded: %s", body)
	}

	_ = inW.Close()
	<-done
}

func TestServer_StateDirOverride(t *testing.T) {
	dataDir := t.TempDir()
	srv := New(Options{DataDir: dataDir, Config: evolution.DefaultConfig()})
	inR, inW := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.Run(inR, io.Discard) }()

	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","id":1,"method":"hook.hello","params":{"config":{"enabled":"true","mode":"observe","state_dir":"custom-state"}}}`,
	)
	writeLine(
		t,
		inW,
		`{"jsonrpc":"2.0","method":"hook.runtime_event","params":`+completedTurnEvent(
			"turn-1",
			"/tmp/workspace-a",
			"hello",
			"world",
		)+`}`,
	)

	recordPath := filepath.Join(dataDir, "custom-state", "task-records.jsonl")
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(recordPath); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := os.Stat(recordPath); err != nil {
		t.Fatalf("expected state at %s: %v", recordPath, err)
	}

	_ = inW.Close()
	<-done
}

func TestServer_ResolveLLMAPIKeyFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, "key.txt"), []byte("  secret-key\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srv := New(Options{DataDir: dataDir, Config: evolution.DefaultConfig()})
	cfg := evolution.LLMConfig{APIKeyFile: "key.txt"}
	srv.resolveLLMAPIKey(&cfg)
	if cfg.APIKey != "secret-key" {
		t.Fatalf("APIKey = %q, want secret-key", cfg.APIKey)
	}
}

func TestParseColdPathTimes(t *testing.T) {
	schedule := parseColdPathTimes([]string{"09:30", "bad", "25:00", "09:30", "07:05"})
	if len(schedule) != 2 {
		t.Fatalf("schedule = %+v, want two entries", schedule)
	}
	if schedule[0].hour != 7 || schedule[0].minute != 5 {
		t.Fatalf("first = %+v, want 07:05", schedule[0])
	}
	if schedule[1].hour != 9 || schedule[1].minute != 30 {
		t.Fatalf("second = %+v, want 09:30", schedule[1])
	}
}

func TestRuntimeEventDecodesPayloadFieldNames(t *testing.T) {
	var evt runtimeEvent
	raw := completedTurnEvent("turn-1", "/tmp/ws", "hi", "done")
	if err := json.Unmarshal([]byte(raw), &evt); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if evt.Payload.Workspace != "/tmp/ws" || evt.Payload.Status != "completed" {
		t.Fatalf("payload = %+v", evt.Payload)
	}
	if len(evt.Payload.ToolExecutions) != 1 || evt.Payload.ToolExecutions[0].Name != "weather" {
		t.Fatalf("tool executions = %+v", evt.Payload.ToolExecutions)
	}
}

func completedTurnEvent(turnID, workspace, userMessage, finalContent string) string {
	return fmt.Sprintf(
		`{"kind":"agent.turn.end","scope":{"agent_id":"agent","session_key":"session","turn_id":%q},"payload":{"Status":"completed","Workspace":%q,"UserMessage":%q,"FinalContent":%q,"ActiveSkills":["weather"],"AttemptedSkills":["weather"],"FinalSuccessfulPath":["weather"],"ToolKinds":["weather"],"SkillContextSnapshots":[{"sequence":1,"trigger":"initial_build","skill_names":["weather"]}],"ToolExecutions":[{"name":"weather","success":true,"skill_names":["weather"]}]}}`,
		turnID,
		workspace,
		userMessage,
		finalContent,
	)
}

func writeLine(t *testing.T, w io.Writer, line string) {
	t.Helper()
	if _, err := fmt.Fprintln(w, line); err != nil {
		t.Fatalf("write line: %v", err)
	}
}

func waitForFile(t *testing.T, root, name string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		matches, _ := filepath.Glob(filepath.Join(root, "state", "*", name))
		if len(matches) > 0 {
			return matches[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s under %s", name, root)
	return ""
}

type safeBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
