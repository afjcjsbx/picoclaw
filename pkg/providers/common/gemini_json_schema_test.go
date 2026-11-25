package common

import (
	"reflect"
	"testing"
)

func TestGeminiAcceptsParametersJSONSchema(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{"v1beta", "https://generativelanguage.googleapis.com/v1beta", true},
		{"v1beta trailing slash", "https://generativelanguage.googleapis.com/v1beta/", true},
		{"v1", "https://generativelanguage.googleapis.com/v1", true},
		{"v1alpha", "https://generativelanguage.googleapis.com/v1alpha", true},
		{"uppercase host", "https://GENERATIVELANGUAGE.GOOGLEAPIS.COM/v1beta", true},
		{"no scheme", "generativelanguage.googleapis.com/v1beta", true},
		{"host only", "https://generativelanguage.googleapis.com", false},
		{"vertex v1beta1", "https://aiplatform.googleapis.com/v1beta1/publishers/google", false},
		{"unknown proxy", "https://proxy.example.com/v1beta", false},
		{"empty", "", false},
		{"not a url", "://bad", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := GeminiAcceptsParametersJSONSchema(tt.baseURL); got != tt.want {
				t.Fatalf("GeminiAcceptsParametersJSONSchema(%q) = %v, want %v", tt.baseURL, got, tt.want)
			}
		})
	}
}

func TestPrepareSchemaForGeminiJSONSchema_Empty(t *testing.T) {
	for _, schema := range []map[string]any{nil, {}} {
		got, err := PrepareSchemaForGeminiJSONSchema(schema)
		if err != nil {
			t.Fatalf("PrepareSchemaForGeminiJSONSchema() error = %v", err)
		}
		want := map[string]any{"type": "object", "properties": map[string]any{}}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestPrepareSchemaForGeminiJSONSchema_InlinesRefsAndDropsDefinitions(t *testing.T) {
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"item": map[string]any{"$ref": "#/$defs/Item"},
			"list": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/$defs/Item"},
			},
			"mode": map[string]any{"anyOf": []any{
				map[string]any{"type": "string"},
				map[string]any{
					"type":       "object",
					"properties": map[string]any{"path": map[string]any{"type": "string"}},
				},
			}},
			"note": map[string]any{"type": []any{"string", "null"}},
		},
		"required": []any{"item"},
		"$defs": map[string]any{
			"Item": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"name": map[string]any{"type": "string"},
					"qty":  map[string]any{"type": "integer", "enum": []any{1, 2, 3}},
				},
				"required": []any{"name"},
			},
		},
	}

	got, err := PrepareSchemaForGeminiJSONSchema(schema)
	if err != nil {
		t.Fatalf("PrepareSchemaForGeminiJSONSchema() error = %v", err)
	}

	if _, ok := got["$schema"]; ok {
		t.Fatalf("$schema was not dropped: %#v", got)
	}
	if _, ok := got["$defs"]; ok {
		t.Fatalf("$defs was not dropped after inlining: %#v", got)
	}
	if got["type"] != "object" {
		t.Fatalf("type = %#v, want object", got["type"])
	}
	if got["additionalProperties"] != false {
		t.Fatalf("additionalProperties = %#v, want preserved false", got["additionalProperties"])
	}

	props := got["properties"].(map[string]any)
	item := props["item"].(map[string]any)
	if item["type"] != "object" {
		t.Fatalf("item.type = %#v, want object", item["type"])
	}
	if item["additionalProperties"] != false {
		t.Fatalf("item.additionalProperties = %#v, want preserved", item["additionalProperties"])
	}
	itemProps := item["properties"].(map[string]any)
	if _, ok := itemProps["name"]; !ok {
		t.Fatalf("item.properties = %#v, want name", itemProps)
	}
	if _, ok := itemProps["qty"]; !ok {
		t.Fatalf("item.properties = %#v, want qty", itemProps)
	}
	itemRequired := item["required"].([]any)
	if !reflect.DeepEqual(itemRequired, []any{"name"}) {
		t.Fatalf("item.required = %#v, want [name]", itemRequired)
	}

	list := props["list"].(map[string]any)
	listItems := list["items"].(map[string]any)
	if listItems["type"] != "object" {
		t.Fatalf("list.items.type = %#v, want inlined object", listItems["type"])
	}

	mode := props["mode"].(map[string]any)
	if _, ok := mode["anyOf"]; !ok {
		t.Fatalf("mode.anyOf was lost: %#v", mode)
	}
	if note := props["note"].(map[string]any); !reflect.DeepEqual(note["type"], []any{"string", "null"}) {
		t.Fatalf("note.type = %#v, want type union preserved", note["type"])
	}
}

func TestPrepareSchemaForGeminiJSONSchema_RefSiblingsWin(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": map[string]any{
				"$ref":        "#/$defs/Item",
				"description": "override",
			},
		},
		"$defs": map[string]any{
			"Item": map[string]any{
				"type":        "object",
				"description": "original",
			},
		},
	}

	got, err := PrepareSchemaForGeminiJSONSchema(schema)
	if err != nil {
		t.Fatalf("PrepareSchemaForGeminiJSONSchema() error = %v", err)
	}

	item := got["properties"].(map[string]any)["item"].(map[string]any)
	if item["description"] != "override" {
		t.Fatalf("description = %#v, want sibling to win", item["description"])
	}
}

func TestPrepareSchemaForGeminiJSONSchema_KeepsSchemaOnUnresolvableRef(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"item": map[string]any{"$ref": "#/$defs/Missing"},
		},
	}

	got, err := PrepareSchemaForGeminiJSONSchema(schema)
	if err == nil {
		t.Fatal("PrepareSchemaForGeminiJSONSchema() error = nil, want error")
	}
	if _, ok := got["$defs"]; ok {
		t.Fatalf("$defs should not be added: %#v", got)
	}
	item := got["properties"].(map[string]any)["item"].(map[string]any)
	if item["$ref"] != "#/$defs/Missing" {
		t.Fatalf("item = %#v, want original $ref preserved", item)
	}
}

func TestPrepareSchemaForGeminiJSONSchema_KeepsSchemaOnCircularRef(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"node": map[string]any{"$ref": "#/$defs/Node"},
		},
		"$defs": map[string]any{
			"Node": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"child": map[string]any{"$ref": "#/$defs/Node"},
				},
			},
		},
	}

	got, err := PrepareSchemaForGeminiJSONSchema(schema)
	if err == nil {
		t.Fatal("PrepareSchemaForGeminiJSONSchema() error = nil, want circular error")
	}
	if _, ok := got["$defs"]; !ok {
		t.Fatalf("$defs should be preserved on failure: %#v", got)
	}
	node := got["properties"].(map[string]any)["node"].(map[string]any)
	if node["$ref"] != "#/$defs/Node" {
		t.Fatalf("node = %#v, want original $ref preserved", node)
	}
}

func TestPrepareSchemaForGeminiJSONSchema_DoesNotMutateInput(t *testing.T) {
	schema := map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"type":    "object",
		"properties": map[string]any{
			"item": map[string]any{"$ref": "#/$defs/Item"},
		},
		"$defs": map[string]any{
			"Item": map[string]any{
				"type":       "object",
				"properties": map[string]any{"name": map[string]any{"type": "string"}},
			},
		},
	}
	before := cloneGeminiSchemaMap(schema)

	if _, err := PrepareSchemaForGeminiJSONSchema(schema); err != nil {
		t.Fatalf("PrepareSchemaForGeminiJSONSchema() error = %v", err)
	}

	if !reflect.DeepEqual(schema, before) {
		t.Fatalf("input schema mutated: got %#v, want %#v", schema, before)
	}
}

func TestPrepareSchemaForGeminiJSONSchema_AddsObjectRoot(t *testing.T) {
	got, err := PrepareSchemaForGeminiJSONSchema(map[string]any{
		"properties": map[string]any{
			"name": map[string]any{"type": "string"},
		},
	})
	if err != nil {
		t.Fatalf("PrepareSchemaForGeminiJSONSchema() error = %v", err)
	}
	if got["type"] != "object" {
		t.Fatalf("type = %#v, want object", got["type"])
	}
}
