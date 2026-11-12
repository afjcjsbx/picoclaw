package cron

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
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

// CronRunRecord is one past execution of a job. The scheduler keeps a small
// bounded history so the dashboard can show recent runs without parsing logs.
type CronRunRecord struct {
	RunAtMS    int64  `json:"runAtMs"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

type CronJobState struct {
	NextRunAtMS *int64          `json:"nextRunAtMs,omitempty"`
	LastRunAtMS *int64          `json:"lastRunAtMs,omitempty"`
	LastStatus  string          `json:"lastStatus,omitempty"`
	LastError   string          `json:"lastError,omitempty"`
	RunHistory  []CronRunRecord `json:"runHistory,omitempty"`
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

// cronRunHistoryLimit caps how many past runs are retained per job.
const cronRunHistoryLimit = 10

var (
	ErrJobRunning        = errors.New("job is already running")
	ErrJobNotFound       = errors.New("job not found")
	ErrServiceNotRunning = errors.New("cron service is not running")
)

type CronService struct {
	storePath string
	store     *CronStore
	onJob     JobHandler
	mu        sync.RWMutex
	running   bool
	stopChan  chan struct{}
	wakeChan  chan struct{}
	gronx     *gronx.Gronx
	// inFlight holds the IDs of jobs whose handler is executing, guarded by mu.
	// A job is claimed under mu before its goroutine starts and released when
	// executeJobByID returns, so a job never runs concurrently with itself.
	inFlight map[string]struct{}
}

func NewCronService(storePath string, onJob JobHandler) *CronService {
	cs := &CronService{
		storePath: storePath,
		onJob:     onJob,
		gronx:     gronx.New(),
		wakeChan:  make(chan struct{}, 1),
		inFlight:  make(map[string]struct{}),
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

	for {
		// every loop, recalculate the next wake time
		cs.mu.RLock()
		nextWake := cs.getNextWakeMS()
		cs.mu.RUnlock()

		// With no jobs there is nothing to poll: block until a job is added or
		// the service is stopped. Job mutations notify the loop.
		var timerC <-chan time.Time
		if nextWake == nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		} else {
			now := time.Now().UnixMilli()
			delay := time.Duration(*nextWake-now) * time.Millisecond
			if *nextWake <= now {
				delay = 0
			}
			timer.Reset(delay)
			timerC = timer.C
		}

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
		case <-timerC:
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

	now := time.Now().UnixMilli()
	var dueJobIDs []string

	// Collect jobs that are due (we need to copy them to execute outside lock)
	for i := range cs.store.Jobs {
		job := &cs.store.Jobs[i]
		if job.Enabled && job.State.NextRunAtMS != nil && *job.State.NextRunAtMS <= now {
			// Claim under the same lock RunJob uses: whoever claims first runs the
			// job, the other side backs off. The skipped occurrence is covered by
			// the run already in flight and the schedule is advanced below.
			if !cs.claimJobUnsafe(job.ID) {
				log.Printf("[cron] job '%s' (id: %s) is still running, skipping scheduled run", job.Name, job.ID)
				continue
			}
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
		go cs.executeClaimedJob(jobID)
	}
}

// claimJobUnsafe marks jobID as executing and reports false if it already is.
// The caller must hold cs.mu and must follow a successful claim with
// executeJobByID, which releases it.
func (cs *CronService) claimJobUnsafe(jobID string) bool {
	if _, busy := cs.inFlight[jobID]; busy {
		return false
	}
	cs.inFlight[jobID] = struct{}{}
	return true
}

func (cs *CronService) releaseJob(jobID string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.inFlight, jobID)
}

// executeClaimedJob runs a job already claimed via claimJobUnsafe, containing
// any panic so it cannot take down the process.
func (cs *CronService) executeClaimedJob(jobID string) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[cron] recovered panic while executing job %s: %v", jobID, r)
		}
	}()
	cs.executeJobByID(jobID)
}

// RunJob starts a job immediately, regardless of its schedule or enabled state,
// and returns without waiting for the handler. The schedule is left untouched,
// except that one-time jobs are finalized as after any other run. It returns
// ErrJobRunning if the job is already executing (scheduled or manual).
func (cs *CronService) RunJob(jobID string) error {
	cs.mu.Lock()
	defer cs.mu.Unlock()

	if !cs.running {
		return ErrServiceNotRunning
	}
	found := false
	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == jobID {
			found = true
			break
		}
	}
	if !found {
		return ErrJobNotFound
	}
	if !cs.claimJobUnsafe(jobID) {
		return ErrJobRunning
	}

	go cs.executeClaimedJob(jobID)
	return nil
}

func (cs *CronService) executeJobByID(jobID string) {
	defer cs.releaseJob(jobID)

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

	appendCronRun(job, CronRunRecord{
		RunAtMS:    startTime,
		Status:     job.State.LastStatus,
		Error:      job.State.LastError,
		DurationMS: execDuration,
	})

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
	return fileutil.WriteFileAtomic(cs.storePath, data, 0o600)
}

// validateCronSchedule rejects schedules that cannot produce a next run.
func validateCronSchedule(schedule CronSchedule) error {
	switch schedule.Kind {
	case "at":
		if schedule.AtMS == nil {
			return errors.New("at schedule requires atMs")
		}
	case "every":
		if schedule.EveryMS == nil || *schedule.EveryMS <= 0 {
			return errors.New("every schedule requires a positive everyMs")
		}
	case "cron":
		expr := strings.TrimSpace(schedule.Expr)
		if expr == "" {
			return errors.New("cron schedule requires an expression")
		}
		if !gronx.IsValid(expr) {
			return fmt.Errorf("invalid cron expression %q", schedule.Expr)
		}
		if schedule.TZ != "" {
			if _, err := time.LoadLocation(schedule.TZ); err != nil {
				return fmt.Errorf("invalid timezone %q: %w", schedule.TZ, err)
			}
		}
	default:
		return fmt.Errorf("unknown schedule kind %q", schedule.Kind)
	}
	return nil
}

func (cs *CronService) AddJob(
	name string,
	schedule CronSchedule,
	message string,
	channel, to string,
) (*CronJob, error) {
	if err := validateCronSchedule(schedule); err != nil {
		return nil, err
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

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

	for i := range cs.store.Jobs {
		if cs.store.Jobs[i].ID == job.ID {
			previous := cs.store.Jobs[i]
			updated := cloneCronJob(*job)
			if len(updated.State.RunHistory) == 0 && len(previous.State.RunHistory) > 0 {
				updated.State.RunHistory = append([]CronRunRecord(nil), previous.State.RunHistory...)
			}
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
	return ErrJobNotFound
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
	if len(job.State.RunHistory) > 0 {
		clone.State.RunHistory = append([]CronRunRecord(nil), job.State.RunHistory...)
	}
	return clone
}

// appendCronRun prepends a run record and keeps the newest entries bounded.
func appendCronRun(job *CronJob, record CronRunRecord) {
	history := make([]CronRunRecord, 0, min(len(job.State.RunHistory)+1, cronRunHistoryLimit))
	history = append(history, record)
	for _, previous := range job.State.RunHistory {
		if len(history) >= cronRunHistoryLimit {
			break
		}
		history = append(history, previous)
	}
	job.State.RunHistory = history
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
