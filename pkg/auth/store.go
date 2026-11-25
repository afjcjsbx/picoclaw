package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/fileutil"
)

type AuthCredential struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	AccountID    string    `json:"account_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitempty"`
	Provider     string    `json:"provider"`
	AuthMethod   string    `json:"auth_method"`
	Email        string    `json:"email,omitempty"`
	ProjectID    string    `json:"project_id,omitempty"`
	ClientID     string    `json:"client_id,omitempty"`
	ClientSecret string    `json:"client_secret,omitempty"`
}

type AuthStore struct {
	Credentials map[string]*AuthCredential `json:"credentials"`
}

const (
	providerGoogleAntigravity = "google-antigravity"
	providerAntigravityAlias  = "antigravity"
	providerGmail             = "gmail"
)

// storeMu serializes read-modify-write operations on auth.json. Without it,
// two token sources refreshing concurrently can overwrite each other's
// credentials even when each individual write is atomic.
var storeMu sync.Mutex

func (c *AuthCredential) IsExpired() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(c.ExpiresAt)
}

func (c *AuthCredential) NeedsRefresh() bool {
	if c.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().Add(5 * time.Minute).After(c.ExpiresAt)
}

func authFilePath() string {
	return filepath.Join(config.GetHome(), "auth.json")
}

func canonicalProvider(provider string) string {
	normalized := strings.ToLower(strings.TrimSpace(provider))
	switch normalized {
	case providerAntigravityAlias:
		return providerGoogleAntigravity
	default:
		return normalized
	}
}

func cloneCredential(cred *AuthCredential) *AuthCredential {
	if cred == nil {
		return nil
	}
	cp := *cred
	return &cp
}

func sameCredential(a, b *AuthCredential) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func mergeCredentials(primary, secondary *AuthCredential) *AuthCredential {
	if primary == nil {
		return cloneCredential(secondary)
	}

	merged := *primary
	if secondary == nil {
		return &merged
	}
	if merged.AccessToken == "" {
		merged.AccessToken = secondary.AccessToken
	}
	if merged.RefreshToken == "" {
		merged.RefreshToken = secondary.RefreshToken
	}
	if merged.AccountID == "" {
		merged.AccountID = secondary.AccountID
	}
	if merged.ExpiresAt.IsZero() {
		merged.ExpiresAt = secondary.ExpiresAt
	}
	if merged.Provider == "" {
		merged.Provider = secondary.Provider
	}
	if merged.AuthMethod == "" {
		merged.AuthMethod = secondary.AuthMethod
	}
	if merged.Email == "" {
		merged.Email = secondary.Email
	}
	if merged.ProjectID == "" {
		merged.ProjectID = secondary.ProjectID
	}
	if merged.ClientID == "" {
		merged.ClientID = secondary.ClientID
	}
	if merged.ClientSecret == "" {
		merged.ClientSecret = secondary.ClientSecret
	}

	return &merged
}

func shouldPreferCredential(
	candidate *AuthCredential,
	candidateCanonical bool,
	current *AuthCredential,
	currentCanonical bool,
) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}

	switch {
	case candidate.ExpiresAt.After(current.ExpiresAt):
		return true
	case current.ExpiresAt.After(candidate.ExpiresAt):
		return false
	case candidateCanonical != currentCanonical:
		return candidateCanonical
	default:
		return false
	}
}

func normalizeStore(store *AuthStore) {
	if store == nil {
		return
	}
	if store.Credentials == nil {
		store.Credentials = make(map[string]*AuthCredential)
		return
	}

	normalized := make(map[string]*AuthCredential, len(store.Credentials))
	canonicalFlags := make(map[string]bool, len(store.Credentials))

	for provider, cred := range store.Credentials {
		normalizedProvider := strings.ToLower(strings.TrimSpace(provider))
		canonical := canonicalProvider(provider)
		normalizedCred := cloneCredential(cred)
		if normalizedCred != nil {
			normalizedCred.Provider = canonicalProvider(normalizedCred.Provider)
			if normalizedCred.Provider == "" {
				normalizedCred.Provider = canonical
			}
		}

		current := normalized[canonical]
		currentCanonical := canonicalFlags[canonical]
		candidateCanonical := normalizedProvider == canonical

		if shouldPreferCredential(normalizedCred, candidateCanonical, current, currentCanonical) {
			normalized[canonical] = mergeCredentials(normalizedCred, current)
			canonicalFlags[canonical] = candidateCanonical
			continue
		}

		normalized[canonical] = mergeCredentials(current, normalizedCred)
	}

	store.Credentials = normalized
}

func LoadStore() (*AuthStore, error) {
	storeMu.Lock()
	defer storeMu.Unlock()
	return loadStore()
}

func loadStore() (*AuthStore, error) {
	path := authFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &AuthStore{Credentials: make(map[string]*AuthCredential)}, nil
		}
		return nil, err
	}

	var store AuthStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	normalizeStore(&store)
	return &store, nil
}

func SaveStore(store *AuthStore) error {
	return withAuthStoreFileLock(context.Background(), func() error {
		storeMu.Lock()
		defer storeMu.Unlock()
		return saveStore(store)
	})
}

func saveStore(store *AuthStore) error {
	path := authFilePath()
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	return fileutil.WriteFileAtomic(path, data, 0o600)
}

func GetCredential(provider string) (*AuthCredential, error) {
	store, err := LoadStore()
	if err != nil {
		return nil, err
	}
	cred, ok := store.Credentials[canonicalProvider(provider)]
	if !ok {
		return nil, nil
	}
	return cred, nil
}

// UpdateCredential runs update while holding the cross-process auth.json lock.
// Token refresh callbacks use this to serialize refresh-token rotation with
// logins, logouts, and other gateway processes. The callback may read the auth
// store, but must not call any auth-store mutator.
func UpdateCredential(
	ctx context.Context,
	provider string,
	update func(*AuthCredential) (*AuthCredential, error),
) (*AuthCredential, error) {
	if update == nil {
		return nil, fmt.Errorf("credential update callback is nil")
	}
	var result *AuthCredential
	err := withAuthStoreFileLock(ctx, func() error {
		storeMu.Lock()
		store, err := loadStore()
		if err != nil {
			storeMu.Unlock()
			return err
		}
		canonical := canonicalProvider(provider)
		current := cloneCredential(store.Credentials[canonical])
		storeMu.Unlock()

		// Do not hold storeMu during the callback: a token refresh performs
		// network I/O, and unrelated credential reads should remain available.
		// Compliant writers cannot enter because they acquire the file lock
		// before storeMu.
		updated, err := update(current)
		if err != nil {
			return err
		}
		if sameCredential(updated, store.Credentials[canonical]) {
			result = cloneCredential(updated)
			return nil
		}

		storeMu.Lock()
		defer storeMu.Unlock()
		normalized := cloneCredential(updated)
		if normalized != nil {
			normalized.Provider = canonicalProvider(normalized.Provider)
			if normalized.Provider == "" {
				normalized.Provider = canonical
			}
		}
		store.Credentials[canonical] = normalized
		if err := saveStore(store); err != nil {
			return err
		}
		result = cloneCredential(normalized)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func SetCredential(provider string, cred *AuthCredential) error {
	return withAuthStoreFileLock(context.Background(), func() error {
		storeMu.Lock()
		defer storeMu.Unlock()

		store, err := loadStore()
		if err != nil {
			return err
		}

		canonical := canonicalProvider(provider)
		normalized := cloneCredential(cred)
		if normalized != nil {
			normalized.Provider = canonicalProvider(normalized.Provider)
			if normalized.Provider == "" {
				normalized.Provider = canonical
			}
		}

		store.Credentials[canonical] = normalized
		return saveStore(store)
	})
}

func DeleteCredential(provider string) error {
	return withAuthStoreFileLock(context.Background(), func() error {
		storeMu.Lock()
		defer storeMu.Unlock()

		store, err := loadStore()
		if err != nil {
			return err
		}
		delete(store.Credentials, canonicalProvider(provider))
		return saveStore(store)
	})
}

func DeleteAllCredentials() error {
	return withAuthStoreFileLock(context.Background(), func() error {
		storeMu.Lock()
		defer storeMu.Unlock()

		path := authFilePath()
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	})
}
