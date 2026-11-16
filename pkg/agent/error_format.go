package agent

import (
	"fmt"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/redact"
)

// formatProcessingError renders err for the chat notice. Errors are machine
// text and can echo credentials, so the loose rules of redact.Text apply.
func formatProcessingError(err error) string {
	if err == nil {
		return ""
	}

	msg := fmt.Sprintf("Error processing message: %v", err)
	if kind, ok := providers.ClassifyAuthError(err); ok {
		msg = fmt.Sprintf(
			"Error processing message: %s\n\nOriginal error:\n%s",
			authErrorFriendlyMessage(kind),
			err.Error(),
		)
	}
	return redact.Text(msg)
}

func authErrorFriendlyMessage(kind providers.AuthErrorKind) string {
	switch kind {
	case providers.AuthErrorInvalidAPIKey:
		return "Authentication failed: the API key appears to be invalid. Check the API key configured for this model or provider."
	case providers.AuthErrorMissingAPIKey:
		return "Authentication failed: no API key is configured for this model or provider. Add an API key in the model settings or config."
	case providers.AuthErrorExpiredToken:
		return "Authentication failed: the saved login or token appears to be expired. Re-authenticate the provider."
	default:
		return "Authentication failed: check the API key, token, OAuth login, or provider permissions for this model."
	}
}
