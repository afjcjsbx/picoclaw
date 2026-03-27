// Package redact provides pluggable sensitive data redaction for the memory
// indexing system. It is used to mask secrets in session file content before
// indexing.
//
// # Redactor Interface
//
// The [Redactor] interface has a single method: Redact(text) string.
// Implementations must be idempotent: Redact(Redact(x)) == Redact(x).
//
// # Built-in Redactor
//
// [SecretRedactor] detects and masks common secret patterns:
//   - API keys (OpenAI sk-*, AWS AKIA*, GitHub ghp_*, etc.)
//   - Bearer tokens
//   - Password/secret assignments
//   - High-entropy strings (>4.5 bits/char) near secret identifiers
//
// Replacement tokens follow the format [REDACTED:<TYPE>] where TYPE is
// API_KEY, BEARER_TOKEN, PASSWORD, CREDENTIAL, or SECRET.
//
// # Composition
//
// [ChainRedactor] applies multiple redactors in sequence, allowing custom
// redactors to be combined with the built-in SecretRedactor.
package redact
