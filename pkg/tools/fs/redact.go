package fstools

import "github.com/sipeed/picoclaw/pkg/redact"

// redactedMarker is the placeholder used by the shared credential redactor
// (see pkg/redact). Tool output uses the same marker for consistency.
const redactedMarker = redact.Marker

// redactSecrets masks credential-looking values in content rendered to the
// model (search results and file reads), so that secrets found in files do not
// reach the model even when they are not part of the configured sensitive
// values covered by the pipeline filter. It only masks well-known formats and
// high-entropy tokens: the looser assignment rules of redact.Text would mangle
// source code the model has to read and edit.
func redactSecrets(text string) string {
	return redact.Formats(text)
}
