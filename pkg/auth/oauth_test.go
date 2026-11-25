package auth

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func makeJWTForClaims(t *testing.T, claims map[string]any) string {
	t.Helper()

	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadJSON)
	return header + "." + payload + ".sig"
}

func TestBuildAuthorizeURL(t *testing.T) {
	cfg := OAuthProviderConfig{
		Issuer:     "https://auth.example.com",
		ClientID:   "test-client-id",
		Scopes:     "openid profile",
		Originator: "codex_cli_rs",
		Port:       1455,
	}
	pkce := PKCECodes{
		CodeVerifier:  "test-verifier",
		CodeChallenge: "test-challenge",
	}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "http://localhost:1455/auth/callback")

	if !strings.HasPrefix(u, "https://auth.example.com/oauth/authorize?") {
		t.Errorf("URL does not start with expected prefix: %s", u)
	}
	if !strings.Contains(u, "client_id=test-client-id") {
		t.Error("URL missing client_id")
	}
	if !strings.Contains(u, "code_challenge=test-challenge") {
		t.Error("URL missing code_challenge")
	}
	if !strings.Contains(u, "code_challenge_method=S256") {
		t.Error("URL missing code_challenge_method")
	}
	if !strings.Contains(u, "state=test-state") {
		t.Error("URL missing state")
	}
	if !strings.Contains(u, "response_type=code") {
		t.Error("URL missing response_type")
	}
	if !strings.Contains(u, "id_token_add_organizations=true") {
		t.Error("URL missing id_token_add_organizations")
	}
	if !strings.Contains(u, "codex_cli_simplified_flow=true") {
		t.Error("URL missing codex_cli_simplified_flow")
	}
	if !strings.Contains(u, "originator=codex_cli_rs") {
		t.Error("URL missing originator")
	}
}

func TestBuildAuthorizeURLOpenAIExtras(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	pkce := PKCECodes{CodeVerifier: "test-verifier", CodeChallenge: "test-challenge"}

	u := BuildAuthorizeURL(cfg, pkce, "test-state", "http://localhost:1455/auth/callback")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("url.Parse() error: %v", err)
	}
	q := parsed.Query()

	if q.Get("id_token_add_organizations") != "true" {
		t.Errorf("id_token_add_organizations = %q, want true", q.Get("id_token_add_organizations"))
	}
	if q.Get("codex_cli_simplified_flow") != "true" {
		t.Errorf("codex_cli_simplified_flow = %q, want true", q.Get("codex_cli_simplified_flow"))
	}
	if q.Get("originator") != "codex_cli_rs" {
		t.Errorf("originator = %q, want codex_cli_rs", q.Get("originator"))
	}
}

func TestParseTokenResponse(t *testing.T) {
	resp := map[string]any{
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      "test-id-token",
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccessToken != "test-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "test-access-token")
	}
	if cred.RefreshToken != "test-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", cred.RefreshToken, "test-refresh-token")
	}
	if cred.Provider != "openai" {
		t.Errorf("Provider = %q, want %q", cred.Provider, "openai")
	}
	if cred.AuthMethod != "oauth" {
		t.Errorf("AuthMethod = %q, want %q", cred.AuthMethod, "oauth")
	}
	if cred.ExpiresAt.IsZero() {
		t.Error("ExpiresAt should not be zero")
	}
}

func TestParseTokenResponseExtractsAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTForClaims(t, map[string]any{"chatgpt_account_id": "acc-id-from-id-token"})
	resp := map[string]any{
		"access_token":  "opaque-access-token",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}
	if cred.AccountID != "acc-id-from-id-token" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-id-from-id-token")
	}
}

func TestExtractAccountIDFromOrganizationsFallback(t *testing.T) {
	token := makeJWTForClaims(t, map[string]any{
		"organizations": []any{
			map[string]any{"id": "org_from_orgs"},
		},
	})

	if got := extractAccountID(token); got != "org_from_orgs" {
		t.Errorf("extractAccountID() = %q, want %q", got, "org_from_orgs")
	}
}

func TestParseTokenResponseNoAccessToken(t *testing.T) {
	body := []byte(`{"refresh_token": "test"}`)
	_, err := parseTokenResponse(body, "openai")
	if err == nil {
		t.Error("expected error for missing access_token")
	}
}

func TestParseTokenResponseAccountIDFromIDToken(t *testing.T) {
	idToken := makeJWTWithAccountID("acc-from-id")
	resp := map[string]any{
		"access_token":  "not-a-jwt",
		"refresh_token": "test-refresh-token",
		"expires_in":    3600,
		"id_token":      idToken,
	}
	body, _ := json.Marshal(resp)

	cred, err := parseTokenResponse(body, "openai")
	if err != nil {
		t.Fatalf("parseTokenResponse() error: %v", err)
	}

	if cred.AccountID != "acc-from-id" {
		t.Errorf("AccountID = %q, want %q", cred.AccountID, "acc-from-id")
	}
}

func makeJWTWithAccountID(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`),
	)
	return header + "." + payload + ".sig"
}

func TestExchangeCodeForTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "authorization_code" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}

		resp := map[string]any{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   "openid",
		Port:     1455,
	}

	cred, err := ExchangeCodeForTokens(cfg, "test-code", "test-verifier", "http://localhost:1455/auth/callback")
	if err != nil {
		t.Fatalf("ExchangeCodeForTokens() error: %v", err)
	}

	if cred.AccessToken != "mock-access-token" {
		t.Errorf("AccessToken = %q, want %q", cred.AccessToken, "mock-access-token")
	}
}

func TestRefreshAccessToken(t *testing.T) {
	expectedScope := "https://www.googleapis.com/auth/cloud-platform custom-scope"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		r.ParseForm()
		if r.FormValue("grant_type") != "refresh_token" {
			http.Error(w, "invalid grant_type", http.StatusBadRequest)
			return
		}
		if gotScope := r.FormValue("scope"); gotScope != "" {
			t.Errorf("Server received scope = %q, want refresh requests to omit it", gotScope)
		}

		resp := map[string]any{
			"access_token":  "refreshed-access-token",
			"refresh_token": "refreshed-refresh-token",
			"expires_in":    3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   expectedScope,
	}

	cred := &AuthCredential{
		AccessToken:  "old-token",
		RefreshToken: "old-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}

	if refreshed.AccessToken != "refreshed-access-token" {
		t.Errorf("AccessToken = %q, want %q", refreshed.AccessToken, "refreshed-access-token")
	}
	if refreshed.RefreshToken != "refreshed-refresh-token" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "refreshed-refresh-token")
	}
}

func TestRefreshAccessTokenNoRefreshToken(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	cred := &AuthCredential{
		AccessToken: "old-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}

	_, err := RefreshAccessToken(cred, cfg)
	if err == nil {
		t.Error("expected error for missing refresh token")
	}
}

func TestRefreshAccessTokenPreservesRefreshAndAccountID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]any{
			"access_token": "new-access-token-only",
			"expires_in":   3600,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{Issuer: server.URL, ClientID: "test-client"}
	cred := &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "existing-refresh",
		AccountID:    "acc_existing",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.RefreshToken != "existing-refresh" {
		t.Errorf("RefreshToken = %q, want %q", refreshed.RefreshToken, "existing-refresh")
	}
	if refreshed.AccountID != "acc_existing" {
		t.Errorf("AccountID = %q, want %q", refreshed.AccountID, "acc_existing")
	}
}

func TestOpenAIOAuthConfig(t *testing.T) {
	cfg := OpenAIOAuthConfig()
	if cfg.Issuer != "https://auth.openai.com" {
		t.Errorf("Issuer = %q, want %q", cfg.Issuer, "https://auth.openai.com")
	}
	if cfg.ClientID == "" {
		t.Error("ClientID is empty")
	}
	if cfg.Port != 1455 {
		t.Errorf("Port = %d, want 1455", cfg.Port)
	}
}

func TestParseDeviceCodeResponseIntervalAsNumber(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":5}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.DeviceAuthID != "abc" {
		t.Errorf("DeviceAuthID = %q, want %q", resp.DeviceAuthID, "abc")
	}
	if resp.UserCode != "DEF-1234" {
		t.Errorf("UserCode = %q, want %q", resp.UserCode, "DEF-1234")
	}
	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseIntervalAsString(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"5"}`)

	resp, err := parseDeviceCodeResponse(body)
	if err != nil {
		t.Fatalf("parseDeviceCodeResponse() error: %v", err)
	}

	if resp.Interval != 5 {
		t.Errorf("Interval = %d, want %d", resp.Interval, 5)
	}
}

func TestParseDeviceCodeResponseInvalidInterval(t *testing.T) {
	body := []byte(`{"device_auth_id":"abc","user_code":"DEF-1234","interval":"abc"}`)

	if _, err := parseDeviceCodeResponse(body); err == nil {
		t.Fatal("expected error for invalid interval")
	}
}

func TestLoginBrowserWithOptionsNoBrowserDoesNotRequireCallbackPort(t *testing.T) {
	server := newMockOAuthTokenServer()
	defer server.Close()
	reservedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error: %v", err)
	}
	defer reservedListener.Close()

	reservedPort := reservedListener.Addr().(*net.TCPAddr).Port
	origOpenBrowserFunc := openBrowserFunc
	origBrowserLoginInput := browserLoginInput
	t.Cleanup(func() {
		openBrowserFunc = origOpenBrowserFunc
		browserLoginInput = origBrowserLoginInput
	})

	var openCalls int
	openBrowserFunc = func(string) error {
		openCalls++
		return nil
	}
	browserLoginInput = strings.NewReader("manual-code\n")

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   "openid",
		Port:     reservedPort,
	}

	cred, err := LoginBrowserWithOptions(cfg, LoginBrowserOptions{NoBrowser: true})
	if err != nil {
		t.Fatalf("LoginBrowserWithOptions() error: %v", err)
	}

	if openCalls != 0 {
		t.Fatalf("openBrowserFunc call count = %d, want 0", openCalls)
	}
	if cred.AccessToken != "mock-access-token" {
		t.Fatalf("AccessToken = %q, want %q", cred.AccessToken, "mock-access-token")
	}
}

func TestLoginBrowserWithOptionsAutoOpensByDefault(t *testing.T) {
	server := newMockOAuthTokenServer()
	defer server.Close()

	origOpenBrowserFunc := openBrowserFunc
	origBrowserLoginInput := browserLoginInput
	t.Cleanup(func() {
		openBrowserFunc = origOpenBrowserFunc
		browserLoginInput = origBrowserLoginInput
	})

	var (
		openCalls  int
		browserURL string
	)
	openBrowserFunc = func(url string) error {
		openCalls++
		browserURL = url
		return nil
	}
	browserLoginInput = strings.NewReader("manual-code\n")

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
		Scopes:   "openid",
		Port:     0,
	}

	_, err := LoginBrowserWithOptions(cfg, LoginBrowserOptions{})
	if err != nil {
		t.Fatalf("LoginBrowserWithOptions() error: %v", err)
	}

	if openCalls != 1 {
		t.Fatalf("openBrowserFunc call count = %d, want 1", openCalls)
	}

	parsedBrowserURL, err := url.Parse(browserURL)
	if err != nil {
		t.Fatalf("url.Parse(browserURL) error: %v", err)
	}

	redirectURI, err := url.Parse(parsedBrowserURL.Query().Get("redirect_uri"))
	if err != nil {
		t.Fatalf("url.Parse(redirectURI) error: %v", err)
	}
	if redirectURI.Port() == "" {
		t.Fatal("redirectURI port is empty")
	}
	if redirectURI.Port() == "0" {
		t.Fatalf("redirectURI port = %q, want dynamically assigned port", redirectURI.Port())
	}
}

func newMockOAuthTokenServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		resp := map[string]any{
			"access_token":  "mock-access-token",
			"refresh_token": "mock-refresh-token",
			"expires_in":    3600,
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

func TestRefreshAccessTokenUsesJSONForOpenAI(t *testing.T) {
	var gotScope string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode JSON body: %v", err)
		}
		if body["grant_type"] != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", body["grant_type"])
		}
		gotScope = body["scope"]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "openai-access-token",
			"expires_in":   3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   "https://auth.openai.com",
		TokenURL: server.URL,
		ClientID: "openai-client",
		Scopes:   "openid profile email offline_access",
	}
	cred := &AuthCredential{
		AccessToken:  "expired-access-token",
		RefreshToken: "openai-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	refreshed, err := RefreshAccessToken(cred, cfg)
	if err != nil {
		t.Fatalf("RefreshAccessToken() error: %v", err)
	}
	if refreshed.AccessToken != "openai-access-token" {
		t.Fatalf("AccessToken = %q, want openai-access-token", refreshed.AccessToken)
	}
	if gotScope != "" {
		t.Fatalf("scope = %q, want refresh requests to omit it", gotScope)
	}
}

func TestRefreshAccessTokenCollapsesConcurrentRefreshes(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "rotated-access-token",
			"refresh_token": "rotated-refresh-token",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		TokenURL: server.URL,
		ClientID: "test-client",
	}
	cred := &AuthCredential{
		AccessToken:  "old-access-token",
		RefreshToken: "rotating-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	const callers = 8
	var wg sync.WaitGroup
	results := make([]*AuthCredential, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = RefreshAccessToken(cred, cfg)
		}(i)
	}
	wg.Wait()

	if got := requests.Load(); got != 1 {
		t.Fatalf("token endpoint requests = %d, want 1", got)
	}
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d error = %v", i, errs[i])
		}
		if results[i].AccessToken != "rotated-access-token" {
			t.Fatalf("caller %d access token = %q", i, results[i].AccessToken)
		}
	}
	if results[0] == results[1] {
		t.Fatal("callers share one credential pointer; want independent copies")
	}
}

func TestOAuthTokenRequestsRespectClientTimeout(t *testing.T) {
	if oauthHTTPClient.Timeout <= 0 {
		t.Fatalf("oauthHTTPClient.Timeout = %v, want a positive timeout", oauthHTTPClient.Timeout)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "late-token"})
	}))
	defer server.Close()

	original := oauthHTTPClient
	oauthHTTPClient = &http.Client{Timeout: 50 * time.Millisecond}
	t.Cleanup(func() { oauthHTTPClient = original })

	cfg := OAuthProviderConfig{
		Issuer:   server.URL,
		ClientID: "test-client",
	}

	if _, err := RequestDeviceCode(cfg); err == nil {
		t.Fatal("RequestDeviceCode() error = nil, want timeout error")
	}
	if _, err := ExchangeCodeForTokens(cfg, "code", "verifier", "http://localhost/callback"); err == nil {
		t.Fatal("ExchangeCodeForTokens() error = nil, want timeout error")
	}

	cred := &AuthCredential{
		RefreshToken: "timeout-refresh-token",
		Provider:     "openai",
		AuthMethod:   "oauth",
	}
	if _, err := RefreshAccessToken(cred, cfg); err == nil {
		t.Fatal("RefreshAccessToken() error = nil, want timeout error")
	}
}
