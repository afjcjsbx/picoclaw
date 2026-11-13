// PicoClaw - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package redact

import (
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
