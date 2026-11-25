package common

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const maxGeminiRefExpansions = 256

var geminiJSONSchemaAPIVersions = map[string]struct{}{
	"v1":      {},
	"v1beta":  {},
	"v1alpha": {},
}

// GeminiAcceptsParametersJSONSchema reports whether the endpoint exposes
// FunctionDeclaration.parametersJsonSchema, the field that accepts a full JSON
// Schema instead of the conservative OpenAPI Schema subset used by the legacy
// parameters field.
//
// Google AI Studio honors the field on generativelanguage.googleapis.com for
// the v1, v1beta and v1alpha surfaces. Vertex AI and unknown proxies keep the
// legacy field.
func GeminiAcceptsParametersJSONSchema(baseURL string) bool {
	parsed, ok := parseGeminiBaseURL(baseURL)
	if !ok {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), "generativelanguage.googleapis.com") {
		return false
	}

	path := strings.Trim(parsed.Path, "/")
	if path == "" {
		return false
	}
	version := strings.ToLower(strings.SplitN(path, "/", 2)[0])
	_, ok = geminiJSONSchemaAPIVersions[version]
	return ok
}

func parseGeminiBaseURL(raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, false
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return nil, false
	}
	return parsed, true
}

// PrepareSchemaForGeminiJSONSchema returns a deep-copied tool parameter schema
// suitable for the parametersJsonSchema field: the root $schema keyword is
// dropped, same-document $refs are inlined, unused $defs/definitions are
// removed, and an object root is guaranteed.
//
// A circular, unresolvable or budget-exhausting reference makes the function
// return the schema as written (references intact) together with an error, so
// the provider can log it and Google can report the real problem instead of
// silently receiving a truncated tool definition.
func PrepareSchemaForGeminiJSONSchema(schema map[string]any) (map[string]any, error) {
	if len(schema) == 0 {
		return emptyGeminiObjectSchema(), nil
	}

	prepared := cloneGeminiSchemaMap(schema)
	if len(prepared) == 0 {
		return emptyGeminiObjectSchema(), nil
	}
	delete(prepared, "$schema")

	budget := maxGeminiRefExpansions
	inlined, err := inlineGeminiSchemaRefs(prepared, prepared, &budget, nil)
	if err != nil {
		return prepared, fmt.Errorf("inline same-document $ref: %w", err)
	}

	result, ok := inlined.(map[string]any)
	if !ok || len(result) == 0 {
		return emptyGeminiObjectSchema(), nil
	}

	delete(result, "$defs")
	delete(result, "definitions")

	if _, ok := result["properties"]; !ok {
		result["properties"] = map[string]any{}
	}
	if _, ok := result["type"]; !ok {
		result["type"] = "object"
	}
	return result, nil
}

func emptyGeminiObjectSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func inlineGeminiSchemaRefs(
	node any,
	root map[string]any,
	budget *int,
	stack map[string]struct{},
) (any, error) {
	switch value := node.(type) {
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			inlined, err := inlineGeminiSchemaRefs(item, root, budget, stack)
			if err != nil {
				return nil, err
			}
			out[i] = inlined
		}
		return out, nil

	case map[string]any:
		ref, hasRef := value["$ref"].(string)
		if !hasRef {
			out := make(map[string]any, len(value))
			for key, item := range value {
				inlined, err := inlineGeminiSchemaRefs(item, root, budget, stack)
				if err != nil {
					return nil, err
				}
				out[key] = inlined
			}
			return out, nil
		}

		if _, seen := stack[ref]; seen {
			return nil, fmt.Errorf("circular $ref %q", ref)
		}
		*budget--
		if *budget < 0 {
			return nil, errors.New("$ref expansion budget exhausted")
		}

		target, ok := resolveGeminiSchemaRef(root, ref)
		if !ok {
			return nil, fmt.Errorf("unresolvable $ref %q", ref)
		}

		nextStack := cloneRefTrail(stack)
		nextStack[ref] = struct{}{}
		inlinedTarget, err := inlineGeminiSchemaRefs(target, root, budget, nextStack)
		if err != nil {
			return nil, err
		}
		inlinedMap, ok := inlinedTarget.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref %q does not resolve to an object schema", ref)
		}

		// JSON Schema: siblings of $ref apply alongside the referenced schema,
		// and take precedence over it.
		out := cloneGeminiSchemaMap(inlinedMap)
		if out == nil {
			out = make(map[string]any, len(value))
		}
		for key, item := range value {
			if key == "$ref" {
				continue
			}
			inlined, err := inlineGeminiSchemaRefs(item, root, budget, stack)
			if err != nil {
				return nil, err
			}
			out[key] = inlined
		}
		return out, nil

	default:
		return node, nil
	}
}
