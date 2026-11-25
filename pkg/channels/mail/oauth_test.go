package mail

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/auth"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

const oauthTestProvider = "gmail:bot@example.com"

func setMailAuthHome(t *testing.T) {
	t.Helper()
	t.Setenv(config.EnvHome, filepath.Join(t.TempDir(), ".picoclaw"))
}

func storeMailCredential(t *testing.T, accessToken string, expiresAt time.Time) *auth.AuthCredential {
	t.Helper()
	cred := &auth.AuthCredential{
		AccessToken:  accessToken,
		RefreshToken: "refresh-token",
		ExpiresAt:    expiresAt,
		Provider:     "gmail",
		AuthMethod:   "oauth",
		Email:        "bot@example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	if err := auth.SetCredential(oauthTestProvider, cred); err != nil {
		t.Fatal(err)
	}
	return cred
}

func useMailTokenServer(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(handler)
	original := mailOAuthConfig
	mailOAuthConfig = func(clientID, clientSecret string) auth.OAuthProviderConfig {
		cfg := original(clientID, clientSecret)
		cfg.TokenURL = server.URL
		return cfg
	}
	t.Cleanup(func() {
		mailOAuthConfig = original
		server.Close()
	})
}

func TestManagedTokenSourceUsesValidTokenWithoutRefresh(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "valid-token", time.Now().Add(time.Hour))
	var calls atomic.Int32
	useMailTokenServer(t, func(http.ResponseWriter, *http.Request) { calls.Add(1) })

	source, err := newManagedTokenSource(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	token, err := source.Token(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if token != "valid-token" || calls.Load() != 0 {
		t.Fatalf("token = %q, refresh calls = %d", token, calls.Load())
	}
}

func TestManagedTokenSourceRefreshesAndPersistsRotation(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "expired-token", time.Now().Add(-time.Minute))
	var calls atomic.Int32
	useMailTokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		for key, want := range map[string]string{
			"grant_type":    "refresh_token",
			"client_id":     "client-id",
			"client_secret": "client-secret",
			"refresh_token": "refresh-token",
		} {
			if got := r.FormValue(key); got != want {
				t.Errorf("%s = %q, want %q", key, got, want)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "fresh-token",
			"refresh_token": "rotated-refresh",
			"expires_in":    3600,
		})
	})

	source, err := newManagedTokenSource(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		token, tokenErr := source.Token(context.Background())
		if tokenErr != nil {
			t.Fatal(tokenErr)
		}
		if token != "fresh-token" {
			t.Fatalf("token = %q", token)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
	stored, err := auth.GetCredential(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "fresh-token" || stored.RefreshToken != "rotated-refresh" {
		t.Fatalf("refreshed credential was not persisted: %+v", stored)
	}
	if stored.ClientID != "client-id" || stored.ClientSecret != "client-secret" || stored.Email != "bot@example.com" {
		t.Errorf("credential metadata was not persisted: %+v", stored)
	}
}

func TestManagedTokenSourceSerializesConcurrentRelogin(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "expired-token", time.Now().Add(-time.Minute))
	refreshStarted := make(chan struct{})
	allowRefresh := make(chan struct{})
	var releaseRefreshOnce sync.Once
	releaseRefresh := func() { releaseRefreshOnce.Do(func() { close(allowRefresh) }) }
	defer releaseRefresh()
	useMailTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		close(refreshStarted)
		<-allowRefresh
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "refresh-token",
			"refresh_token": "refresh-rotated",
			"expires_in":    3600,
		})
	})

	source, err := newManagedTokenSource(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	tokenResult := make(chan string, 1)
	tokenDoneErr := make(chan error, 1)
	go func() {
		token, tokenErr := source.Token(context.Background())
		tokenResult <- token
		tokenDoneErr <- tokenErr
	}()
	<-refreshStarted

	relogged := &auth.AuthCredential{
		AccessToken:  "relogin-token",
		RefreshToken: "relogin-refresh",
		ExpiresAt:    time.Now().Add(time.Hour),
		Provider:     "gmail",
		AuthMethod:   "oauth",
		Email:        "bot@example.com",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	loginDone := make(chan error, 1)
	loginStarted := make(chan struct{})
	go func() {
		close(loginStarted)
		loginDone <- auth.SetCredential(oauthTestProvider, relogged)
	}()
	<-loginStarted
	select {
	case loginErr := <-loginDone:
		t.Fatalf("re-login completed while token refresh held the auth lock: %v", loginErr)
	case <-time.After(50 * time.Millisecond):
	}

	releaseRefresh()
	if token := <-tokenResult; token != "refresh-token" {
		t.Fatalf("token = %q", token)
	}
	if refreshErr := <-tokenDoneErr; refreshErr != nil {
		t.Fatal(refreshErr)
	}
	if loginErr := <-loginDone; loginErr != nil {
		t.Fatal(loginErr)
	}
	stored, err := auth.GetCredential(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RefreshToken != "relogin-refresh" {
		t.Fatalf("re-login did not win after refresh: %+v", stored)
	}
}

func TestManagedTokenSourceConcurrentRefreshIsSingleFlight(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "expired-token", time.Now().Add(-time.Minute))
	var calls atomic.Int32
	useMailTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-token",
			"expires_in":   3600,
		})
	})

	source, err := newManagedTokenSource(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 12
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := source.Token(context.Background())
			if err == nil && token != "fresh-token" {
				err = errors.New("unexpected token: " + token)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("refresh calls = %d, want 1", calls.Load())
	}
}

func TestManagedTokenSourceInvalidGrantRequiresLogin(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "expired-token", time.Now().Add(-time.Minute))
	useMailTokenServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"SECRET-PROVIDER-DATA"}`))
	})

	source, err := newManagedTokenSource(oauthTestProvider)
	if err != nil {
		t.Fatal(err)
	}
	_, err = source.Token(context.Background())
	if err == nil {
		t.Fatal("refresh succeeded")
	}
	for _, want := range []string{"picoclaw auth login --provider gmail", "invalid_grant"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "SECRET-PROVIDER-DATA") {
		t.Errorf("provider error body leaked: %v", err)
	}
}

func TestManagedTokenSourceRequiresStoredCredential(t *testing.T) {
	setMailAuthHome(t)
	_, err := newManagedTokenSource(oauthTestProvider)
	if err == nil || !strings.Contains(err.Error(), "picoclaw auth login --provider gmail") {
		t.Fatalf("err = %v", err)
	}

	cred := storeMailCredential(t, "token", time.Now().Add(time.Hour))
	cred.RefreshToken = ""
	if setErr := auth.SetCredential(oauthTestProvider, cred); setErr != nil {
		t.Fatal(setErr)
	}
	_, err = newManagedTokenSource(oauthTestProvider)
	if err == nil || !strings.Contains(err.Error(), "no refresh token") {
		t.Fatalf("err = %v", err)
	}
}

func TestMailChannelOAuthProviderUsesXOAUTH2ForSMTP(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, "smtp-token", time.Now().Add(time.Hour))
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, func(s *config.MailSettings) { s.OAuthProvider = oauthTestProvider })

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatal(err)
	}
	rec := f.next(t)
	if rec.authMech != "XOAUTH2" || !strings.Contains(rec.authPayload, "Bearer smtp-token") {
		t.Errorf("auth = %s %q", rec.authMech, rec.authPayload)
	}
}

func TestMailChannelOAuthProviderUsesXOAUTH2ForIMAP(t *testing.T) {
	setMailAuthHome(t)
	storeMailCredential(t, testToken, time.Now().Add(time.Hour))
	srv := newTestIMAP(t, nil)
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) {
		s.OAuthProvider = oauthTestProvider
	})
	startChannel(t, c)
	waitIdle(t, srv, 1)

	srv.deliver(fixture("oauth@example.org", alice, "OAuth IMAP", "hello"))
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "OAuth IMAP" {
		t.Fatalf("subject = %q", got)
	}
}

func TestSetTokenSourceEnablesXOAuth2WithoutStaticToken(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, nil)
	c.SetTokenSource(func(context.Context) (string, error) { return "injected-token", nil })
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatal(err)
	}
	if rec := f.next(t); rec.authMech != "XOAUTH2" || !strings.Contains(rec.authPayload, "injected-token") {
		t.Errorf("auth = %s %q", rec.authMech, rec.authPayload)
	}
}
