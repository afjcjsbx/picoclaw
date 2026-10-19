package toolshared

import (
	"encoding/json"
	"testing"
)

func TestSchemaMap(t *testing.T) {
	direct := map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{}}}
	if got := SchemaMap(direct); len(got) != 2 {
		t.Fatalf("SchemaMap(map) = %v, want passthrough", got)
	}

	raw := json.RawMessage(`{"type":"object","properties":{"b":{}}}`)
	got := SchemaMap(raw)
	if props, ok := got["properties"].(map[string]any); !ok || len(props) != 1 {
		t.Fatalf("SchemaMap(json.RawMessage) = %v, want decoded schema", got)
	}

	if got := SchemaMap([]byte(`{"type":"object"}`)); got["type"] != "object" {
		t.Fatalf("SchemaMap([]byte) = %v, want decoded schema", got)
	}

	type schema struct {
		Type string `json:"type"`
	}
	if got := SchemaMap(schema{Type: "object"}); got["type"] != "object" {
		t.Fatalf("SchemaMap(struct) = %v, want decoded schema", got)
	}

	if got := SchemaMap(nil); len(got) != 0 {
		t.Fatalf("SchemaMap(nil) = %v, want empty map", got)
	}
	if got := SchemaMap(json.RawMessage(`{invalid`)); len(got) != 0 {
		t.Fatalf("SchemaMap(invalid) = %v, want empty map", got)
	}
}

func TestSchemaProperties(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"zeta":  map[string]any{"type": "string", "description": " last "},
			"alpha": map[string]any{"type": "integer"},
			"empty": "not-a-map",
		},
		"required": []any{"zeta"},
	}

	got := SchemaProperties(schema)
	if len(got) != 3 {
		t.Fatalf("SchemaProperties() = %v, want 3 entries", got)
	}
	if got[0].Name != "alpha" || got[1].Name != "empty" || got[2].Name != "zeta" {
		t.Fatalf("SchemaProperties() order = %v, want sorted names", got)
	}
	if got[2].Type != "string" || got[2].Description != "last" || !got[2].Required {
		t.Fatalf("zeta property = %+v, want trimmed type/description and required", got[2])
	}
	if got[0].Required {
		t.Fatalf("alpha property = %+v, want not required", got[0])
	}

	withStringRequired := map[string]any{
		"properties": map[string]any{"a": map[string]any{"type": "boolean"}},
		"required":   []string{"a"},
	}
	if props := SchemaProperties(withStringRequired); len(props) != 1 || !props[0].Required {
		t.Fatalf("SchemaProperties(required []string) = %+v, want required entry", props)
	}

	if props := SchemaProperties(map[string]any{"type": "object"}); props != nil {
		t.Fatalf("SchemaProperties(no properties) = %v, want nil", props)
	}
	if props := SchemaProperties(nil); props != nil {
		t.Fatalf("SchemaProperties(nil) = %v, want nil", props)
	}
}

func TestEmptyObjectSchema(t *testing.T) {
	schema := EmptyObjectSchema()
	if schema["type"] != "object" {
		t.Fatalf("EmptyObjectSchema() = %v, want object type", schema)
	}
	if props, ok := schema["properties"].(map[string]any); !ok || len(props) != 0 {
		t.Fatalf("EmptyObjectSchema() = %v, want empty properties", schema)
	}
	if required, ok := schema["required"].([]string); !ok || len(required) != 0 {
		t.Fatalf("EmptyObjectSchema() = %v, want empty required list", schema)
	}
}
