package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func precheckContext(t *testing.T) PluginContext {
	t.Helper()
	root, err := canonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data := filepath.Join(t.TempDir(), "plugin-data")
	if mkdirErr := os.MkdirAll(data, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	data, err = canonicalRoot(data)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "scripts", "gate.sh")
	if mkdirErr := os.MkdirAll(filepath.Dir(script), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if writeErr := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o700); writeErr != nil {
		t.Fatal(writeErr)
	}
	return PluginContext{ID: "dreaming", Root: root, DataDir: data}
}

func TestParseSchedulePrecheckResolvesLaunch(t *testing.T) {
	pc := precheckContext(t)
	raw := json.RawMessage(`{
		"name": "nightly",
		"cron": "0 3 * * *",
		"message": "run",
		"precheck": {
			"command": "./scripts/gate.sh",
			"args": ["--root", "${PLUGIN_ROOT}/scripts", "--data", "${PLUGIN_DATA}", "--job", "${PICOCLAW_CRON_JOB_ID}"],
			"cwd": "${PLUGIN_DATA}",
			"env": {"DREAM_MODE": "nightly"},
			"timeout_seconds": 120
		}
	}`)
	schedule, err := parseScheduleEntry(pc, raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if schedule.Precheck == nil {
		t.Fatal("precheck was not parsed")
	}
	precheck := schedule.Precheck
	if len(precheck.Command) != 7 || precheck.Command[0] != filepath.Join(pc.Root, "scripts", "gate.sh") {
		t.Fatalf("command = %v", precheck.Command)
	}
	if precheck.Command[2] != filepath.Join(pc.Root, "scripts") ||
		precheck.Command[4] != pc.DataDir ||
		precheck.Command[6] != "${PICOCLAW_CRON_JOB_ID}" {
		t.Fatalf("args = %v", precheck.Command[1:])
	}
	if precheck.Dir != pc.DataDir {
		t.Fatalf("dir = %q, want %q", precheck.Dir, pc.DataDir)
	}
	if precheck.Timeout != 120*time.Second {
		t.Fatalf("timeout = %v", precheck.Timeout)
	}
	if precheck.Env["DREAM_MODE"] != "nightly" {
		t.Fatalf("env = %v", precheck.Env)
	}

	decl := schedule.Declaration()
	if decl.Precheck == nil {
		t.Fatal("declaration lost the precheck")
	}
	if decl.Precheck.TimeoutMS != 120_000 {
		t.Fatalf("timeoutMs = %d", decl.Precheck.TimeoutMS)
	}
	if got := strings.Join(decl.Precheck.Command, " "); got != strings.Join(precheck.Command, " ") {
		t.Fatalf("declaration command = %q", got)
	}
	if decl.Precheck.Env["DREAM_MODE"] != "nightly" {
		t.Fatalf("declaration env = %v", decl.Precheck.Env)
	}
}

func TestParseSchedulePrecheckRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name  string
		value map[string]any
		want  string
	}{
		{
			"missing command",
			map[string]any{"args": []string{"--gate"}},
			"command must be one executable token",
		},
		{
			"unknown field",
			map[string]any{"command": "./scripts/gate.sh", "extra": true},
			"unknown precheck field",
		},
		{
			"zero timeout",
			map[string]any{"command": "./scripts/gate.sh", "timeout_seconds": 0},
			"between 1 and 600",
		},
		{
			"timeout too large",
			map[string]any{"command": "./scripts/gate.sh", "timeout_seconds": 601},
			"between 1 and 600",
		},
		{
			"cwd escapes",
			map[string]any{"command": "./scripts/gate.sh", "cwd": "../outside"},
			"invalid cwd",
		},
		{
			"absolute command",
			map[string]any{"command": "/bin/sh"},
			"command must be bare or begin with ./",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			pc := precheckContext(t)
			entry := map[string]any{
				"name": "nightly", "cron": "0 3 * * *", "message": "run",
				"precheck": test.value,
			}
			raw, err := json.Marshal(entry)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseScheduleEntry(pc, raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseSchedulePrecheckRequiresDataDirectory(t *testing.T) {
	pc := precheckContext(t)
	pc.DataDir = ""
	raw := json.RawMessage(`{
		"name": "nightly", "cron": "0 3 * * *", "message": "run",
		"precheck": {"command": "./scripts/gate.sh"}
	}`)
	if _, err := parseScheduleEntry(pc, raw); err == nil ||
		!strings.Contains(err.Error(), "data directory is unavailable") {
		t.Fatalf("err = %v", err)
	}
}

func TestDiscoverSchedulesKeepsResolvedPrecheck(t *testing.T) {
	pc := precheckContext(t)
	writeTestFile(t, pc.Root, "plugin.json", manifestJSON(scheduleExtension(map[string]any{
		"name": "nightly", "cron": "0 3 * * *", "message": "run",
		"precheck": map[string]any{
			"command": "./scripts/gate.sh",
			"args":    []string{"--gate"},
			"cwd":     "${PLUGIN_DATA}",
		},
	})))
	data, err := ReadPackageFile(pc.Root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	pc.AllowSchedules = true
	schedules, diagnostics := discoverSchedules(manifest, pc)
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	if len(schedules) != 1 || schedules[0].Precheck == nil {
		t.Fatalf("schedules = %+v", schedules)
	}
	if schedules[0].Precheck.Dir != pc.DataDir {
		t.Fatalf("cwd = %q, want %q", schedules[0].Precheck.Dir, pc.DataDir)
	}
}
