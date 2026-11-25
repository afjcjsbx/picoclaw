package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sipeed/picoclaw/web/backend/middleware"
)

// PasswordStore is the interface for dashboard password persistence.
// Implemented by dashboardauth.Store and launcherconfig.PasswordStore.
type PasswordStore interface {
	IsInitialized(ctx context.Context) (bool, error)
	SetPassword(ctx context.Context, plain string) error
	VerifyPassword(ctx context.Context, plain string) (bool, error)
}

// LauncherAuthRouteOpts configures dashboard auth handlers.
type LauncherAuthRouteOpts struct {
	SessionCookie string
	SecureCookie  func(*http.Request) bool
	// PasswordStore enables password login. It must be non-nil for auth to work.
	PasswordStore PasswordStore
	// StoreError holds the error returned when opening the password store. When
	// non-nil and PasswordStore is nil, auth endpoints fail closed with a
	// recovery message.
	StoreError error
	// SetupToken is the one-time bootstrap grant required to initialize the
	// first dashboard password. It is never required for password changes.
	SetupToken string
	// OnInitialSetup runs after the first password is persisted successfully.
	OnInitialSetup func()
}

type launcherAuthLoginBody struct {
	Password string `json:"password"`
}

type launcherAuthSetupBody struct {
	Password   string `json:"password"`
	Confirm    string `json:"confirm"`
	SetupToken string `json:"setup_token"`
}

type launcherAuthStatusResponse struct {
	Authenticated bool `json:"authenticated"`
	Initialized   bool `json:"initialized"`
}

// RegisterLauncherAuthRoutes registers /api/auth/login|logout|status|setup.
func RegisterLauncherAuthRoutes(mux *http.ServeMux, opts LauncherAuthRouteOpts) {
	secure := opts.SecureCookie
	if secure == nil {
		secure = middleware.DefaultLauncherDashboardSecureCookie
	}
	h := &launcherAuthHandlers{
		sessionCookie:  opts.SessionCookie,
		secureCookie:   secure,
		store:          opts.PasswordStore,
		storeErr:       opts.StoreError,
		onInitialSetup: opts.OnInitialSetup,
		setupLimit:     newLoginRateLimiter(),
		loginLimit:     newLoginRateLimiter(),
	}
	if token := strings.TrimSpace(opts.SetupToken); token != "" {
		h.setupToken.Store(&token)
	}
	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.HandleFunc("GET /api/auth/status", h.handleStatus)
	mux.HandleFunc("POST /api/auth/setup", h.handleSetup)
}

type launcherAuthHandlers struct {
	sessionCookie  string
	secureCookie   func(*http.Request) bool
	store          PasswordStore
	storeErr       error // set when the store failed to open; drives recovery messages
	setupToken     atomic.Pointer[string]
	onInitialSetup func()
	setupMu        sync.Mutex
	setupLimit     *loginRateLimiter
	loginLimit     *loginRateLimiter
}

// isStoreInitialized safely queries the store.
// Returns (false, err) on store errors — callers must treat this as a 5xx, not as
// "uninitialized", to keep auth fail-closed.
func (h *launcherAuthHandlers) isStoreInitialized(ctx context.Context) (bool, error) {
	if h.store == nil {
		if h.storeErr != nil {
			return false, fmt.Errorf(
				"password store unavailable (%w); "+
					"to recover, stop the application, reset dashboard password storage, and restart",
				h.storeErr)
		}
		return false, fmt.Errorf("password store not configured")
	}
	return h.store.IsInitialized(ctx)
}

func launcherSetupCrossSite(r *http.Request) bool {
	fetchSite := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if fetchSite == "cross-site" {
		return true
	}

	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return !sameLauncherRequestOrigin(r, origin)
	}

	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return !sameLauncherRequestOrigin(r, referer)
	}

	return false
}

func validLauncherSetupToken(got, want string) bool {
	got = strings.TrimSpace(got)
	return got != "" && want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (h *launcherAuthHandlers) validSetupToken(got string) bool {
	want := h.setupToken.Load()
	return want != nil && validLauncherSetupToken(got, *want)
}

func (h *launcherAuthHandlers) hasValidDashboardSession(r *http.Request) bool {
	c, err := r.Cookie(middleware.LauncherDashboardCookieName)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(h.sessionCookie)) == 1
}

func (h *launcherAuthHandlers) allowSetupAttempt(w http.ResponseWriter, r *http.Request) bool {
	if h.setupLimit.allow(clientIPForLimiter(r)) {
		return true
	}
	w.WriteHeader(http.StatusTooManyRequests)
	_, _ = w.Write([]byte(`{"error":"too many setup attempts"}`))
	return false
}

func sameLauncherRequestOrigin(r *http.Request, raw string) bool {
	if strings.ContainsAny(raw, " \t\r\n") {
		return false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	wantScheme := launcherRequestScheme(r)
	wantHost := r.Host
	if wantHost == "" {
		wantHost = r.URL.Host
	}
	return strings.EqualFold(u.Scheme, wantScheme) && strings.EqualFold(u.Host, wantHost)
}

func launcherRequestScheme(r *http.Request) string {
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		if i := strings.IndexByte(proto, ','); i >= 0 {
			proto = proto[:i]
		}
		proto = strings.ToLower(strings.TrimSpace(proto))
		if proto == "http" || proto == "https" {
			return proto
		}
	}
	if r.TLS != nil {
		return "https"
	}
	if r.URL != nil && r.URL.Scheme != "" {
		return r.URL.Scheme
	}
	return "http"
}

func (h *launcherAuthHandlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var body launcherAuthLoginBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON"}`))
		return
	}
	ip := clientIPForLimiter(r)
	if !h.loginLimit.allow(ip) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"too many login attempts"}`))
		return
	}
	in := strings.TrimSpace(body.Password)

	initialized, initErr := h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}
	if !initialized {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"password has not been set"}`))
		return
	}

	ok, err := h.store.VerifyPassword(r.Context(), in)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeErrorf(w, "password verification failed: %v", err)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid password"}`))
		return
	}

	middleware.SetLauncherDashboardSessionCookie(w, r, h.sessionCookie, h.secureCookie)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *launcherAuthHandlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		_, _ = w.Write([]byte(`{"error":"method not allowed"}`))
		return
	}
	ct := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	if !strings.HasPrefix(ct, "application/json") {
		w.WriteHeader(http.StatusUnsupportedMediaType)
		_, _ = w.Write([]byte(`{"error":"Content-Type must be application/json"}`))
		return
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, logoutBodyMaxBytes))
	if err := dec.Decode(&struct{}{}); err != nil && err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON body"}`))
		return
	}

	middleware.ClearLauncherDashboardSessionCookie(w, r, h.secureCookie)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (h *launcherAuthHandlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	authed := false
	if c, err := r.Cookie(middleware.LauncherDashboardCookieName); err == nil {
		authed = subtle.ConstantTimeCompare([]byte(c.Value), []byte(h.sessionCookie)) == 1
	}
	initialized, initErr := h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}
	resp := launcherAuthStatusResponse{
		Authenticated: authed,
		Initialized:   initialized,
	}
	enc, err := json.Marshal(resp)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeErrorf(w, "marshal response failed: %v", err)
		return
	}
	_, _ = w.Write(enc)
}

// handleSetup sets or changes the dashboard password.
//
// Rules:
//   - If the store has no password yet, the caller must provide the one-time
//     bootstrap token delivered to the launcher operator.
//   - If a password is already set, the caller must hold a valid session cookie.
func (h *launcherAuthHandlers) handleSetup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if launcherSetupCrossSite(r) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":"cross-site setup request rejected"}`))
		return
	}

	if h.store == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		if h.storeErr != nil {
			writeErrorf(w, "password store unavailable: %v", h.storeErr)
		} else {
			_, _ = w.Write([]byte(`{"error":"password store not configured"}`))
		}
		return
	}

	// Parse and validate the bounded body before taking the setup lock. The
	// endpoint is public during first run, so an unauthenticated slow body must
	// not block legitimate setup or password-change requests.
	var body launcherAuthSetupBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid JSON"}`))
		return
	}

	pw := strings.TrimSpace(body.Password)
	if pw == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"password must not be empty"}`))
		return
	}
	if pw != strings.TrimSpace(body.Confirm) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"passwords do not match"}`))
		return
	}
	if len([]rune(pw)) < 8 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"password must be at least 8 characters"}`))
		return
	}

	// Reject invalid public attempts before contending on the persistence lock.
	// Valid bootstrap tokens and authenticated password changes bypass the
	// limiter, so a proxy-shared bucket cannot lock out the legitimate operator.
	initialized, initErr := h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}
	if initialized {
		if !h.hasValidDashboardSession(r) {
			if !h.allowSetupAttempt(w, r) {
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"must be authenticated to change password"}`))
			return
		}
	} else if !h.validSetupToken(body.SetupToken) {
		if !h.allowSetupAttempt(w, r) {
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or missing dashboard setup token"}`))
		return
	}

	// Serialize the initialized check with the write so two concurrent requests
	// cannot both consume the first-run grant.
	h.setupMu.Lock()
	defer h.setupMu.Unlock()

	initialized, initErr = h.isStoreInitialized(r.Context())
	if initErr != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		writeErrorf(w, "%v", initErr)
		return
	}

	// If already initialized, require an active session (change-password flow).
	if initialized {
		if !h.hasValidDashboardSession(r) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"must be authenticated to change password"}`))
			return
		}
	} else if !h.validSetupToken(body.SetupToken) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid or missing dashboard setup token"}`))
		return
	}

	if err := h.store.SetPassword(r.Context(), pw); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		writeErrorf(w, "failed to save password: %v", err)
		return
	}

	if !initialized {
		h.setupToken.Store(nil)
		if h.onInitialSetup != nil {
			h.onInitialSetup()
		}
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

// writeErrorf writes a JSON error response with a formatted message.
// json.Marshal is used to safely escape the message string.
func writeErrorf(w http.ResponseWriter, format string, args ...any) {
	msg, _ := json.Marshal(fmt.Sprintf(format, args...))
	_, _ = w.Write([]byte(`{"error":` + string(msg) + `}`))
}
