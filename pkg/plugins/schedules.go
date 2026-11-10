package plugins

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
)

const (
	maxScheduleMessageBytes = 16 * 1024
	maxSchedulesPerPlugin   = 32
)

// discoverSchedules reads host-managed cron declarations from the extension
// namespace. The host owns the stored job; the package only describes it.
func discoverSchedules(manifest PluginManifest, pc PluginContext) ([]Schedule, []Diagnostic) {
	if !pc.AllowSchedules {
		return nil, nil
	}
	var diagnostics []Diagnostic
	report := func(err error) {
		diagnostics = append(
			diagnostics,
			Diagnostic{Plugin: pc.ID, Component: "schedules", Message: err.Error()},
		)
	}

	raw, inline := manifest.Extensions[ExtensionNamespace]
	file := filepath.Join(ExtensionNamespace, "schedules.json")
	hasFile := componentExists(pc.Root, file)
	if inline {
		inlineObj, err := object(raw)
		if err != nil {
			report(err)
			return nil, diagnostics
		}
		if hasFile {
			if _, ok := inlineObj["schedules"]; ok {
				report(fmt.Errorf("schedules must be configured inline or in %s, not both", file))
				return nil, diagnostics
			}
			raw, err = ReadPackageFile(pc.Root, file)
			if err != nil {
				report(err)
				return nil, diagnostics
			}
		}
	} else if hasFile {
		var err error
		raw, err = ReadPackageFile(pc.Root, file)
		if err != nil {
			report(err)
			return nil, diagnostics
		}
	} else {
		return nil, nil
	}

	obj, err := object(raw)
	if err != nil {
		report(err)
		return nil, diagnostics
	}
	for key := range obj {
		if key != "hooks" && key != "schedules" {
			report(fmt.Errorf("unknown extension field %q", key))
		}
	}
	rawSchedules, ok := obj["schedules"]
	if !ok {
		return nil, diagnostics
	}
	var entries []json.RawMessage
	if err := decodeField(rawSchedules, &entries); err != nil {
		report(err)
		return nil, diagnostics
	}
	if len(entries) > maxSchedulesPerPlugin {
		report(fmt.Errorf("too many schedules: %d (max %d)", len(entries), maxSchedulesPerPlugin))
		return nil, diagnostics
	}

	seen := map[string]bool{}
	result := make([]Schedule, 0, len(entries))
	for _, entry := range entries {
		schedule, err := parseScheduleEntry(pc, entry)
		if err != nil {
			report(err)
			continue
		}
		if seen[schedule.Name] {
			report(fmt.Errorf("duplicate schedule name %q", schedule.Name))
			continue
		}
		seen[schedule.Name] = true
		result = append(result, schedule)
	}
	return result, diagnostics
}

func parseScheduleEntry(pc PluginContext, raw json.RawMessage) (Schedule, error) {
	var result Schedule
	fields, err := object(raw)
	if err != nil {
		return result, err
	}
	for key := range fields {
		switch key {
		case "name", "schedule", "cron", "cron_expr", "every_seconds",
			"at", "at_ms", "tz", "message", "channel", "to", "precheck", "enabled":
		default:
			return result, fmt.Errorf("unknown schedule field %q", key)
		}
	}

	rawName, ok := fields["name"]
	if !ok {
		return result, fmt.Errorf("schedule name is required")
	}
	if decodeErr := decodeField(rawName, &result.Name); decodeErr != nil {
		return result, fmt.Errorf("schedule name: %w", decodeErr)
	}
	if !ValidName(result.Name) {
		return result, fmt.Errorf("invalid schedule name %q", result.Name)
	}
	rawMessage, ok := fields["message"]
	if !ok {
		return result, fmt.Errorf("schedule %q requires a message", result.Name)
	}
	if decodeErr := decodeField(rawMessage, &result.Message); decodeErr != nil {
		return result, fmt.Errorf("schedule %q message: %w", result.Name, decodeErr)
	}
	if strings.TrimSpace(result.Message) == "" {
		return result, fmt.Errorf("schedule %q requires a message", result.Name)
	}
	if len(result.Message) > maxScheduleMessageBytes {
		return result, fmt.Errorf(
			"schedule %q message exceeds %d bytes",
			result.Name,
			maxScheduleMessageBytes,
		)
	}
	if value, ok := fields["channel"]; ok {
		if decodeErr := decodeField(value, &result.Channel); decodeErr != nil {
			return result, fmt.Errorf("schedule %q channel: %w", result.Name, decodeErr)
		}
	}
	if value, ok := fields["to"]; ok {
		if decodeErr := decodeField(value, &result.To); decodeErr != nil {
			return result, fmt.Errorf("schedule %q to: %w", result.Name, decodeErr)
		}
	}
	if (result.Channel == "") != (result.To == "") {
		return result, fmt.Errorf("schedule %q must set both channel and to", result.Name)
	}
	if value, ok := fields["enabled"]; ok {
		var enabled bool
		if decodeErr := decodeField(value, &enabled); decodeErr != nil {
			return result, fmt.Errorf("schedule %q enabled: %w", result.Name, decodeErr)
		}
		result.Enabled = &enabled
	}
	if value, ok := fields["precheck"]; ok {
		precheck, precheckErr := parseSchedulePrecheck(pc, value)
		if precheckErr != nil {
			return result, fmt.Errorf("schedule %q precheck: %w", result.Name, precheckErr)
		}
		result.Precheck = precheck
	}

	schedule, err := parseScheduleSpec(fields)
	if err != nil {
		return result, fmt.Errorf("schedule %q: %w", result.Name, err)
	}
	result.Schedule = schedule
	result.PluginID = pc.ID
	return result, nil
}

// parseSchedulePrecheck accepts the same launch fields as a portable stdio MCP
// server, so path containment, environment rules and ${PLUGIN_ROOT} expansion
// stay in one place. A precheck never receives ambient host credentials.
func parseSchedulePrecheck(pc PluginContext, raw json.RawMessage) (*Precheck, error) {
	fields, err := object(raw)
	if err != nil {
		return nil, err
	}
	for key := range fields {
		switch key {
		case "command", "args", "env", "cwd", "timeout_seconds":
		default:
			return nil, fmt.Errorf("unknown precheck field %q", key)
		}
	}
	if pc.DataDir == "" {
		return nil, fmt.Errorf("plugin data directory is unavailable")
	}

	timeout := cron.DefaultPrecheckTimeout
	if value, ok := fields["timeout_seconds"]; ok {
		var seconds int64
		if decodeErr := decodeField(value, &seconds); decodeErr != nil {
			return nil, fmt.Errorf("timeout_seconds: %w", decodeErr)
		}
		if seconds <= 0 || seconds > int64(cron.MaxPrecheckTimeout/time.Second) {
			return nil, fmt.Errorf(
				"timeout_seconds must be between 1 and %d",
				int64(cron.MaxPrecheckTimeout/time.Second),
			)
		}
		timeout = time.Duration(seconds) * time.Second
	}

	launch := map[string]any{"type": "stdio"}
	for _, key := range []string{"command", "args", "env", "cwd"} {
		if value, ok := fields[key]; ok {
			launch[key] = value
		}
	}
	encoded, err := json.Marshal(launch)
	if err != nil {
		return nil, err
	}
	server, err := parseServer(encoded)
	if err != nil {
		return nil, err
	}
	cfg, dir, env, err := server.RuntimeConfig(pc.Root, pc.DataDir)
	if err != nil {
		return nil, err
	}
	return &Precheck{
		Command: append([]string{cfg.Command}, cfg.Args...),
		Dir:     dir,
		Env:     environmentMap(env),
		Timeout: timeout,
	}, nil
}

// environmentMap converts the host-owned environment slice produced by
// Environment into the map stored on the job.
func environmentMap(env []string) map[string]string {
	result := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && key != "" {
			result[key] = value
		}
	}
	return result
}

func parseScheduleSpec(fields map[string]json.RawMessage) (cron.CronSchedule, error) {
	var schedule cron.CronSchedule
	rawSchedule, hasSchedule := fields["schedule"]
	rawCron, hasCron := fields["cron"]
	rawCronExpr, hasCronExpr := fields["cron_expr"]
	rawEvery, hasEvery := fields["every_seconds"]
	rawAt, hasAt := fields["at"]
	rawAtMS, hasAtMS := fields["at_ms"]
	rawTZ, hasTZ := fields["tz"]

	shortcuts := 0
	for _, present := range []bool{hasCron, hasCronExpr, hasEvery, hasAt, hasAtMS} {
		if present {
			shortcuts++
		}
	}
	if hasSchedule {
		if shortcuts > 0 {
			return schedule, fmt.Errorf(
				"schedule cannot be combined with cron, cron_expr, every_seconds, at or at_ms",
			)
		}
		if hasTZ {
			return schedule, fmt.Errorf("tz belongs inside the schedule object")
		}
		return parseScheduleObject(rawSchedule)
	}
	if shortcuts != 1 {
		return schedule, fmt.Errorf(
			"exactly one of schedule, cron, cron_expr, every_seconds, at or at_ms is required",
		)
	}

	var timezone string
	if hasTZ {
		if err := decodeField(rawTZ, &timezone); err != nil {
			return schedule, fmt.Errorf("tz: %w", err)
		}
	}
	switch {
	case hasCron || hasCronExpr:
		if hasCron && hasCronExpr {
			return schedule, fmt.Errorf("cron and cron_expr are mutually exclusive")
		}
		raw := rawCron
		if !hasCron {
			raw = rawCronExpr
		}
		var expr string
		if err := decodeField(raw, &expr); err != nil {
			return schedule, fmt.Errorf("cron expression: %w", err)
		}
		schedule = cron.CronSchedule{Kind: "cron", Expr: expr, TZ: timezone}
	case hasEvery:
		var seconds int64
		if err := decodeField(rawEvery, &seconds); err != nil {
			return schedule, fmt.Errorf("every_seconds: %w", err)
		}
		if timezone != "" {
			return schedule, fmt.Errorf("tz is only valid with cron expressions")
		}
		everyMS := seconds * 1000
		schedule = cron.CronSchedule{Kind: "every", EveryMS: &everyMS}
	case hasAtMS:
		var atMS int64
		if err := decodeField(rawAtMS, &atMS); err != nil {
			return schedule, fmt.Errorf("at_ms: %w", err)
		}
		if timezone != "" {
			return schedule, fmt.Errorf("tz is only valid with cron expressions")
		}
		schedule = cron.CronSchedule{Kind: "at", AtMS: &atMS}
	case hasAt:
		var at string
		if err := decodeField(rawAt, &at); err != nil {
			return schedule, fmt.Errorf("at: %w", err)
		}
		parsed, err := time.Parse(time.RFC3339, at)
		if err != nil {
			return schedule, fmt.Errorf("at must be an RFC3339 timestamp: %w", err)
		}
		if timezone != "" {
			return schedule, fmt.Errorf("tz is only valid with cron expressions")
		}
		atMS := parsed.UnixMilli()
		schedule = cron.CronSchedule{Kind: "at", AtMS: &atMS}
	}
	return schedule, cron.ValidateSchedule(schedule)
}

func parseScheduleObject(raw json.RawMessage) (cron.CronSchedule, error) {
	var schedule cron.CronSchedule
	fields, err := object(raw)
	if err != nil {
		return schedule, fmt.Errorf("schedule: %w", err)
	}
	for key := range fields {
		switch key {
		case "kind", "atMs", "everyMs", "expr", "tz":
		default:
			return schedule, fmt.Errorf("unknown schedule object field %q", key)
		}
	}
	if err := json.Unmarshal(raw, &schedule); err != nil {
		return schedule, fmt.Errorf("schedule: %w", err)
	}
	if err := validateScheduleObjectFields(schedule, fields); err != nil {
		return schedule, err
	}
	return schedule, cron.ValidateSchedule(schedule)
}

func validateScheduleObjectFields(schedule cron.CronSchedule, fields map[string]json.RawMessage) error {
	allowed := map[string]bool{"kind": true}
	switch schedule.Kind {
	case "at":
		allowed["atMs"] = true
	case "every":
		allowed["everyMs"] = true
	case "cron":
		allowed["expr"] = true
		allowed["tz"] = true
	default:
		return nil
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("schedule field %q is not valid for kind %q", key, schedule.Kind)
		}
	}
	return nil
}
