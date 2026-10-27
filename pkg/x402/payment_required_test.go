package x402

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

const testV2Challenge = `{
  "x402Version": 2,
  "error": "PAYMENT-SIGNATURE header is required",
  "resource": {
    "url": "https://api.example.com/premium-data",
    "description": "Access to premium market data",
    "mimeType": "application/json",
    "serviceName": "Example Market Data",
    "tags": ["market-data", "finance"],
    "iconUrl": "https://api.example.com/icon.png"
  },
  "accepts": [
    {
      "scheme": "exact",
      "network": "eip155:84532",
      "amount": "10000",
      "asset": "0x036CbD53842c5426634e7929541eC2318f3dCF7e",
      "payTo": "0x209693Bc6afc0C5328bA36FaF03C514EF312287C",
      "maxTimeoutSeconds": 60,
      "extra": {"name": "USDC", "version": "2"}
    }
  ]
}`

const testV1Challenge = `{
  "x402Version": 1,
  "error": "X-PAYMENT header is required",
  "accepts": [
    {
      "scheme": "exact",
      "network": "base-sepolia",
      "maxAmountRequired": "2500",
      "resource": "https://api.example.com/v1/data",
      "description": "v1 protected resource",
      "mimeType": "application/json",
      "payTo": "0x1111111111111111111111111111111111111111",
      "maxTimeoutSeconds": 30,
      "asset": "0x2222222222222222222222222222222222222222"
    }
  ]
}`

func TestDecodeV2Header(t *testing.T) {
	value := base64.StdEncoding.EncodeToString([]byte(testV2Challenge))

	challenge, err := Decode(value)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if challenge.Version != 2 {
		t.Errorf("Version = %d, want 2", challenge.Version)
	}
	if challenge.Resource.URL != "https://api.example.com/premium-data" {
		t.Errorf("Resource.URL = %q", challenge.Resource.URL)
	}
	if challenge.Resource.ServiceName != "Example Market Data" {
		t.Errorf("Resource.ServiceName = %q", challenge.Resource.ServiceName)
	}
	if len(challenge.Resource.Tags) != 2 {
		t.Errorf("Resource.Tags = %v", challenge.Resource.Tags)
	}
	if len(challenge.Accepts) != 1 {
		t.Fatalf("Accepts = %d, want 1", len(challenge.Accepts))
	}

	requirement := challenge.Accepts[0]
	if requirement.Scheme != "exact" {
		t.Errorf("Scheme = %q", requirement.Scheme)
	}
	if requirement.Network != "eip155:84532" {
		t.Errorf("Network = %q", requirement.Network)
	}
	if requirement.Amount != "10000" {
		t.Errorf("Amount = %q, want 10000", requirement.Amount)
	}
	if requirement.AssetLabel() != "USDC" {
		t.Errorf("AssetLabel() = %q, want USDC", requirement.AssetLabel())
	}
	if requirement.PayTo != "0x209693Bc6afc0C5328bA36FaF03C514EF312287C" {
		t.Errorf("PayTo = %q", requirement.PayTo)
	}
	if requirement.MaxTimeoutSeconds != 60 {
		t.Errorf("MaxTimeoutSeconds = %d", requirement.MaxTimeoutSeconds)
	}
	description := challenge.Describe()
	if !strings.Contains(description, "10000 atomic units of USDC") ||
		!strings.Contains(description, "on eip155:84532") ||
		!strings.Contains(description, requirement.Asset) {
		t.Errorf("Describe() = %q", description)
	}
}

func TestDecodeSpecHeaderVector(t *testing.T) {
	// Header value from the x402 v2 HTTP transport specification.
	const value = "eyJ4NDAyVmVyc2lvbiI6MiwiZXJyb3IiOiJQQVlNRU5ULVNJR05BVFVSRSBoZWFkZXIgaXMgcmVxdWlyZWQiLCJyZXNvdXJjZSI6eyJ1cmwiOiJodHRwczovL2FwaS5leGFtcGxlLmNvbS9wcmVtaXVtLWRhdGEiLCJkZXNjcmlwdGlvbiI6IkFjY2VzcyB0byBwcmVtaXVtIG1hcmtldCBkYXRhIiwibWltZVR5cGUiOiJhcHBsaWNhdGlvbi9qc29uIn0sImFjY2VwdHMiOlt7InNjaGVtZSI6ImV4YWN0IiwibmV0d29yayI6ImVpcDE1NTo4NDUzMiIsImFtb3VudCI6IjEwMDAwIiwiYXNzZXQiOiIweDAzNkNiRDUzODQyYzU0MjY2MzRlNzkyOTU0MWVDMjMxOGYzZENGN2UiLCJwYXlUbyI6IjB4MjA5NjkzQmM2YWZjMEM1MzI4YkEzNkZhRjAzQzUxNEVGMzEyMjg3QyIsIm1heFRpbWVvdXRTZWNvbmRzIjo2MCwiZXh0cmEiOnsibmFtZSI6IlVTREMiLCJ2ZXJzaW9uIjoiMiJ9fV19"

	challenge, err := Decode(value)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if challenge.Version != 2 {
		t.Errorf("Version = %d, want 2", challenge.Version)
	}
	if challenge.Resource.URL != "https://api.example.com/premium-data" {
		t.Errorf("Resource.URL = %q", challenge.Resource.URL)
	}
	if len(challenge.Accepts) != 1 {
		t.Fatalf("Accepts = %d, want 1", len(challenge.Accepts))
	}
	option := challenge.Accepts[0]
	if option.Amount != "10000" || option.Network != "eip155:84532" ||
		option.PayTo != "0x209693Bc6afc0C5328bA36FaF03C514EF312287C" {
		t.Errorf("unexpected spec option: %+v", option)
	}
	if option.AssetLabel() != "USDC" {
		t.Errorf("AssetLabel() = %q, want USDC", option.AssetLabel())
	}
}

func TestParseV1Body(t *testing.T) {
	challenge, err := Parse(nil, []byte(testV1Challenge))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if challenge == nil {
		t.Fatal("Parse() returned nil challenge")
	}
	if challenge.Version != 1 {
		t.Errorf("Version = %d, want 1", challenge.Version)
	}
	if challenge.Resource.URL != "https://api.example.com/v1/data" {
		t.Errorf("Resource.URL = %q, want the requirement resource", challenge.Resource.URL)
	}
	if challenge.Resource.Description != "v1 protected resource" {
		t.Errorf("Resource.Description = %q", challenge.Resource.Description)
	}
	if len(challenge.Accepts) != 1 {
		t.Fatalf("Accepts = %d, want 1", len(challenge.Accepts))
	}
	if challenge.Accepts[0].Amount != "2500" {
		t.Errorf("Amount = %q, want maxAmountRequired 2500", challenge.Accepts[0].Amount)
	}
}

func TestParseBodyWithUTF8BOM(t *testing.T) {
	body := append([]byte{0xEF, 0xBB, 0xBF}, []byte(testV1Challenge)...)

	challenge, err := Parse(nil, body)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if challenge == nil || challenge.Version != 1 {
		t.Fatalf("expected the BOM-prefixed v1 challenge, got %+v", challenge)
	}
}

func TestParseHeaderWinsOverBody(t *testing.T) {
	header := http.Header{
		HeaderPaymentRequired: []string{base64.StdEncoding.EncodeToString([]byte(testV2Challenge))},
	}

	challenge, err := Parse(header, []byte(testV1Challenge))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if challenge.Version != 2 {
		t.Errorf("Version = %d, want header version 2", challenge.Version)
	}
}

func TestParseFallsBackToBody(t *testing.T) {
	header := http.Header{HeaderPaymentRequired: []string{"not-a-valid-challenge"}}

	challenge, err := Parse(header, []byte(testV1Challenge))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if challenge == nil || challenge.Version != 1 {
		t.Fatalf("expected v1 body challenge, got %+v", challenge)
	}
}

func TestParseHeaderSkipsEmptyValues(t *testing.T) {
	header := http.Header{
		HeaderPaymentRequired: []string{
			"",
			base64.RawURLEncoding.EncodeToString([]byte(testV2Challenge)),
		},
	}

	challenge, err := Parse(header, nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if challenge == nil || challenge.Version != 2 {
		t.Fatalf("expected v2 challenge, got %+v", challenge)
	}
}

func TestParseMalformedHeader(t *testing.T) {
	header := http.Header{HeaderPaymentRequired: []string{"!!!"}}

	challenge, err := Parse(header, []byte("plain error page"))
	if challenge != nil {
		t.Fatalf("expected nil challenge, got %+v", challenge)
	}
	if !errors.Is(err, ErrMalformedChallenge) {
		t.Fatalf("error = %v, want ErrMalformedChallenge", err)
	}
}

func TestParseNoChallenge(t *testing.T) {
	for _, body := range [][]byte{nil, []byte(""), []byte("plain error"), []byte(`{"error":"payment required"}`)} {
		challenge, err := Parse(nil, body)
		if challenge != nil || err != nil {
			t.Errorf("Parse(nil, %q) = (%+v, %v), want (nil, nil)", body, challenge, err)
		}
	}
}

func TestParseMalformedBody(t *testing.T) {
	body := []byte(`{"x402Version":2,"accepts":`)

	challenge, err := Parse(nil, body)
	if challenge != nil {
		t.Fatalf("expected nil challenge, got %+v", challenge)
	}
	if !errors.Is(err, ErrMalformedChallenge) {
		t.Fatalf("error = %v, want ErrMalformedChallenge", err)
	}
}

func TestDecodeVariants(t *testing.T) {
	payload := []byte(testV2Challenge)
	tests := map[string]string{
		"standard padded":   base64.StdEncoding.EncodeToString(payload),
		"standard raw":      base64.RawStdEncoding.EncodeToString(payload),
		"url padded":        base64.URLEncoding.EncodeToString(payload),
		"url raw":           base64.RawURLEncoding.EncodeToString(payload),
		"raw json":          testV2Challenge,
		"quoted json":       strconv.Quote(testV2Challenge),
		"whitespace base64": " " + base64.StdEncoding.EncodeToString(payload) + "\n",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			challenge, err := Decode(value)
			if err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if challenge == nil || challenge.Version != 2 {
				t.Fatalf("expected v2 challenge, got %+v", challenge)
			}
			if challenge.Accepts[0].Amount != "10000" {
				t.Errorf("Amount = %q", challenge.Accepts[0].Amount)
			}
		})
	}
}

func TestDecodeFlexibleScalarTypes(t *testing.T) {
	payload := `{
	  "x402Version": "2",
	  "accepts": [{
	    "scheme": "exact",
	    "network": "eip155:8453",
	    "amount": 5000,
	    "asset": "USDC",
	    "payTo": "merchant",
	    "maxTimeoutSeconds": "45"
	  }]
	}`

	challenge, err := Decode(base64.StdEncoding.EncodeToString([]byte(payload)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if challenge.Version != 2 {
		t.Errorf("Version = %d, want 2", challenge.Version)
	}
	if challenge.Accepts[0].Amount != "5000" {
		t.Errorf("Amount = %q, want 5000", challenge.Accepts[0].Amount)
	}
	if challenge.Accepts[0].MaxTimeoutSeconds != 45 {
		t.Errorf("MaxTimeoutSeconds = %d, want 45", challenge.Accepts[0].MaxTimeoutSeconds)
	}
}

func TestDecodeSizeLimits(t *testing.T) {
	t.Run("encoded", func(t *testing.T) {
		_, err := Decode(strings.Repeat("A", maxEncodedChallengeBytes+1))
		if !errors.Is(err, ErrChallengeTooLarge) {
			t.Fatalf("error = %v, want ErrChallengeTooLarge", err)
		}
	})

	t.Run("raw json", func(t *testing.T) {
		payload := `{"x402Version":2,"padding":"` + strings.Repeat("A", MaxChallengeBytes) + `"}`
		_, err := Decode(payload)
		if !errors.Is(err, ErrChallengeTooLarge) {
			t.Fatalf("error = %v, want ErrChallengeTooLarge", err)
		}
	})
}

func TestDecodeRejectsNonObject(t *testing.T) {
	_, err := Decode(base64.StdEncoding.EncodeToString([]byte(`[]`)))
	if !errors.Is(err, ErrMalformedChallenge) {
		t.Fatalf("error = %v, want ErrMalformedChallenge", err)
	}
}

func TestDecodeToleratesUnknownFields(t *testing.T) {
	payload := `{
	  "x402Version": 2,
	  "unknown": {"nested": true},
	  "accepts": [{
	    "scheme": "exact",
	    "network": "eip155:8453",
	    "amount": "1",
	    "asset": "USDC",
	    "payTo": "merchant",
	    "futureField": "ignored"
	  }]
	}`

	challenge, err := Decode(base64.StdEncoding.EncodeToString([]byte(payload)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if len(challenge.Accepts) != 1 {
		t.Fatalf("Accepts = %d, want 1", len(challenge.Accepts))
	}
}

func TestDescribeSanitizesServerText(t *testing.T) {
	payload := `{
	  "x402Version": 2,
	  "error": "line one\nline two\u0000",
	  "resource": {"url": "https://example.com/x", "description": "  spaced\n description  "},
	  "accepts": [{
	    "scheme": "exact",
	    "network": "eip155:8453",
	    "amount": "10",
	    "asset": "0xabc",
	    "payTo": "0xdef",
	    "extra": {"name": "USDC\u0001"}
	  }]
	}`

	challenge, err := Decode(base64.StdEncoding.EncodeToString([]byte(payload)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	description := challenge.Describe()
	if strings.ContainsRune(description, '\x00') || strings.ContainsRune(description, '\x01') {
		t.Fatalf("Describe() kept control characters: %q", description)
	}
	if !strings.Contains(description, "line one line two") {
		t.Errorf("Describe() = %q, want sanitized server message", description)
	}
	if !strings.Contains(description, "10 atomic units of USDC") {
		t.Errorf("Describe() = %q, want sanitized asset label", description)
	}
}

func TestDecodeNeverReturnsNilChallengeWithNilError(t *testing.T) {
	inputs := []string{
		"",
		"not base64",
		base64.StdEncoding.EncodeToString([]byte(`{}`)),
		base64.StdEncoding.EncodeToString([]byte(`{"x402Version":2}`)),
		base64.StdEncoding.EncodeToString([]byte(`null`)),
	}
	for _, input := range inputs {
		challenge, err := Decode(input)
		if challenge == nil && err == nil {
			t.Fatalf("Decode(%q) returned (nil, nil)", input)
		}
	}
}

func FuzzDecode(f *testing.F) {
	f.Add("")
	f.Add("not base64")
	f.Add("!!!")
	f.Add(base64.StdEncoding.EncodeToString([]byte(testV2Challenge)))
	f.Add(base64.RawURLEncoding.EncodeToString([]byte(testV1Challenge)))
	f.Add(`{"x402Version":2,"accepts":[]}`)

	f.Fuzz(func(t *testing.T, value string) {
		challenge, err := Decode(value)
		if err != nil {
			return
		}
		if challenge == nil {
			t.Fatal("Decode() returned (nil, nil)")
		}
		if _, marshalErr := json.Marshal(challenge); marshalErr != nil {
			t.Fatalf("json.Marshal() error = %v", marshalErr)
		}
		_ = challenge.Describe()
	})
}
