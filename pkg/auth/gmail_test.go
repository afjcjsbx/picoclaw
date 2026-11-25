package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGoogleMailOAuthConfig(t *testing.T) {
	cfg := GoogleMailOAuthConfig(" client-id ", " client-secret ")
	if cfg.Issuer != "https://accounts.google.com/o/oauth2/v2" {
		t.Errorf("Issuer = %q", cfg.Issuer)
	}
	if cfg.TokenURL != "https://oauth2.googleapis.com/token" {
		t.Errorf("TokenURL = %q", cfg.TokenURL)
	}
	if cfg.ClientID != "client-id" || cfg.ClientSecret != "client-secret" {
		t.Errorf("client credentials were not trimmed: %+v", cfg)
	}
	if cfg.Provider != providerGmail {
		t.Errorf("Provider = %q, want %q", cfg.Provider, providerGmail)
	}
	for _, scope := range []string{gmailMailScope, "https://www.googleapis.com/auth/userinfo.email"} {
		if !strings.Contains(cfg.Scopes, scope) {
			t.Errorf("Scopes = %q, missing %q", cfg.Scopes, scope)
		}
	}
}

func TestGmailAuthorizationURLRequestsOfflineAccess(t *testing.T) {
	cfg := GoogleMailOAuthConfig("client-id", "client-secret")
	rawURL := BuildAuthorizeURL(cfg, PKCECodes{
		CodeVerifier:  "verifier",
		CodeChallenge: "challenge",
	}, "state", "http://localhost:1234/auth/callback")
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/o/oauth2/v2/auth" {
		t.Errorf("authorization path = %q", parsed.Path)
	}
	for key, want := range map[string]string{
		"access_type": "offline",
		"prompt":      "consent",
		"state":       "state",
	} {
		if got := parsed.Query().Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestExchangeGmailCodeStoresClientCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.FormValue("client_id") != "client-id" || r.FormValue("client_secret") != "client-secret" {
			t.Errorf("OAuth client credentials missing from exchange: %v", r.Form)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "access",
			"refresh_token": "refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	cfg := GoogleMailOAuthConfig("client-id", "client-secret")
	cfg.TokenURL = server.URL
	cred, err := ExchangeCodeForTokens(cfg, "code", "verifier", "http://localhost/auth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if cred.Provider != providerGmail || cred.ClientID != "client-id" || cred.ClientSecret != "client-secret" {
		t.Errorf("credential = %+v", cred)
	}
}

func TestGmailCredentialKey(t *testing.T) {
	for input, want := range map[string]string{
		"user@gmail.com":                 "gmail:user@gmail.com",
		" User@Gmail.COM ":               "gmail:user@gmail.com",
		"PicoClaw User <User@Gmail.com>": "gmail:user@gmail.com",
	} {
		got, err := GmailCredentialKey(input)
		if err != nil {
			t.Fatalf("GmailCredentialKey(%q): %v", input, err)
		}
		if got != want {
			t.Errorf("GmailCredentialKey(%q) = %q, want %q", input, got, want)
		}
	}
	for _, input := range []string{"", "not-an-email", "@gmail.com"} {
		if _, err := GmailCredentialKey(input); err == nil {
			t.Errorf("GmailCredentialKey(%q) succeeded", input)
		}
	}
}

func TestRefreshAccessTokenContextUsesStoredClientCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		for key, want := range map[string]string{
			"grant_type":    "refresh_token",
			"client_id":     "stored-client",
			"client_secret": "stored-secret",
			"refresh_token": "old-refresh",
		} {
			if got := r.FormValue(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		if got := r.FormValue("scope"); got != "" {
			t.Errorf("refresh request unexpectedly set scope = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "new-access",
			"refresh_token": "new-refresh",
			"expires_in":    3600,
		})
	}))
	defer server.Close()

	cred := &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		Provider:     providerGmail,
		AuthMethod:   "oauth",
		Email:        "user@gmail.com",
		ClientID:     "stored-client",
		ClientSecret: "stored-secret",
	}
	cfg := GoogleMailOAuthConfig("", "")
	cfg.TokenURL = server.URL

	refreshed, err := RefreshAccessTokenContext(context.Background(), cred, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "new-refresh" {
		t.Fatalf("refreshed credential = %+v", refreshed)
	}
	if refreshed.Email != cred.Email || refreshed.ClientID != cred.ClientID ||
		refreshed.ClientSecret != cred.ClientSecret || refreshed.Provider != providerGmail {
		t.Errorf("credential metadata was not preserved: %+v", refreshed)
	}
	if time.Until(refreshed.ExpiresAt) < 50*time.Minute {
		t.Errorf("ExpiresAt = %v", refreshed.ExpiresAt)
	}
}

func TestRefreshAccessTokenContextHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := RefreshAccessTokenContext(ctx, &AuthCredential{
		RefreshToken: "refresh",
		ClientID:     "client",
	}, GoogleMailOAuthConfig("client", "secret"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestRefreshAccessTokenContextRejectsRedirects(t *testing.T) {
	var redirectedCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		redirectedCalls.Add(1)
		_, _ = w.Write([]byte(`{"access_token":"must-not-be-requested"}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	cfg := GoogleMailOAuthConfig("client", "secret")
	cfg.TokenURL = origin.URL
	_, err := RefreshAccessTokenContext(context.Background(), &AuthCredential{
		RefreshToken: "refresh",
		ClientID:     "client",
		ClientSecret: "secret",
	}, cfg)
	if err == nil {
		t.Fatal("refresh followed a redirect")
	}
	if redirectedCalls.Load() != 0 {
		t.Fatal("refresh request reached the redirect target")
	}
}

func TestOAuthResponseErrorDoesNotLeakProviderBody(t *testing.T) {
	err := oauthResponseError(http.StatusBadRequest, []byte(`{
		"error":"invalid_grant",
		"error_description":"SECRET-PROVIDER-DATA",
		"access_token":"SECRET-ACCESS-TOKEN"
	}`))
	if err == nil || !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %v", err)
	}
	if !IsOAuthTokenError(err, "invalid_grant") {
		t.Errorf("err = %v, want typed invalid_grant error", err)
	}
	for _, secret := range []string{"SECRET-PROVIDER-DATA", "SECRET-ACCESS-TOKEN"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaked %q: %v", secret, err)
		}
	}
}
