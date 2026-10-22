package weixin

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	basechannels "github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseWeixinMediaAESKey(t *testing.T) {
	raw := []byte("1234567890abcdef")

	got, err := parseWeixinMediaAESKey(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatalf("parseWeixinMediaAESKey(raw) error = %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("parseWeixinMediaAESKey(raw) = %x, want %x", got, raw)
	}

	hexEncoded := base64.StdEncoding.EncodeToString([]byte("31323334353637383930616263646566"))
	got, err = parseWeixinMediaAESKey(hexEncoded)
	if err != nil {
		t.Fatalf("parseWeixinMediaAESKey(hex-string) error = %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("parseWeixinMediaAESKey(hex-string) = %x, want %x", got, raw)
	}
}

func TestDownloadAndDecryptCDNBuffer(t *testing.T) {
	key := []byte("1234567890abcdef")
	plaintext := []byte("hello weixin")
	ciphertext, err := encryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encryptAESECB() error = %v", err)
	}

	ch := &WeixinChannel{
		api: &ApiClient{
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/download" {
					t.Fatalf("download path = %q, want /download", r.URL.Path)
				}
				if r.URL.Query().Get("encrypted_query_param") != "token" {
					t.Fatalf("encrypted_query_param = %q, want token", r.URL.Query().Get("encrypted_query_param"))
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(ciphertext)),
					Header:     make(http.Header),
				}, nil
			})},
		},
		config: &config.WeixinSettings{
			CDNBaseURL: "https://cdn.example.com",
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	got, err := ch.downloadAndDecryptCDNBuffer(context.Background(), "token", "", key)
	if err != nil {
		t.Fatalf("downloadAndDecryptCDNBuffer() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("downloadAndDecryptCDNBuffer() = %q, want %q", got, plaintext)
	}
}

func TestDownloadAndDecryptCDNBufferUsesFullURLWhenProvided(t *testing.T) {
	key := []byte("1234567890abcdef")
	plaintext := []byte("hello weixin")
	ciphertext, err := encryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encryptAESECB() error = %v", err)
	}

	fullURLAttempts := 0
	ch := &WeixinChannel{
		api: &ApiClient{
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == "https://full.example.com/download" {
					fullURLAttempts++
					return &http.Response{
						StatusCode: http.StatusOK,
						Body:       io.NopCloser(bytes.NewReader(ciphertext)),
						Header:     make(http.Header),
					}, nil
				}
				t.Fatalf("unexpected fallback request: %s", r.URL.String())
				return nil, nil
			})},
		},
		config: &config.WeixinSettings{
			CDNBaseURL: "https://cdn.example.com",
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	got, err := ch.downloadAndDecryptCDNBuffer(context.Background(), "token", "https://full.example.com/download", key)
	if err != nil {
		t.Fatalf("downloadAndDecryptCDNBuffer() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("downloadAndDecryptCDNBuffer() = %q, want %q", got, plaintext)
	}
	if fullURLAttempts == 0 {
		t.Fatalf("fullURLAttempts = %d, want > 0", fullURLAttempts)
	}
}

func TestDownloadAndDecryptCDNBufferFallsBackToConstructedURLWhenFullURLFails(t *testing.T) {
	key := []byte("1234567890abcdef")
	plaintext := []byte("hello weixin")
	ciphertext, err := encryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encryptAESECB() error = %v", err)
	}

	fullURLAttempts := 0
	constructedAttempts := 0
	ch := &WeixinChannel{
		api: &ApiClient{
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.String() == "https://full.example.com/download?encrypted_query_param=token&taskid=123" {
					fullURLAttempts++
					return &http.Response{
						StatusCode: http.StatusInternalServerError,
						Body:       io.NopCloser(bytes.NewReader(nil)),
						Header:     make(http.Header),
					}, nil
				}
				if r.URL.String() != "https://cdn.example.com/download?encrypted_query_param=token" {
					t.Fatalf("unexpected fallback request: %s", r.URL.String())
				}
				constructedAttempts++
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(ciphertext)),
					Header:     make(http.Header),
				}, nil
			})},
		},
		config: &config.WeixinSettings{
			CDNBaseURL: "https://cdn.example.com",
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	got, err := ch.downloadAndDecryptCDNBuffer(
		context.Background(),
		"token",
		"https://full.example.com/download?encrypted_query_param=token&taskid=123",
		key,
	)
	if err != nil {
		t.Fatalf("downloadAndDecryptCDNBuffer() error = %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("downloadAndDecryptCDNBuffer() = %q, want %q", got, plaintext)
	}
	if fullURLAttempts == 0 {
		t.Fatalf("fullURLAttempts = %d, want > 0", fullURLAttempts)
	}
	if constructedAttempts == 0 {
		t.Fatalf("constructedAttempts = %d, want > 0", constructedAttempts)
	}
}

func TestBuildCDNDownloadURLEscapesOpaqueToken(t *testing.T) {
	token := "MFcCAQAESzBJAgEAAgSieMV9AgM9CcwCBEoKPqICBGnHZB0EJDk4OWY5YWU0LTc4OGItNGQ5Ni1iMjZhLWU4YjhlMmEwOWVkZgIEIR0IAgIBAAQFAExUPQA%3D"

	got := buildCDNDownloadURL("https://cdn.example.com", token)

	if got != "https://cdn.example.com/download?encrypted_query_param=MFcCAQAESzBJAgEAAgSieMV9AgM9CcwCBEoKPqICBGnHZB0EJDk4OWY5YWU0LTc4OGItNGQ5Ni1iMjZhLWU4YjhlMmEwOWVkZgIEIR0IAgIBAAQFAExUPQA%253D" {
		t.Fatalf("buildCDNDownloadURL() = %q", got)
	}
}

func TestUploadBufferToCDN(t *testing.T) {
	key := []byte("1234567890abcdef")
	plaintext := []byte("upload me")
	wantCipher, err := encryptAESECB(plaintext, key)
	if err != nil {
		t.Fatalf("encryptAESECB() error = %v", err)
	}

	ch := &WeixinChannel{
		api: &ApiClient{
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/upload" {
					t.Fatalf("upload path = %q, want /upload", r.URL.Path)
				}
				if got := r.URL.Query().Get("encrypted_query_param"); got != "upload-param" {
					t.Fatalf("encrypted_query_param = %q, want upload-param", got)
				}
				if got := r.URL.Query().Get("filekey"); got != "file-key" {
					t.Fatalf("filekey = %q, want file-key", got)
				}
				body, _ := io.ReadAll(r.Body)
				if !bytes.Equal(body, wantCipher) {
					t.Fatalf("upload body = %x, want %x", body, wantCipher)
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(nil)),
					Header: http.Header{
						"X-Encrypted-Param": []string{"download-param"},
					},
				}, nil
			})},
		},
		config: &config.WeixinSettings{
			CDNBaseURL: "https://cdn.example.com",
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	got, err := ch.uploadBufferToCDN(context.Background(), plaintext, "upload-param", "", "file-key", key)
	if err != nil {
		t.Fatalf("uploadBufferToCDN() error = %v", err)
	}
	if got != "download-param" {
		t.Fatalf("uploadBufferToCDN() = %q, want download-param", got)
	}
}

func TestLoadSaveGetUpdatesBuf(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sync.json")

	if err := saveGetUpdatesBuf(path, "cursor-123"); err != nil {
		t.Fatalf("saveGetUpdatesBuf() error = %v", err)
	}

	got, err := loadGetUpdatesBuf(path)
	if err != nil {
		t.Fatalf("loadGetUpdatesBuf() error = %v", err)
	}
	if got != "cursor-123" {
		t.Fatalf("loadGetUpdatesBuf() = %q, want cursor-123", got)
	}
}

func TestBuildWeixinSyncBufPathUsesPicoclawHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)

	wxCfg := &config.WeixinSettings{
		BaseURL: "https://ilinkai.weixin.qq.com/",
	}
	wxCfg.SetToken("token-123")
	got := buildWeixinSyncBufPath(wxCfg)
	if filepath.Dir(got) != filepath.Join(home, "channels", "weixin", "sync") {
		t.Fatalf("sync path dir = %q", filepath.Dir(got))
	}
}

func TestSessionPauseGuard(t *testing.T) {
	ch := &WeixinChannel{
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	ch.pauseSession("getupdates", 0, weixinSessionExpiredCode, "expired")

	if err := ch.ensureSessionActive(); !errors.Is(err, basechannels.ErrSendFailed) {
		t.Fatalf("ensureSessionActive() error = %v, want ErrSendFailed", err)
	}

	ch.pauseMu.Lock()
	ch.pauseUntil = time.Now().Add(-time.Second)
	ch.pauseMu.Unlock()

	if err := ch.ensureSessionActive(); err != nil {
		t.Fatalf("ensureSessionActive() after expiry error = %v, want nil", err)
	}
}

func TestSelectInboundMediaItemFallsBackToRefMessage(t *testing.T) {
	msg := WeixinMessage{
		ItemList: []MessageItem{
			{
				Type: MessageItemTypeText,
				TextItem: &TextItem{
					Text: "look",
				},
				RefMsg: &RefMessage{
					MessageItem: &MessageItem{
						Type: MessageItemTypeImage,
						ImageItem: &ImageItem{
							Media: &CDNMedia{
								EncryptQueryParam: "abc",
							},
						},
					},
				},
			},
		},
	}

	item := selectInboundMediaItem(msg)
	if item == nil {
		t.Fatal("selectInboundMediaItem() = nil, want ref media item")
	}
	if item.Type != MessageItemTypeImage {
		t.Fatalf("selectInboundMediaItem().Type = %d, want %d", item.Type, MessageItemTypeImage)
	}
}

func TestSendUploadedMedia_SendsCaptionAsSeparateTextBeforeMedia(t *testing.T) {
	var requests []SendMessageReq
	ch := &WeixinChannel{
		api: &ApiClient{
			BaseURL: "https://ilinkai.weixin.qq.com/",
			HttpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/ilink/bot/sendmessage" {
					t.Fatalf("sendmessage path = %q, want /ilink/bot/sendmessage", r.URL.Path)
				}
				var req SendMessageReq
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Fatalf("decode sendmessage req: %v", err)
				}
				requests = append(requests, req)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader([]byte(`{"ret":0,"errcode":0}`))),
					Header:     make(http.Header),
				}, nil
			})},
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}

	err := ch.sendUploadedMedia(
		context.Background(),
		"user-1",
		"ctx-1",
		"recipe translation",
		UploadMediaTypeImage,
		&uploadedFileInfo{
			downloadParam: "download-token",
			aesKeyHex:     "31323334353637383930616263646566",
			fileSize:      11,
			cipherSize:    16,
			filename:      "photo.png",
		},
	)
	if err != nil {
		t.Fatalf("sendUploadedMedia() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("sendUploadedMedia() sent %d requests, want 2", len(requests))
	}
	if len(requests[0].Msg.ItemList) != 1 || requests[0].Msg.ItemList[0].Type != MessageItemTypeText {
		t.Fatalf("first request item = %+v, want text item", requests[0].Msg.ItemList)
	}
	if got := requests[0].Msg.ItemList[0].TextItem.Text; got != "recipe translation" {
		t.Fatalf("first request text = %q, want recipe translation", got)
	}
	if len(requests[1].Msg.ItemList) != 1 || requests[1].Msg.ItemList[0].Type != MessageItemTypeImage {
		t.Fatalf("second request item = %+v, want image item", requests[1].Msg.ItemList)
	}
	if requests[1].Msg.ItemList[0].ImageItem == nil || requests[1].Msg.ItemList[0].ImageItem.Media == nil {
		t.Fatalf("second request image media = %+v, want media ref", requests[1].Msg.ItemList[0].ImageItem)
	}
}

// newSendTestChannel returns a running channel with a context token for "user-1"
// whose iLink and CDN requests are served by transport.
func newSendTestChannel(transport roundTripFunc) *WeixinChannel {
	ch := &WeixinChannel{
		BaseChannel: basechannels.NewBaseChannel("weixin", nil, nil, nil),
		api: &ApiClient{
			BaseURL:    "https://ilinkai.weixin.qq.com/",
			HttpClient: &http.Client{Transport: transport},
		},
		config: &config.WeixinSettings{
			CDNBaseURL: "https://cdn.example.com",
		},
		typingCache: make(map[string]typingTicketCacheEntry),
	}
	ch.SetRunning(true)
	ch.contextTokens.Store("user-1", "ctx-1")
	return ch
}

func iLinkResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
		Header:     make(http.Header),
	}
}

func TestSendClassifiesFailuresForRetry(t *testing.T) {
	tests := []struct {
		name      string
		transport roundTripFunc
		want      error
		notWant   error
	}{
		{
			name: "iLink rejection is not retried",
			transport: func(*http.Request) (*http.Response, error) {
				return iLinkResponse(http.StatusOK, `{"ret":-2,"errcode":0,"errmsg":"prepare failed"}`), nil
			},
			want:    basechannels.ErrSendFailed,
			notWant: basechannels.ErrTemporary,
		},
		{
			name: "other iLink error is retried",
			transport: func(*http.Request) (*http.Response, error) {
				return iLinkResponse(http.StatusOK, `{"ret":-1,"errcode":0,"errmsg":"system error"}`), nil
			},
			want:    basechannels.ErrTemporary,
			notWant: basechannels.ErrSendFailed,
		},
		{
			name: "iLink errcode without ret is retried",
			transport: func(*http.Request) (*http.Response, error) {
				return iLinkResponse(http.StatusOK, `{"ret":0,"errcode":-1,"errmsg":"busy"}`), nil
			},
			want:    basechannels.ErrTemporary,
			notWant: basechannels.ErrSendFailed,
		},
		{
			name: "network error is retried",
			transport: func(*http.Request) (*http.Response, error) {
				return nil, errors.New("connection reset by peer")
			},
			want:    basechannels.ErrTemporary,
			notWant: basechannels.ErrSendFailed,
		},
		{
			name: "HTTP 5xx is retried",
			transport: func(*http.Request) (*http.Response, error) {
				return iLinkResponse(http.StatusBadGateway, "bad gateway"), nil
			},
			want:    basechannels.ErrTemporary,
			notWant: basechannels.ErrSendFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			ch := newSendTestChannel(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/ilink/bot/sendmessage" {
					t.Fatalf("request path = %q, want /ilink/bot/sendmessage", r.URL.Path)
				}
				requests++
				return tt.transport(r)
			})

			_, err := ch.Send(context.Background(), bus.OutboundMessage{ChatID: "user-1", Content: "hello"})

			if !errors.Is(err, tt.want) {
				t.Fatalf("Send() error = %v, want %v", err, tt.want)
			}
			if errors.Is(err, tt.notWant) {
				t.Fatalf("Send() error = %v, must not be %v", err, tt.notWant)
			}
			if requests != 1 {
				t.Fatalf("Send() made %d sendmessage requests, want 1", requests)
			}
		})
	}
}

func TestSendMediaClassifiesFailuresForRetry(t *testing.T) {
	const (
		uploadURLOK       = `{"ret":0,"errcode":0,"upload_param":"upload-param"}`
		uploadURLRejected = `{"ret":-2,"errcode":0,"errmsg":"prepare failed"}`
		sendOK            = `{"ret":0,"errcode":0}`
		sendRejected      = `{"ret":-2,"errcode":0,"errmsg":"prepare failed"}`
		otherILinkError   = `{"ret":-1,"errcode":0,"errmsg":"system error"}`
	)
	tests := []struct {
		name          string
		uploadURLResp string
		cdnStatus     int
		sendResp      string
		wantSends     int
		want          error
		notWant       error
	}{
		{
			name:          "sendmessage rejection is not retried",
			uploadURLResp: uploadURLOK,
			cdnStatus:     http.StatusOK,
			sendResp:      sendRejected,
			wantSends:     1,
			want:          basechannels.ErrSendFailed,
			notWant:       basechannels.ErrTemporary,
		},
		{
			name:          "getuploadurl rejection is not retried",
			uploadURLResp: uploadURLRejected,
			cdnStatus:     http.StatusOK,
			sendResp:      sendOK,
			wantSends:     0,
			want:          basechannels.ErrSendFailed,
			notWant:       basechannels.ErrTemporary,
		},
		{
			name:          "other sendmessage error is retried",
			uploadURLResp: uploadURLOK,
			cdnStatus:     http.StatusOK,
			sendResp:      otherILinkError,
			wantSends:     1,
			want:          basechannels.ErrTemporary,
			notWant:       basechannels.ErrSendFailed,
		},
		{
			name:          "other getuploadurl error is retried",
			uploadURLResp: otherILinkError,
			cdnStatus:     http.StatusOK,
			sendResp:      sendOK,
			wantSends:     0,
			want:          basechannels.ErrTemporary,
			notWant:       basechannels.ErrSendFailed,
		},
		{
			name:          "CDN upload failure is retried",
			uploadURLResp: uploadURLOK,
			cdnStatus:     http.StatusInternalServerError,
			sendResp:      sendOK,
			wantSends:     0,
			want:          basechannels.ErrTemporary,
			notWant:       basechannels.ErrSendFailed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "report.txt")
			if err := os.WriteFile(file, []byte("weekly report"), 0o600); err != nil {
				t.Fatalf("write test file: %v", err)
			}

			calls := map[string]int{}
			ch := newSendTestChannel(func(r *http.Request) (*http.Response, error) {
				calls[r.URL.Path]++
				switch r.URL.Path {
				case "/ilink/bot/getuploadurl":
					return iLinkResponse(http.StatusOK, tt.uploadURLResp), nil
				case "/upload":
					resp := iLinkResponse(tt.cdnStatus, "")
					resp.Header.Set("X-Encrypted-Param", "download-param")
					return resp, nil
				case "/ilink/bot/sendmessage":
					return iLinkResponse(http.StatusOK, tt.sendResp), nil
				}
				t.Fatalf("unexpected request path %q", r.URL.Path)
				return nil, nil
			})

			_, err := ch.SendMedia(context.Background(), bus.OutboundMediaMessage{
				ChatID: "user-1",
				Parts:  []bus.MediaPart{{Type: "file", Ref: file}},
			})

			if !errors.Is(err, tt.want) {
				t.Fatalf("SendMedia() error = %v, want %v", err, tt.want)
			}
			if errors.Is(err, tt.notWant) {
				t.Fatalf("SendMedia() error = %v, must not be %v", err, tt.notWant)
			}
			if calls["/ilink/bot/getuploadurl"] != 1 {
				t.Fatalf("SendMedia() made %d getuploadurl requests, want 1", calls["/ilink/bot/getuploadurl"])
			}
			if calls["/ilink/bot/sendmessage"] != tt.wantSends {
				t.Fatalf(
					"SendMedia() made %d sendmessage requests, want %d",
					calls["/ilink/bot/sendmessage"],
					tt.wantSends,
				)
			}
		})
	}
}
