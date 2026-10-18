package mcp

import (
	"reflect"
	"testing"
)

func TestParseEnvAssignments(t *testing.T) {
	env, err := parseEnvAssignments([]string{"TOKEN=abc", " SPACED =value=with=equals"})
	if err != nil {
		t.Fatalf("parseEnvAssignments() error = %v", err)
	}
	want := map[string]string{"TOKEN": "abc", "SPACED": "value=with=equals"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("parseEnvAssignments() = %v, want %v", env, want)
	}

	if _, err := parseEnvAssignments([]string{"NO_EQUALS"}); err == nil {
		t.Fatal("parseEnvAssignments(no separator) = nil, want error")
	}
	if _, err := parseEnvAssignments([]string{"=value"}); err == nil {
		t.Fatal("parseEnvAssignments(empty key) = nil, want error")
	}
	if env, err := parseEnvAssignments(nil); err != nil || env != nil {
		t.Fatalf("parseEnvAssignments(nil) = (%v, %v), want (nil, nil)", env, err)
	}
}

func TestParseHeaderAssignments(t *testing.T) {
	headers, err := parseHeaderAssignments([]string{"Authorization: Bearer x", "X-Trace=1"})
	if err != nil {
		t.Fatalf("parseHeaderAssignments() error = %v", err)
	}
	want := map[string]string{"Authorization": "Bearer x", "X-Trace": "1"}
	if !reflect.DeepEqual(headers, want) {
		t.Fatalf("parseHeaderAssignments() = %v, want %v", headers, want)
	}

	if _, err := parseHeaderAssignments([]string{"NoSeparator"}); err == nil {
		t.Fatal("parseHeaderAssignments(no separator) = nil, want error")
	}
	if _, err := parseHeaderAssignments([]string{": value"}); err == nil {
		t.Fatal("parseHeaderAssignments(empty name) = nil, want error")
	}
}

func TestParseAddArgsRejectsConflictingDeferredFlags(t *testing.T) {
	if _, _, _, err := parseAddArgs([]string{"fs", "npx", "server", "--deferred", "--no-deferred"}); err == nil {
		t.Fatal("parseAddArgs(--deferred --no-deferred) = nil, want conflict error")
	}
	opts, target, _, err := parseAddArgs([]string{"fs", "npx", "server", "--no-deferred"})
	if err != nil {
		t.Fatalf("parseAddArgs(--no-deferred) error = %v", err)
	}
	if opts.Deferred == nil || *opts.Deferred {
		t.Fatalf("opts.Deferred = %v, want explicit false", opts.Deferred)
	}
	if target.Name != "fs" || target.Command != "npx" {
		t.Fatalf("target = %+v, want fs/npx", target)
	}
}
