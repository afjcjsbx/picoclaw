package auth

import (
	"fmt"
	"net/mail"
	"strings"
)

const gmailMailScope = "https://mail.google.com/"

// GoogleMailOAuthConfig returns the OAuth configuration used by the mail
// channel for Gmail IMAP/SMTP XOAUTH2 authentication. The caller supplies the
// client credentials from their own Google Cloud Desktop OAuth client.
func GoogleMailOAuthConfig(clientID, clientSecret string) OAuthProviderConfig {
	return OAuthProviderConfig{
		Issuer:       "https://accounts.google.com/o/oauth2/v2",
		ClientID:     strings.TrimSpace(clientID),
		ClientSecret: strings.TrimSpace(clientSecret),
		TokenURL:     "https://oauth2.googleapis.com/token",
		Scopes:       gmailMailScope + " https://www.googleapis.com/auth/userinfo.email",
		Provider:     providerGmail,
	}
}

// GmailCredentialKey namespaces Gmail credentials by account so multiple mail
// channels can use different Gmail accounts without overwriting each other.
func GmailCredentialKey(email string) (string, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(email))
	if err != nil || addr.Address == "" {
		return "", fmt.Errorf("invalid Gmail address %q", email)
	}
	return providerGmail + ":" + strings.ToLower(addr.Address), nil
}
