package a2a

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	proto "github.com/sipeed/picoclaw/pkg/a2a"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/utils"
)

// Push notifications (A2A 1.0 §9.4.7): a client registers a webhook per task and
// the agent POSTs the final state to it. Resource model: no goroutine, timer or
// connection exists until a task with a webhook finishes; then one short-lived
// goroutine per webhook delivers a single StreamResponse{task} (artifacts
// included, history left out, <= ~64 KiB). Intermediate states and token
// deltas are NOT pushed: clients that want them use SubscribeToTask.
const (
	maxPushPerTask = 3
	pushTimeout    = 10 * time.Second // spec recommends 10-30 s
	pushAttempts   = 3                // pushBackoff, then 4x, between attempts
	pushParallel   = 4                // concurrent POSTs across all tasks
	tokenHeader    = "X-A2A-Notification-Token"
)

var (
	errStopping = errors.New("server stopping")
	pushBackoff = time.Second // first retry delay, x4 after each failure (a var for tests)
)

// pushCfg is a webhook as stored: secrets live only while the task runs.
type pushCfg struct {
	id, url, token, scheme, creds string
}

// wire renders the config for responses. token and credentials are write-only:
// they are never echoed back, so a leaked response cannot leak the webhook secret.
func (p pushCfg) wire(taskID string) proto.PushConfig {
	out := proto.PushConfig{ID: p.id, TaskID: taskID, URL: p.url}
	if p.scheme != "" {
		out.Authentication = &proto.AuthInfo{Scheme: p.scheme}
	}
	return out
}

// newPush validates client-supplied webhook settings (a trust boundary: this
// makes the server issue requests to an address the client chose). The connect
// time guard in pushClient re-checks every resolved IP, so DNS rebinding cannot
// slip past this early check.
func (c *Channel) newPush(in *proto.PushConfig) (pushCfg, error) {
	p := pushCfg{id: in.ID, url: in.URL, token: in.Token}
	if p.id == "" {
		p.id = proto.NewID()
	}
	u, err := url.Parse(in.URL)
	switch {
	case !validID(p.id):
		return p, errors.New("id must be 1-128 chars of [A-Za-z0-9._-]")
	case err != nil || len(in.URL) > 1024 || u.User != nil:
		return p, errors.New("url must be an http(s) URL of at most 1024 chars without credentials")
	case len(in.Token) > 128:
		return p, errors.New("token is limited to 128 bytes")
	}
	if err := utils.ValidateSafeHTTPURL(in.URL, nil, c.allowPrivatePush); err != nil {
		return p, err
	}
	if a := in.Authentication; a != nil {
		if !validScheme(a.Scheme) || len(a.Credentials) > 512 {
			return p, errors.New("authentication needs a scheme (e.g. Bearer) and credentials of at most 512 bytes")
		}
		p.scheme, p.creds = a.Scheme, a.Credentials
	}
	return p, nil
}

func (c *Channel) allowPrivatePush() bool { return c.cfg.PushAllowPrivate }

func validScheme(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// newPushClient never follows redirects and never uses a proxy: the dial guard is
// the SSRF boundary, and both would route around it.
func (c *Channel) newPushClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: utils.NewSafeDialContext(
				&net.Dialer{Timeout: 5 * time.Second}, nil, c.allowPrivatePush,
			),
			DisableKeepAlives:     true, // deliveries are rare: idle sockets would only cost RAM
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: pushTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// handlePush serves the four *TaskPushNotificationConfig methods.
func (c *Channel) handlePush(w http.ResponseWriter, who string, req rpcRequest) {
	var p proto.PushConfig
	if json.Unmarshal(req.Params, &p) != nil || p.TaskID == "" {
		writeErr(w, req.ID, proto.CodeInvalidParams, "params.taskId is required")
		return
	}
	t := c.reg.get(who, p.TaskID)
	if t == nil {
		writeErr(w, req.ID, proto.CodeTaskNotFound, "task not found")
		return
	}
	switch req.Method {
	case "CreateTaskPushNotificationConfig":
		pc, err := c.newPush(&p)
		if err == nil {
			err = c.reg.addPush(t, pc)
		}
		switch {
		case errors.Is(err, errPushEnd):
			writeErr(w, req.ID, proto.CodeUnsupportedOp, err.Error())
		case err != nil:
			writeErr(w, req.ID, proto.CodeInvalidParams, err.Error())
		default:
			writeResult(w, req.ID, pc.wire(t.id))
		}
	case "GetTaskPushNotificationConfig":
		if p.ID == "" {
			writeErr(w, req.ID, proto.CodeInvalidParams, "params.id is required")
		} else if l := c.reg.pushList(t, p.ID); len(l) == 0 {
			writeErr(w, req.ID, proto.CodeTaskNotFound, "push notification config not found")
		} else {
			writeResult(w, req.ID, l[0].wire(t.id))
		}
	case "ListTaskPushNotificationConfigs": // at most maxPushPerTask: always a single page
		cfgs := []proto.PushConfig{}
		for _, pc := range c.reg.pushList(t, "") {
			cfgs = append(cfgs, pc.wire(t.id))
		}
		writeResult(w, req.ID, map[string]any{"configs": cfgs, "nextPageToken": ""})
	default: // DeleteTaskPushNotificationConfig
		if p.ID == "" {
			writeErr(w, req.ID, proto.CodeInvalidParams, "params.id is required")
		} else if !c.reg.delPush(t, p.ID) {
			writeErr(w, req.ID, proto.CodeTaskNotFound, "push notification config not found")
		} else {
			writeResult(w, req.ID, map[string]any{}) // google.protobuf.Empty
		}
	}
}

// notify is the registry's onDone hook (runs under the registry lock): it only
// spawns, the network work happens outside the lock.
func (c *Channel) notify(snap proto.Task, cfgs []pushCfg) {
	for _, p := range cfgs {
		go c.deliver(p, snap)
	}
}

// deliver POSTs the task to one webhook: at least once, up to pushAttempts
// tries, only retrying what can heal (network errors, 408/429/5xx).
func (c *Channel) deliver(p pushCfg, snap proto.Task) {
	body, err := json.Marshal(proto.StreamResult{Task: &snap})
	if err != nil {
		return
	}
	for i, wait := 0, pushBackoff; ; i, wait = i+1, wait*4 {
		retry, err := c.post(p, body)
		if err == nil || !retry || i+1 == pushAttempts || errors.Is(err, errStopping) {
			if err != nil && !errors.Is(err, errStopping) {
				// Host only: the path and query of a webhook may embed a secret.
				logger.WarnCF("a2a", "Push notification not delivered", map[string]any{
					"task": snap.ID, "host": hostOf(p.url), "error": err.Error(),
				})
			}
			return
		}
		select {
		case <-time.After(wait):
		case <-c.quitCh():
			return
		}
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
}

func (c *Channel) post(p pushCfg, body []byte) (retry bool, err error) {
	select {
	case <-c.quitCh():
		return false, errStopping
	case c.pushSem <- struct{}{}:
		defer func() { <-c.pushSem }()
	}
	ctx, cancel := context.WithTimeout(c.runCtx(), pushTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.url, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/a2a+json")
	if p.scheme != "" {
		req.Header.Set("Authorization", p.scheme+" "+p.creds)
	}
	if p.token != "" {
		req.Header[tokenHeader] = []string{p.token} // set raw: keeps the conventional spelling on the wire
	}
	resp, err := c.push.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // url.Error embeds the full URL, whose query may be a secret
		}
		return true, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	switch s := resp.StatusCode; {
	case s/100 == 2:
		return false, nil
	case s == http.StatusRequestTimeout || s == http.StatusTooManyRequests || s >= 500:
		return true, fmt.Errorf("webhook answered http %d", s)
	}
	return false, fmt.Errorf("webhook answered http %d", resp.StatusCode)
}
