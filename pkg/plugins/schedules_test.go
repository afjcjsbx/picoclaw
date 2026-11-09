package plugins

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/config"
)

func scheduleExtension(schedules ...map[string]any) map[string]any {
	return map[string]any{
		"extensions": map[string]any{
			ExtensionNamespace: map[string]any{"schedules": schedules},
		},
	}
}

func TestValidateReportsSchedules(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(scheduleExtension(
		map[string]any{
			"name":    "nightly",
			"cron":    "0 3 * * *",
			"tz":      "Europe/Rome",
			"message": "Run the nightly cycle",
		},
		map[string]any{
			"name":          "interval",
			"every_seconds": 3600,
			"message":       "Hourly",
			"enabled":       false,
		},
	)))
	report, err := Validate("alias", root, config.PluginEntryConfig{})
	if err != nil || len(report.Diagnostics) != 0 {
		t.Fatalf("err=%v diagnostics=%v", err, report.Diagnostics)
	}
	if got := strings.Join(report.Schedules, ","); got != "plugin:alias:nightly,plugin:alias:interval" {
		t.Fatalf("schedules=%q", got)
	}
}

func TestDiscoverSchedulesRequiresAllowSchedules(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(scheduleExtension(map[string]any{
		"name": "nightly", "cron": "0 3 * * *", "message": "Run",
	})))
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	gated, diagnostics := discoverSchedules(manifest, PluginContext{
		ID: "alias", Root: root, AllowSchedules: false,
	})
	if len(gated) != 0 || len(diagnostics) != 0 {
		t.Fatalf("gated schedules=%v diagnostics=%v", gated, diagnostics)
	}
	allowed, diagnostics := discoverSchedules(manifest, PluginContext{
		ID: "alias", Root: root, AllowSchedules: true,
	})
	if len(allowed) != 1 || len(diagnostics) != 0 {
		t.Fatalf("allowed schedules=%v diagnostics=%v", allowed, diagnostics)
	}
	if got := allowed[0].DeclarationKey(); got != "plugin:alias:nightly" {
		t.Fatalf("declaration key=%q", got)
	}
}

func TestDiscoverSchedulesFileForm(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(nil))
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "schedules.json"), []byte(`{
		"schedules": [{"name": "nightly", "cron": "0 3 * * *", "message": "Run"}]
	}`))
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	schedules, diagnostics := discoverSchedules(manifest, PluginContext{
		ID: "alias", Root: root, AllowSchedules: true,
	})
	if len(schedules) != 1 || len(diagnostics) != 0 {
		t.Fatalf("schedules=%v diagnostics=%v", schedules, diagnostics)
	}
}

func TestParseScheduleRejectsInvalidEntries(t *testing.T) {
	cases := []struct {
		name  string
		entry map[string]any
		want  string
	}{
		{"missing message", map[string]any{"name": "a", "cron": "0 3 * * *"}, "requires a message"},
		{
			"unknown field",
			map[string]any{"name": "a", "cron": "0 3 * * *", "message": "m", "extra": true},
			"unknown schedule field",
		},
		{
			"bad cron",
			map[string]any{"name": "a", "cron": "nope", "message": "m"},
			"invalid cron expression",
		},
		{"missing schedule", map[string]any{"name": "a", "message": "m"}, "exactly one"},
		{
			"schedule and shortcut",
			map[string]any{
				"name":     "a",
				"message":  "m",
				"cron":     "0 3 * * *",
				"schedule": map[string]any{"kind": "every", "everyMs": 1000},
			},
			"cannot be combined",
		},
		{
			"channel without to",
			map[string]any{"name": "a", "cron": "0 3 * * *", "message": "m", "channel": "cli"},
			"both channel and to",
		},
		{
			"tz without cron",
			map[string]any{"name": "a", "every_seconds": 60, "tz": "UTC", "message": "m"},
			"only valid with cron",
		},
		{"bad at", map[string]any{"name": "a", "at": "tomorrow", "message": "m"}, "RFC3339"},
		{
			"every with timezone",
			map[string]any{
				"name": "a", "message": "m",
				"schedule": map[string]any{"kind": "every", "everyMs": 1000, "tz": "Not/AZone"},
			},
			"not valid for kind",
		},
		{
			"at with expression",
			map[string]any{
				"name": "a", "message": "m",
				"schedule": map[string]any{"kind": "at", "atMs": 4102444800000, "expr": "not a cron"},
			},
			"not valid for kind",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			raw, err := json.Marshal(test.entry)
			if err != nil {
				t.Fatal(err)
			}
			_, err = parseScheduleEntry(PluginContext{ID: "alias"}, raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err=%v, want %q", err, test.want)
			}
		})
	}
}

func TestParseScheduleObjectAndAt(t *testing.T) {
	raw := json.RawMessage(`{
		"name": "ts",
		"schedule": {"kind": "cron", "expr": "0 3 * * *", "tz": "Europe/Rome"},
		"message": "m"
	}`)
	schedule, err := parseScheduleEntry(PluginContext{ID: "alias"}, raw)
	if err != nil {
		t.Fatalf("schedule object: %v", err)
	}
	if schedule.Schedule.Kind != "cron" || schedule.Schedule.TZ != "Europe/Rome" {
		t.Fatalf("unexpected schedule: %+v", schedule.Schedule)
	}

	raw = json.RawMessage(
		`{"name":"one","at":"2030-01-02T03:04:05Z","message":"m","enabled":false}`,
	)
	schedule, err = parseScheduleEntry(PluginContext{ID: "alias"}, raw)
	if err != nil {
		t.Fatalf("at schedule: %v", err)
	}
	if schedule.Schedule.Kind != "at" || schedule.Enabled == nil || *schedule.Enabled {
		t.Fatalf("unexpected one-shot: %+v", schedule)
	}
}

func TestDiscoverSchedulesRejectsDuplicateNames(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(scheduleExtension(
		map[string]any{"name": "dup", "cron": "0 3 * * *", "message": "a"},
		map[string]any{"name": "dup", "cron": "0 4 * * *", "message": "b"},
	)))
	data, _ := ReadPackageFile(root, "plugin.json")
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	schedules, diagnostics := discoverSchedules(manifest, PluginContext{
		ID: "alias", Root: root, AllowSchedules: true,
	})
	if len(schedules) != 1 || len(diagnostics) == 0 ||
		!strings.Contains(diagnostics[0].Message, "duplicate") {
		t.Fatalf("schedules=%v diagnostics=%v", schedules, diagnostics)
	}
}

func TestMixedInlineHooksAndScheduleFile(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(map[string]any{
		"extensions": map[string]any{
			ExtensionNamespace: map[string]any{
				"hooks": []map[string]any{{
					"name":    "observer",
					"command": "python3",
					"observe": []string{"turn_completed"},
				}},
			},
		},
	}))
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "schedules.json"), []byte(`{
		"schedules": [{"name": "nightly", "cron": "0 3 * * *", "message": "Run"}]
	}`))
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	pc := PluginContext{
		ID:             "alias",
		Root:           root,
		DataDir:        t.TempDir(),
		AllowHooks:     true,
		AllowSchedules: true,
	}
	hooks, diagnostics := discoverHooks(manifest, pc)
	if len(hooks) != 1 || len(diagnostics) != 0 {
		t.Fatalf("hooks=%v diagnostics=%v", hooks, diagnostics)
	}
	schedules, diagnostics := discoverSchedules(manifest, pc)
	if len(schedules) != 1 || len(diagnostics) != 0 {
		t.Fatalf("schedules=%v diagnostics=%v", schedules, diagnostics)
	}
}

func TestMixedInlineSchedulesAndHookFile(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(scheduleExtension(map[string]any{
		"name": "nightly", "cron": "0 3 * * *", "message": "Run",
	})))
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "hooks.json"), []byte(`{
		"hooks": [{"name": "observer", "command": "python3", "observe": ["turn_completed"]}]
	}`))
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	pc := PluginContext{
		ID:             "alias",
		Root:           root,
		DataDir:        t.TempDir(),
		AllowHooks:     true,
		AllowSchedules: true,
	}
	hooks, diagnostics := discoverHooks(manifest, pc)
	if len(hooks) != 1 || len(diagnostics) != 0 {
		t.Fatalf("hooks=%v diagnostics=%v", hooks, diagnostics)
	}
	schedules, diagnostics := discoverSchedules(manifest, pc)
	if len(schedules) != 1 || len(diagnostics) != 0 {
		t.Fatalf("schedules=%v diagnostics=%v", schedules, diagnostics)
	}
}

func TestSameComponentInlineAndFileStillRejected(t *testing.T) {
	root, _ := canonicalRoot(t.TempDir())
	writeTestFile(t, root, "plugin.json", manifestJSON(map[string]any{
		"extensions": map[string]any{
			ExtensionNamespace: map[string]any{
				"hooks": []map[string]any{{
					"name":    "observer",
					"command": "python3",
					"observe": []string{"turn_completed"},
				}},
				"schedules": []map[string]any{{
					"name": "nightly", "cron": "0 3 * * *", "message": "Run",
				}},
			},
		},
	}))
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "hooks.json"), []byte(`{
		"hooks": [{"name": "observer", "command": "python3", "observe": ["turn_completed"]}]
	}`))
	writeTestFile(t, root, filepath.Join(ExtensionNamespace, "schedules.json"), []byte(`{
		"schedules": [{"name": "nightly", "cron": "0 3 * * *", "message": "Run"}]
	}`))
	data, err := ReadPackageFile(root, "plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := ParseManifest(data)
	if err != nil {
		t.Fatal(err)
	}
	pc := PluginContext{
		ID:             "alias",
		Root:           root,
		DataDir:        t.TempDir(),
		AllowHooks:     true,
		AllowSchedules: true,
	}
	if _, diagnostics := discoverHooks(manifest, pc); len(diagnostics) == 0 {
		t.Fatal("inline and file hooks were both accepted")
	}
	if _, diagnostics := discoverSchedules(manifest, pc); len(diagnostics) == 0 {
		t.Fatal("inline and file schedules were both accepted")
	}
}
