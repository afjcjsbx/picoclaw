package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/health"
	"github.com/sipeed/picoclaw/pkg/logger"
)

var (
	gatewayReloadRequestTimeout = 5 * time.Second
	gatewayReloadWaitTimeout    = 15 * time.Second
	gatewayReloadPollInterval   = 100 * time.Millisecond
)

const gatewayReloadMaxAttempts = 3

// gatewayReloadTarget returns the base URL and bearer token of the running
// gateway. The second return value is false when the gateway is not running.
func gatewayReloadTarget() (string, string, bool) {
	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	if gatewayStatusWithoutHealthLocked() != "running" {
		return "", "", false
	}
	pidData := gateway.pidData
	if pidData == nil || pidData.Port <= 0 {
		return "", "", false
	}

	host := gatewayProbeHost(pidData.Host)
	baseURL := "http://" + net.JoinHostPort(host, strconv.Itoa(pidData.Port))
	return baseURL, pidData.Token, true
}

// reloadGatewayAfterModelSave applies a saved model configuration to a running
// gateway. Failure to reload does not fail the save: the config is already
// persisted and the dashboard will keep showing the restart-required banner.
func (h *Handler) reloadGatewayAfterModelSave(cfg *config.Config) {
	if err := h.reloadGatewayAfterConfigSave(cfg); err != nil {
		logger.Warnf("gateway reload after model config change failed: %v", err)
	}
}

// reloadGatewayAfterConfigSave reloads a running gateway so model changes take
// effect without restarting the process. It waits for the gateway loop to
// report completion, then records the saved config as the new boot signature
// so the dashboard no longer shows a false "restart required" banner.
//
// A stopped gateway is a no-op: the next start reads the saved config anyway.
func (h *Handler) reloadGatewayAfterConfigSave(appliedCfg *config.Config) error {
	baseURL, token, ok := gatewayReloadTarget()
	if !ok {
		return nil
	}

	deadline := time.Now().Add(gatewayReloadWaitTimeout)
	for attempt := 0; attempt < gatewayReloadMaxAttempts; attempt++ {
		before, err := fetchGatewayReloadStatus(baseURL, token)
		if err != nil {
			// Older gateways expose /reload but not /reload/status. Trigger the
			// reload anyway; the banner stays visible until the next restart.
			_, _, _ = postGatewayReload(baseURL, token)
			return fmt.Errorf("read gateway reload status: %w", err)
		}
		if before.InProgress {
			if err = waitForGatewayReloadIdle(baseURL, token, deadline); err != nil {
				return err
			}
			continue
		}

		postAt := time.Now()
		statusCode, body, err := postGatewayReload(baseURL, token)
		if err != nil {
			return fmt.Errorf("trigger gateway reload: %w", err)
		}
		if statusCode == http.StatusAccepted || statusCode == http.StatusOK {
			status, waitErr := waitForGatewayReloadAfter(
				baseURL,
				token,
				before.Generation,
				postAt,
				deadline,
			)
			if waitErr != nil {
				return waitErr
			}
			if status.LastError != "" {
				return fmt.Errorf("gateway reload failed: %s", status.LastError)
			}
			h.markGatewayConfigApplied(appliedCfg)
			return nil
		}
		if statusCode == http.StatusInternalServerError && strings.Contains(body, "reload already in progress") {
			if err = waitForGatewayReloadIdle(baseURL, token, deadline); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("trigger gateway reload: status %d: %s", statusCode, body)
	}

	return fmt.Errorf("gateway reload did not start: another reload kept running")
}

func (h *Handler) markGatewayConfigApplied(cfg *config.Config) {
	if cfg == nil {
		return
	}

	gateway.mu.Lock()
	defer gateway.mu.Unlock()

	// Do not resurrect a stale signature if the gateway stopped while waiting.
	if gatewayStatusWithoutHealthLocked() != "running" {
		return
	}
	gateway.bootDefaultModel = strings.TrimSpace(cfg.Agents.Defaults.GetModelName())
	gateway.bootConfigSignature = computeConfigSignature(cfg)
}

func fetchGatewayReloadStatus(baseURL, token string) (*health.ReloadStatus, error) {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/reload/status", nil)
	if err != nil {
		return nil, err
	}
	setGatewayReloadAuth(req, token)

	client := &http.Client{Timeout: gatewayReloadRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var status health.ReloadStatus
	if err = json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &status, nil
}

func postGatewayReload(baseURL, token string) (int, string, error) {
	req, err := http.NewRequest(http.MethodPost, baseURL+"/reload", nil)
	if err != nil {
		return 0, "", err
	}
	setGatewayReloadAuth(req, token)

	client := &http.Client{Timeout: gatewayReloadRequestTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return resp.StatusCode, strings.TrimSpace(string(body)), nil
}

func setGatewayReloadAuth(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func waitForGatewayReloadIdle(baseURL, token string, deadline time.Time) error {
	for {
		status, err := fetchGatewayReloadStatus(baseURL, token)
		if err == nil && !status.InProgress {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return fmt.Errorf("wait for gateway reload: %w", err)
			}
			return fmt.Errorf("timed out waiting for gateway reload to finish")
		}
		time.Sleep(gatewayReloadPollInterval)
	}
}

func waitForGatewayReloadAfter(
	baseURL, token string,
	generation uint64,
	startedAfter time.Time,
	deadline time.Time,
) (*health.ReloadStatus, error) {
	var lastErr error
	for {
		status, err := fetchGatewayReloadStatus(baseURL, token)
		if err == nil &&
			status.Generation > generation &&
			!status.InProgress &&
			status.StartedAt != nil &&
			!status.StartedAt.Before(startedAfter) {
			return status, nil
		}
		if err != nil {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("wait for gateway reload: %w", lastErr)
			}
			return nil, fmt.Errorf("timed out waiting for gateway reload to finish")
		}
		time.Sleep(gatewayReloadPollInterval)
	}
}
