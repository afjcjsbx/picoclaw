package qq

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/constant"
	"golang.org/x/oauth2"
)

// TestQQBotAuthorizationScheme pins the reason the botgo replace in go.mod
// exists: the QQ OpenAPI only accepts "Authorization: QQBot <token>".
//
// botgo v0.2.1 set the resty auth scheme on the *Client from inside
// OnBeforeRequest, but resty >= v2.17 snapshots Client.AuthScheme into the
// Request at R() time and the auth middleware reads only the request-level
// scheme. Every call therefore went out as "Bearer <token>" instead of
// "QQBot <token>", and the gateway rejected it with 401 (code 11241).
func TestQQBotAuthorizationScheme(t *testing.T) {
	var (
		mu    sync.Mutex
		auths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	prevDomain := constant.APIDomain
	constant.APIDomain = srv.URL
	t.Cleanup(func() { constant.APIDomain = prevDomain })

	api := botgo.NewOpenAPI("test-app-id", oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: "test-token",
		TokenType:   "QQBot",
	}))

	if _, err := api.Me(t.Context()); err != nil {
		t.Fatalf("Me() returned error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(auths) == 0 {
		t.Fatal("no request reached the test server")
	}
	for _, got := range auths {
		if want := "QQBot test-token"; got != want {
			t.Fatalf("Authorization header = %q, want %q", got, want)
		}
	}
}
