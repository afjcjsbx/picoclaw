package plugins

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// object rejects JSON null as well as arrays/scalars. Go's ordinary Unmarshal
// accepts null for many fields, which is not sufficient for this specification.
func object(data []byte) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, err
	}
	if fields == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return fields, nil
}

func decodeField(data json.RawMessage, dest any) error {
	if string(data) == "null" {
		return fmt.Errorf("null is not allowed")
	}
	return json.Unmarshal(data, dest)
}

func ValidName(name string) bool {
	if len(name) < 1 || len(name) > 64 || strings.Contains(name, "--") || strings.Contains(name, "..") {
		return false
	}
	alnum := func(c byte) bool { return c >= 'a' && c <= 'z' || c >= '0' && c <= '9' }
	if !alnum(name[0]) || !alnum(name[len(name)-1]) {
		return false
	}
	for i := range name {
		if !alnum(name[i]) && name[i] != '-' && name[i] != '.' {
			return false
		}
	}
	return true
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ParseManifest implements the official schema plus the normative non-fatal
// exceptions in sections 5.2 and 8.1. It never fetches a schema over the network.
func ParseManifest(data []byte) (PluginManifest, []Diagnostic, error) {
	var manifest PluginManifest
	fields, err := object(data)
	if err != nil {
		return manifest, nil, err
	}
	var diagnostics []Diagnostic
	stringsByName := map[string]*string{
		"$schema": &manifest.Schema, "name": &manifest.Name, "version": &manifest.Version,
		"description": &manifest.Description, "homepage": &manifest.Homepage,
		"repository": &manifest.Repository, "license": &manifest.License,
	}
	for _, key := range sortedKeys(fields) {
		raw := fields[key]
		if target, ok := stringsByName[key]; ok {
			if err := decodeField(raw, target); err != nil {
				return manifest, diagnostics, fmt.Errorf("%s: %w", key, err)
			}
			continue
		}
		switch key {
		case "author":
			author, err := object(raw)
			if err != nil {
				return manifest, diagnostics, fmt.Errorf("author: %w", err)
			}
			manifest.Author = &Author{}
			for name, value := range author {
				targets := map[string]*string{
					"name":  &manifest.Author.Name,
					"email": &manifest.Author.Email,
					"url":   &manifest.Author.URL,
				}
				target, ok := targets[name]
				if !ok {
					return manifest, diagnostics, fmt.Errorf("unknown author field %q", name)
				}
				if err := decodeField(value, target); err != nil {
					return manifest, diagnostics, fmt.Errorf("author.%s: %w", name, err)
				}
			}
		case "keywords":
			var values []json.RawMessage
			if err := decodeField(raw, &values); err != nil {
				return manifest, diagnostics, fmt.Errorf("keywords: %w", err)
			}
			for _, value := range values {
				var keyword string
				if err := decodeField(value, &keyword); err != nil {
					return manifest, diagnostics, fmt.Errorf("keyword: %w", err)
				}
				manifest.Keywords = append(manifest.Keywords, keyword)
			}
		case "extensions":
			extensions, err := object(raw)
			if err != nil {
				diagnostics = append(
					diagnostics,
					Diagnostic{Component: "manifest", Message: "ignored non-object extensions"},
				)
			} else {
				manifest.Extensions = extensions
			}
		default:
			diagnostics = append(
				diagnostics,
				Diagnostic{Component: "manifest", Message: fmt.Sprintf("ignored unknown field %q", key)},
			)
		}
	}
	if manifest.Schema != ManifestSchema {
		return manifest, diagnostics, fmt.Errorf("missing or unsupported $schema %q", manifest.Schema)
	}
	if !ValidName(manifest.Name) {
		return manifest, diagnostics, fmt.Errorf("invalid plugin name %q", manifest.Name)
	}
	return manifest, diagnostics, nil
}
