package config

import (
	"encoding/json"
	"testing"
)

func TestPluginConfigPreservesDenyAllAgents(t *testing.T) {
	original := PluginsConfig{Entries: map[string]PluginEntryConfig{"deny": {Agents: []string{}}, "allow": {}}}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored PluginsConfig
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Entries["deny"].Agents == nil || restored.Entries["allow"].Agents != nil {
		t.Fatalf("lost allowlist semantics: %s", data)
	}
	if original.Parallelism() < 1 || original.InitTimeout() <= 0 || original.CallTimeout() <= 0 ||
		original.StartupWait() <= 0 {
		t.Fatal("invalid defaults")
	}
}
