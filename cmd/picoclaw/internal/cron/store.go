package cron

import (
	"context"

	"github.com/sipeed/picoclaw/pkg/cron"
)

// jobStore is the CLI's view of the cron store. The running gateway exposes it
// over its authenticated internal API; when no gateway is running, the CLI
// reads and writes jobs.json directly.
type jobStore interface {
	List(ctx context.Context) ([]cron.CronJob, error)
	Add(
		ctx context.Context,
		name string,
		schedule cron.CronSchedule,
		message, channel, to string,
	) (*cron.CronJob, error)
	Remove(ctx context.Context, id string) (bool, error)
	SetEnabled(ctx context.Context, id string, enabled bool) (*cron.CronJob, error)
}

// fileStore is used only while no gateway is running. In that case no process
// holds an in-memory copy of the store, so editing the file is safe.
type fileStore struct {
	path string
}

// service opens the store and surfaces a load error instead of letting the
// constructor silently fall back to an empty store. Offline CLI operations must
// never overwrite a corrupt jobs.json with a partial view of its contents.
func (s fileStore) service() (*cron.CronService, error) {
	cs := cron.NewCronService(s.path, nil)
	if err := cs.Load(); err != nil {
		return nil, err
	}
	return cs, nil
}

func (s fileStore) List(context.Context) ([]cron.CronJob, error) {
	cs, err := s.service()
	if err != nil {
		return nil, err
	}
	return cs.ListJobs(true), nil
}

func (s fileStore) Add(
	_ context.Context,
	name string,
	schedule cron.CronSchedule,
	message, channel, to string,
) (*cron.CronJob, error) {
	cs, err := s.service()
	if err != nil {
		return nil, err
	}
	return cs.AddJob(name, schedule, message, channel, to)
}

func (s fileStore) Remove(_ context.Context, id string) (bool, error) {
	cs, err := s.service()
	if err != nil {
		return false, err
	}
	return cs.RemoveJob(id), nil
}

func (s fileStore) SetEnabled(_ context.Context, id string, enabled bool) (*cron.CronJob, error) {
	cs, err := s.service()
	if err != nil {
		return nil, err
	}
	job := cs.EnableJob(id, enabled)
	if job == nil {
		return nil, cron.ErrJobNotFound
	}
	return job, nil
}
