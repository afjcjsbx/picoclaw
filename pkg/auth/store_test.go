package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
)

func setTestAuthHome(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	t.Setenv(config.EnvHome, filepath.Join(tmpDir, ".picoclaw"))
	return tmpDir
}

func TestAuthCredentialIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"zero time", time.Time{}, false},
		{"future", time.Now().Add(time.Hour), false},
		{"past", time.Now().Add(-time.Hour), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &AuthCredential{ExpiresAt: tt.expiresAt}
			if got := c.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthCredentialNeedsRefresh(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{"zero time", time.Time{}, false},
		{"far future", time.Now().Add(time.Hour), false},
		{"within 5 min", time.Now().Add(3 * time.Minute), true},
		{"already expired", time.Now().Add(-time.Minute), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &AuthCredential{ExpiresAt: tt.expiresAt}
			if got := c.NeedsRefresh(); got != tt.want {
				t.Errorf("NeedsRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStoreRoundtrip(t *testing.T) {
	setTestAuthHome(t)

	cred := &AuthCredential{
		AccessToken:  "test-access-token",
		RefreshToken: "test-refresh-token",
		AccountID:    "acct-123",
		ExpiresAt:    time.Now().Add(time.Hour).Truncate(time.Second),
		Provider:     "openai",
		AuthMethod:   "oauth",
	}

	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if loaded == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if loaded.AccessToken != cred.AccessToken {
		t.Errorf("AccessToken = %q, want %q", loaded.AccessToken, cred.AccessToken)
	}
	if loaded.RefreshToken != cred.RefreshToken {
		t.Errorf("RefreshToken = %q, want %q", loaded.RefreshToken, cred.RefreshToken)
	}
	if loaded.Provider != cred.Provider {
		t.Errorf("Provider = %q, want %q", loaded.Provider, cred.Provider)
	}
}

func TestStoreCredentialClientFields(t *testing.T) {
	setTestAuthHome(t)
	cred := &AuthCredential{
		AccessToken:  "access",
		RefreshToken: "refresh",
		Provider:     providerGmail,
		AuthMethod:   "oauth",
		ClientID:     "client-id",
		ClientSecret: "client-secret",
	}
	if err := SetCredential("gmail:user@gmail.com", cred); err != nil {
		t.Fatal(err)
	}
	loaded, err := GetCredential("gmail:user@gmail.com")
	if err != nil {
		t.Fatal(err)
	}
	if loaded == nil || loaded.ClientID != cred.ClientID || loaded.ClientSecret != cred.ClientSecret {
		t.Fatalf("loaded credential = %+v", loaded)
	}
}

func TestUpdateCredentialPersistsUnderLock(t *testing.T) {
	setTestAuthHome(t)
	original := &AuthCredential{
		AccessToken:  "old-access",
		RefreshToken: "refresh",
		Provider:     providerGmail,
		AuthMethod:   "oauth",
	}
	if err := SetCredential("gmail:user@gmail.com", original); err != nil {
		t.Fatal(err)
	}
	updated, err := UpdateCredential(context.Background(), "gmail:user@gmail.com",
		func(current *AuthCredential) (*AuthCredential, error) {
			if current.AccessToken != "old-access" {
				t.Errorf("current token = %q", current.AccessToken)
			}
			current.AccessToken = "new-access"
			return current, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AccessToken != "new-access" {
		t.Fatalf("updated token = %q", updated.AccessToken)
	}
	stored, err := GetCredential("gmail:user@gmail.com")
	if err != nil {
		t.Fatal(err)
	}
	if stored.AccessToken != "new-access" {
		t.Fatalf("stored token = %q", stored.AccessToken)
	}
}

func TestUpdateCredentialAllowsReadsDuringCallback(t *testing.T) {
	setTestAuthHome(t)
	if err := SetCredential("gmail:user@gmail.com", &AuthCredential{
		AccessToken: "token",
		Provider:    providerGmail,
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatal(err)
	}
	callbackStarted := make(chan struct{})
	releaseCallback := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCallback) }) }
	defer release()
	updateDone := make(chan error, 1)
	go func() {
		_, err := UpdateCredential(context.Background(), "gmail:user@gmail.com",
			func(current *AuthCredential) (*AuthCredential, error) {
				if _, err := GetCredential("gmail:user@gmail.com"); err != nil {
					return nil, err
				}
				close(callbackStarted)
				<-releaseCallback
				return current, nil
			})
		updateDone <- err
	}()
	<-callbackStarted

	readDone := make(chan error, 1)
	go func() {
		cred, err := GetCredential("gmail:user@gmail.com")
		if err == nil && (cred == nil || cred.AccessToken != "token") {
			err = fmt.Errorf("unexpected credential: %+v", cred)
		}
		readDone <- err
	}()
	select {
	case err := <-readDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("credential read blocked behind token refresh callback")
	}

	release()
	if err := <-updateDone; err != nil {
		t.Fatal(err)
	}
}

func TestUpdateCredentialHonorsCancellationWhileWaiting(t *testing.T) {
	setTestAuthHome(t)
	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	var releaseLockOnce sync.Once
	release := func() { releaseLockOnce.Do(func() { close(releaseLock) }) }
	defer release()
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- withAuthStoreFileLock(context.Background(), func() error {
			close(lockHeld)
			<-releaseLock
			return nil
		})
	}()
	<-lockHeld

	lockContended := make(chan struct{})
	originalHook := authStoreLockRetryHook
	var contendOnce sync.Once
	authStoreLockRetryHook = func() { contendOnce.Do(func() { close(lockContended) }) }
	t.Cleanup(func() { authStoreLockRetryHook = originalHook })

	ctx, cancel := context.WithCancel(context.Background())
	updateDone := make(chan error, 1)
	go func() {
		_, err := UpdateCredential(ctx, "gmail:user@gmail.com",
			func(current *AuthCredential) (*AuthCredential, error) { return current, nil })
		updateDone <- err
	}()
	<-lockContended
	cancel()
	select {
	case err := <-updateDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("UpdateCredential did not honor cancellation while waiting")
	}

	release()
	if err := <-lockDone; err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSetCredentialPreservesEntries(t *testing.T) {
	setTestAuthHome(t)
	const workers = 20
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider := fmt.Sprintf("provider-%d", i)
			errs <- SetCredential(provider, &AuthCredential{
				AccessToken: provider,
				Provider:    provider,
				AuthMethod:  "oauth",
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := LoadStore()
	if err != nil {
		t.Fatal(err)
	}
	if len(store.Credentials) != workers {
		t.Fatalf("stored %d credentials, want %d", len(store.Credentials), workers)
	}
}

func TestStoreFilePermissions(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	cred := &AuthCredential{
		AccessToken: "secret-token",
		Provider:    "openai",
		AuthMethod:  "oauth",
	}
	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error: %v", err)
	}
	perm := info.Mode().Perm()
	if runtime.GOOS == "windows" {
		return
	}
	if perm != 0o600 {
		t.Errorf("file permissions = %o, want 0600", perm)
	}
}

func TestStoreMultiProvider(t *testing.T) {
	setTestAuthHome(t)

	openaiCred := &AuthCredential{AccessToken: "openai-token", Provider: "openai", AuthMethod: "oauth"}
	anthropicCred := &AuthCredential{AccessToken: "anthropic-token", Provider: "anthropic", AuthMethod: "token"}

	if err := SetCredential("openai", openaiCred); err != nil {
		t.Fatalf("SetCredential(openai) error: %v", err)
	}
	if err := SetCredential("anthropic", anthropicCred); err != nil {
		t.Fatalf("SetCredential(anthropic) error: %v", err)
	}

	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential(openai) error: %v", err)
	}
	if loaded.AccessToken != "openai-token" {
		t.Errorf("openai token = %q, want %q", loaded.AccessToken, "openai-token")
	}

	loaded, err = GetCredential("anthropic")
	if err != nil {
		t.Fatalf("GetCredential(anthropic) error: %v", err)
	}
	if loaded.AccessToken != "anthropic-token" {
		t.Errorf("anthropic token = %q, want %q", loaded.AccessToken, "anthropic-token")
	}
}

func TestDeleteCredential(t *testing.T) {
	setTestAuthHome(t)

	cred := &AuthCredential{AccessToken: "to-delete", Provider: "openai", AuthMethod: "oauth"}
	if err := SetCredential("openai", cred); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	if err := DeleteCredential("openai"); err != nil {
		t.Fatalf("DeleteCredential() error: %v", err)
	}

	loaded, err := GetCredential("openai")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil after delete")
	}
}

func TestLoadStoreEmpty(t *testing.T) {
	setTestAuthHome(t)

	store, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if store == nil {
		t.Fatal("LoadStore() returned nil")
	}
	if len(store.Credentials) != 0 {
		t.Errorf("expected empty credentials, got %d", len(store.Credentials))
	}
}

func TestGetCredentialCanonicalizesLegacyAntigravityProvider(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	expiresAt := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	store := map[string]any{
		"credentials": map[string]any{
			"antigravity": map[string]any{
				"access_token": "legacy-token",
				"expires_at":   expiresAt.Format(time.RFC3339),
				"provider":     "antigravity",
				"auth_method":  "oauth",
				"project_id":   "project-1",
			},
		},
	}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	cred, err := GetCredential("google-antigravity")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if cred == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if cred.Provider != "google-antigravity" {
		t.Fatalf("Provider = %q, want %q", cred.Provider, "google-antigravity")
	}
	if !cred.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", cred.ExpiresAt, expiresAt)
	}
}

func TestLoadStoreMergesAntigravityAliasesPreferringNewerExpiry(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	legacyExpiry := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)
	refreshedExpiry := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	store := map[string]any{
		"credentials": map[string]any{
			"antigravity": map[string]any{
				"access_token":  "legacy-token",
				"refresh_token": "legacy-refresh",
				"expires_at":    legacyExpiry.Format(time.RFC3339),
				"provider":      "antigravity",
				"auth_method":   "oauth",
				"email":         "legacy@example.com",
			},
			"google-antigravity": map[string]any{
				"access_token": "fresh-token",
				"expires_at":   refreshedExpiry.Format(time.RFC3339),
				"provider":     "google-antigravity",
				"auth_method":  "oauth",
				"project_id":   "project-2",
			},
		},
	}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	loaded, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(loaded.Credentials) != 1 {
		t.Fatalf("credential count = %d, want 1", len(loaded.Credentials))
	}

	cred := loaded.Credentials["google-antigravity"]
	if cred == nil {
		t.Fatal("google-antigravity credential missing")
	}
	if cred.AccessToken != "fresh-token" {
		t.Fatalf("AccessToken = %q, want %q", cred.AccessToken, "fresh-token")
	}
	if cred.RefreshToken != "legacy-refresh" {
		t.Fatalf("RefreshToken = %q, want %q", cred.RefreshToken, "legacy-refresh")
	}
	if cred.Email != "legacy@example.com" {
		t.Fatalf("Email = %q, want %q", cred.Email, "legacy@example.com")
	}
	if cred.ProjectID != "project-2" {
		t.Fatalf("ProjectID = %q, want %q", cred.ProjectID, "project-2")
	}
	if !cred.ExpiresAt.Equal(refreshedExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", cred.ExpiresAt, refreshedExpiry)
	}
}

func TestLoadStorePrefersCanonicalKeyWhenExpiryMatchesAlias(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	expiresAt := time.Date(2026, 4, 16, 12, 0, 0, 0, time.UTC)
	store := map[string]any{
		"credentials": map[string]any{
			"antigravity": map[string]any{
				"access_token":  "legacy-token",
				"refresh_token": "legacy-refresh",
				"expires_at":    expiresAt.Format(time.RFC3339),
				"provider":      "antigravity",
				"auth_method":   "oauth",
				"email":         "legacy@example.com",
			},
			" Google-Antigravity ": map[string]any{
				"access_token": "fresh-token",
				"expires_at":   expiresAt.Format(time.RFC3339),
				"provider":     " Google-Antigravity ",
				"auth_method":  "oauth",
				"project_id":   "project-2",
			},
		},
	}
	data, err := json.Marshal(store)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	loaded, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(loaded.Credentials) != 1 {
		t.Fatalf("credential count = %d, want 1", len(loaded.Credentials))
	}

	cred := loaded.Credentials["google-antigravity"]
	if cred == nil {
		t.Fatal("google-antigravity credential missing")
	}
	if cred.AccessToken != "fresh-token" {
		t.Fatalf("AccessToken = %q, want %q", cred.AccessToken, "fresh-token")
	}
	if cred.RefreshToken != "legacy-refresh" {
		t.Fatalf("RefreshToken = %q, want %q", cred.RefreshToken, "legacy-refresh")
	}
	if cred.Email != "legacy@example.com" {
		t.Fatalf("Email = %q, want %q", cred.Email, "legacy@example.com")
	}
	if cred.ProjectID != "project-2" {
		t.Fatalf("ProjectID = %q, want %q", cred.ProjectID, "project-2")
	}
}

func TestSetCredentialReplacesLegacyAntigravityEntry(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	legacyStore := map[string]any{
		"credentials": map[string]any{
			"antigravity": map[string]any{
				"access_token": "legacy-token",
				"expires_at":   time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC).Format(time.RFC3339),
				"provider":     "antigravity",
				"auth_method":  "oauth",
			},
		},
	}
	data, err := json.Marshal(legacyStore)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	refreshedExpiry := time.Date(2026, 4, 16, 12, 30, 0, 0, time.UTC)
	err = SetCredential("google-antigravity", &AuthCredential{
		AccessToken: "fresh-token",
		ExpiresAt:   refreshedExpiry,
		Provider:    "google-antigravity",
		AuthMethod:  "oauth",
	})
	if err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	loaded, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(loaded.Credentials) != 1 {
		t.Fatalf("credential count = %d, want 1", len(loaded.Credentials))
	}

	cred := loaded.Credentials["google-antigravity"]
	if cred == nil {
		t.Fatal("google-antigravity credential missing")
	}
	if cred.AccessToken != "fresh-token" {
		t.Fatalf("AccessToken = %q, want %q", cred.AccessToken, "fresh-token")
	}
	if !cred.ExpiresAt.Equal(refreshedExpiry) {
		t.Fatalf("ExpiresAt = %v, want %v", cred.ExpiresAt, refreshedExpiry)
	}
}

func TestDeleteCredentialRemovesLegacyAntigravityAlias(t *testing.T) {
	tmpDir := setTestAuthHome(t)

	legacyStore := map[string]any{
		"credentials": map[string]any{
			"antigravity": map[string]any{
				"access_token": "legacy-token",
				"provider":     "antigravity",
				"auth_method":  "oauth",
			},
		},
	}
	data, err := json.Marshal(legacyStore)
	if err != nil {
		t.Fatalf("json.Marshal() error: %v", err)
	}
	path := filepath.Join(tmpDir, ".picoclaw", "auth.json")
	err = os.MkdirAll(filepath.Dir(path), 0o755)
	if err != nil {
		t.Fatalf("MkdirAll() error: %v", err)
	}
	err = os.WriteFile(path, data, 0o600)
	if err != nil {
		t.Fatalf("WriteFile() error: %v", err)
	}

	err = DeleteCredential(" google-antigravity ")
	if err != nil {
		t.Fatalf("DeleteCredential() error: %v", err)
	}

	loaded, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(loaded.Credentials) != 0 {
		t.Fatalf("credential count = %d, want 0", len(loaded.Credentials))
	}
}

func TestSetCredentialCanonicalizesTrimmedMixedCaseProvider(t *testing.T) {
	setTestAuthHome(t)

	expiresAt := time.Date(2026, 4, 16, 13, 0, 0, 0, time.UTC)
	if err := SetCredential("  AnTiGrAvItY  ", &AuthCredential{
		AccessToken: "fresh-token",
		ExpiresAt:   expiresAt,
		Provider:    "  AnTiGrAvItY  ",
		AuthMethod:  "oauth",
	}); err != nil {
		t.Fatalf("SetCredential() error: %v", err)
	}

	loaded, err := LoadStore()
	if err != nil {
		t.Fatalf("LoadStore() error: %v", err)
	}
	if len(loaded.Credentials) != 1 {
		t.Fatalf("credential count = %d, want 1", len(loaded.Credentials))
	}

	cred := loaded.Credentials["google-antigravity"]
	if cred == nil {
		t.Fatal("google-antigravity credential missing")
	}
	if cred.Provider != "google-antigravity" {
		t.Fatalf("Provider = %q, want %q", cred.Provider, "google-antigravity")
	}
	if !cred.ExpiresAt.Equal(expiresAt) {
		t.Fatalf("ExpiresAt = %v, want %v", cred.ExpiresAt, expiresAt)
	}

	got, err := GetCredential("  GoOgLe-AnTiGrAvItY ")
	if err != nil {
		t.Fatalf("GetCredential() error: %v", err)
	}
	if got == nil {
		t.Fatal("GetCredential() returned nil")
	}
	if got.Provider != "google-antigravity" {
		t.Fatalf("GetCredential provider = %q, want %q", got.Provider, "google-antigravity")
	}
}
