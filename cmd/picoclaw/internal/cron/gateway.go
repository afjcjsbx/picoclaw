package cron

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/netbind"
	"github.com/sipeed/picoclaw/pkg/pid"
)

const gatewayCronJobsPath = "/internal/cron/jobs"

// errGatewayJobNotFound maps a 404 from the gateway API to the CLI's
// not-found handling, without treating it as a transport failure.
var errGatewayJobNotFound = errors.New("job not found")

type gatewayClient struct {
	baseURL string
	token   string
	http    *http.Client
}

// newGatewayStore returns a store backed by the running gateway, or nil when
// no gateway is running. A nil result tells the caller to use jobs.json
// directly; a non-nil result must never fall back to the file, because the
// gateway owns the in-memory schedule and would overwrite the edit.
func newGatewayStore(home string) jobStore {
	data := pid.ReadPidFileWithCheck(home)
	if data == nil || data.Token == "" || data.Port <= 0 {
		return nil
	}
	return gatewayStore{client: &gatewayClient{
		baseURL: "http://" + net.JoinHostPort(
			normalizeGatewayHost(data.Host),
			strconv.Itoa(data.Port),
		),
		token: data.Token,
		http:  &http.Client{Timeout: 5 * time.Second},
	}}
}

func normalizeGatewayHost(host string) string {
	switch strings.TrimSpace(host) {
	case "", "0.0.0.0", "::", "[::]":
		return netbind.ResolveAdaptiveLoopbackHost()
	default:
		return strings.TrimSpace(host)
	}
}

type gatewayStore struct {
	client *gatewayClient
}

func (s gatewayStore) List(ctx context.Context) ([]cron.CronJob, error) {
	var response struct {
		Jobs []cron.CronJob `json:"jobs"`
	}
	if err := s.client.do(ctx, http.MethodGet, gatewayCronJobsPath, nil, &response); err != nil {
		return nil, err
	}
	return response.Jobs, nil
}

type gatewayAddRequest struct {
	Name     string            `json:"name"`
	Message  string            `json:"message"`
	Schedule cron.CronSchedule `json:"schedule"`
	Channel  string            `json:"channel,omitempty"`
	To       string            `json:"to,omitempty"`
}

func (s gatewayStore) Add(
	ctx context.Context,
	name string,
	schedule cron.CronSchedule,
	message, channel, to string,
) (*cron.CronJob, error) {
	var job cron.CronJob
	err := s.client.do(ctx, http.MethodPost, gatewayCronJobsPath, gatewayAddRequest{
		Name:     name,
		Message:  message,
		Schedule: schedule,
		Channel:  channel,
		To:       to,
	}, &job)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s gatewayStore) Remove(ctx context.Context, id string) (bool, error) {
	err := s.client.do(ctx, http.MethodDelete, gatewayCronJobsPath+"/"+url.PathEscape(id), nil, nil)
	if errors.Is(err, errGatewayJobNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s gatewayStore) SetEnabled(ctx context.Context, id string, enabled bool) (*cron.CronJob, error) {
	var job cron.CronJob
	err := s.client.do(ctx, http.MethodPatch, gatewayCronJobsPath+"/"+url.PathEscape(id), map[string]bool{
		"enabled": enabled,
	}, &job)
	if errors.Is(err, errGatewayJobNotFound) {
		return nil, cron.ErrJobNotFound
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (c *gatewayClient) do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode gateway request: %w", err)
		}
		payload = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("build gateway request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf(
			"gateway is running but its API at %s is unreachable; stop the gateway to edit jobs.json directly: %w",
			c.baseURL, err,
		)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read gateway response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return errGatewayJobNotFound
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(data))
		var apiError struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &apiError) == nil && apiError.Error != "" {
			message = apiError.Error
		}
		return fmt.Errorf("gateway API returned %s: %s", resp.Status, message)
	}

	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode gateway response: %w", err)
		}
	}
	return nil
}
