package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestOAuthLoginRefreshAndLogout(t *testing.T) {
	for _, clientID := range []string{"", "registered-client"} {
		for _, transport := range []string{"http", "sse"} {
			t.Run(
				transport+"/client="+clientID,
				func(t *testing.T) { testOAuthLoginRefreshAndLogout(t, clientID, transport) },
			)
		}
	}
}

func testOAuthLoginRefreshAndLogout(t *testing.T, clientID, transport string) {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	var serverURL, redirect, challenge string
	var registrations, refreshes atomic.Int32
	mcpServer := sdk.NewServer(&sdk.Implementation{Name: "oauth-test", Version: "1"}, nil)
	mcpHandler := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return mcpServer }, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-protected-resource/mcp":
			json.NewEncoder(w).
				Encode(map[string]any{"resource": serverURL + "/mcp", "authorization_servers": []string{serverURL}, "scopes_supported": []string{"tools"}})
		case "/.well-known/oauth-authorization-server":
			json.NewEncoder(w).
				Encode(map[string]any{"issuer": serverURL, "authorization_endpoint": serverURL + "/authorize", "token_endpoint": serverURL + "/token", "registration_endpoint": serverURL + "/register", "code_challenge_methods_supported": []string{"S256"}})
		case "/register":
			registrations.Add(1)
			var metadata map[string]any
			if json.NewDecoder(r.Body).Decode(&metadata) != nil {
				w.WriteHeader(400)
				return
			}
			uris := metadata["redirect_uris"].([]any)
			redirect = uris[0].(string)
			metadata["client_id"] = "test-client"
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(metadata)
		case "/token":
			r.ParseForm()
			if r.Form.Get("resource") != serverURL+"/mcp" {
				t.Error("token request missing resource binding")
				w.WriteHeader(400)
				return
			}
			if r.Form.Get("grant_type") == "refresh_token" {
				refreshes.Add(1)
				if r.Form.Get("refresh_token") != "refresh-one" {
					t.Error("wrong refresh token")
				}
				fmt.Fprint(
					w,
					`{"access_token":"access-two","token_type":"Bearer","refresh_token":"refresh-two","expires_in":3600}`,
				)
			} else {
				hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(hash[:]) != challenge || r.Form.Get("code") != "test-code" {
					t.Error("invalid PKCE exchange")
				}
				fmt.Fprint(
					w,
					`{"access_token":"access-one","token_type":"Bearer","refresh_token":"refresh-one","expires_in":3600}`,
				)
			}
		case "/mcp":
			bearer := r.Header.Get("Authorization")
			if bearer != "Bearer access-one" && bearer != "Bearer access-two" {
				w.Header().
					Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, serverURL))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			mcpHandler.ServeHTTP(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	serverURL = server.URL
	cfg := config.MCPServerConfig{
		Type:  transport,
		URL:   server.URL + "/mcp",
		OAuth: &config.MCPOAuthConfig{ClientID: clientID, Issuer: serverURL},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output bytes.Buffer
	err := LoginOAuth(ctx, "test", cfg, OAuthLoginOptions{Output: &output, OpenBrowser: func(raw string) error {
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}
		q := u.Query()
		challenge = q.Get("code_challenge")
		if q.Get("code_challenge_method") != "S256" || q.Get("resource") != cfg.URL || challenge == "" {
			return errors.New("missing PKCE/resource")
		}
		if clientID == "" && redirect != q.Get("redirect_uri") {
			return errors.New("redirect mismatch")
		}
		callback := q.Get("redirect_uri") + "?code=test-code&state=" + url.QueryEscape(q.Get("state"))
		resp, err := http.Get(callback)
		if err == nil {
			resp.Body.Close()
		}
		return err
	}})
	require.NoError(t, err)
	if clientID == "" {
		require.EqualValues(t, 1, registrations.Load())
	} else {
		require.Zero(t, registrations.Load())
	}
	require.NotContains(t, output.String(), "access-one")
	store := newOAuthStore("test", cfg)
	session, err := store.load()
	require.NoError(t, err)
	require.Equal(t, "refresh-one", session.Token.RefreshToken)
	info, err := os.Stat(store.path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	info, err = os.Stat(filepath.Dir(store.path))
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
	// A new connection restores the credentials without registration or browser.
	conn, err := connectServer(ctx, "test", cfg)
	require.NoError(t, err)
	require.NoError(t, conn.Session.Close())
	session.Token.Expiry = time.Now().Add(-time.Minute)
	require.NoError(t, store.save(session))
	// Concurrent connections must refresh a rotating token just once.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, tokenErr := (&storedTokenSource{ctx: ctx, name: "test", store: store}).Token()
			if tokenErr != nil {
				t.Error(tokenErr)
				return
			}
			if token.AccessToken != "access-two" {
				t.Error("unexpected access token")
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, refreshes.Load())
	session, err = store.load()
	require.NoError(t, err)
	require.Equal(t, "refresh-two", session.Token.RefreshToken)
	require.NoError(t, LogoutOAuth("test", cfg))
	_, err = connectServer(ctx, "test", cfg)
	require.ErrorContains(t, err, `picoclaw mcp login "test"`)
	require.NoError(t, LogoutOAuth("test", cfg))
}

func TestOAuthCallbackValidation(t *testing.T) {
	for _, outcome := range []string{"success", "denied", "cancel"} {
		t.Run(outcome, func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var output bytes.Buffer
			result, err := fetchBrowserCode(
				ctx,
				listener,
				"https://auth.example/authorize?state=expected",
				OAuthLoginOptions{Output: &output, OpenBrowser: func(string) error {
					base := "http://" + listener.Addr().String() + "/callback"
					for _, query := range []string{"?state=wrong&code=bad", "?state=expected"} {
						resp, requestErr := http.Get(base + query)
						if requestErr != nil {
							return requestErr
						}
						resp.Body.Close()
						if resp.StatusCode != 400 {
							return errors.New("invalid callback accepted")
						}
					}
					if outcome == "cancel" {
						cancel()
						return nil
					}
					query := "?state=expected&code=good&iss=https%3A%2F%2Fauth.example"
					if outcome == "denied" {
						query = "?state=expected&error=access_denied&error_description=SECRET"
					}
					resp, requestErr := http.Get(base + query)
					if requestErr == nil {
						resp.Body.Close()
					}
					return requestErr
				}},
			)
			switch outcome {
			case "success":
				require.NoError(t, err)
				require.Equal(t, "good", result.Code)
				require.Equal(t, "https://auth.example", result.Iss)
			case "denied":
				require.ErrorContains(t, err, "denied")
				require.NotContains(t, err.Error(), "SECRET")
			case "cancel":
				require.ErrorIs(t, err, context.Canceled)
			}
			require.NotContains(t, output.String(), "good")
		})
	}
}

func TestOAuthStoreIsolationAndValidation(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	cfg := config.MCPServerConfig{URL: "https://mcp.example/mcp", OAuth: &config.MCPOAuthConfig{}}
	store := newOAuthStore("../../escape", cfg)
	require.Equal(t, filepath.Join(config.GetHome(), "auth", "mcp"), filepath.Dir(store.path))
	changed := cfg
	changed.URL = "https://other.example/mcp"
	require.NotEqual(t, store.path, newOAuthStore("../../escape", changed).path)
	changed = cfg
	changed.OAuth = &config.MCPOAuthConfig{ClientID: "other"}
	require.NotEqual(t, store.path, newOAuthStore("../../escape", changed).path)
	require.NotEqual(t, store.path, newOAuthStore("other", cfg).path)
	session := &oauthSession{
		Config:   &oauth2.Config{Endpoint: oauth2.Endpoint{TokenURL: "https://auth.example/token"}},
		Token:    &oauth2.Token{AccessToken: "secret"},
		Resource: cfg.URL,
	}
	require.NoError(t, store.save(session))
	require.NoError(t, os.Chmod(store.path, 0o644))
	_, err := store.load()
	require.ErrorContains(t, err, "owner-only")
	require.NoError(t, os.Chmod(store.path, 0o600))
	require.NoError(t, os.WriteFile(store.path, []byte("corrupt secret"), 0o600))
	_, err = store.load()
	require.ErrorContains(t, err, "invalid MCP OAuth token file")
	require.NotContains(t, err.Error(), "secret")
}

func TestOAuthConfigValidation(t *testing.T) {
	for _, raw := range []string{"https://mcp.example/mcp", "http://127.0.0.1/mcp", "http://[::1]/mcp"} {
		require.NoError(t, validateOAuthURL(raw))
	}
	for _, raw := range []string{"http://mcp.example", "file:///tmp/token", "https://user:pass@example.com", "https://example.com/#fragment"} {
		require.Error(t, validateOAuthURL(raw))
	}
	for _, cfg := range []config.MCPServerConfig{
		{Type: "stdio", Command: "server", OAuth: &config.MCPOAuthConfig{}},
		{URL: "https://example.com", OAuth: &config.MCPOAuthConfig{CallbackPort: -1}},
		{URL: "https://example.com", OAuth: &config.MCPOAuthConfig{}, Headers: map[string]string{"authorization": "Bearer secret"}},
	} {
		require.Error(t, validateOAuthConfig(cfg))
	}
}

func TestOAuthDoesNotFollowCredentialRedirect(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }),
	)
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := &http.Client{Transport: oauthHTTPTransport{}, CheckRedirect: rejectOAuthRedirect}
	req, err := http.NewRequest(http.MethodPost, source.URL, strings.NewReader("refresh_token=secret"))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	require.False(t, leaked.Load())
	require.Equal(t, http.StatusTemporaryRedirect, resp.StatusCode)
}

func TestOAuthRefreshFailureDoesNotExposeProviderBody(t *testing.T) {
	t.Setenv(config.EnvHome, t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"secret-refresh-token"}`)
	}))
	defer server.Close()
	cfg := config.MCPServerConfig{URL: "https://mcp.example/mcp", OAuth: &config.MCPOAuthConfig{}}
	store := newOAuthStore("test", cfg)
	session := &oauthSession{
		Config: &oauth2.Config{ClientID: "client", Endpoint: oauth2.Endpoint{TokenURL: server.URL}},
		Token: &oauth2.Token{
			AccessToken:  "expired",
			RefreshToken: "secret-refresh-token",
			Expiry:       time.Now().Add(-time.Minute),
		},
		Resource: cfg.URL,
	}
	require.NoError(t, store.save(session))
	_, err := (&storedTokenSource{ctx: context.Background(), name: "test", store: store}).Token()
	require.ErrorContains(t, err, `picoclaw mcp login "test"`)
	require.NotContains(t, err.Error(), "secret-refresh-token")
	// A failed refresh must leave the existing file intact.
	saved, err := store.load()
	require.NoError(t, err)
	require.Equal(t, session.Token.RefreshToken, saved.Token.RefreshToken)
}

func TestOAuthBrowserFallbackAndNoBrowser(t *testing.T) {
	for _, noBrowser := range []bool{true, false} {
		t.Run(fmt.Sprint(noBrowser), func(t *testing.T) {
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			require.NoError(t, err)
			defer listener.Close()
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var output bytes.Buffer
			calls := 0
			_, err = fetchBrowserCode(
				ctx,
				listener,
				"https://auth.example?state=expected",
				OAuthLoginOptions{
					NoBrowser:   noBrowser,
					Output:      &output,
					OpenBrowser: func(string) error { calls++; return errors.New("no desktop") },
				},
			)
			require.ErrorIs(t, err, context.Canceled)
			require.Contains(t, output.String(), "https://auth.example?state=expected")
			if noBrowser {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
				require.Contains(t, output.String(), "manually")
			}
		})
	}
}

func TestOAuthLoginRejectsInvalidAuthorization(t *testing.T) {
	for _, scenario := range []string{"missing-pkce", "wrong-issuer", "denied", "token-error"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv(config.EnvHome, t.TempDir())
			var serverURL string
			var tokenCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/.well-known/oauth-protected-resource/mcp":
					json.NewEncoder(w).
						Encode(map[string]any{"resource": serverURL + "/mcp", "authorization_servers": []string{serverURL}})
				case "/.well-known/oauth-authorization-server":
					methods := []string{"S256"}
					if scenario == "missing-pkce" {
						methods = nil
					}
					json.NewEncoder(w).
						Encode(map[string]any{"issuer": serverURL, "authorization_endpoint": serverURL + "/authorize", "token_endpoint": serverURL + "/token", "code_challenge_methods_supported": methods, "authorization_response_iss_parameter_supported": true})
				case "/token":
					tokenCalls.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					fmt.Fprint(w, `{"error":"invalid_grant","error_description":"SECRET-PROVIDER-DATA"}`)
				default:
					w.Header().
						Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata="%s/.well-known/oauth-protected-resource/mcp"`, serverURL))
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			defer server.Close()
			serverURL = server.URL
			cfg := config.MCPServerConfig{
				Type:  "http",
				URL:   serverURL + "/mcp",
				OAuth: &config.MCPOAuthConfig{ClientID: "public-client", Issuer: serverURL},
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := LoginOAuth(ctx, "test", cfg, OAuthLoginOptions{OpenBrowser: func(raw string) error {
				parsed, err := url.Parse(raw)
				if err != nil {
					return err
				}
				query := url.Values{"state": {parsed.Query().Get("state")}, "code": {"code"}, "iss": {serverURL}}
				if scenario == "denied" {
					query.Set("error", "access_denied")
				}
				if scenario == "wrong-issuer" {
					query.Set("iss", "https://other.example")
				}
				resp, err := http.Get(parsed.Query().Get("redirect_uri") + "?" + query.Encode())
				if err == nil {
					resp.Body.Close()
				}
				return err
			}})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "SECRET-PROVIDER-DATA")
			_, err = newOAuthStore("test", cfg).load()
			require.ErrorIs(t, err, os.ErrNotExist)
			if scenario != "token-error" {
				require.Zero(t, tokenCalls.Load())
			} else {
				require.Positive(t, tokenCalls.Load())
			}
		})
	}
}
