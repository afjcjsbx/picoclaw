package tools

import (
	"strings"
	"testing"

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

	text, next, total, dropped, hasMore := session.ReadRange(2, 3)
	require.Equal(t, "cde", text)
	require.Equal(t, int64(5), next)
	require.Equal(t, int64(10), total)
	require.Zero(t, dropped)
	require.True(t, hasMore)

	text, next, total, dropped, hasMore = session.ReadRange(8, 10)
	require.Equal(t, "ij", text)
	require.Equal(t, int64(10), next)
	require.Equal(t, int64(10), total)
	require.Zero(t, dropped)
	require.False(t, hasMore)
}

func TestProcessSession_ReadRangeReportsDroppedPrefix(t *testing.T) {
	session := &ProcessSession{Status: "running"}
	payload := strings.Repeat("x", maxOutputBufferSize+100)
	session.appendOutput([]byte(payload))

	dropped := session.DroppedBytes()
	require.Positive(t, dropped)

	text, next, total, skipped, hasMore := session.ReadRange(0, 10)
	require.Len(t, text, 10)
	require.Equal(t, dropped, skipped)
	require.Equal(t, dropped+10, next)
	require.Equal(t, int64(len(payload)), total)
	require.True(t, hasMore)
}
