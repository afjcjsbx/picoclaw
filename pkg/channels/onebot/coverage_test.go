package onebot

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

func newCoverageOneBot(t *testing.T, allowFrom ...string) (*OneBotChannel, *bus.MessageBus) {
	t.Helper()
	messageBus := bus.NewMessageBus()
	ch, err := NewOneBotChannel(
		&config.Channel{AllowFrom: config.FlexibleStringSlice(allowFrom)},
		&config.OneBotSettings{},
		messageBus,
	)
	if err != nil {
		t.Fatalf("NewOneBotChannel() error = %v", err)
	}
	ch.ctx = context.Background()
	return ch, messageBus
}

func startCoverageOneBotWS(t *testing.T, handle func(*websocket.Conn)) (string, <-chan *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	accepted := make(chan *websocket.Conn, 8)
	var mu sync.Mutex
	var conns []*websocket.Conn
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		mu.Lock()
		conns = append(conns, conn)
		mu.Unlock()
		accepted <- conn
		if handle != nil {
			handle(conn)
		}
	}))
	t.Cleanup(func() {
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		server.Close()
	})
	return "ws" + strings.TrimPrefix(server.URL, "http"), accepted
}

func disableCoverageOneBotProxy(t *testing.T) {
	t.Helper()
	oldProxy := websocket.DefaultDialer.Proxy
	websocket.DefaultDialer.Proxy = nil
	t.Cleanup(func() { websocket.DefaultDialer.Proxy = oldProxy })
}

func connectCoverageOneBot(
	t *testing.T,
	ch *OneBotChannel,
	url string,
	accepted <-chan *websocket.Conn,
) *websocket.Conn {
	t.Helper()
	disableCoverageOneBotProxy(t)
	ch.config.WSUrl = url
	ch.ctx, ch.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	if err := ch.connect(); err != nil {
		t.Fatalf("connect() error = %v", err)
	}
	select {
	case conn := <-accepted:
		return conn
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for WebSocket connection")
		return nil
	}
}

type coverageOneBotRequest struct {
	Action string `json:"action"`
	Params struct {
		UserID  int64                  `json:"user_id"`
		GroupID int64                  `json:"group_id"`
		Message []oneBotMessageSegment `json:"message"`
	} `json:"params"`
	Echo string `json:"echo"`
}

func readCoverageOneBotRequest(t *testing.T, conn *websocket.Conn) coverageOneBotRequest {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline() error = %v", err)
	}
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage() error = %v", err)
	}
	var req coverageOneBotRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("json.Unmarshal(request) error = %v", err)
	}
	return req
}

func waitCoverageOneBotInbound(t *testing.T, messageBus *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		return inbound
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for inbound message")
		return bus.InboundMessage{}
	}
}

func assertNoCoverageOneBotInbound(t *testing.T, messageBus *bus.MessageBus) {
	t.Helper()
	select {
	case inbound := <-messageBus.InboundChan():
		t.Fatalf("unexpected inbound message: %+v", inbound)
	default:
	}
}

func TestParseJSONHelpers(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     json.RawMessage
		want    int64
		wantErr bool
	}{
		{name: "empty", raw: nil},
		{name: "number", raw: json.RawMessage(`42`), want: 42},
		{name: "string", raw: json.RawMessage(`"42"`), want: 42},
		{name: "invalid number", raw: json.RawMessage(`true`), wantErr: true},
		{name: "invalid string", raw: json.RawMessage(`"nope"`), wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseJSONInt64(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseJSONInt64() error = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("parseJSONInt64() = %d, want %d", got, tc.want)
			}
		})
	}

	for _, tc := range []struct {
		raw  json.RawMessage
		want string
	}{
		{raw: nil},
		{raw: json.RawMessage(`"hello"`), want: "hello"},
		{raw: json.RawMessage(`123`), want: "123"},
	} {
		if got := parseJSONString(tc.raw); got != tc.want {
			t.Errorf("parseJSONString(%s) = %q, want %q", tc.raw, got, tc.want)
		}
	}

	for _, tc := range []struct {
		raw  json.RawMessage
		want bool
	}{
		{raw: nil},
		{raw: json.RawMessage(`"ok"`), want: true},
		{raw: json.RawMessage(`"failed"`), want: true},
		{raw: json.RawMessage(`"other"`)},
		{raw: json.RawMessage(`{"online":true}`), want: true},
		{raw: json.RawMessage(`{"good":true}`), want: true},
		{raw: json.RawMessage(`{"online":false,"good":false}`)},
		{raw: json.RawMessage(`not-json`)},
	} {
		if got := isAPIResponse(tc.raw); got != tc.want {
			t.Errorf("isAPIResponse(%s) = %v, want %v", tc.raw, got, tc.want)
		}
	}
}

func TestParseMessageSegments_TextMentionsAndMalformedValues(t *testing.T) {
	ch := &OneBotChannel{}

	result := ch.parseMessageSegments(json.RawMessage(`"  [CQ:at,qq=7] hello  "`), 7, nil, "scope")
	if result.Text != "hello" || !result.IsBotMentioned {
		t.Fatalf("string segment result = %+v, want mentioned hello", result)
	}

	result = ch.parseMessageSegments(json.RawMessage(`[
		{"type":"text","data":{"text":"hello "}},
		{"type":"at","data":{"qq":7}},
		{"type":"reply","data":{"id":99}},
		{"type":"face","data":{"id":1}},
		{"type":"forward"},
		{"type":"unknown","data":{}}
	]`), 7, nil, "scope")
	if result.Text != "hello [face:1][forward message]" || !result.IsBotMentioned || result.ReplyTo != "99" {
		t.Fatalf("array segment result = %+v", result)
	}

	result = ch.parseMessageSegments(json.RawMessage(`[{"type":"at","data":{"qq":"all"}}]`), 7, nil, "scope")
	if !result.IsBotMentioned {
		t.Fatal("at-all segment should mention the bot")
	}

	for _, raw := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`42`)} {
		if got := ch.parseMessageSegments(raw, 7, nil, "scope"); !reflect.DeepEqual(got, parseMessageResult{}) {
			t.Errorf("parseMessageSegments(%s) = %+v, want zero value", raw, got)
		}
	}
}

func TestParseMessageSegments_InboundMediaWithoutNetwork(t *testing.T) {
	tmpDir := t.TempDir()
	files := map[string]string{
		"picture.png": "picture.png",
		"clip.mp4":    "clip.mp4",
		"file":        "file",
		"voice.amr":   "voice.amr",
	}
	for filename := range files {
		if err := os.WriteFile(filepath.Join(tmpDir, filename), []byte(filename), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
	}

	var downloads []string
	ch := &OneBotChannel{downloadFn: func(urlStr, filename string) string {
		downloads = append(downloads, urlStr+"|"+filename)
		return filepath.Join(tmpDir, filename)
	}}
	store := media.NewFileMediaStore()
	raw := json.RawMessage(`[
		{"type":"image","data":{"url":"https://cdn.example/image","file":"picture.png"}},
		{"type":"video","data":{"url":"https://cdn.example/video","name":"clip.mp4"}},
		{"type":"file","data":{"url":"https://cdn.example/file"}},
		{"type":"record","data":{"url":"https://cdn.example/voice"}}
	]`)
	result := ch.parseMessageSegments(raw, 0, store, "onebot:group:1:m1")

	if result.Text != "[image][video][file][voice]" {
		t.Fatalf("Text = %q", result.Text)
	}
	if len(result.Media) != 4 {
		t.Fatalf("len(Media) = %d, want 4", len(result.Media))
	}
	if len(downloads) != 4 {
		t.Fatalf("len(downloads) = %d, want 4", len(downloads))
	}
	for i, ref := range result.Media {
		_, meta, err := store.ResolveWithMeta(ref)
		if err != nil {
			t.Fatalf("ResolveWithMeta(%q) error = %v", ref, err)
		}
		if meta.Source != "onebot" || meta.Filename != []string{"picture.png", "clip.mp4", "file", "voice.amr"}[i] {
			t.Errorf("media metadata = %+v", meta)
		}
	}

	ch.downloadFn = func(string, string) string { return "" }
	result = ch.parseMessageSegments(
		json.RawMessage(`[{"type":"image","data":{"url":"https://cdn.example/fail"}}]`),
		0,
		store,
		"scope",
	)
	if result.Text != "" || len(result.Media) != 0 {
		t.Fatalf("failed download result = %+v, want empty", result)
	}

	ch.downloadFn = func(string, string) string { return filepath.Join(tmpDir, "picture.png") }
	result = ch.parseMessageSegments(
		json.RawMessage(`[{"type":"image","data":{"url":"https://cdn.example/fallback","file":"picture.png"}}]`),
		0,
		nil,
		"scope",
	)
	if len(result.Media) != 1 || result.Media[0] != filepath.Join(tmpDir, "picture.png") {
		t.Fatalf("nil-store fallback media = %v", result.Media)
	}
}

func TestHandleRawEvent_RoutesPrivateAndGroupMessages(t *testing.T) {
	ch, messageBus := newCoverageOneBot(t)
	private := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "private",
		MessageID:   json.RawMessage(`"m1"`),
		UserID:      json.RawMessage(`"42"`),
		SelfID:      json.RawMessage(`7`),
		RawMessage:  "[CQ:at,qq=7] hello",
		Message:     json.RawMessage(`"hello"`),
		Sender:      json.RawMessage(`{"user_id":42,"nickname":"Alice"}`),
	}
	ch.handleRawEvent(private)

	inbound := waitCoverageOneBotInbound(t, messageBus)
	if inbound.Content != "hello" || inbound.Context.ChatID != "private:42" || inbound.Context.ChatType != "direct" {
		t.Fatalf("private inbound = %+v", inbound)
	}
	if !inbound.Context.Mentioned || inbound.Context.MessageID != "m1" || inbound.Context.Raw["nickname"] != "Alice" {
		t.Fatalf("private context = %+v", inbound.Context)
	}
	if inbound.Sender.CanonicalID != "onebot:42" || inbound.Sender.PlatformID != "42" {
		t.Fatalf("private sender = %+v", inbound.Sender)
	}

	group := &oneBotRawEvent{
		PostType:    "message",
		MessageType: "group",
		MessageID:   json.RawMessage(`1002`),
		UserID:      json.RawMessage(`42`),
		GroupID:     json.RawMessage(`99`),
		Message: json.RawMessage(`[
			{"type":"at","data":{"qq":7}},
			{"type":"text","data":{"text":"group hello"}},
			{"type":"reply","data":{"id":"m0"}}
		]`),
		Sender: json.RawMessage(`{"user_id":"42","card":"Alice Card","nickname":"Alice"}`),
	}
	ch.handleRawEvent(group)
	inbound = waitCoverageOneBotInbound(t, messageBus)
	if inbound.Content != "group hello" || inbound.Context.ChatID != "group:99" || inbound.Context.ChatType != "group" {
		t.Fatalf("group inbound = %+v", inbound)
	}
	if inbound.Context.ReplyToMessageID != "m0" || inbound.Context.Raw["reply_to_message_id"] != "m0" {
		t.Fatalf("group reply context = %+v", inbound.Context)
	}
	if inbound.Context.Raw["sender_name"] != "Alice Card" || inbound.Context.Raw["sender_user_id"] != "42" {
		t.Fatalf("group sender metadata = %v", inbound.Context.Raw)
	}

	ch.handleRawEvent(group)
	assertNoCoverageOneBotInbound(t, messageBus)
}

func TestHandleRawEvent_RejectsInvalidAndDisallowedMessages(t *testing.T) {
	ch, messageBus := newCoverageOneBot(t, "onebot:100")
	for _, raw := range []*oneBotRawEvent{
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`"not-a-number"`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`42`), MessageID: json.RawMessage(`1`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "unknown", UserID: json.RawMessage(`100`), MessageID: json.RawMessage(`2`), Message: json.RawMessage(`"hello"`)},
		{PostType: "message", MessageType: "private", UserID: json.RawMessage(`100`), MessageID: json.RawMessage(`3`), Message: json.RawMessage(`""`)},
		{PostType: "message_sent"},
		{PostType: "meta_event", MetaEventType: "heartbeat"},
		{PostType: "notice", NoticeType: "group_recall", GroupID: json.RawMessage(`99`)},
		{PostType: "request", SubType: "friend"},
		{PostType: "future_event"},
		{},
	} {
		ch.handleRawEvent(raw)
	}
	assertNoCoverageOneBotInbound(t, messageBus)
}

func TestOneBotDedupRingEviction(t *testing.T) {
	ch := &OneBotChannel{
		dedup:     make(map[string]struct{}, 2),
		dedupRing: make([]string, 2),
	}
	if ch.isDuplicate("") || ch.isDuplicate("0") {
		t.Fatal("empty and zero message IDs must not be deduplicated")
	}
	if ch.isDuplicate("a") || ch.isDuplicate("b") {
		t.Fatal("new message IDs must not be deduplicated")
	}
	if !ch.isDuplicate("a") {
		t.Fatal("existing message ID must be deduplicated")
	}
	if ch.isDuplicate("c") {
		t.Fatal("new message ID must not be deduplicated")
	}
	if ch.isDuplicate("a") {
		t.Fatal("oldest message ID should have been evicted")
	}
}

func TestBuildSendRequest(t *testing.T) {
	ch := &OneBotChannel{}
	ch.lastMessageID.Store("group:42", "m1")

	for _, tc := range []struct {
		chatID string
		action string
		idKey  string
		id     int64
	}{
		{chatID: "group:42", action: "send_group_msg", idKey: "group_id", id: 42},
		{chatID: "private:7", action: "send_private_msg", idKey: "user_id", id: 7},
		{chatID: "7", action: "send_private_msg", idKey: "user_id", id: 7},
	} {
		t.Run(tc.chatID, func(t *testing.T) {
			action, params, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: tc.chatID, Content: "hello"})
			if err != nil {
				t.Fatalf("buildSendRequest() error = %v", err)
			}
			if action != tc.action {
				t.Fatalf("action = %q, want %q", action, tc.action)
			}
			got := params.(map[string]any)
			if got[tc.idKey] != tc.id {
				t.Fatalf("%s = %v, want %d", tc.idKey, got[tc.idKey], tc.id)
			}
			segments := got["message"].([]oneBotMessageSegment)
			wantLen := 1
			if tc.chatID == "group:42" {
				wantLen = 2
				if segments[0].Type != "reply" || segments[0].Data["id"] != "m1" {
					t.Fatalf("reply segment = %+v", segments[0])
				}
			}
			if len(segments) != wantLen || segments[wantLen-1].Data["text"] != "hello" {
				t.Fatalf("message segments = %+v", segments)
			}
		})
	}

	if _, _, err := ch.buildSendRequest(bus.OutboundMessage{ChatID: "group:nope"}); err == nil {
		t.Fatal("buildSendRequest() should reject an invalid chat ID")
	}
}

func TestSendAndSendMedia_WriteExpectedRequests(t *testing.T) {
	ch, _ := newCoverageOneBot(t)
	url, accepted := startCoverageOneBotWS(t, nil)
	conn := connectCoverageOneBot(t, ch, url, accepted)
	ch.SetRunning(true)
	ch.lastMessageID.Store("group:42", "m1")

	if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "group:42", Content: "hello"}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	req := readCoverageOneBotRequest(t, conn)
	if req.Action != "send_group_msg" || req.Params.GroupID != 42 || req.Echo == "" {
		t.Fatalf("Send request = %+v", req)
	}
	if len(req.Params.Message) != 2 || req.Params.Message[0].Type != "reply" || req.Params.Message[1].Type != "text" {
		t.Fatalf("Send message = %+v", req.Params.Message)
	}

	store := media.NewFileMediaStore()
	ch.SetMediaStore(store)
	tmpDir := t.TempDir()
	parts := []bus.MediaPart{
		{Type: "image", Caption: "picture", Filename: "picture.png"},
		{Type: "video", Filename: "clip.mp4"},
		{Type: "audio", Filename: "voice.amr"},
		{Type: "file", Filename: "document.pdf"},
	}
	for i, part := range parts {
		path := filepath.Join(tmpDir, part.Filename)
		if err := os.WriteFile(path, []byte(part.Filename), 0o600); err != nil {
			t.Fatalf("os.WriteFile() error = %v", err)
		}
		ref, err := store.Store(path, media.MediaMeta{Filename: part.Filename}, "scope")
		if err != nil {
			t.Fatalf("Store() error = %v", err)
		}
		parts[i].Ref = ref
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "private:42", Parts: parts,
	}); err != nil {
		t.Fatalf("SendMedia() error = %v", err)
	}
	req = readCoverageOneBotRequest(t, conn)
	if req.Action != "send_private_msg" || req.Params.UserID != 42 {
		t.Fatalf("SendMedia request = %+v", req)
	}
	wantTypes := []string{"image", "text", "video", "record", "file"}
	if len(req.Params.Message) != len(wantTypes) {
		t.Fatalf("SendMedia segments = %+v", req.Params.Message)
	}
	for i, want := range wantTypes {
		if req.Params.Message[i].Type != want {
			t.Errorf("segment %d type = %q, want %q", i, req.Params.Message[i].Type, want)
		}
	}
	if got := req.Params.Message[0].Data["file"]; got != "file://"+filepath.Join(tmpDir, "picture.png") {
		t.Errorf("image file = %v", got)
	}
	if got := req.Params.Message[1].Data["text"]; got != "picture" {
		t.Errorf("caption = %v", got)
	}
}

func TestSendErrorsAndEmptyMedia(t *testing.T) {
	ch, _ := newCoverageOneBot(t)
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); !errors.Is(err, channels.ErrNotRunning) {
		t.Fatalf("Send() error = %v, want ErrNotRunning", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Fatalf("SendMedia() error = %v, want ErrNotRunning", err)
	}

	ch.SetRunning(true)
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("Send() disconnected error = %v", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{}); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("SendMedia() disconnected error = %v", err)
	}

	url, accepted := startCoverageOneBotWS(t, nil)
	_ = connectCoverageOneBot(t, ch, url, accepted)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ch.Send(ctx, bus.OutboundMessage{ChatID: "42"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Send() canceled error = %v", err)
	}
	if _, err := ch.SendMedia(ctx, bus.OutboundMediaMessage{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("SendMedia() canceled error = %v", err)
	}
	if _, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "bad"}); err == nil {
		t.Fatal("Send() should reject invalid chat ID")
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{ChatID: "bad"}); !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Fatalf("SendMedia() without store error = %v, want ErrSendFailed", err)
	}

	ch.SetMediaStore(media.NewFileMediaStore())
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "42",
		Parts:  []bus.MediaPart{{Type: "image", Ref: "media://missing"}},
	}); err != nil {
		t.Fatalf("SendMedia() unknown ref error = %v", err)
	}
	if _, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID: "bad",
		Parts:  []bus.MediaPart{{Ref: "media://missing"}},
	}); err != nil {
		t.Fatalf("SendMedia() empty segments error = %v", err)
	}

	ch.mu.Lock()
	_ = ch.conn.Close()
	ch.mu.Unlock()
	_, err := ch.Send(context.Background(), bus.OutboundMessage{
		ChatID: "42", Content: "hello",
	})
	if !errors.Is(err, channels.ErrTemporary) {
		t.Fatalf("Send() closed connection error = %v, want ErrTemporary", err)
	}
}

func TestSendAPIRequest_ResponseTimeoutAndMarshalError(t *testing.T) {
	ch, _ := newCoverageOneBot(t)
	if _, err := ch.sendAPIRequest("test", nil, time.Second); err == nil ||
		!strings.Contains(err.Error(), "not connected") {
		t.Fatalf("sendAPIRequest() disconnected error = %v", err)
	}

	url, accepted := startCoverageOneBotWS(t, nil)
	conn := connectCoverageOneBot(t, ch, url, accepted)
	if _, err := ch.sendAPIRequest("bad", func() {}, time.Second); err == nil ||
		!strings.Contains(err.Error(), "marshal") {
		t.Fatalf("sendAPIRequest() marshal error = %v", err)
	}

	go ch.listen()
	type result struct {
		raw json.RawMessage
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		raw, err := ch.sendAPIRequest("get_login_info", nil, time.Second)
		resultCh <- result{raw: raw, err: err}
	}()
	req := readCoverageOneBotRequest(t, conn)
	if req.Action != "get_login_info" {
		t.Fatalf("API action = %q", req.Action)
	}
	response := json.RawMessage(`{"status":"ok","echo":"` + req.Echo + `","data":{"user_id":"42","nickname":"Bot"}}`)
	if err := conn.WriteMessage(websocket.TextMessage, response); err != nil {
		t.Fatalf("WriteMessage() error = %v", err)
	}
	got := <-resultCh
	if got.err != nil || string(got.raw) != string(response) {
		t.Fatalf("sendAPIRequest() = %s, %v", got.raw, got.err)
	}

	if _, err := ch.sendAPIRequest("no_response", nil, 10*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "timed out") {
		t.Fatalf("sendAPIRequest() timeout error = %v", err)
	}
}

func TestFetchSelfID_ResponseFormats(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response string
		want     int64
	}{
		{name: "wrapped", response: `{"status":"ok","echo":"%s","data":{"user_id":"42"}}`, want: 42},
		{name: "flat", response: `{"status":"ok","echo":"%s","user_id":43}`, want: 43},
		{name: "invalid", response: `{"status":"ok","echo":"%s","data":{"user_id":"bad"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ch, _ := newCoverageOneBot(t)
			url, accepted := startCoverageOneBotWS(t, nil)
			conn := connectCoverageOneBot(t, ch, url, accepted)
			go ch.listen()
			done := make(chan struct{})
			go func() {
				ch.fetchSelfID()
				close(done)
			}()
			req := readCoverageOneBotRequest(t, conn)
			response := strings.ReplaceAll(tc.response, "%s", req.Echo)
			if err := conn.WriteMessage(websocket.TextMessage, []byte(response)); err != nil {
				t.Fatalf("WriteMessage() error = %v", err)
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for fetchSelfID")
			}
			if got := atomic.LoadInt64(&ch.selfID); got != tc.want {
				t.Fatalf("selfID = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestListen_SkipsMalformedAndAPIResponsesThenHandlesEvent(t *testing.T) {
	ch, messageBus := newCoverageOneBot(t)
	url, accepted := startCoverageOneBotWS(t, nil)
	conn := connectCoverageOneBot(t, ch, url, accepted)
	go ch.listen()

	for _, payload := range []string{
		`{`,
		`{"status":"ok"}`,
		`{"echo":"missing","status":"failed"}`,
		`{"post_type":"message","message_type":"private","message_id":1,"user_id":42,"message":"hello"}`,
	} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
			t.Fatalf("WriteMessage() error = %v", err)
		}
	}
	inbound := waitCoverageOneBotInbound(t, messageBus)
	if inbound.Content != "hello" {
		t.Fatalf("inbound content = %q", inbound.Content)
	}
	assertNoCoverageOneBotInbound(t, messageBus)

	_ = conn.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ch.mu.Lock()
		connected := ch.conn != nil
		ch.mu.Unlock()
		if !connected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("listener did not clear the closed connection")
}

func TestStartAndReconnect_LocalWebSocket(t *testing.T) {
	disableCoverageOneBotProxy(t)
	var ready atomic.Bool
	accepted := make(chan *websocket.Conn, 1)
	requestSeen := make(chan struct{}, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		accepted <- conn
		var req coverageOneBotRequest
		if err := conn.ReadJSON(&req); err != nil {
			return
		}
		requestSeen <- struct{}{}
		_ = conn.WriteJSON(map[string]any{"status": "ok", "echo": req.Echo, "data": map[string]any{"user_id": 42}})
	}))
	defer server.Close()

	ch, _ := newCoverageOneBot(t)
	ch.config.WSUrl = "ws" + strings.TrimPrefix(server.URL, "http")
	ch.config.AccessToken = *config.NewSecureString("secret")
	ch.config.ReconnectInterval = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ch.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = ch.Stop(context.Background()) })
	if !ch.IsRunning() {
		t.Fatal("channel should be running after Start")
	}

	ready.Store(true)
	select {
	case <-accepted:
	case <-time.After(8 * time.Second):
		t.Fatal("timeout waiting for reconnect")
	}
	select {
	case <-requestSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for get_login_info after reconnect")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt64(&ch.selfID) == 42 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("self ID was not fetched after reconnect")
}

func TestOneBotLifecycleAndCapabilities(t *testing.T) {
	disableCoverageOneBotProxy(t)
	ch, _ := newCoverageOneBot(t)
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("Start() should reject an empty ws_url")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	ch.config.WSUrl = "ws" + strings.TrimPrefix(server.URL, "http")
	if err := ch.Start(context.Background()); err == nil {
		t.Fatal("Start() should fail when reconnect is disabled")
	}
	if ch.IsRunning() {
		t.Fatal("channel should not be running after failed Start")
	}

	undo, err := ch.ReactToMessage(context.Background(), "private:42", "m1")
	if err != nil || undo == nil {
		t.Fatalf("ReactToMessage(private) returned nil undo=%v, err=%v", undo == nil, err)
	}
	undo()
	if got := ch.VoiceCapabilities(); !got.ASR || !got.TTS {
		t.Fatalf("VoiceCapabilities() = %+v", got)
	}
	if err := ch.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
