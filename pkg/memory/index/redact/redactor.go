package redact

// Redactor masks sensitive data in text before it is indexed.
type Redactor interface {
	// Redact returns a copy of text with sensitive data replaced by
	// placeholder tokens. Must be idempotent: Redact(Redact(x)) == Redact(x).
	Redact(text string) string
}

// ChainRedactor applies multiple redactors in sequence.
type ChainRedactor struct {
	Redactors []Redactor
}

// Redact applies all chained redactors in order.
func (c *ChainRedactor) Redact(text string) string {
	for _, r := range c.Redactors {
		text = r.Redact(text)
	}
	return text
}
