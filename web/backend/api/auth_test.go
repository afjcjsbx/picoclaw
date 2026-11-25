package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/web/backend/middleware"
)

type gatedSetupBody struct {
	entered chan<- struct{}
	release <-chan struct{}
	reader  *strings.Reader
}

func (b *gatedSetupBody) Read(p []byte) (int, error) {
	select {
	case b.entered <- struct{}{}:
	default:
	}
	<-b.release
	return b.reader.Read(p)
}

func (*gatedSetupBody) Close() error { return nil }

type fakePasswordStore struct {
	mu          sync.Mutex
	initialized bool
	password    string
	err         error
	setErr      error
}

func (s *fakePasswordStore) IsInitialized(context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.initialized, nil
}

func (s *fakePasswordStore) SetPassword(_ context.Context, plain string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if s.setErr != nil {
		return s.setErr
	}
	s.password = plain
	s.initialized = true
	return nil
}

func (s *fakePasswordStore) VerifyPassword(_ context.Context, plain string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return false, s.err
	}
	return s.initialized && plain == s.password, nil
}

func TestLauncherAuthLoginAndStatus(t *testing.T) {
	const password = "dashboard-test-password"
	const sess = "session-cookie-value"
	store := &fakePasswordStore{initialized: true, password: password}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: sess,
		PasswordStore: store,
	})

	t.Run("status_unauthenticated", func(t *testing.T) {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("status code = %d", rec.Code)
		}
		var body struct {
			Authenticated bool `json:"authenticated"`
			Initialized   bool `json:"initialized"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Authenticated {
			t.Fatalf("unexpected authenticated=true: %+v", body)
		}
	})

	t.Run("login_ok", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "127.0.0.1:12345"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("login code = %d body=%s", rec.Code, rec.Body.String())
		}
		cookies := rec.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != middleware.LauncherDashboardCookieName {
			t.Fatalf("cookies = %#v", cookies)
		}
	})

	t.Run("status_authenticated", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/auth/status", nil)
		req.AddCookie(&http.Cookie{Name: middleware.LauncherDashboardCookieName, Value: sess})
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status code = %d", rec.Code)
		}
		if !bytes.Contains(rec.Body.Bytes(), []byte(`"authenticated":true`)) {
			t.Fatalf("body = %s", rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "token_help") {
			t.Fatalf("authenticated response should omit token_help: %s", rec.Body.String())
		}
	})
}

func TestLauncherAuthUninitializedStoreRequiresSetup(t *testing.T) {
	const sess = "session-cookie-value"
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: sess,
		PasswordStore: store,
		SetupToken:    setupToken,
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/status", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status code = %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Authenticated bool `json:"authenticated"`
		Initialized   bool `json:"initialized"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Initialized {
		t.Fatalf("initialized = true, want false before setup")
	}
	if body.Authenticated {
		t.Fatalf("unexpected authenticated=true: %+v", body)
	}
	if strings.Contains(rec.Body.String(), "setup_token") || strings.Contains(rec.Body.String(), setupToken) {
		t.Fatalf("status response leaked the setup token: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"not-set-yet"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("login before setup code = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"12345678","confirm":"12345678"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup without token code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.initialized || store.password != "" {
		t.Fatalf("setup without token mutated store: initialized=%v password=%q", store.initialized, store.password)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup code = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"password":"12345678"}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login after setup code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLauncherAuthSetupRequiresSessionWhenInitialized(t *testing.T) {
	const sess = "session-cookie-value"
	store := &fakePasswordStore{initialized: true, password: "old-password"}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: sess,
		PasswordStore: store,
	})

	body := strings.NewReader(
		`{"password":"new-password","confirm":"new-password","setup_token":"not-needed-for-session"}`,
	)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup without session code = %d body=%s", rec.Code, rec.Body.String())
	}

	body = strings.NewReader(`{"password":"new-password","confirm":"new-password"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: middleware.LauncherDashboardCookieName, Value: sess})
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup with session code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.password != "new-password" {
		t.Fatalf("password = %q, want new-password", store.password)
	}
}

func TestLauncherAuthInitialSetupRequiresBootstrapToken(t *testing.T) {
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{}
	initialSetupCalls := 0
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    setupToken,
		OnInitialSetup: func() {
			initialSetupCalls++
		},
	})

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "missing token", body: `{"password":"12345678","confirm":"12345678"}`},
		{name: "empty token", body: `{"password":"12345678","confirm":"12345678","setup_token":""}`},
		{name: "wrong token", body: `{"password":"12345678","confirm":"12345678","setup_token":"wrong-token"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("setup code = %d body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), setupToken) {
				t.Fatalf("rejected setup response leaked the setup token: %s", rec.Body.String())
			}
			if store.initialized || store.password != "" {
				t.Fatalf("rejected setup mutated store: initialized=%v password=%q", store.initialized, store.password)
			}
		})
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup with grant code = %d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), setupToken) {
		t.Fatalf("successful setup response leaked the setup token: %s", rec.Body.String())
	}
	if !store.initialized || store.password != "12345678" {
		t.Fatalf("setup store: initialized=%v password=%q", store.initialized, store.password)
	}
	if initialSetupCalls != 1 {
		t.Fatalf("OnInitialSetup calls = %d, want 1", initialSetupCalls)
	}
}

func TestLauncherAuthInitialSetupFailsClosedWithoutConfiguredToken(t *testing.T) {
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"12345678","confirm":"12345678","setup_token":"attacker-token"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("setup code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.initialized {
		t.Fatal("setup without a configured token initialized the store")
	}
}

func TestLauncherAuthSetupTokenIsSingleUse(t *testing.T) {
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    setupToken,
	})

	body := `{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`
	for i := 0; i < 2; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 1 {
			want = http.StatusUnauthorized
		}
		if rec.Code != want {
			t.Fatalf("attempt %d code = %d body=%s, want %d", i+1, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestLauncherAuthSetupTokenSurvivesPasswordWriteFailure(t *testing.T) {
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{setErr: errors.New("disk full")}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    setupToken,
	})

	body := `{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("failed setup code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.initialized {
		t.Fatal("failed password write initialized the store")
	}

	store.setErr = nil
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("retry setup code = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestLauncherAuthSlowSetupBodyDoesNotBlockOtherRequests(t *testing.T) {
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    setupToken,
	})

	body := `{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`
	release := make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	entered := make(chan struct{}, 1)
	firstDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", nil)
		req.Body = &gatedSetupBody{
			entered: entered,
			release: release,
			reader:  strings.NewReader(body),
		}
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(rec, req)
		firstDone <- rec.Code
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first setup request did not start reading its body")
	}

	secondDone := make(chan int, 1)
	go func() {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		mux.ServeHTTP(rec, req)
		secondDone <- rec.Code
	}()
	select {
	case code := <-secondDone:
		if code != http.StatusOK {
			t.Fatalf("second setup code = %d, want 200", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("slow setup body blocked another setup request")
	}

	close(release)
	released = true
	select {
	case code := <-firstDone:
		if code != http.StatusUnauthorized {
			t.Fatalf("first setup code = %d, want 401 after the token was consumed", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first setup request did not finish after its body was released")
	}
}

func TestLauncherAuthSetupRateLimit(t *testing.T) {
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    "bootstrap-test-token",
	})

	body := `{"password":"12345678","confirm":"12345678","setup_token":"wrong-token"}`
	for i := 0; i < loginAttemptsPerIP; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.10:12345"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d code = %d body=%s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:12345"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("rate-limited setup code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.initialized {
		t.Fatal("rate-limited setup initialized the store")
	}

	validBody := `{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(validBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:12345"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid token after rate limit code = %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(
		http.MethodPost,
		"/api/auth/setup",
		strings.NewReader(`{"password":"new-password","confirm":"new-password"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:12345"
	req.AddCookie(&http.Cookie{Name: middleware.LauncherDashboardCookieName, Value: "session-cookie-value"})
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("authenticated change after rate limit code = %d body=%s", rec.Code, rec.Body.String())
	}
	if store.password != "new-password" {
		t.Fatalf("password after authenticated change = %q", store.password)
	}
}

func TestLauncherAuthConcurrentSetupConsumesTokenOnce(t *testing.T) {
	const setupToken = "bootstrap-test-token"
	store := &fakePasswordStore{}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
		SetupToken:    setupToken,
	})

	start := make(chan struct{})
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/setup",
				strings.NewReader(`{"password":"12345678","confirm":"12345678","setup_token":"bootstrap-test-token"}`),
			)
			req.Header.Set("Content-Type", "application/json")
			mux.ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	close(start)

	got := map[int]int{}
	for i := 0; i < 2; i++ {
		got[<-codes]++
	}
	if got[http.StatusOK] != 1 || got[http.StatusUnauthorized] != 1 {
		t.Fatalf("concurrent setup codes = %#v, want one 200 and one 401", got)
	}
}

func TestLauncherAuthStoreUnavailableFailsClosed(t *testing.T) {
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		StoreError:    errors.New("open auth store"),
	})

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "status", method: http.MethodGet, path: "/api/auth/status"},
		{name: "login", method: http.MethodPost, path: "/api/auth/login", body: `{"password":"password"}`},
		{name: "setup", method: http.MethodPost, path: "/api/auth/setup", body: `{"password":"12345678","confirm":"12345678"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("code = %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestLauncherAuthLogoutRequiresPostAndJSON(t *testing.T) {
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
	})

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/auth/logout", nil))
	if rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound {
		t.Fatalf("GET logout: code = %d (expected 404 or 405)", rec.Code)
	}

	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	mux.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("wrong content-type: code = %d body=%s", rec2.Code, rec2.Body.String())
	}

	rec3 := httptest.NewRecorder()
	req3 := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}`))
	req3.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusOK {
		t.Fatalf("POST json logout: code = %d", rec3.Code)
	}
}

func TestLauncherAuthLoginRateLimit(t *testing.T) {
	store := &fakePasswordStore{initialized: true, password: "correct-password"}
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
		PasswordStore: store,
	})

	// 11 failing logins by wrong password; each consumes allow() slot after valid JSON.
	wrongBody := `{"password":"wrong"}`
	for i := 0; i < loginAttemptsPerIP; i++ {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(wrongBody))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.5.5:9999"
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("iter %d: want 401 got %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(wrongBody))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.5.5:9999"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("11th attempt: want 429 got %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginRateLimiterWindow(t *testing.T) {
	l := newLoginRateLimiter()
	t0 := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return t0 }
	for i := 0; i < loginAttemptsPerIP; i++ {
		if !l.allow("ip") {
			t.Fatalf("want allow at %d", i)
		}
	}
	if l.allow("ip") {
		t.Fatal("want deny on 11th")
	}
	l.now = func() time.Time { return t0.Add(loginAttemptWindow + time.Second) }
	if !l.allow("ip") {
		t.Fatal("want allow after window")
	}
}

func TestReferrerPolicyMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	h := middleware.ReferrerPolicyNoReferrer(next)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
}

func TestLauncherAuthLogoutEmptyBody(t *testing.T) {
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Content-Type", "application/json")
	req.Body = http.NoBody
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
}

func TestLauncherAuthLogoutRejectsTrailingJSON(t *testing.T) {
	mux := http.NewServeMux()
	RegisterLauncherAuthRoutes(mux, LauncherAuthRouteOpts{
		SessionCookie: "session-cookie-value",
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", strings.NewReader(`{}{}`))
	req.Header.Set("Content-Type", "application/json")
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d %s", rec.Code, rec.Body.String())
	}
}
