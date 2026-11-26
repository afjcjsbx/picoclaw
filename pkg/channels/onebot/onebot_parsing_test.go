package onebot

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/media"
)

func TestOneBotParseJSONHelpers(t *testing.T) {
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

func TestParseMessageSegments_BlocksLoopbackInboundMediaURL(t *testing.T) {
	ch := &OneBotChannel{}
	store := media.NewFileMediaStore()

	raw := json.RawMessage(`[
		{"type":"text","data":{"text":"see attachment"}},
		{"type":"image","data":{"url":"http://127.0.0.1:8080/evil.png","file":"evil.png"}}
	]`)

	result := ch.parseMessageSegments(raw, 0, store, "onebot:test:msg1")

	if got := result.Text; got != "see attachment" {
		t.Fatalf("Text = %q, want %q", got, "see attachment")
	}
	if len(result.Media) != 0 {
		t.Fatalf("Media count = %d, want 0", len(result.Media))
	}
}

func TestParseMessageSegments_BlocksInboundMediaRedirectToLoopback(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("secret"))
	}))
	defer target.Close()

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.String(), "http://example.com/evil.png"; got != want {
			t.Fatalf("proxy request URL = %q, want %q", got, want)
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer proxy.Close()

	t.Setenv("HTTP_PROXY", proxy.URL)
	t.Setenv("http_proxy", proxy.URL)
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("all_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")

	ch := &OneBotChannel{}
	store := media.NewFileMediaStore()

	raw := json.RawMessage(`[
		{"type":"text","data":{"text":"see attachment"}},
		{"type":"image","data":{"url":"http://example.com/evil.png","file":"evil.png"}}
	]`)

	result := ch.parseMessageSegments(raw, 0, store, "onebot:test:msg-redirect")

	if got := result.Text; got != "see attachment" {
		t.Fatalf("Text = %q, want %q", got, "see attachment")
	}
	if len(result.Media) != 0 {
		t.Fatalf("Media count = %d, want 0", len(result.Media))
	}
}

func TestParseMessageSegments_StoresDownloadedMediaRef(t *testing.T) {
	tmpDir := t.TempDir()
	localPath := filepath.Join(tmpDir, "image.png")
	if err := os.WriteFile(localPath, []byte("fake-image"), 0o600); err != nil {
		t.Fatalf("os.WriteFile() error = %v", err)
	}

	ch := &OneBotChannel{
		downloadFn: func(urlStr, filename string) string {
			if urlStr != "https://cdn.example.com/image.png" {
				t.Fatalf("download url = %q, want %q", urlStr, "https://cdn.example.com/image.png")
			}
			if filename != "image.png" {
				t.Fatalf("download filename = %q, want %q", filename, "image.png")
			}
			return localPath
		},
	}
	store := media.NewFileMediaStore()

	raw := json.RawMessage(`[
		{"type":"text","data":{"text":"see attachment"}},
		{"type":"image","data":{"url":"https://cdn.example.com/image.png","file":"image.png"}}
	]`)

	result := ch.parseMessageSegments(raw, 0, store, "onebot:test:msg2")

	if got := result.Text; got != "see attachment[image]" {
		t.Fatalf("Text = %q, want %q", got, "see attachment[image]")
	}
	if len(result.Media) != 1 {
		t.Fatalf("Media count = %d, want 1", len(result.Media))
	}
	if !strings.HasPrefix(result.Media[0], "media://") {
		t.Fatalf("media ref = %q, want media:// prefix", result.Media[0])
	}

	resolvedPath, meta, err := store.ResolveWithMeta(result.Media[0])
	if err != nil {
		t.Fatalf("ResolveWithMeta() error = %v", err)
	}
	if resolvedPath != localPath {
		t.Fatalf("resolved path = %q, want %q", resolvedPath, localPath)
	}
	if meta.Source != "onebot" {
		t.Fatalf("meta.Source = %q, want %q", meta.Source, "onebot")
	}
	if meta.Filename != "image.png" {
		t.Fatalf("meta.Filename = %q, want %q", meta.Filename, "image.png")
	}
}
