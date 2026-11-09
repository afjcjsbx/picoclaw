package cron

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// ErrUnknownTrigger is returned when a configured trigger provider is not
// registered. Callers should fall back to the in-process trigger.
var ErrUnknownTrigger = errors.New("unknown cron trigger")

// Trigger decides when due jobs are checked. It never executes a job itself:
// it calls Tick and the shared execution path in CronService runs the work.
//
// A trigger owns only the "when" axis. Job storage, due detection, schedule
// advancement, handler execution, state persistence and delivery remain in
// CronService. External triggers (webhooks, managed schedulers) can be added
// without changing job execution semantics.
type Trigger interface {
	Run(ctx context.Context, scheduler TriggerScheduler)
}

// TriggerScheduler is the scheduler surface exposed to trigger providers.
type TriggerScheduler interface {
	// Tick evaluates due jobs and dispatches them through the normal executor.
	Tick()
	// NextWakeMS returns the next scheduled instant, or nil when no job is armed.
	NextWakeMS() *int64
	// Wake is signaled when jobs are added, updated or removed.
	Wake() <-chan struct{}
	// Reload applies an external edit of the job store.
	Reload()
	// ReloadInterval is the configured store polling interval; zero disables it.
	ReloadInterval() time.Duration
}

// TriggerFactory builds one trigger provider.
type TriggerFactory func() Trigger

var (
	triggerRegistryMu sync.RWMutex
	triggerRegistry   = map[string]TriggerFactory{
		"inprocess": func() Trigger { return InProcessTrigger{} },
	}
)

// RegisterTrigger registers a named trigger provider. It is safe to call from
// an init function. The in-process provider always remains available.
func RegisterTrigger(name string, factory TriggerFactory) error {
	if name == "" {
		return fmt.Errorf("trigger name is required")
	}
	if factory == nil {
		return fmt.Errorf("trigger %q factory is nil", name)
	}
	triggerRegistryMu.Lock()
	defer triggerRegistryMu.Unlock()
	if _, exists := triggerRegistry[name]; exists {
		return fmt.Errorf("trigger %q is already registered", name)
	}
	triggerRegistry[name] = factory
	return nil
}

// NewTrigger builds a registered trigger provider. An empty name selects the
// in-process trigger.
func NewTrigger(name string) (Trigger, error) {
	if name == "" {
		name = "inprocess"
	}
	triggerRegistryMu.RLock()
	factory, ok := triggerRegistry[name]
	triggerRegistryMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownTrigger, name)
	}
	return factory(), nil
}

// TriggerNames returns the registered providers in stable order.
func TriggerNames() []string {
	triggerRegistryMu.RLock()
	defer triggerRegistryMu.RUnlock()
	names := make([]string, 0, len(triggerRegistry))
	for name := range triggerRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// InProcessTrigger is the default provider. It arms a timer for the next due
// job and also polls the job store for external edits when configured.
type InProcessTrigger struct{}

func (InProcessTrigger) Run(ctx context.Context, scheduler TriggerScheduler) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	var reloadC <-chan time.Time
	if interval := scheduler.ReloadInterval(); interval > 0 {
		reloadTicker := time.NewTicker(interval)
		defer reloadTicker.Stop()
		reloadC = reloadTicker.C
	}

	for {
		nextWake := scheduler.NextWakeMS()
		delay := time.Hour
		if nextWake != nil {
			diff := *nextWake - time.Now().UnixMilli()
			if diff <= 0 {
				delay = 0
			} else {
				delay = time.Duration(diff) * time.Millisecond
			}
		}
		timer.Reset(delay)

		select {
		case <-ctx.Done():
			return
		case <-scheduler.Wake():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-reloadC:
			scheduler.Reload()
		case <-timer.C:
			scheduler.Tick()
		}
	}
}
