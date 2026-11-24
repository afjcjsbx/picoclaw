package a2a

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/config"
)

type hit struct {
	header http.Header
	body   proto.StreamResult
}

// webhook records every POST it gets and answers statuses in order (last repeats).
func webhook(t *testing.T, statuses ...int) (url string, hits chan hit) {
	t.Helper()
	hits = make(chan hit, 8)
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var h hit
		h.header = r.Header.Clone()
		_ = json.Unmarshal(b, &h.body)
		hits <- h
		i := int(n.Add(1)) - 1
		if len(statuses) == 0 {
			return
		}
		w.WriteHeader(statuses[min(i, len(statuses)-1)])
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/hook?secret=1", hits
}

func recv(t *testing.T, hits <-chan hit) hit {
	t.Helper()
	select {
	case h := <-hits:
		return h
	case <-time.After(3 * time.Second):
		t.Fatal("no webhook delivery")
		return hit{}
	}
}

func sendWithHook(hook map[string]any) map[string]any {
	p := msg("m1", "c1", "ping")
	p["configuration"] = map[string]any{"returnImmediately": true, "taskPushNotificationConfig": hook}
	return p
}

func TestPushOnSendMessageDeliversFinalTask(t *testing.T) {
	r := newRig(t, config.A2ASettings{PushAllowPrivate: true})
	url, hits := webhook(t)
	r.agent("pong")
	_, out := r.rpc("", "SendMessage", sendWithHook(map[string]any{
		"url": url, "token": "tk-1", "authentication": map[string]any{"scheme": "Bearer", "credentials": "cred"},
	}))
	assert.NotEqual(t, proto.StateCompleted, state(taskOf(t, out)), "returnImmediately must not wait")

	h := recv(t, hits)
	require.NotNil(t, h.body.Task)
	assert.Equal(t, proto.StateCompleted, h.body.Task.Status.State)
	assert.Equal(t, "pong", h.body.Task.Text())
	assert.Empty(t, h.body.Task.History, "webhook payload carries no history")
	assert.Equal(t, "Bearer cred", h.header.Get("Authorization"))
	// Go canonicalizes received names; on the wire the server sends X-A2A-Notification-Token.
	assert.Equal(t, "tk-1", h.header.Get("X-A2a-Notification-Token"))
	assert.Equal(t, "application/a2a+json", h.header.Get("Content-Type"))
	select {
	case <-hits:
		t.Fatal("exactly one delivery expected")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPushConfigLifecycleAndSecretsAreWriteOnly(t *testing.T) {
	r := newRig(t, config.A2ASettings{PushAllowPrivate: true})
	url, hits := webhook(t)
	_, out := r.rpc("", "SendMessage", map[string]any{
		"message":       msg("m1", "c1", "ping")["message"],
		"configuration": map[string]any{"returnImmediately": true},
	})
	id := taskOf(t, out)["id"].(string)

	_, out = r.rpc("", "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": id, "id": "cfg1", "url": url, "token": "sekret",
		"authentication": map[string]any{"scheme": "Bearer", "credentials": "hunter2"},
	})
	require.Nil(t, out["error"], "%v", out)
	res := out["result"].(map[string]any)
	assert.Equal(t, "cfg1", res["id"])
	assert.Equal(t, id, res["taskId"])
	assert.NotContains(t, res, "token")
	assert.NotContains(t, res["authentication"], "credentials")

	_, out = r.rpc("", "GetTaskPushNotificationConfig", map[string]any{"taskId": id, "id": "cfg1"})
	assert.Equal(t, url, out["result"].(map[string]any)["url"])
	_, out = r.rpc("", "ListTaskPushNotificationConfigs", map[string]any{"taskId": id})
	assert.Len(t, out["result"].(map[string]any)["configs"], 1)

	_, out = r.rpc("", "DeleteTaskPushNotificationConfig", map[string]any{"taskId": id, "id": "cfg1"})
	assert.Nil(t, out["error"])
	_, out = r.rpc("", "GetTaskPushNotificationConfig", map[string]any{"taskId": id, "id": "cfg1"})
	assert.Equal(t, proto.CodeTaskNotFound, errCode(out))
	_, out = r.rpc("", "ListTaskPushNotificationConfigs", map[string]any{"taskId": id})
	assert.Empty(t, out["result"].(map[string]any)["configs"])

	// A deleted webhook is never called.
	r.agent("pong")
	require.Eventually(t, func() bool {
		_, o := r.rpc("", "GetTask", map[string]any{"id": id})
		return state(taskOf(t, o)) == proto.StateCompleted
	}, 3*time.Second, 20*time.Millisecond)
	select {
	case <-hits:
		t.Fatal("deleted webhook was called")
	case <-time.After(150 * time.Millisecond):
	}
}

func TestPushRejectsUnsafeAndOversizedConfigs(t *testing.T) {
	r := newRig(t, config.A2ASettings{}) // PushAllowPrivate off
	_, out := r.rpc("", "SendMessage", map[string]any{
		"message":       msg("m1", "c1", "ping")["message"],
		"configuration": map[string]any{"returnImmediately": true},
	})
	id := taskOf(t, out)["id"].(string)
	bad := []map[string]any{
		{"url": "http://127.0.0.1:9/x"},
		{"url": "http://localhost/x"},
		{"url": "http://169.254.169.254/latest/meta-data"},
		{"url": "http://[::1]/x"},
		{"url": "file:///etc/passwd"},
		{"url": "https://user:pw@example.com/x"},
		{"url": "https://example.com/x", "authentication": map[string]any{"scheme": "Bearer\r\nX-Evil: 1"}},
		{"url": "https://example.com/x", "id": "../x"},
		{"url": ""},
	}
	for _, b := range bad {
		b["taskId"] = id
		_, out = r.rpc("", "CreateTaskPushNotificationConfig", b)
		assert.Equal(t, proto.CodeInvalidParams, errCode(out), "%v", b)
	}
	// The same URL is also refused when it rides on SendMessage, and no task is created for it.
	_, out = r.rpc("", "SendMessage", sendWithHook(map[string]any{"url": "http://127.0.0.1:9/x"}))
	assert.Equal(t, proto.CodeInvalidParams, errCode(out))

	for i := range maxPushPerTask {
		_, out = r.rpc("", "CreateTaskPushNotificationConfig", map[string]any{
			"taskId": id, "id": string(rune('a' + i)), "url": "https://example.com/x",
		})
		require.Nil(t, out["error"])
	}
	_, out = r.rpc("", "CreateTaskPushNotificationConfig", map[string]any{
		"taskId": id, "id": "overflow", "url": "https://example.com/x",
	})
	assert.Equal(t, proto.CodeInvalidParams, errCode(out))
}

func TestPushIsPerPrincipalAndRefusedOnFinishedTasks(t *testing.T) {
	r := newRig(t, config.A2ASettings{Tokens: config.SimpleSecureStrings("a", "b"), PushAllowPrivate: true})
	r.agent("pong")
	_, out := r.rpc("a", "SendMessage", msg("m1", "c1", "ping"))
	id := taskOf(t, out)["id"].(string) // blocking: already completed

	_, out = r.rpc(
		"a",
		"CreateTaskPushNotificationConfig",
		map[string]any{"taskId": id, "url": "https://example.com/x"},
	)
	assert.Equal(t, proto.CodeUnsupportedOp, errCode(out), "nothing left to notify")
	_, out = r.rpc("b", "ListTaskPushNotificationConfigs", map[string]any{"taskId": id})
	assert.Equal(t, proto.CodeTaskNotFound, errCode(out), "other principal must not see the task")
	_, out = r.rpc("a", "ListTaskPushNotificationConfigs", map[string]any{})
	assert.Equal(t, proto.CodeInvalidParams, errCode(out))
}

func TestPushRetriesTransientFailuresOnly(t *testing.T) {
	pushBackoff = 10 * time.Millisecond
	t.Cleanup(func() { pushBackoff = time.Second })

	r := newRig(t, config.A2ASettings{PushAllowPrivate: true})
	url, hits := webhook(t, 503, 200)
	r.agent("pong")
	r.rpc("", "SendMessage", sendWithHook(map[string]any{"url": url}))
	recv(t, hits)
	recv(t, hits) // second attempt succeeds

	url, hits = webhook(t, 400) // permanent: no retry
	r2 := newRig(t, config.A2ASettings{PushAllowPrivate: true})
	r2.agent("pong")
	r2.rpc("", "SendMessage", sendWithHook(map[string]any{"url": url}))
	recv(t, hits)
	select {
	case <-hits:
		t.Fatal("4xx must not be retried")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestCardAdvertisesPush(t *testing.T) {
	r := newRig(t, config.A2ASettings{})
	resp, err := http.Get(r.url + proto.WellKnownPath)
	require.NoError(t, err)
	defer resp.Body.Close()
	var card proto.AgentCard
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&card))
	assert.True(t, card.Capabilities.PushNotifications)
}

func TestPushDialGuardBlocksPrivateEvenIfURLCheckIsBypassed(t *testing.T) {
	// A hostname that passes the early check but resolves to a private address
	// (DNS rebinding) is only stopped at connect time; post() skips the early check.
	url, hits := webhook(t)
	r := newRig(t, config.A2ASettings{})
	retry, err := r.ch.post(pushCfg{url: url}, []byte("{}"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "blocked private")
	assert.NotContains(t, err.Error(), "secret=1", "errors must not leak the webhook URL")
	assert.True(t, retry)
	assert.Empty(t, hits, "the request must never have been sent")
}
