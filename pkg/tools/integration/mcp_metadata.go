package integrationtools

import (
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sipeed/picoclaw/pkg/logger"
	toolshared "github.com/sipeed/picoclaw/pkg/tools/shared"
)

// mcpSchemaMapKeys are JSON Schema keywords whose values map names to
// sub-schemas. The keys are part of the server's call contract and stay
// byte-exact; only the sub-schemas are cleaned.
var mcpSchemaMapKeys = map[string]struct{}{
	"properties":        {},
	"$defs":             {},
	"definitions":       {},
	"patternProperties": {},
	"dependentSchemas":  {},
}

// mcpSchemaContractKeys are JSON Schema keywords whose string values name
// something the server later matches a request against. They stay byte-exact
// so a tool call built from the cleaned schema still validates on the server;
// a hidden character there is reported instead of rewritten.
var mcpSchemaContractKeys = map[string]struct{}{
	"$ref":              {},
	"$dynamicRef":       {},
	"$recursiveRef":     {},
	"$id":               {},
	"$anchor":           {},
	"enum":              {},
	"const":             {},
	"required":          {},
	"dependentRequired": {},
	"pattern":           {},
}

// sanitizeMCPToolMetadata returns a tool whose model-facing text no longer
// carries invisible Unicode. The description and every free-text string in the
// input schema are cleaned; names and allowed values that form the call
// contract are kept byte-exact. When there is nothing to clean, the original
// tool is returned so its wire bytes (and the prompt-cache prefix built from
// them) stay unchanged.
func sanitizeMCPToolMetadata(serverName string, tool *mcp.Tool) *mcp.Tool {
	if tool == nil {
		return nil
	}

	description := toolshared.StripInvisibleUnicode(tool.Description)
	schema, schemaChanged := sanitizeMCPInputSchema(serverName, tool.Name, tool.InputSchema)
	if description == tool.Description && !schemaChanged {
		return tool
	}

	clone := *tool
	clone.Description = description
	clone.InputSchema = schema
	return &clone
}

// sanitizeMCPInputSchema cleans the free-text strings of an input schema. It
// accepts an already decoded map as well as any value that marshals to a JSON
// schema (json.RawMessage, []byte, or a typed schema value). A clean schema is
// returned unchanged so its identity and wire representation are preserved.
func sanitizeMCPInputSchema(serverName, toolName string, schema any) (any, bool) {
	if schema == nil {
		return nil, false
	}
	if schemaMap, ok := schema.(map[string]any); ok {
		return sanitizeMCPProseMap(serverName, toolName, schemaMap)
	}

	data, err := json.Marshal(schema)
	if err != nil {
		return schema, false
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return schema, false
	}
	cleaned, changed := sanitizeMCPProseMap(serverName, toolName, decoded)
	if !changed {
		return schema, false
	}
	return cleaned, true
}

// sanitizeMCPProseMap walks a schema node. It distinguishes prose (anything the
// model reads but the server does not match against a call), call-contract
// strings (names, refs, patterns, and allowed values), and instance data
// (default/examples, where every string is prose regardless of its key).
func sanitizeMCPProseMap(serverName, toolName string, node map[string]any) (map[string]any, bool) {
	changed := false
	out := make(map[string]any, len(node))

	for key, raw := range node {
		switch {
		case isMCPNameToSchemaKey(key):
			cleaned, subChanged := sanitizeMCPNameToSchemaMap(serverName, toolName, key, raw)
			out[key] = cleaned
			changed = changed || subChanged
		case isMCPContractKey(key):
			warnInvisibleMCPContractValue(serverName, toolName, key, raw)
			out[key] = raw
		case key == "dependencies":
			cleaned, subChanged := sanitizeMCPDependencies(serverName, toolName, raw)
			out[key] = cleaned
			changed = changed || subChanged
		case key == "discriminator":
			cleaned, subChanged := sanitizeMCPDiscriminator(serverName, toolName, raw)
			out[key] = cleaned
			changed = changed || subChanged
		case key == "default" || key == "examples":
			if hasInvisibleUnicodeDeep(raw) {
				out[key] = toolshared.StripInvisibleUnicodeDeep(raw)
				changed = true
			} else {
				out[key] = raw
			}
		default:
			cleaned, subChanged := sanitizeMCPProseValue(serverName, toolName, raw)
			out[key] = cleaned
			changed = changed || subChanged
		}
	}

	if !changed {
		return node, false
	}
	return out, true
}

// sanitizeMCPNameToSchemaMap walks a map whose keys are property names and
// whose values are sub-schemas. The names stay byte-exact; hidden characters in
// them are reported because a cleaned schema cannot silently rename them.
func sanitizeMCPNameToSchemaMap(
	serverName, toolName, keyword string,
	raw any,
) (any, bool) {
	names, ok := raw.(map[string]any)
	if !ok {
		return raw, false
	}

	changed := false
	out := make(map[string]any, len(names))
	for name, subSchema := range names {
		warnInvisibleMCPContractString(serverName, toolName, keyword+" name", name)
		cleaned, subChanged := sanitizeMCPProseValue(serverName, toolName, subSchema)
		out[name] = cleaned
		changed = changed || subChanged
	}
	if !changed {
		return raw, false
	}
	return out, true
}

// sanitizeMCPDependencies handles the draft-07 "dependencies" keyword. Keys and
// array values name properties and stay exact; object values are sub-schemas
// and are walked as schema.
func sanitizeMCPDependencies(serverName, toolName string, raw any) (any, bool) {
	dependencies, ok := raw.(map[string]any)
	if !ok {
		return raw, false
	}

	changed := false
	out := make(map[string]any, len(dependencies))
	for name, dependency := range dependencies {
		warnInvisibleMCPContractString(serverName, toolName, "dependencies name", name)

		switch value := dependency.(type) {
		case map[string]any:
			cleaned, subChanged := sanitizeMCPProseMap(serverName, toolName, value)
			out[name] = cleaned
			changed = changed || subChanged
		case []any:
			warnInvisibleMCPContractValue(serverName, toolName, "dependencies."+name, value)
			out[name] = value
		case []string:
			for _, property := range value {
				warnInvisibleMCPContractString(serverName, toolName, "dependencies."+name, property)
			}
			out[name] = value
		default:
			out[name] = value
		}
	}
	if !changed {
		return raw, false
	}
	return out, true
}

// sanitizeMCPDiscriminator keeps propertyName and mapping byte-exact because
// they name a property and its $ref targets, and walks any other member as
// prose.
func sanitizeMCPDiscriminator(serverName, toolName string, raw any) (any, bool) {
	discriminator, ok := raw.(map[string]any)
	if !ok {
		return raw, false
	}

	changed := false
	out := make(map[string]any, len(discriminator))
	for key, value := range discriminator {
		switch key {
		case "propertyName":
			if name, ok := value.(string); ok {
				warnInvisibleMCPContractString(serverName, toolName, "discriminator.propertyName", name)
			}
			out[key] = value
		case "mapping":
			if mapping, ok := value.(map[string]any); ok {
				for name, ref := range mapping {
					warnInvisibleMCPContractString(serverName, toolName, "discriminator.mapping name", name)
					if refString, ok := ref.(string); ok {
						warnInvisibleMCPContractString(serverName, toolName, "discriminator.mapping $ref", refString)
					}
				}
			}
			out[key] = value
		default:
			cleaned, subChanged := sanitizeMCPProseValue(serverName, toolName, value)
			out[key] = cleaned
			changed = changed || subChanged
		}
	}
	if !changed {
		return raw, false
	}
	return out, true
}

// sanitizeMCPProseValue cleans a generic schema value: strings are prose, maps
// are schema nodes, and arrays are walked element by element.
func sanitizeMCPProseValue(serverName, toolName string, value any) (any, bool) {
	switch v := value.(type) {
	case string:
		cleaned := toolshared.StripInvisibleUnicode(v)
		return cleaned, cleaned != v
	case map[string]any:
		return sanitizeMCPProseMap(serverName, toolName, v)
	case []any:
		changed := false
		out := make([]any, len(v))
		for i, item := range v {
			cleaned, itemChanged := sanitizeMCPProseValue(serverName, toolName, item)
			out[i] = cleaned
			changed = changed || itemChanged
		}
		if !changed {
			return v, false
		}
		return out, true
	case []string:
		changed := false
		out := make([]string, len(v))
		for i, item := range v {
			cleaned := toolshared.StripInvisibleUnicode(item)
			out[i] = cleaned
			changed = changed || cleaned != item
		}
		if !changed {
			return v, false
		}
		return out, true
	default:
		return value, false
	}
}

func isMCPNameToSchemaKey(key string) bool {
	_, ok := mcpSchemaMapKeys[key]
	return ok
}

func isMCPContractKey(key string) bool {
	_, ok := mcpSchemaContractKeys[key]
	return ok
}

func hasInvisibleUnicodeDeep(value any) bool {
	switch v := value.(type) {
	case string:
		return toolshared.HasInvisibleUnicode(v)
	case map[string]any:
		for _, item := range v {
			if hasInvisibleUnicodeDeep(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if hasInvisibleUnicodeDeep(item) {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if toolshared.HasInvisibleUnicode(item) {
				return true
			}
		}
	}
	return false
}

func warnInvisibleMCPContractValue(serverName, toolName, keyword string, value any) {
	switch v := value.(type) {
	case string:
		warnInvisibleMCPContractString(serverName, toolName, keyword, v)
	case []any:
		for _, item := range v {
			if text, ok := item.(string); ok {
				warnInvisibleMCPContractString(serverName, toolName, keyword, text)
			}
		}
	case []string:
		for _, text := range v {
			warnInvisibleMCPContractString(serverName, toolName, keyword, text)
		}
	case map[string]any:
		for name, item := range v {
			warnInvisibleMCPContractString(serverName, toolName, keyword+" name", name)
			warnInvisibleMCPContractValue(serverName, toolName, keyword+"."+name, item)
		}
	}
}

func warnInvisibleMCPContractString(serverName, toolName, field, value string) {
	if !toolshared.HasInvisibleUnicode(value) {
		return
	}
	logger.WarnCF("mcp", "MCP tool metadata carries invisible characters in a call-contract string; kept as-is",
		map[string]any{
			"server": serverName,
			"tool":   toolName,
			"field":  field,
		})
}
