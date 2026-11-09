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
		schedule, err := parseScheduleEntry(pc.ID, entry)
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

func parseScheduleEntry(pluginID string, raw json.RawMessage) (Schedule, error) {
	var result Schedule
	fields, err := object(raw)
	if err != nil {
		return result, err
	}
	for key := range fields {
		switch key {
		case "name", "schedule", "cron", "cron_expr", "every_seconds",
			"at", "at_ms", "tz", "message", "channel", "to", "enabled":
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

	schedule, err := parseScheduleSpec(fields)
	if err != nil {
		return result, fmt.Errorf("schedule %q: %w", result.Name, err)
	}
	result.Schedule = schedule
	result.PluginID = pluginID
	return result, nil
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
	return schedule, cron.ValidateSchedule(schedule)
}
