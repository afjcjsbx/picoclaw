//go:build !mipsle && !netbsd && !(freebsd && arm)

package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/web/backend/dashboardauth"
	"github.com/sipeed/picoclaw/web/backend/middleware"
)

func TestLauncherAuthLifecycleWithRealStore(t *testing.T) {
	store, err := dashboardauth.New(t.TempDir())
	if err != nil {
		t.Fatalf("dashboardauth.New() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	const session = "real-store-session"
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: session,
		PasswordStore: store,
	})

	request := func(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:18800"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://127.0.0.1:18800")
		req.RemoteAddr = "127.0.0.1:12345"
		for _, cookie := range cookies {
			req.AddCookie(cookie)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	sessionCookie := func() *http.Cookie {
		return &http.Cookie{Name: middleware.LauncherDashboardCookieName, Value: session}
	}
	requireCode := func(rec *httptest.ResponseRecorder, want int) {
		t.Helper()
		if rec.Code != want {
			t.Fatalf("status = %d, want %d; body=%s", rec.Code, want, rec.Body.String())
		}
	}

	requireCode(
		request(http.MethodPost, "/api/auth/setup", `{"password":"first-password","confirm":"first-password"}`),
		http.StatusOK,
	)
	status := request(http.MethodGet, "/api/auth/status", "")
	requireCode(status, http.StatusOK)
	if !strings.Contains(status.Body.String(), `"initialized":true`) {
		t.Fatalf("status after setup = %s", status.Body.String())
	}

	login := request(http.MethodPost, "/api/auth/login", `{"password":"first-password"}`)
	requireCode(login, http.StatusOK)
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != middleware.LauncherDashboardCookieName || cookies[0].Value != session {
		t.Fatalf("login cookies = %#v", cookies)
	}

	status = request(http.MethodGet, "/api/auth/status", "", sessionCookie())
	requireCode(status, http.StatusOK)
	if !strings.Contains(status.Body.String(), `"authenticated":true`) {
		t.Fatalf("authenticated status = %s", status.Body.String())
	}

	logout := request(http.MethodPost, "/api/auth/logout", `{}`, sessionCookie())
	requireCode(logout, http.StatusOK)
	cleared := false
	for _, cookie := range logout.Result().Cookies() {
		if cookie.Name == middleware.LauncherDashboardCookieName && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear the dashboard session cookie")
	}

	requireCode(request(http.MethodPost, "/api/auth/login", `{"password":"first-password"}`), http.StatusOK)
	requireCode(
		request(
			http.MethodPost,
			"/api/auth/setup",
			`{"password":"second-password","confirm":"second-password"}`,
			sessionCookie(),
		),
		http.StatusOK,
	)
	requireCode(request(http.MethodPost, "/api/auth/login", `{"password":"first-password"}`), http.StatusUnauthorized)
	requireCode(request(http.MethodPost, "/api/auth/login", `{"password":"second-password"}`), http.StatusOK)

	ok, err := store.VerifyPassword(context.Background(), "second-password")
	if err != nil || !ok {
		t.Fatalf("store.VerifyPassword(second-password) = %v, %v; want true, nil", ok, err)
	}
}
