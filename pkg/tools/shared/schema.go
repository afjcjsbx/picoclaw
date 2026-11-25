package toolshared

import (
	"encoding/json"
	"sort"
	"strings"
)

// EmptyObjectSchema returns the minimal JSON object schema accepted by model
// providers when a tool exposes no input schema.
func EmptyObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
		"required":   []string{},
	}
}

// SchemaMap decodes an MCP tool input schema into a JSON Schema object. It
// accepts an already decoded map directly and falls back to unmarshalling
// json.RawMessage, []byte or any other JSON-marshalable value. A nil or
// undecodable schema yields an empty map.
func SchemaMap(schema any) map[string]any {
	if schema == nil {
		return map[string]any{}
	}
	if schemaMap, ok := schema.(map[string]any); ok {
		return schemaMap
	}

	var jsonData []byte
	switch raw := schema.(type) {
	case json.RawMessage:
		jsonData = raw
	case []byte:
		jsonData = raw
	default:
		var err error
		jsonData, err = json.Marshal(schema)
		if err != nil {
			return map[string]any{}
		}
	}

	var result map[string]any
	if err := json.Unmarshal(jsonData, &result); err != nil {
		return map[string]any{}
	}
	return result
}

// SchemaProperty describes one property of an object schema.
type SchemaProperty struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

// SchemaProperties extracts the sorted properties of an object schema. It
// returns nil when the schema has no object properties.
func SchemaProperties(schema any) []SchemaProperty {
	schemaMap := SchemaMap(schema)
	properties, ok := schemaMap["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		return nil
	}

	required := make(map[string]struct{})
	switch raw := schemaMap["required"].(type) {
	case []string:
		for _, name := range raw {
			required[name] = struct{}{}
		}
	case []any:
		for _, value := range raw {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}

	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	sort.Strings(names)

	params := make([]SchemaProperty, 0, len(names))
	for _, name := range names {
		param := SchemaProperty{Name: name}
		if propMap, ok := properties[name].(map[string]any); ok {
			if typeName, ok := propMap["type"].(string); ok {
				param.Type = strings.TrimSpace(typeName)
			}
			if desc, ok := propMap["description"].(string); ok {
				param.Description = strings.TrimSpace(desc)
			}
		}
		_, param.Required = required[name]
		params = append(params, param)
	}
	return params
}
