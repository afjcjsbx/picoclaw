package cron

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

// CronJobDeclaration is a plugin-owned job specification. The declaration key
// is stable across restarts and reloads; reconciliation converges the stored
// job with this specification instead of creating a new job on every load.
type CronJobDeclaration struct {
	DeclarationKey string
	Name           string
	Schedule       CronSchedule
	Message        string
	Channel        string
	To             string
	// Precheck is an optional wake gate that runs before the agent turn.
	Precheck *JobPrecheck
	// AgentID routes runs to a specific agent. Empty uses the default route.
	AgentID string
	// Enabled is applied only when non-nil on every reconciliation. Routine
	// reconciliation omits it, which preserves an operator's pause/disable
	// decision.
	Enabled *bool
}

// ReconcileReport summarizes one convergence pass.
type ReconcileReport struct {
	Created     int
	Updated     int
	Unchanged   int
	Removed     int
	RemovedKeys []string
}

// ReconcileDeclarations converges jobs with DeclarationKey under prefix.
//
// The caller owns only declarations under prefix. User-created jobs and other
// owners are never touched. Existing jobs keep their ID, creation time, run
// history and failure state. A job paused or disabled by the operator stays
// that way unless the declaration explicitly sets Enabled.
//
// When prune is true, declared jobs under prefix that no longer appear in decls
// are removed. When prune is false they are left in place; this is used when
// plugin discovery did not complete, so a slow or failed load cannot delete
// valid schedules.
func (cs *CronService) ReconcileDeclarations(
	prefix string,
	decls []CronJobDeclaration,
	prune bool,
) (ReconcileReport, error) {
	var report ReconcileReport
	if prefix == "" {
		return report, errors.New("declaration prefix is required")
	}
	seen := make(map[string]struct{}, len(decls))
	for _, decl := range decls {
		if !strings.HasPrefix(decl.DeclarationKey, prefix) {
			return report, fmt.Errorf(
				"declaration %q is outside prefix %q",
				decl.DeclarationKey,
				prefix,
			)
		}
		if _, duplicate := seen[decl.DeclarationKey]; duplicate {
			return report, fmt.Errorf("duplicate declaration key %q", decl.DeclarationKey)
		}
		if err := validateDeclaration(decl); err != nil {
			return report, err
		}
		seen[decl.DeclarationKey] = struct{}{}
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	now := time.Now().UnixMilli()
	keep := make(map[string]struct{}, len(decls))
	changed := false

	for _, decl := range decls {
		keep[decl.DeclarationKey] = struct{}{}
		index := -1
		for i := range cs.store.Jobs {
			if cs.store.Jobs[i].DeclarationKey == decl.DeclarationKey {
				index = i
				break
			}
		}
		if index < 0 {
			// A one-shot whose instant already passed is not re-created after
			// it fired. The declaration stays valid; only this occurrence is gone.
			if decl.Schedule.Kind == "at" && decl.Schedule.AtMS != nil &&
				*decl.Schedule.AtMS <= now {
				continue
			}
			enabled := true
			if decl.Enabled != nil {
				enabled = *decl.Enabled
			}
			job := CronJob{
				ID:       generateID(),
				Name:     decl.Name,
				Enabled:  enabled,
				Schedule: cloneSchedule(decl.Schedule),
				Payload: CronPayload{
					Message:  decl.Message,
					Channel:  decl.Channel,
					To:       decl.To,
					Precheck: cloneJobPrecheck(decl.Precheck),
				},
				AgentID:        decl.AgentID,
				DeclarationKey: decl.DeclarationKey,
				CreatedAtMS:    now,
				UpdatedAtMS:    now,
				DeleteAfterRun: decl.Schedule.Kind == "at",
			}
			if enabled {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
			}
			cs.store.Jobs = append(cs.store.Jobs, job)
			report.Created++
			changed = true
			continue
		}

		job := &cs.store.Jobs[index]
		previousEnabled := job.Enabled
		scheduleChanged := !sameSchedule(job.Schedule, decl.Schedule)
		payloadChanged := job.Payload.Message != decl.Message ||
			job.Payload.Channel != decl.Channel ||
			job.Payload.To != decl.To ||
			!reflect.DeepEqual(job.Payload.Precheck, decl.Precheck)
		enabledChanged := decl.Enabled != nil && job.Enabled != *decl.Enabled
		agentChanged := job.AgentID != decl.AgentID
		nameChanged := job.Name != decl.Name
		if !scheduleChanged && !payloadChanged && !enabledChanged && !agentChanged && !nameChanged {
			report.Unchanged++
			continue
		}

		job.Name = decl.Name
		job.Schedule = cloneSchedule(decl.Schedule)
		job.Payload = CronPayload{
			Message: decl.Message, Channel: decl.Channel, To: decl.To,
			Precheck: cloneJobPrecheck(decl.Precheck),
		}
		job.AgentID = decl.AgentID
		job.DeleteAfterRun = decl.Schedule.Kind == "at"
		if enabledChanged {
			job.Enabled = *decl.Enabled
		}
		job.UpdatedAtMS = now
		switch {
		case !job.Enabled:
			job.State.NextRunAtMS = nil
		case !previousEnabled || scheduleChanged:
			job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
		}
		report.Updated++
		changed = true
	}

	if prune {
		kept := cs.store.Jobs[:0]
		for _, job := range cs.store.Jobs {
			if job.DeclarationKey != "" &&
				strings.HasPrefix(job.DeclarationKey, prefix) {
				if _, ok := keep[job.DeclarationKey]; !ok {
					report.Removed++
					report.RemovedKeys = append(report.RemovedKeys, job.DeclarationKey)
					changed = true
					continue
				}
			}
			kept = append(kept, job)
		}
		cs.store.Jobs = kept
	}

	if changed {
		if err := cs.saveStoreUnsafe(); err != nil {
			return report, err
		}
		cs.notify()
	}
	return report, nil
}

func validateDeclaration(decl CronJobDeclaration) error {
	if strings.TrimSpace(decl.DeclarationKey) == "" {
		return errors.New("declaration key is required")
	}
	if strings.TrimSpace(decl.Name) == "" {
		return fmt.Errorf("declaration %q requires a name", decl.DeclarationKey)
	}
	if strings.TrimSpace(decl.Message) == "" {
		return fmt.Errorf("declaration %q requires a message", decl.DeclarationKey)
	}
	if (decl.Channel == "") != (decl.To == "") {
		return fmt.Errorf("declaration %q must set both channel and to", decl.DeclarationKey)
	}
	if err := validateJobPrecheck(decl.Precheck); err != nil {
		return fmt.Errorf("declaration %q precheck: %w", decl.DeclarationKey, err)
	}
	return validateCronSchedule(decl.Schedule)
}

func validateJobPrecheck(precheck *JobPrecheck) error {
	if precheck == nil {
		return nil
	}
	if len(precheck.Command) == 0 || strings.TrimSpace(precheck.Command[0]) == "" {
		return errors.New("command is required")
	}
	if precheck.TimeoutMS < 0 {
		return errors.New("timeoutMs must not be negative")
	}
	if precheck.TimeoutMS > MaxPrecheckTimeout.Milliseconds() {
		return fmt.Errorf("timeoutMs exceeds %s", MaxPrecheckTimeout)
	}
	for key := range precheck.Env {
		if key == "" || strings.ContainsAny(key, "=\x00") {
			return fmt.Errorf("invalid environment key %q", key)
		}
	}
	return nil
}

func cloneSchedule(schedule CronSchedule) CronSchedule {
	cloned := schedule
	if schedule.AtMS != nil {
		value := *schedule.AtMS
		cloned.AtMS = &value
	}
	if schedule.EveryMS != nil {
		value := *schedule.EveryMS
		cloned.EveryMS = &value
	}
	return cloned
}

// GetJobByDeclarationKey returns a copy of the job owned by key.
func (cs *CronService) GetJobByDeclarationKey(key string) (*CronJob, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].DeclarationKey == key {
			job := cloneCronJob(cs.store.Jobs[i])
			return &job, true
		}
	}
	return nil, false
}
