package tools

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSessionManager_AddGet(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	session := &ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}

	sm.Add(session)

	got, err := sm.Get("test-1")
	require.NoError(t, err)
	require.Equal(t, "test-1", got.ID)
}

func TestSessionManager_Remove(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	session := &ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}
	sm.Add(session)
	sm.Remove("test-1")

	_, err := sm.Get("test-1")
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSessionManager_List(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)
	sm.Add(&ProcessSession{
		ID:        "test-1",
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	})
	sm.Add(&ProcessSession{
		ID:        "test-2",
		Command:   "echo world",
		Status:    "running",
		StartTime: 1001,
	})
	sm.Add(&ProcessSession{
		ID:        "test-3",
		Command:   "echo done",
		Status:    "done",
		StartTime: 1002,
	})

	sessions := sm.List()
	require.Len(t, sessions, 3)

	ids := make(map[string]bool)
	for _, s := range sessions {
		ids[s.ID] = true
	}
	require.True(t, ids["test-1"])
	require.True(t, ids["test-2"])
	require.True(t, ids["test-3"])
}

func TestProcessSession_IsDone(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	require.False(t, session.IsDone())

	session.Status = "done"
	require.True(t, session.IsDone())

	session.Status = "exited"
	require.True(t, session.IsDone())
}

func TestProcessSession_ToSessionInfo(t *testing.T) {
	session := &ProcessSession{
		ID:        "test-1",
		PID:       12345,
		Command:   "echo hello",
		Status:    "running",
		StartTime: 1000,
	}

	info := session.ToSessionInfo()
	require.Equal(t, "test-1", info.ID)
	require.Equal(t, "echo hello", info.Command)
	require.Equal(t, "running", info.Status)
	require.Equal(t, 12345, info.PID)
	require.Equal(t, int64(1000), info.StartedAt)
}

func TestSessionInfo_ExitCodeZeroIsSerialized(t *testing.T) {
	info := (&ProcessSession{
		ID:       "done",
		Status:   "done",
		ExitCode: 0,
	}).ToSessionInfo()

	data, err := json.Marshal(info)
	require.NoError(t, err)
	require.Contains(t, string(data), `"exitCode":0`)
}

func TestProcessSession_ReadIsNonDestructive(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	session.appendOutput([]byte("hello"))

	require.Equal(t, "hello", session.Read())
	require.Equal(t, "hello", session.Read())
	require.Equal(t, int64(5), session.TotalBytes())
}

func TestProcessSession_ReadRangePages(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	session.appendOutput([]byte("abcdefghij"))

	page := session.ReadRange(2, 3)
	require.Equal(t, "cde", page.Text)
	require.Equal(t, int64(2), page.Offset)
	require.Equal(t, int64(5), page.NextOffset)
	require.Equal(t, int64(10), page.TotalBytes)
	require.Zero(t, page.DroppedBytes)
	require.Zero(t, page.SkippedBytes)
	require.True(t, page.HasMore)

	page = session.ReadRange(8, 10)
	require.Equal(t, "ij", page.Text)
	require.Equal(t, int64(8), page.Offset)
	require.Equal(t, int64(10), page.NextOffset)
	require.Equal(t, int64(10), page.TotalBytes)
	require.Zero(t, page.DroppedBytes)
	require.Zero(t, page.SkippedBytes)
	require.False(t, page.HasMore)
}

func TestProcessSession_ReadRangeReportsDroppedPrefix(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	payload := strings.Repeat("x", maxOutputBufferSize+100)
	session.appendOutput([]byte(payload))

	dropped := session.DroppedBytes()
	require.Positive(t, dropped)

	page := session.ReadRange(0, 10)
	require.Len(t, page.Text, 10)
	require.Equal(t, dropped, page.DroppedBytes)
	require.Equal(t, dropped, page.SkippedBytes)
	require.Equal(t, dropped, page.Offset)
	require.Equal(t, dropped+10, page.NextOffset)
	require.Equal(t, int64(len(payload)), page.TotalBytes)
	require.True(t, page.HasMore)

	page = session.ReadRange(dropped, 10)
	require.Len(t, page.Text, 10)
	require.Equal(t, dropped, page.DroppedBytes)
	require.Zero(t, page.SkippedBytes)
	require.Equal(t, dropped, page.Offset)
}

func TestSessionManager_CleanupUsesExitTime(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)

	now := time.Now()
	sm.Add(&ProcessSession{
		ID:        "long-job",
		Status:    "done",
		StartTime: now.Add(-2 * time.Hour).Unix(),
		ExitTime:  now.Add(-time.Minute).Unix(),
	})
	sm.Add(&ProcessSession{
		ID:        "old-result",
		Status:    "done",
		StartTime: now.Add(-time.Hour).Unix(),
		ExitTime:  now.Add(-31 * time.Minute).Unix(),
	})

	sm.cleanupOldSessions()

	_, err := sm.Get("long-job")
	require.NoError(t, err, "a long job keeps its full post-exit retention window")
	_, err = sm.Get("old-result")
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestSessionManager_CleanupFallsBackToStartTime(t *testing.T) {
	sm := NewSessionManager()
	t.Cleanup(sm.Stop)

	sm.Add(&ProcessSession{
		ID:        "legacy-done",
		Status:    "done",
		StartTime: time.Now().Add(-time.Hour).Unix(),
	})

	sm.cleanupOldSessions()

	_, err := sm.Get("legacy-done")
	require.ErrorIs(t, err, ErrSessionNotFound)
}

func TestProcessSession_FinishSetsExitTimeAndClosesDone(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	session.finish(7)

	require.Equal(t, 7, session.GetExitCode())
	require.Equal(t, "done", session.GetStatus())
	require.Positive(t, session.ExitTime)
	select {
	case <-session.Done():
	default:
		t.Fatal("Done channel should be closed after finish")
	}
}
