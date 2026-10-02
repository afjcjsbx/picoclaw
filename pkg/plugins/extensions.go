package plugins

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

type hookSpec struct {
	Name      string            `json:"name"`
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
	Cwd       string            `json:"cwd,omitempty"`
	Observe   []string          `json:"observe,omitempty"`
	Intercept []string          `json:"intercept,omitempty"`
}

// An extension may be inline or file-based, but not both. This avoids ambiguous
// override rules. Each hook entry has an independent failure boundary.
func discoverHooks(manifest PluginManifest, pc PluginContext) ([]Hook, []Diagnostic) {
	if !pc.AllowHooks {
		return nil, nil
	}
	var result []Hook
	var diagnostics []Diagnostic
	report := func(err error) {
		diagnostics = append(diagnostics, Diagnostic{Plugin: pc.ID, Component: "hooks", Message: err.Error()})
	}
	raw, inline := manifest.Extensions[ExtensionNamespace]
	file := filepath.Join(ExtensionNamespace, "hooks.json")
	hasFile := componentExists(pc.Root, file)
	if inline && hasFile {
		report(fmt.Errorf("hooks must be configured inline or in %s, not both", file))
		return nil, diagnostics
	}
	if hasFile {
		var err error
		raw, err = ReadPackageFile(pc.Root, file)
		if err != nil {
			report(err)
			return nil, diagnostics
		}
	}
	if !inline && !hasFile {
		return nil, nil
	}
	if pc.DataDir == "" {
		report(fmt.Errorf("plugin data directory is unavailable"))
		return nil, diagnostics
	}
	obj, err := object(raw)
	if err != nil {
		report(err)
		return nil, diagnostics
	}
	for key := range obj {
		if key != "hooks" {
			report(fmt.Errorf("unknown extension field %q", key))
		}
	}
	if _, ok := obj["hooks"]; !ok {
		return nil, diagnostics
	}
	var entries []json.RawMessage
	if err := decodeField(obj["hooks"], &entries); err != nil {
		report(err)
		return nil, diagnostics
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		var spec hookSpec
		fields, err := object(entry)
		if err != nil {
			report(err)
			continue
		}
		bad := false
		for key := range fields {
			switch key {
			case "name", "command", "args", "env", "cwd", "observe", "intercept":
			default:
				report(fmt.Errorf("unknown hook field %q", key))
				bad = true
			}
		}
		if bad {
			continue
		}
		if err = json.Unmarshal(entry, &spec); err != nil {
			report(err)
			continue
		}
		if !ValidName(spec.Name) || seen[spec.Name] {
			report(fmt.Errorf("invalid or duplicate hook name %q", spec.Name))
			continue
		}
		seen[spec.Name] = true
		for _, stage := range spec.Intercept {
			switch stage {
			case "before_llm", "after_llm", "before_tool", "after_tool", "approve_tool":
			default:
				report(fmt.Errorf("unsupported interception %q", stage))
				bad = true
			}
		}
		if bad {
			continue
		}
		// Apply the same strict launch validation as portable stdio servers.
		launch := map[string]any{"type": "stdio", "command": spec.Command}
		for _, key := range []string{"args", "env", "cwd"} {
			if v, ok := fields[key]; ok {
				launch[key] = v
			}
		}
		encoded, err := json.Marshal(launch)
		if err != nil {
			report(err)
			continue
		}
		server, err := parseServer(encoded)
		if err != nil {
			report(err)
			continue
		}
		cfg, dir, env, err := server.RuntimeConfig(pc.Root, pc.DataDir)
		if err != nil {
			report(err)
			continue
		}
		result = append(
			result,
			Hook{
				Name:      pc.ID + ":" + spec.Name,
				Command:   append([]string{cfg.Command}, cfg.Args...),
				Dir:       dir,
				Env:       env,
				Observe:   spec.Observe,
				Intercept: spec.Intercept,
				Config:    pc.Config,
			},
		)
	}
	return result, diagnostics
}
