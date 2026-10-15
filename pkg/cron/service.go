package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/adhocore/gronx"

	"github.com/sipeed/picoclaw/pkg/fileutil"
)

type CronSchedule struct {
	Kind    string `json:"kind"`
	AtMS    *int64 `json:"atMs,omitempty"`
	EveryMS *int64 `json:"everyMs,omitempty"`
	Expr    string `json:"expr,omitempty"`
	TZ      string `json:"tz,omitempty"`
}

type CronPayload struct {
	Message string `json:"message"`
	Command string `json:"command,omitempty"`
	Channel string `json:"channel,omitempty"`
	To      string `json:"to,omitempty"`
}

type CronJobState struct {
	NextRunAtMS *int64 `json:"nextRunAtMs,omitempty"`
	LastRunAtMS *int64 `json:"lastRunAtMs,omitempty"`
	LastStatus  string `json:"lastStatus,omitempty"`
	LastError   string `json:"lastError,omitempty"`
}

type CronJob struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Enabled        bool         `json:"enabled"`
	Schedule       CronSchedule `json:"schedule"`
	Payload        CronPayload  `json:"payload"`
	State          CronJobState `json:"state"`
	CreatedAtMS    int64        `json:"createdAtMs"`
	UpdatedAtMS    int64        `json:"updatedAtMs"`
	DeleteAfterRun bool         `json:"deleteAfterRun"`
}

type CronStore struct {
	Version int       `json:"version"`
	Jobs    []CronJob `json:"jobs"`
}

type JobHandler func(job *CronJob) (string, error)

type CronService struct {
	storePath string
	store     *CronStore
	onJob     JobHandler
	mu        sync.RWMutex
	running   bool
	stopChan  chan struct{}
	wakeChan  chan struct{}
	gronx     *gronx.Gronx

	// reloadEvery is the polling interval used to detect external edits of the
	// store file (hand edits, CLI). Zero disables hot-reload.
	reloadEvery time.Duration
	// storeStat is the state of the store file as last read or written by this
	// service; any difference means someone else changed it.
	storeStat os.FileInfo
}

func NewCronService(storePath string, onJob JobHandler) *CronService {
	cs := &CronService{
		storePath: storePath,
		onJob:     onJob,
		gronx:     gronx.New(),
		wakeChan:  make(chan struct{}, 1),
	}
	// Initialize and load store on creation
	cs.loadStore()
	return cs
}

func (cs *CronService) Start() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if cs.running {
		return nil
	}

	if err := cs.loadStore(); err != nil {
		return fmt.Errorf("failed to load store: %w", err)
	}

	cs.recomputeNextRuns()
	if err := cs.saveStoreUnsafe(); err != nil {
		return fmt.Errorf("failed to save store: %w", err)
	}

	cs.stopChan = make(chan struct{})
	if cs.wakeChan == nil {
		// Buffered so a notify() that races with the loop recomputing its
		// wake time is not lost.
		cs.wakeChan = make(chan struct{}, 1)
	}
	cs.running = true
	go cs.runLoop(cs.stopChan)

	return nil
}

func (cs *CronService) Stop() {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return
	}

	cs.running = false
	if cs.stopChan != nil {
		close(cs.stopChan)
		cs.stopChan = nil
	}
}

func (cs *CronService) runLoop(stopChan chan struct{}) {
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()

	var reloadC <-chan time.Time
	cs.mu.RLock()
	reloadEvery := cs.reloadEvery
	cs.mu.RUnlock()
	if reloadEvery > 0 {
		reloadTicker := time.NewTicker(reloadEvery)
		defer reloadTicker.Stop()
		reloadC = reloadTicker.C
	}

	for {
		// every loop, recalculate the next wake time
		cs.mu.RLock()
		nextWake := cs.getNextWakeMS()
		cs.mu.RUnlock()

		var delay time.Duration
		now := time.Now().UnixMilli()

		if nextWake == nil {
			// no jobs, sleep for a long time (or until a new job is added)
			delay = time.Hour
		} else {
			diff := *nextWake - now
			if diff <= 0 {
				delay = 0
			} else {
				delay = time.Duration(diff) * time.Millisecond
			}
		}

		timer.Reset(delay)

		select {
		case <-stopChan:
			return
		case <-cs.wakeChan: // wake on new job or update
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			continue
		case <-reloadC:
			// The loop recomputes its wake time on the next iteration, so no notify is needed.
			cs.mu.Lock()
			if cs.running {
				cs.syncFromDiskUnsafe()
			}
			cs.mu.Unlock()
		case <-timer.C:
			cs.checkJobs()
		}
	}
}

func (cs *CronService) checkJobs() {
	cs.mu.Lock()

	if !cs.running {
		cs.mu.Unlock()
		return
	}

	cs.syncFromDiskUnsafe()

	now := time.Now().UnixMilli()
	var dueJobIDs []string

	// Collect jobs that are due (we need to copy them to execute outside lock)
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS <= now {
			dueJobIDs = append(dueJobIDs, job.ID)
		}
	}

	// Advance the schedule for due jobs synchronously before unlocking. This
	// keeps the scheduler loop's wake time valid while the handler runs in a
	// goroutine, and prevents duplicate execution.
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.State.NextRunAtMS == nil || *job.State.NextRunAtMS > now {
			continue
		}
		cs.advanceNextRun(job, now)
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}

	cs.mu.Unlock()

	// Execute jobs outside the lock, each in its own goroutine so a slow
	// handler does not block the scheduler loop or other due jobs.
	for _, jobID := range dueJobIDs {
		go func(id string) {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[cron] recovered panic while executing job %s: %v", id, r)
				}
			}()
			cs.executeJobByID(id)
		}(jobID)
	}
}

func (cs *CronService) executeJobByID(jobID string) {
	startTime := time.Now().UnixMilli()

	cs.mu.RLock()
	var callbackJob *CronJob
	onJob := cs.onJob
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			jobCopy := *job
			callbackJob = &jobCopy
			break
		}
	}
	cs.mu.RUnlock()

	if callbackJob == nil {
		log.Printf("[cron] job %s not found, skipping", jobID)
		return
	}

	// Log job execution start
	log.Printf("[cron] ▶ executing job '%s' (id: %s, schedule: %s, channel: %s)",
		callbackJob.Name, jobID, callbackJob.Schedule.Kind, callbackJob.Payload.Channel)

	var (
		err     error
		skipped bool
	)
	if onJob != nil {
		// Isolate handler panics so the job state is still updated and the
		// schedule remains alive instead of being left with a nil next run.
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("job handler panicked: %v", r)
				}
			}()
			_, err = onJob(callbackJob)
		}()
	} else {
		skipped = true
	}

	execDuration := time.Now().UnixMilli() - startTime

	// Now acquire lock to update state
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	var job *CronJob
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			job = &cs.store.Jobs[i]
			break
		}
	}
	if job == nil {
		log.Printf("[cron] job %s disappeared before state update", jobID)
		return
	}

	job.State.LastRunAtMS = &startTime
	job.UpdatedAtMS = time.Now().UnixMilli()

	switch {
	case err != nil:
		job.State.LastStatus = "error"
		job.State.LastError = err.Error()
		log.Printf("[cron] ✗ job '%s' failed after %dms: %v", job.Name, execDuration, err)
	case skipped:
		job.State.LastStatus = "skipped"
		job.State.LastError = "no job handler configured"
	default:
		job.State.LastStatus = "ok"
		job.State.LastError = ""
	}

	// Recurring schedules were already advanced in checkJobs; only one-time
	// jobs need their lifecycle finalized here.
	var nextRunStr string
	if job.Schedule.Kind == "at" {
		if job.DeleteAfterRun {
			cs.removeJobUnsafe(job.ID)
			nextRunStr = "(deleted)"
		} else {
			job.Enabled = false
			job.State.NextRunAtMS = nil
			nextRunStr = "(disabled)"
		}
	} else if job.State.NextRunAtMS != nil {
		nextRunStr = time.UnixMilli(*job.State.NextRunAtMS).Format("2006-01-02 15:04:05")
	} else {
		nextRunStr = "(none)"
	}

	switch {
	case err != nil:
		// Failure already logged above.
	case skipped:
		log.Printf(
			"[cron] ⚠ job '%s' skipped in %dms (no job handler), next run: %s",
			job.Name, execDuration, nextRunStr,
		)
	default:
		log.Printf("[cron] ✓ job '%s' completed in %dms, next run: %s", job.Name, execDuration, nextRunStr)
	}

	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] failed to save store: %v", err)
	}
}

// advanceNextRun moves a due job to its next occurrence. Recurring jobs are
// anchored to their previously scheduled time so handler duration and loop
// wake lateness do not accumulate drift. If the scheduled time is so far in
// the past that the next occurrence already elapsed (e.g. after downtime), it
// re-anchors to now to avoid a catch-up burst.
func (cs *CronService) advanceNextRun(job *CronJob, nowMS int64) {
	if job.Schedule.Kind == "at" {
		// One-time jobs are finalized by executeJobByID.
		job.State.NextRunAtMS = nil
		return
	}

	anchor := nowMS
	if job.State.NextRunAtMS != nil {
		anchor = *job.State.NextRunAtMS
	}

	next := cs.computeNextRun(&job.Schedule, anchor)
	if next != nil && *next <= nowMS {
		next = cs.computeNextRun(&job.Schedule, nowMS)
	}
	job.State.NextRunAtMS = next
}

func (cs *CronService) computeNextRun(schedule *CronSchedule, nowMS int64) *int64 {
	switch schedule.Kind {
	case "at":
		if schedule.AtMS != nil && *schedule.AtMS > nowMS {
			return schedule.AtMS
		}
		return nil
	case "every":
		if schedule.EveryMS == nil || *schedule.EveryMS <= 0 {
			return nil
		}
		next := nowMS + *schedule.EveryMS
		return &next
	case "cron":
		if schedule.Expr == "" {
			return nil
		}

		// Use gronx to calculate next run time, honoring the schedule timezone
		// when provided. gronx computes in the location of the reference time,
		// so evaluate in the target location and return an absolute instant.
		now := time.UnixMilli(nowMS)
		if schedule.TZ != "" {
			loc, err := time.LoadLocation(schedule.TZ)
			if err != nil {
				log.Printf(
					"[cron] invalid timezone '%s' for expr '%s': %v",
					schedule.TZ, schedule.Expr, err,
				)
				return nil
			}
			now = now.In(loc)
		}

		nextTime, err := gronx.NextTickAfter(schedule.Expr, now, false)
		if err != nil {
			log.Printf("[cron] failed to compute next run for expr '%s': %v", schedule.Expr, err)
			return nil
		}

		nextMS := nextTime.UnixMilli()
		return &nextMS
	default:
		log.Printf("[cron] unknown schedule kind '%s'", schedule.Kind)
		return nil
	}
}

// wake up the loop to re-evaluate next wake time immediately (e.g. after add/update/remove jobs)
func (cs *CronService) notify() {
	select {
	case cs.wakeChan <- struct{}{}:
	default:
		// if the channel is full, it means the loop will wake up soon anyway, so we can skip sending
	}
}

func (cs *CronService) recomputeNextRuns() {
	now := time.Now().UnixMilli()
	kept := cs.store.Jobs[:0]
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]

		if job.Enabled && job.Schedule.Kind == "at" &&
			(job.Schedule.AtMS == nil || *job.Schedule.AtMS <= now) {
			// The one-time schedule elapsed while the service was down; it can
			// never fire again. Finalize it instead of leaving an enabled job
			// with a nil next run that the scheduler will ignore forever.
			if job.DeleteAfterRun {
				log.Printf(
					"[cron] dropping one-time job '%s' (id: %s) whose scheduled time elapsed before startup",
					job.Name, job.ID,
				)
				continue
			}
			job.Enabled = false
			job.State.NextRunAtMS = nil
			kept = append(kept, *job)
			continue
		}

		if job.Enabled {
			job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, now)
		}
		kept = append(kept, *job)
	}
	cs.store.Jobs = kept
}

func (cs *CronService) getNextWakeMS() *int64 {
	var nextWake *int64
	for _, job := range cs.store.Jobs {
		if job.Enabled && job.State.NextRunAtMS != nil {
			if nextWake == nil || *job.State.NextRunAtMS < *nextWake {
				nextWake = job.State.NextRunAtMS
			}
		}
	}
	return nextWake
}

func (cs *CronService) Load() error {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.loadStore()
}

func (cs *CronService) SetOnJob(handler JobHandler) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.onJob = handler
}

func newEmptyStore() *CronStore {
	return &CronStore{Version: 1, Jobs: []CronJob{}}
}

func readStoreFile(path string) (*CronStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	store := newEmptyStore()
	if err := json.Unmarshal(data, store); err != nil {
		return nil, err
	}
	return store, nil
}

// loadStore replaces the in-memory store with the file content. A missing file
// yields an empty store; an unreadable or invalid file leaves the current store
// untouched (empty on first load) and returns the error.
func (cs *CronService) loadStore() error {
	// Stat before reading: a write landing in between is then seen as a change.
	cs.storeStat, _ = os.Stat(cs.storePath)

	store, err := readStoreFile(cs.storePath)
	switch {
	case err == nil:
		cs.store = store
	case os.IsNotExist(err):
		cs.store = newEmptyStore()
	default:
		if cs.store == nil {
			cs.store = newEmptyStore()
		}
		return err
	}
	return nil
}

func (cs *CronService) saveStoreUnsafe() error {
	data, err := json.MarshalIndent(cs.store, "", "  ")
	if err != nil {
		return err
	}

	// Use unified atomic write utility with explicit sync for flash storage reliability.
	if err := fileutil.WriteFileAtomic(cs.storePath, data, 0o600); err != nil {
		return err
	}
	// Our own write must not look like an external edit.
	cs.storeStat, _ = os.Stat(cs.storePath)
	return nil
}

// SetReloadInterval sets how often the store file is polled for external edits
// (hand edits, CLI). A non-positive value disables hot-reload. Call before Start.
func (cs *CronService) SetReloadInterval(d time.Duration) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.reloadEvery = d
}

// storeChanged reports whether the store file differs from what this service
// last read or wrote. A missing or unreadable file is not a change: the
// in-memory store stays authoritative and the next save recreates the file.
// ponytail: mtime+size only; a same-size edit within the filesystem timestamp
// granularity of our own write is missed. Hash the content if that ever matters.
func (cs *CronService) storeChanged() (os.FileInfo, bool) {
	info, err := os.Stat(cs.storePath)
	if err != nil {
		return nil, false
	}
	prev := cs.storeStat
	return info, prev == nil || info.Size() != prev.Size() || !info.ModTime().Equal(prev.ModTime())
}

// syncFromDiskUnsafe replaces the in-memory store when the store file was
// changed by someone else. Callers hold cs.mu. Mutators call it before touching
// the store so an external edit is never overwritten by a stale in-memory copy;
// the poll tick in runLoop calls it to pick edits up while idle.
func (cs *CronService) syncFromDiskUnsafe() {
	if cs.reloadEvery <= 0 {
		return
	}
	info, changed := cs.storeChanged()
	if !changed {
		return
	}

	next, err := readStoreFile(cs.storePath)
	// Advance the watermark even on failure so a broken file is reported once;
	// the next edit changes mtime/size again. Using the stat taken before the
	// read means a write racing with the read is picked up on the next check.
	cs.storeStat = info
	if err != nil {
		log.Printf("[cron] hot-reload failed, keeping in-memory store: %v", err)
		return
	}

	cs.replaceStoreUnsafe(next)
	if err := cs.saveStoreUnsafe(); err != nil {
		log.Printf("[cron] hot-reload: failed to persist reloaded store: %v", err)
	}
	log.Printf("[cron] store reloaded from disk (%d jobs)", len(cs.store.Jobs))
}

// replaceStoreUnsafe swaps in a store read from disk. Next runs are recomputed
// like on startup, except for jobs whose schedule and enabled flag are
// unchanged: those keep their in-memory position so unrelated edits do not
// push back (or re-anchor) a recurring job.
func (cs *CronService) replaceStoreUnsafe(next *CronStore) {
	prev := make(map[string]CronJob, len(cs.store.Jobs))
	for _, job := range cs.store.Jobs {
		prev[job.ID] = job
	}

	cs.store = next
	cs.recomputeNextRuns()

	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		p, ok := prev[job.ID]
		if ok && job.Enabled && p.Enabled && p.State.NextRunAtMS != nil &&
			sameSchedule(p.Schedule, job.Schedule) {
			nextRun := *p.State.NextRunAtMS
			job.State.NextRunAtMS = &nextRun
		}
	}
}

func (cs *CronService) AddJob(
	name string,
	schedule CronSchedule,
	message string,
	channel, to string,
) (*CronJob, error) {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	now := time.Now().UnixMilli()

	// One-time tasks (at) should be deleted after execution
	deleteAfterRun := (schedule.Kind == "at")

	job := CronJob{
		ID:       generateID(),
		Name:     name,
		Enabled:  true,
		Schedule: schedule,
		Payload: CronPayload{
			Message: message,
			Channel: channel,
			To:      to,
		},
		State: CronJobState{
			NextRunAtMS: cs.computeNextRun(&schedule, now),
		},
		CreatedAtMS:    now,
		UpdatedAtMS:    now,
		DeleteAfterRun: deleteAfterRun,
	}

	cs.store.Jobs = append(cs.store.Jobs, job)
	if err := cs.saveStoreUnsafe(); err != nil {
		return nil, err
	}

	cs.notify()

	return &job, nil
}

func (cs *CronService) GetJob(jobID string) (*CronJob, bool) {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			jobCopy := cloneCronJob(cs.store.Jobs[i])
			return &jobCopy, true
		}
	}
	return nil, false
}

func (cs *CronService) UpdateJob(job *CronJob) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			previous := cs.store.Jobs[i]
			updated := cloneCronJob(*job)
			now := time.Now().UnixMilli()
			updated.UpdatedAtMS = now
			if updated.Enabled {
				if previous.Enabled != updated.Enabled || !sameSchedule(previous.Schedule, updated.Schedule) {
					updated.State.NextRunAtMS = cs.computeNextRun(&updated.Schedule, now)
				}
			} else {
				updated.State.NextRunAtMS = nil
			}
			cs.store.Jobs[i] = updated

			cs.notify()

			return cs.saveStoreUnsafe()
		}
	}
	return fmt.Errorf("job not found")
}

func cloneCronJob(job CronJob) CronJob {
	clone := job
	if job.Schedule.AtMS != nil {
		atMS := *job.Schedule.AtMS
		clone.Schedule.AtMS = &atMS
	}
	if job.Schedule.EveryMS != nil {
		everyMS := *job.Schedule.EveryMS
		clone.Schedule.EveryMS = &everyMS
	}
	if job.State.NextRunAtMS != nil {
		nextRunAtMS := *job.State.NextRunAtMS
		clone.State.NextRunAtMS = &nextRunAtMS
	}
	if job.State.LastRunAtMS != nil {
		lastRunAtMS := *job.State.LastRunAtMS
		clone.State.LastRunAtMS = &lastRunAtMS
	}
	return clone
}

func sameSchedule(a, b CronSchedule) bool {
	return a.Kind == b.Kind &&
		sameInt64(a.AtMS, b.AtMS) &&
		sameInt64(a.EveryMS, b.EveryMS) &&
		a.Expr == b.Expr &&
		a.TZ == b.TZ
}

func sameInt64(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (cs *CronService) RemoveJob(jobID string) bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	return cs.removeJobUnsafe(jobID)
}

func (cs *CronService) removeJobUnsafe(jobID string) bool {
	before := len(cs.store.Jobs)
	var jobs []CronJob
	for _, job := range cs.store.Jobs {
		if job.ID != jobID {
			jobs = append(jobs, job)
		}
	}
	cs.store.Jobs = jobs
	removed := len(cs.store.Jobs) < before

	if removed {
		if err := cs.saveStoreUnsafe(); err != nil {
			log.Printf("[cron] failed to save store after remove: %v", err)
		}
	}

	cs.notify()

	return removed
}

func (cs *CronService) EnableJob(jobID string, enabled bool) *CronJob {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	cs.syncFromDiskUnsafe()

	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.ID == jobID {
			job.Enabled = enabled
			job.UpdatedAtMS = time.Now().UnixMilli()

			if enabled {
				job.State.NextRunAtMS = cs.computeNextRun(&job.Schedule, time.Now().UnixMilli())
			} else {
				job.State.NextRunAtMS = nil
			}

			if err := cs.saveStoreUnsafe(); err != nil {
				log.Printf("[cron] failed to save store after enable: %v", err)
			}

			cs.notify()

			updated := cloneCronJob(*job)
			return &updated
		}
	}

	return nil
}

func (cs *CronService) ListJobs(includeDisabled bool) []CronJob {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	jobs := make([]CronJob, 0, len(cs.store.Jobs))
	for _, job := range cs.store.Jobs {
		if !includeDisabled && !job.Enabled {
			continue
		}
		jobs = append(jobs, cloneCronJob(job))
	}
	return jobs
}

func (cs *CronService) Status() map[string]any {
	cs.mu.RLock()
	defer cs.mu.RUnlock()

	var enabledCount int
	for _, job := range cs.store.Jobs {
		if job.Enabled {
			enabledCount++
		}
	}

	return map[string]any{
		"enabled":      cs.running,
		"jobs":         len(cs.store.Jobs),
		"nextWakeAtMS": cs.getNextWakeMS(),
	}
}

func generateID() string {
	// Use crypto/rand for better uniqueness under concurrent access
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback to time-based if crypto/rand fails
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}
