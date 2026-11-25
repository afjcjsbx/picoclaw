package mail

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/auth"
)

// mailOAuthConfig is a variable so tests can point token refresh at a local
// server without changing the production Google endpoint.
var mailOAuthConfig = auth.GoogleMailOAuthConfig

type managedTokenSource struct {
	provider string
}

func newManagedTokenSource(provider string) (*managedTokenSource, error) {
	source := &managedTokenSource{provider: strings.ToLower(strings.TrimSpace(provider))}
	if source.provider == "" {
		return nil, fmt.Errorf("mail oauth_provider is required")
	}
	if _, err := source.credential(); err != nil {
		return nil, err
	}
	return source, nil
}

func (s *managedTokenSource) credential() (*auth.AuthCredential, error) {
	cred, err := auth.GetCredential(s.provider)
	if err != nil {
		return nil, fmt.Errorf("load mail OAuth credential %q: %w", s.provider, err)
	}
	if err := validateManagedCredential(s.provider, cred); err != nil {
		return nil, err
	}
	return cred, nil
}

func validateManagedCredential(provider string, cred *auth.AuthCredential) error {
	if cred == nil {
		return fmt.Errorf(
			"mail OAuth credential %q not found; run: picoclaw auth login --provider gmail",
			provider,
		)
	}
	if cred.RefreshToken == "" {
		return fmt.Errorf(
			"mail OAuth credential %q has no refresh token; run: picoclaw auth login --provider gmail",
			provider,
		)
	}
	if cred.ClientID == "" {
		return fmt.Errorf(
			"mail OAuth credential %q has no client_id; run: picoclaw auth login --provider gmail",
			provider,
		)
	}
	return nil
}

// Token returns a valid access token, refreshing and persisting it shortly
// before expiry. UpdateCredential holds the auth store's cross-process lock
// across the refresh and save, so rotating refresh tokens cannot race a login,
// logout, or another gateway process.
func (s *managedTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	update := func(current *auth.AuthCredential) (*auth.AuthCredential, error) {
		if err := validateManagedCredential(s.provider, current); err != nil {
			return nil, err
		}
		if !current.ExpiresAt.IsZero() && !current.NeedsRefresh() {
			return current, nil
		}

		cfg := mailOAuthConfig(current.ClientID, current.ClientSecret)
		refreshed, err := auth.RefreshAccessTokenContext(ctx, current, cfg)
		if err != nil {
			if auth.IsOAuthTokenError(err, "invalid_grant") {
				return nil, fmt.Errorf(
					"Gmail authorization expired or was revoked; run: picoclaw auth login --provider gmail: %w",
					err,
				)
			}
			return nil, fmt.Errorf("refresh Gmail OAuth token: %w", err)
		}
		return refreshed, nil
	}
	cred, err := auth.UpdateCredential(ctx, s.provider, update)
	if err != nil {
		return "", err
	}
	if cred.AccessToken == "" {
		return "", fmt.Errorf("mail OAuth credential %q has no access token", s.provider)
	}
	return cred.AccessToken, nil
}
