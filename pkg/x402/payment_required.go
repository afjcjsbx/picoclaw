// Package x402 decodes HTTP 402 Payment Required challenges used by the x402
// payment protocol.
//
// PicoClaw does not create or submit payments. This package only turns the
// server-provided challenge into a structured, size-bounded value so the agent
// and the user can understand which resource is paid and on what terms.
package x402

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
)

const (
	// HeaderPaymentRequired is the canonical HTTP header carrying a
	// base64-encoded PaymentRequired object (x402 v2).
	HeaderPaymentRequired = "Payment-Required"

	// MaxChallengeBytes bounds the decoded challenge. Real x402 challenges are
	// small; the limit keeps a hostile or misconfigured server from forcing
	// unbounded JSON parsing and context growth.
	MaxChallengeBytes = 64 << 10

	// maxEncodedChallengeBytes bounds the header value before base64 decoding.
	// It is generous enough for padded base64 of MaxChallengeBytes.
	maxEncodedChallengeBytes = 128 << 10

	maxURLLength         = 2048
	maxDescriptionLength = 512
	maxAddressLength     = 256
	maxLabelLength       = 128
	maxErrorLength       = 512
	maxTags              = 8
	maxOptionsInSummary  = 8
)

var (
	// ErrMalformedChallenge reports a challenge that was present but could not
	// be decoded into an x402 PaymentRequired object.
	ErrMalformedChallenge = errors.New("x402: malformed payment challenge")

	// ErrChallengeTooLarge reports a challenge that exceeds MaxChallengeBytes.
	ErrChallengeTooLarge = errors.New("x402: payment challenge exceeds size limit")
)

// Challenge is a PaymentRequired object normalized across x402 v1 and v2.
type Challenge struct {
	Version    int                        `json:"x402Version"`
	Error      string                     `json:"error,omitempty"`
	Resource   ResourceInfo               `json:"resource"`
	Accepts    []Requirement              `json:"accepts"`
	Extensions map[string]json.RawMessage `json:"extensions,omitempty"`
}

// ResourceInfo describes the protected resource.
type ResourceInfo struct {
	URL         string   `json:"url,omitempty"`
	Description string   `json:"description,omitempty"`
	MIMEType    string   `json:"mimeType,omitempty"`
	ServiceName string   `json:"serviceName,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	IconURL     string   `json:"iconUrl,omitempty"`
}

// Requirement is one payment option accepted by the resource server. Amount is
// normalized from v2 "amount" or v1 "maxAmountRequired" and remains in atomic
// token units because the challenge does not always advertise the token
// decimals.
type Requirement struct {
	Scheme            string                     `json:"scheme,omitempty"`
	Network           string                     `json:"network,omitempty"`
	Amount            string                     `json:"amount,omitempty"`
	Asset             string                     `json:"asset,omitempty"`
	PayTo             string                     `json:"payTo,omitempty"`
	MaxTimeoutSeconds int                        `json:"maxTimeoutSeconds,omitempty"`
	Resource          string                     `json:"resource,omitempty"`
	Description       string                     `json:"description,omitempty"`
	MIMEType          string                     `json:"mimeType,omitempty"`
	Extra             map[string]json.RawMessage `json:"extra,omitempty"`
}

// Parse extracts a challenge from an HTTP response. The canonical location is
// the base64-encoded PAYMENT-REQUIRED header; the v1 JSON response body is
// accepted as a fallback because older servers send it there.
//
// It returns (nil, nil) when neither location carries a challenge. A non-nil
// error means a challenge was present but malformed.
func Parse(header http.Header, body []byte) (*Challenge, error) {
	var headerErr error
	if value := paymentRequiredHeader(header); value != "" {
		challenge, err := Decode(value)
		if err == nil {
			return challenge, nil
		}
		headerErr = fmt.Errorf("invalid %s header: %w", HeaderPaymentRequired, err)
	}

	challenge, bodyErr := decodeBody(body)
	if challenge != nil {
		return challenge, nil
	}
	if bodyErr != nil {
		if headerErr != nil {
			return nil, fmt.Errorf("%w; %w", headerErr, bodyErr)
		}
		return nil, bodyErr
	}
	return nil, headerErr
}

// Decode decodes one PAYMENT-REQUIRED value. It accepts the standard x402
// shape (base64 JSON, padded or unpadded, standard or URL-safe) and raw JSON,
// which some servers expose for debugging.
func Decode(value string) (*Challenge, error) {
	payload, err := decodeEnvelope(value)
	if err != nil {
		return nil, err
	}
	return parseChallenge(payload)
}

// AssetLabel returns the token name advertised in extra.name (or extra.symbol),
// falling back to the asset identifier.
func (r Requirement) AssetLabel() string {
	if name := extraString(r.Extra, "name"); name != "" {
		return name
	}
	if symbol := extraString(r.Extra, "symbol"); symbol != "" {
		return symbol
	}
	return r.Asset
}

// Describe renders the payment options as short human-readable lines.
func (r Requirement) Describe() string {
	description := "amount not specified"
	if r.Amount != "" {
		description = r.Amount + " atomic units"
		if label := r.AssetLabel(); label != "" {
			description += " of " + label
			if r.Asset != "" && !strings.EqualFold(r.Asset, label) {
				description += " (asset " + r.Asset + ")"
			}
		}
	}
	if r.Network != "" {
		description += " on " + r.Network
	}
	if r.PayTo != "" {
		description += " to " + r.PayTo
	}

	details := make([]string, 0, 2)
	if r.Scheme != "" {
		details = append(details, "scheme "+r.Scheme)
	}
	if r.MaxTimeoutSeconds > 0 {
		details = append(details, fmt.Sprintf("timeout %ds", r.MaxTimeoutSeconds))
	}
	if len(details) > 0 {
		description += " (" + strings.Join(details, ", ") + ")"
	}
	return description
}

// Describe renders the challenge details without a leading "payment required"
// sentence, so callers can frame the message for their own audience.
func (c *Challenge) Describe() string {
	if c == nil {
		return ""
	}

	lines := make([]string, 0, 3+min(len(c.Accepts), maxOptionsInSummary))
	if c.Error != "" {
		lines = append(lines, "Server message: "+c.Error)
	}
	if c.Resource.Description != "" {
		lines = append(lines, "Resource: "+c.Resource.Description)
	}
	if len(c.Accepts) == 0 {
		lines = append(lines, "No payment options were provided.")
		return strings.Join(lines, "\n")
	}

	lines = append(lines, "Payment options:")
	for i, requirement := range c.Accepts {
		if i >= maxOptionsInSummary {
			lines = append(lines, fmt.Sprintf("- and %d more", len(c.Accepts)-i))
			break
		}
		lines = append(lines, "- "+requirement.Describe())
	}
	return strings.Join(lines, "\n")
}

func paymentRequiredHeader(header http.Header) string {
	for _, value := range header.Values(HeaderPaymentRequired) {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func decodeEnvelope(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "\ufeff")
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err == nil {
			value = strings.TrimSpace(unquoted)
		}
	}
	if value == "" {
		return nil, fmt.Errorf("%w: empty value", ErrMalformedChallenge)
	}
	if value[0] == '{' {
		if len(value) > MaxChallengeBytes {
			return nil, ErrChallengeTooLarge
		}
		return []byte(value), nil
	}

	compact := stripHeaderWhitespace(value)
	if len(compact) > maxEncodedChallengeBytes {
		return nil, ErrChallengeTooLarge
	}
	if compact == "" {
		return nil, fmt.Errorf("%w: empty value", ErrMalformedChallenge)
	}

	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	var lastErr error
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(compact)
		if err != nil {
			lastErr = err
			continue
		}
		if len(decoded) > MaxChallengeBytes {
			return nil, ErrChallengeTooLarge
		}
		return decoded, nil
	}
	return nil, fmt.Errorf("%w: value is neither JSON nor base64: %v", ErrMalformedChallenge, lastErr)
}

func stripHeaderWhitespace(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, char := range value {
		switch char {
		case ' ', '\t', '\r', '\n':
			continue
		default:
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func trimJSONDocument(document []byte) []byte {
	document = bytes.TrimSpace(document)
	document = bytes.TrimPrefix(document, []byte{0xEF, 0xBB, 0xBF})
	return bytes.TrimSpace(document)
}

func decodeBody(body []byte) (*Challenge, error) {
	trimmed := trimJSONDocument(body)
	if len(trimmed) == 0 || trimmed[0] != '{' || len(trimmed) > MaxChallengeBytes {
		return nil, nil
	}

	challenge, err := parseChallenge(trimmed)
	if err == nil {
		return challenge, nil
	}
	if looksLikeX402(trimmed) {
		return nil, fmt.Errorf("invalid x402 response body: %w", err)
	}
	return nil, nil
}

func looksLikeX402(body []byte) bool {
	return bytes.Contains(body, []byte(`"x402Version"`))
}

type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*f = ""
		return nil
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return err
		}
		*f = flexString(value)
		return nil
	}
	if trimmed[0] == '-' || (trimmed[0] >= '0' && trimmed[0] <= '9') {
		*f = flexString(trimmed)
		return nil
	}
	return errors.New("expected a string or number")
}

type flexInt int

func (f *flexInt) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*f = 0
		return nil
	}
	if trimmed[0] == '"' {
		if len(trimmed) < 2 || trimmed[len(trimmed)-1] != '"' {
			return errors.New("expected a closed string")
		}
		trimmed = trimmed[1 : len(trimmed)-1]
		if len(trimmed) == 0 {
			*f = 0
			return nil
		}
	}
	value, err := strconv.Atoi(string(trimmed))
	if err == nil {
		*f = flexInt(value)
		return nil
	}
	floatValue, floatErr := strconv.ParseFloat(string(trimmed), 64)
	if floatErr != nil {
		return err
	}
	if math.IsNaN(floatValue) || math.IsInf(floatValue, 0) ||
		floatValue < math.MinInt32 || floatValue > math.MaxInt32 {
		return errors.New("integer out of range")
	}
	*f = flexInt(int(floatValue))
	return nil
}

type wireChallenge struct {
	Version    flexInt                    `json:"x402Version"`
	Error      flexString                 `json:"error"`
	Resource   json.RawMessage            `json:"resource"`
	Accepts    []wireRequirement          `json:"accepts"`
	Extensions map[string]json.RawMessage `json:"extensions"`
}

type wireResourceInfo struct {
	URL         flexString   `json:"url"`
	Description flexString   `json:"description"`
	MIMEType    flexString   `json:"mimeType"`
	ServiceName flexString   `json:"serviceName"`
	Tags        []flexString `json:"tags"`
	IconURL     flexString   `json:"iconUrl"`
}

type wireRequirement struct {
	Scheme            flexString                 `json:"scheme"`
	Network           flexString                 `json:"network"`
	Amount            flexString                 `json:"amount"`
	MaxAmountRequired flexString                 `json:"maxAmountRequired"`
	Resource          json.RawMessage            `json:"resource"`
	Description       flexString                 `json:"description"`
	MIMEType          flexString                 `json:"mimeType"`
	PayTo             flexString                 `json:"payTo"`
	MaxTimeoutSeconds flexInt                    `json:"maxTimeoutSeconds"`
	Asset             flexString                 `json:"asset"`
	Extra             map[string]json.RawMessage `json:"extra"`
}

func parseChallenge(payload []byte) (*Challenge, error) {
	payload = trimJSONDocument(payload)
	if len(payload) == 0 || payload[0] != '{' {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrMalformedChallenge)
	}
	if len(payload) > MaxChallengeBytes {
		return nil, ErrChallengeTooLarge
	}

	var wire wireChallenge
	if err := json.Unmarshal(payload, &wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedChallenge, err)
	}

	challenge := &Challenge{
		Version:    int(wire.Version),
		Error:      CleanText(string(wire.Error), maxErrorLength, false),
		Resource:   parseResourceInfo(wire.Resource),
		Accepts:    make([]Requirement, 0, len(wire.Accepts)),
		Extensions: wire.Extensions,
	}
	if challenge.Version <= 0 && len(wire.Accepts) == 0 {
		return nil, fmt.Errorf("%w: missing x402Version and accepts", ErrMalformedChallenge)
	}

	for _, wireRequirement := range wire.Accepts {
		requirement := normalizeRequirement(wireRequirement)
		if requirement.empty() {
			continue
		}
		challenge.Accepts = append(challenge.Accepts, requirement)
	}
	if len(challenge.Accepts) == 0 && challenge.Version <= 0 {
		return nil, fmt.Errorf("%w: no usable payment options", ErrMalformedChallenge)
	}

	fillResourceDefaults(&challenge.Resource, challenge.Accepts)
	return challenge, nil
}

func normalizeRequirement(wire wireRequirement) Requirement {
	amount := string(wire.Amount)
	if amount == "" {
		amount = string(wire.MaxAmountRequired)
	}
	resource := parseResourceInfo(wire.Resource)
	description := string(wire.Description)
	if description == "" {
		description = resource.Description
	}
	mimeType := string(wire.MIMEType)
	if mimeType == "" {
		mimeType = resource.MIMEType
	}

	return Requirement{
		Scheme:            CleanText(string(wire.Scheme), maxLabelLength, false),
		Network:           CleanText(string(wire.Network), maxLabelLength, false),
		Amount:            CleanText(amount, maxLabelLength, false),
		Asset:             CleanText(string(wire.Asset), maxAddressLength, false),
		PayTo:             CleanText(string(wire.PayTo), maxAddressLength, false),
		MaxTimeoutSeconds: max(int(wire.MaxTimeoutSeconds), 0),
		Resource:          CleanText(resource.URL, maxURLLength, false),
		Description:       CleanText(description, maxDescriptionLength, false),
		MIMEType:          CleanText(mimeType, maxLabelLength, false),
		Extra:             wire.Extra,
	}
}

func (r Requirement) empty() bool {
	return r.Scheme == "" &&
		r.Network == "" &&
		r.Amount == "" &&
		r.Asset == "" &&
		r.PayTo == "" &&
		r.Resource == "" &&
		r.Description == ""
}

func parseResourceInfo(raw json.RawMessage) ResourceInfo {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ResourceInfo{}
	}
	if trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(trimmed, &value); err != nil {
			return ResourceInfo{}
		}
		return ResourceInfo{URL: CleanText(value, maxURLLength, false)}
	}
	if trimmed[0] != '{' {
		return ResourceInfo{}
	}

	var wire wireResourceInfo
	if err := json.Unmarshal(trimmed, &wire); err != nil {
		return ResourceInfo{}
	}
	tags := make([]string, 0, min(len(wire.Tags), maxTags))
	for _, tag := range wire.Tags {
		if len(tags) >= maxTags {
			break
		}
		if cleaned := CleanText(string(tag), maxLabelLength, false); cleaned != "" {
			tags = append(tags, cleaned)
		}
	}
	return ResourceInfo{
		URL:         CleanText(string(wire.URL), maxURLLength, false),
		Description: CleanText(string(wire.Description), maxDescriptionLength, false),
		MIMEType:    CleanText(string(wire.MIMEType), maxLabelLength, false),
		ServiceName: CleanText(string(wire.ServiceName), maxLabelLength, false),
		Tags:        tags,
		IconURL:     CleanText(string(wire.IconURL), maxURLLength, false),
	}
}

func fillResourceDefaults(resource *ResourceInfo, accepts []Requirement) {
	if resource.URL == "" {
		for _, requirement := range accepts {
			if requirement.Resource != "" {
				resource.URL = requirement.Resource
				break
			}
		}
	}
	if resource.Description == "" {
		for _, requirement := range accepts {
			if requirement.Description != "" {
				resource.Description = requirement.Description
				break
			}
		}
	}
	if resource.MIMEType == "" {
		for _, requirement := range accepts {
			if requirement.MIMEType != "" {
				resource.MIMEType = requirement.MIMEType
				break
			}
		}
	}
	for index := range accepts {
		if accepts[index].Resource == "" {
			accepts[index].Resource = resource.URL
		}
	}
}

func extraString(extra map[string]json.RawMessage, key string) string {
	raw, ok := extra[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return CleanText(value, maxLabelLength, false)
}

// CleanText sanitizes untrusted text: invalid UTF-8 and control characters are
// dropped, whitespace is collapsed, and the result is truncated to limit runes.
// With preserveNewlines each line is cleaned separately and kept on its own
// line; otherwise newlines collapse into spaces.
func CleanText(value string, limit int, preserveNewlines bool) string {
	if limit <= 0 {
		return ""
	}
	value = strings.ToValidUTF8(strings.TrimSpace(value), "")
	lines := []string{value}
	if preserveNewlines {
		lines = strings.Split(value, "\n")
	}
	for index, line := range lines {
		line = strings.Map(func(char rune) rune {
			switch {
			case char == '\n' || char == '\r' || char == '\t':
				return ' '
			case char < 0x20 || char == 0x7f:
				return -1
			default:
				return char
			}
		}, line)
		lines[index] = strings.Join(strings.Fields(line), " ")
	}
	value = strings.TrimSpace(strings.Join(lines, "\n"))
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
