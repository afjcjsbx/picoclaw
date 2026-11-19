package toolshared

import (
	"reflect"
	"testing"
)

func TestStripInvisibleUnicode(t *testing.T) {
	const (
		tagChar   = "\U000E0073" // TAG LATIN SMALL LETTER S
		bidiRLO   = "\u202E"
		bidiLRI   = "\u2066"
		familyZWJ = "\U0001F468\u200D\U0001F469\u200D\U0001F467"
	)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain text is returned unchanged",
			in:   "hello world",
			want: "hello world",
		},
		{
			name: "TAG characters are removed",
			in:   "send " + tagChar + tagChar + "file",
			want: "send file",
		},
		{
			name: "bidi controls are removed",
			in:   "look up a record" + bidiRLO + bidiLRI,
			want: "look up a record",
		},
		{
			name: "mixed invisible characters are removed",
			in:   "safe" + bidiRLO + tagChar + " text" + bidiLRI,
			want: "safe text",
		},
		{
			name: "ZWJ emoji sequence is preserved",
			in:   familyZWJ,
			want: familyZWJ,
		},
		{
			name: "zero-width non-joiner is preserved",
			in:   "caf\u200Ce",
			want: "caf\u200Ce",
		},
		{
			name: "invisible characters only",
			in:   tagChar + bidiRLO,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StripInvisibleUnicode(tt.in); got != tt.want {
				t.Fatalf("StripInvisibleUnicode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHasInvisibleUnicode(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{name: "plain text", in: "hello", want: false},
		{name: "TAG character", in: "\U000E0073", want: true},
		{name: "bidi override", in: "\u202E", want: true},
		{name: "bidi isolate", in: "\u2066", want: true},
		{name: "ZWJ emoji", in: "\U0001F468\u200D\U0001F469", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasInvisibleUnicode(tt.in); got != tt.want {
				t.Fatalf("HasInvisibleUnicode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStripInvisibleUnicodeDeep(t *testing.T) {
	in := map[string]any{
		"description": "look up a record\u202E\u2066",
		"properties": map[string]any{
			"query": map[string]any{
				"title":   "query\U000E0071",
				"default": "send \U000E0073\U000E0065\U000E006E\U000E0064",
				"enum":    []any{"fast\u202E", "slow"},
			},
		},
		"required": []string{"query\u2066"},
		"clean":    "untouched",
	}

	got, ok := StripInvisibleUnicodeDeep(in).(map[string]any)
	if !ok {
		t.Fatalf("StripInvisibleUnicodeDeep() returned %T, want map[string]any", got)
	}

	if desc := got["description"]; desc != "look up a record" {
		t.Fatalf("description = %q, want stripped value", desc)
	}
	properties := got["properties"].(map[string]any)
	query := properties["query"].(map[string]any)
	if title := query["title"]; title != "query" {
		t.Fatalf("title = %q, want stripped value", title)
	}
	if def := query["default"]; def != "send " {
		t.Fatalf("default = %q, want stripped value", def)
	}
	enum := query["enum"].([]any)
	if enum[0] != "fast" || enum[1] != "slow" {
		t.Fatalf("enum = %#v, want stripped values", enum)
	}
	required := got["required"].([]string)
	if required[0] != "query" {
		t.Fatalf("required = %#v, want stripped value", required)
	}
	if clean := got["clean"]; clean != "untouched" {
		t.Fatalf("clean = %q, want untouched value", clean)
	}
}

func TestStripInvisibleUnicodeDeep_ReturnsSameObjectWhenClean(t *testing.T) {
	clean := map[string]any{
		"description": "a clean schema",
		"properties": map[string]any{
			"query": map[string]any{
				"type":  "string",
				"enum":  []any{"fast", "slow"},
				"notes": []string{"one", "two"},
			},
		},
	}

	got := StripInvisibleUnicodeDeep(clean)
	gotMap, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("StripInvisibleUnicodeDeep() returned %T, want map[string]any", got)
	}
	if reflect.ValueOf(gotMap).Pointer() != reflect.ValueOf(clean).Pointer() {
		t.Fatal("clean schema must be returned by identity so the prompt-cache prefix is unchanged")
	}

	gotProperties := gotMap["properties"].(map[string]any)
	cleanProperties := clean["properties"].(map[string]any)
	if reflect.ValueOf(gotProperties).Pointer() != reflect.ValueOf(cleanProperties).Pointer() {
		t.Fatal("clean nested map must be returned by identity")
	}

	gotQuery := gotProperties["query"].(map[string]any)
	cleanQuery := cleanProperties["query"].(map[string]any)
	if reflect.ValueOf(gotQuery["notes"]).Pointer() != reflect.ValueOf(cleanQuery["notes"]).Pointer() {
		t.Fatal("clean nested slice must be returned by identity")
	}
}

func TestStripInvisibleUnicodeDeep_NonContainerValues(t *testing.T) {
	if got := StripInvisibleUnicodeDeep(nil); got != nil {
		t.Fatalf("nil = %#v, want nil", got)
	}
	if got := StripInvisibleUnicodeDeep(42); got != 42 {
		t.Fatalf("int = %#v, want 42", got)
	}
	if got := StripInvisibleUnicodeDeep(true); got != true {
		t.Fatalf("bool = %#v, want true", got)
	}
}
