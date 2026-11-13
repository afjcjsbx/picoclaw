// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package redact

import (
	"bytes"
	"strings"
	"testing"
)

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

func TestWriterRedactsSecretSplitAcrossWrites(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	var buf bytes.Buffer
	w := Writer(&buf)
	// Split inside the credential so the writer must carry bytes between calls.
	for _, chunk := range []string{"before ", key[:7], key[7:15], key[15:], " after"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := buf.String()
	if strings.Contains(got, key) {
		t.Fatalf("credential leaked: %q", got)
	}
	if !strings.Contains(got, Marker) {
		t.Fatalf("expected %s in %q", Marker, got)
	}
	if got != "before "+Marker+" after" {
		t.Fatalf("unexpected redacted stream: %q", got)
	}
}

func TestWriterMasksTrailingPartialSecret(t *testing.T) {
	const key = "ghp_0123456789abcdefghij"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	var buf bytes.Buffer
	w := Writer(&buf)
	if _, err := w.Write([]byte("token=" + key[:8])); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := buf.String()
	if strings.Contains(got, key[:8]) {
		t.Fatalf("partial credential leaked: %q", got)
	}
	if !strings.Contains(got, Marker) {
		t.Fatalf("expected %s in %q", Marker, got)
	}
}

func TestWriterPassesThroughWithoutSecrets(t *testing.T) {
	SetSecrets(nil)
	var buf bytes.Buffer
	w := Writer(&buf)
	for _, chunk := range []string{"plain ", "text"} {
		if _, err := w.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got, want := buf.String(), "plain text"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSafeCutAndStartKeepSecretsWhole(t *testing.T) {
	const key = "sk-live-0123456789abcdef"
	SetSecrets([]string{key})
	t.Cleanup(func() { SetSecrets(nil) })

	text := "head " + key + " tail"
	cut := strings.Index(text, key) + 5 // inside the credential
	if got := SafeCut(text, cut); got > strings.Index(text, key) {
		t.Fatalf("SafeCut(%d) = %d, want <= %d", cut, got, strings.Index(text, key))
	}
	if got := SafeStart(text, cut); got < strings.Index(text, key)+len(key) {
		t.Fatalf("SafeStart(%d) = %d, want >= %d", cut, got, strings.Index(text, key)+len(key))
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
