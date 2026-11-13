// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package redact

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

func TestTextMasksSecretsWithJSONSpecialCharacters(t *testing.T) {
	secrets := []string{`pa&ss<word>1234`, `quo"te\back-slash-99`}
	SetSecrets(secrets)
	t.Cleanup(func() { SetSecrets(nil) })

	if Registered() != len(secrets) {
		t.Fatalf("Registered() = %d, want %d: escaped forms must not count", Registered(), len(secrets))
	}
	for _, secret := range secrets {
		raw, err := json.Marshal(map[string]string{"detail": "echoed " + secret})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("test needs a secret that JSON escapes, got %s", raw)
		}
		got := Text(string(raw))
		if !strings.Contains(got, Marker) {
			t.Errorf("escaped secret %q was not masked in %s: %s", secret, raw, got)
		}
		if !json.Valid([]byte(got)) {
			t.Errorf("masking must keep marshaled JSON valid: %s", got)
		}
		if plain := Text("echoed " + secret); strings.Contains(plain, secret) {
			t.Errorf("raw form of %q must still be masked: %s", secret, plain)
		}
	}
}

func TestTextMasksRegisteredSecretRegardlessOfFormat(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	got := Text("provider echoed " + key + " twice: " + key)
	if strings.Contains(got, key) {
		t.Fatalf("registered secret leaked: %q", got)
	}
	if strings.Count(got, Marker) != 2 {
		t.Fatalf("expected two masked values, got %q", got)
	}
}

func TestSetSecretsReplacesPreviousValues(t *testing.T) {
	const oldKey = "old_secret_value_123456"
	const newKey = "new_secret_value_654321"
	SetSecrets([]string{oldKey})
	t.Cleanup(func() { SetSecrets(nil) })
	SetSecrets([]string{newKey})

	if got := Text(oldKey); got != oldKey {
		t.Fatalf("stale secret must not stay registered: %q", got)
	}
	if got := Text(newKey); !strings.Contains(got, Marker) {
		t.Fatalf("new secret must be masked: %q", got)
	}
}

func TestSetSecretsIgnoresShortValues(t *testing.T) {
	SetSecrets([]string{"abc"})
	t.Cleanup(func() { SetSecrets(nil) })

	if Registered() != 0 {
		t.Fatalf("Registered() = %d, want 0", Registered())
	}
	if got := Text("abc is ordinary text"); got != "abc is ordinary text" {
		t.Fatalf("short value must not be registered: %q", got)
	}
}

func TestSetSecretsIgnoresBooleanLikeValues(t *testing.T) {
	SetSecrets([]string{"true", "false", "oauth"})
	t.Cleanup(func() { SetSecrets(nil) })

	if Registered() != 0 {
		t.Fatalf("Registered() = %d, want 0", Registered())
	}
	if got := Text("enabled=true auth=oauth"); got != "enabled=true auth=oauth" {
		t.Fatalf("boolean-like values must not be registered: %q", got)
	}
}

func TestSecretsReplacesOnlyRegisteredValues(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	got := Secrets("registered " + key + " but not sk-abcdefghijklmnopqrstuvwx")
	if strings.Contains(got, key) {
		t.Fatalf("registered secret leaked: %q", got)
	}
	if !strings.Contains(got, "sk-abcdefghijklmnopqrstuvwx") {
		t.Fatalf("Secrets must not run heuristic detection: %q", got)
	}
}

func TestTextMasksKnownTokenFormats(t *testing.T) {
	cases := []string{
		"Authorization: Bearer 8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo",
		"github token ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"google key AIzaSyA1234567890abcdefghijklmnopqrstuv",
		"opencode key oc_sk_test_key_1234567890",
	}
	for _, tc := range cases {
		if got := Text(tc); !strings.Contains(got, Marker) {
			t.Errorf("expected %s in %q", Marker, got)
		}
	}
}

func TestTextMasksURLCredentials(t *testing.T) {
	cases := []string{
		"https://example.test/v1/models?key=short",
		"https://example.test/cb?code=oauth-code&state=x",
		"https://bucket.example.test/object?X-Amz-Signature=deadbeef&X-Amz-Credential=abc",
	}
	for _, tc := range cases {
		got := Text(tc)
		if !strings.Contains(got, Marker) {
			t.Errorf("expected %s in %q", Marker, got)
		}
	}
	if got := Text("https://example.test/docs?page=2"); got != "https://example.test/docs?page=2" {
		t.Errorf("ordinary query must stay intact: %q", got)
	}
}

func TestTextMasksCredentialHeadersAndAssignments(t *testing.T) {
	cases := []string{
		"X-Api-Key: shortkey123",
		"x-goog-api-key: googlevalue",
		"client_secret=abcdefgh",
		"OPENAI_API_KEY=shortkey123",
		"password: hunter2!!",
	}
	for _, tc := range cases {
		if got := Text(tc); !strings.Contains(got, Marker) {
			t.Errorf("expected %s in %q", Marker, got)
		}
	}
	if got := Text("api_key_rotation_policy: monthly"); got != "api_key_rotation_policy: monthly" {
		t.Errorf("configuration identifiers must stay intact: %q", got)
	}
}

func TestTextMasksBasicAuthURLPasswords(t *testing.T) {
	got := Text("proxy https://user:super-secret@example.test/path failed")
	if strings.Contains(got, "super-secret") {
		t.Fatalf("basic-auth password leaked: %q", got)
	}
	if !strings.Contains(got, Marker) {
		t.Fatalf("expected %s: %q", Marker, got)
	}
}

func TestTextKeepsOrdinaryText(t *testing.T) {
	cases := []string{
		`SomeVeryLongCamelCaseIdentifierNameXYZ1234`,
		`commit e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`,
		`https://github.com/sipeed/picoclaw/blob/main/README.md`,
	}
	for _, line := range cases {
		if got := Text(line); got != line {
			t.Errorf("line must be left unchanged:\n in: %q\nout: %q", line, got)
		}
	}
}

func TestFormatsSkipsLooseRulesAndRegisteredValues(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	SetSecrets([]string{"registered-value-1234"})
	t.Cleanup(func() { SetSecrets(nil) })

	unchanged := []string{
		`password = getPassword()`,
		`token = os.Getenv("TOKEN")`,
		`secret: db-credentials`,
		`the basic responsibilities of the module`,
		`https://example.test/cb?code=abc&key=main`,
		`proxy https://user:super-secret@example.test/path`,
		`X-Api-Key: shortkey123`,
		`registered-value-1234`,
	}
	for _, in := range unchanged {
		if got := Formats(in); got != in {
			t.Errorf("Formats must leave %q unchanged, got %q", in, got)
		}
	}

	masked := []string{
		"Authorization: Bearer 8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo",
		"github ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789",
		"opencode " + key,
		"value 8fJd92kLmQ73xPqR5tNvBw6YcAeZ1sUo2dF5gH7jK9lM",
	}
	for _, in := range masked {
		if got := Formats(in); !strings.Contains(got, Marker) {
			t.Errorf("Formats must mask %q, got %q", in, got)
		}
	}
	got := Formats("first\nOPENAI_API_KEY=sk-proj-4eC39HqLyjWDarjtT1zdp7dc\nlast")
	if !strings.Contains(got, Marker) || !strings.HasPrefix(got, "first\n") || !strings.HasSuffix(got, "\nlast") {
		t.Errorf("Formats must mask per line and keep the others: %q", got)
	}
}

func TestTextMasksMultilineSecrets(t *testing.T) {
	const key = "oc_sk_test_key_1234567890"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	got := Text("first line clean\nsecond line " + key + "\nthird line clean")
	if strings.Contains(got, key) {
		t.Fatalf("multiline secret leaked: %q", got)
	}
}

// TestTextCleanLinesDoNotAllocate pins the cheap-path guarantee: log lines and
// chat text with nothing credential-shaped must cost no allocation, because
// every log call and outbound message goes through Text. regexp.ReplaceAllString
// copies its input even without a match, so each rule needs a prefilter.
func TestTextCleanLinesDoNotAllocate(t *testing.T) {
	SetSecrets(nil)
	lines := []string{
		"starting gateway on port 18790",
		"loaded 12 tools for agent main",
		"https://github.com/sipeed/picoclaw/blob/main/README.md",
		"is this a question? maybe & maybe not",
		"see /usr/local/share/picoclaw/some/very/long/path/that/is/not/a/real/file/at/all.txt",
		"retry_policy: exponential-backoff-with-jitter-enabled-by-default",
	}
	for _, in := range lines {
		if got := Text(in); got != in {
			t.Fatalf("clean line changed: %q -> %q", in, got)
		}
		if allocs := testing.AllocsPerRun(50, func() { _ = Text(in) }); allocs != 0 {
			t.Errorf("Text(%q) allocates %v times, want 0", in, allocs)
		}
	}
}

// TestTextPrefiltersStillReachTheirRules makes sure the guards in front of the
// URL and entropy rules never skip a line those rules should mask.
func TestTextPrefiltersStillReachTheirRules(t *testing.T) {
	SetSecrets(nil)
	longRun := "kZ8x3PqR7tYv2wBc5nMf9jLh4gD1sA6eUoQpXmWz"
	cases := map[string]string{
		"query after question mark": "GET /v1/models?key=short",
		"query after ampersand":     "GET /v1/models?page=2&token=abcd",
		"basic auth url":            "proxy https://user:super-secret@example.test/path failed",
		"entropy run at end":        "value " + longRun,
		"entropy run at start":      longRun + " trailing",
	}
	for name, in := range cases {
		if got := Text(in); !strings.Contains(got, Marker) {
			t.Errorf("%s: %q was not masked: %q", name, in, got)
		}
	}
}

// TestOpaqueRunPrefilterMatchesUnfilteredRule checks the prefilter against the
// rule it guards on random lines: skipping the regexp must never change the
// result. Lines mix every token character with occasional separators and an
// optional secret-like word, so a good share of them really get masked.
func TestOpaqueRunPrefilterMatchesUnfilteredRule(t *testing.T) {
	const tokenChars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+=_-/"
	rng := rand.New(rand.NewSource(1))
	masked := 0
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		if rng.Intn(2) == 0 {
			b.WriteString("token ")
		}
		for i, size := 0, rng.Intn(120); i < size; i++ {
			if rng.Intn(40) == 0 {
				b.WriteByte(" .:"[rng.Intn(3)])
				continue
			}
			b.WriteByte(tokenChars[rng.Intn(len(tokenChars))])
		}
		in := b.String()
		want := maskOpaqueRuns(in)
		if got := redactHighEntropyRuns(in); got != want {
			t.Fatalf("prefilter changed the result for %q:\n got: %q\nwant: %q", in, got, want)
		}
		if want != in {
			masked++
		}
	}
	if masked < 500 {
		t.Fatalf("only %d of 20000 random lines were masked; the test no longer exercises the rule", masked)
	}
}

// TestSecretsDoesNotAllocateWithoutAHit pins the streaming cost. Every streamed
// update passes the whole accumulated answer through Secrets, and
// strings.Replacer copies its input even when nothing matches, so text with no
// registered value must go through without a single allocation.
func TestSecretsDoesNotAllocateWithoutAHit(t *testing.T) {
	SetSecrets([]string{"sk-or-v1-FAKEFAKEFAKEFAKEFAKE", "voice-key-plain-123456", `pa&ss<word>1234`})
	t.Cleanup(func() { SetSecrets(nil) })

	answer := strings.Repeat("Sure, here is how the gateway reloads its configuration. ", 70)
	for _, n := range []int{20, 400, len(answer)} {
		prefix := answer[:n]
		if got := Secrets(prefix); got != prefix {
			t.Fatalf("text without a secret changed: %q", got)
		}
		if allocs := testing.AllocsPerRun(50, func() { _ = Secrets(prefix) }); allocs != 0 {
			t.Errorf("Secrets on %d clean bytes allocates %v times, want 0", n, allocs)
		}
	}
}

// TestSecretsMatchesReplacerOnRandomText checks the substring prefilter against
// the replacer it guards: texts built from secrets, their JSON-escaped forms,
// near misses and filler must come out exactly as the replacer alone renders
// them.
func TestSecretsMatchesReplacerOnRandomText(t *testing.T) {
	secrets := []string{"sk-or-v1-FAKEFAKEFAKEFAKEFAKE", "voice-key-plain-123456", `pa&ss<word>1234`}
	SetSecrets(secrets)
	t.Cleanup(func() { SetSecrets(nil) })

	pieces := []string{
		" ", "\n", "abc", "token=", "{\"k\":\"",
		secrets[0], secrets[1], secrets[2], jsonEscaped(secrets[2]),
		secrets[0][:12], secrets[1][:10], secrets[2][:5], // near misses
	}
	rng := rand.New(rand.NewSource(2))
	hits := 0
	for n := 0; n < 5000; n++ {
		var b strings.Builder
		for i, size := 0, rng.Intn(12); i < size; i++ {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		in := b.String()
		want := registered.Load().replacer.Replace(in)
		if got := Secrets(in); got != want {
			t.Fatalf("prefilter changed the result for %q:\n got: %q\nwant: %q", in, got, want)
		}
		if want != in {
			hits++
		}
	}
	if hits < 1000 {
		t.Fatalf("only %d of 5000 random texts held a secret; the test no longer exercises the replacer", hits)
	}
}
