# Contract: Redactor Interface

**Package**: `pkg/memory/index/redact`

## Redactor

Pluggable interface for sensitive data redaction before indexing.

```go
// Redactor masks sensitive data in text before it is indexed.
type Redactor interface {
    // Redact returns a copy of text with sensitive data replaced by
    // placeholder tokens (e.g., "[REDACTED:API_KEY]").
    // Must be idempotent: Redact(Redact(x)) == Redact(x).
    Redact(text string) string
}
```

## Default Implementation: SecretRedactor

Detects and masks:
- API keys: patterns like `sk-*`, `AKIA*`, `gsk_*`, `xoxb-*`, `ghp_*`
- Bearer tokens: `Bearer <token>` in any context
- Passwords: `password=<value>`, `passwd=<value>`, `secret=<value>`
- Base64-encoded credentials: high-entropy strings in credential-like contexts
- Generic high-entropy strings (>4.5 bits/char Shannon entropy) adjacent to
  key-like identifiers (`key`, `token`, `secret`, `credential`, `auth`)

Replacement format: `[REDACTED:<TYPE>]` where TYPE is one of:
`API_KEY`, `BEARER_TOKEN`, `PASSWORD`, `CREDENTIAL`, `SECRET`

## Composition

Multiple redactors can be chained:

```go
// ChainRedactor applies redactors in sequence.
type ChainRedactor struct {
    Redactors []Redactor
}

func (c *ChainRedactor) Redact(text string) string {
    for _, r := range c.Redactors {
        text = r.Redact(text)
    }
    return text
}
```
