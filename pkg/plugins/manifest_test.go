package plugins

import (
	"encoding/json"
	"strings"
	"testing"
)

func manifestJSON(extra map[string]any) []byte {
	obj := map[string]any{"$schema": ManifestSchema, "name": "demo.plugin"}
	for k, v := range extra {
		obj[k] = v
	}
	data, _ := json.Marshal(obj)
	return data
}

func TestManifestValidation(t *testing.T) {
	for _, test := range []struct {
		name     string
		fields   map[string]any
		invalid  bool
		warnings int
	}{
		{"minimal", nil, false, 0},
		{"unconstrained metadata", map[string]any{"version": "rolling", "homepage": "not a URL", "license": "custom"}, false, 0},
		{"unknown top level", map[string]any{"tools": true}, false, 1},
		{"extensions scalar", map[string]any{"extensions": 7}, false, 1},
		{"extensions null", map[string]any{"extensions": nil}, false, 1},
		{"foreign namespace", map[string]any{"extensions": map[string]any{"org.other": 7}}, false, 0},
		{"schema null", map[string]any{"$schema": nil}, true, 0},
		{"unsupported schema", map[string]any{"$schema": "https://example.org/plugin.json"}, true, 0},
		{"uppercase", map[string]any{"name": "Demo"}, true, 0},
		{"double dot", map[string]any{"name": "a..b"}, true, 0},
		{"double hyphen", map[string]any{"name": "a--b"}, true, 0},
		{"long name", map[string]any{"name": strings.Repeat("a", 65)}, true, 0},
		{"wrong metadata type", map[string]any{"version": 1}, true, 0},
		{"null metadata", map[string]any{"description": nil}, true, 0},
		{"author extra", map[string]any{"author": map[string]any{"other": "x"}}, true, 0},
		{"author null value", map[string]any{"author": map[string]any{"name": nil}}, true, 0},
		{"keywords null element", map[string]any{"keywords": []any{nil}}, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, diagnostics, err := ParseManifest(manifestJSON(test.fields))
			if (err != nil) != test.invalid {
				t.Fatalf("err=%v", err)
			}
			if !test.invalid && len(diagnostics) != test.warnings {
				t.Fatalf("diagnostics=%v", diagnostics)
			}
		})
	}
	for _, data := range []string{"null", "[]", "{}", "{", string(manifestJSON(nil)) + "{}"} {
		if _, _, err := ParseManifest([]byte(data)); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}
