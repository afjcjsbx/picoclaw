package cron

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/sipeed/picoclaw/cmd/picoclaw/internal"
	"github.com/sipeed/picoclaw/pkg/netbind"
	"github.com/sipeed/picoclaw/pkg/pid"
)

const runRequestTimeout = 10 * time.Second

// newRunCommand asks the running gateway to execute a job now. Jobs execute
// inside the gateway process, which already serializes manual and scheduled
// runs of the same job; a separate CLI process could not offer that guarantee.
func newRunCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run a job now via the running gateway",
		Long: "Run a job immediately, without changing its schedule. The job runs inside the running gateway " +
			"(its output goes to the job's channel) and this command returns once the gateway has started it.",
		Args:    cobra.ExactArgs(1),
		Example: `picoclaw cron run 1`,
		RunE: func(_ *cobra.Command, args []string) error {
			data := pid.ReadPidFileWithCheck(internal.GetPicoclawHome())
			if data == nil {
				return errors.New(
					"gateway is not running: jobs run inside the gateway, start it with `picoclaw gateway`",
				)
			}
			client := &http.Client{Timeout: runRequestTimeout}
			if err := postCronRun(client, gatewayURL(data), data.Token, args[0]); err != nil {
				return err
			}
			fmt.Printf("✓ Triggered job %s on the running gateway\n", args[0])
			return nil
		},
	}
}

func gatewayURL(data *pid.PidFileData) string {
	host := data.Host
	if host == "" {
		host = netbind.ResolveAdaptiveLoopbackHost()
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(data.Port))
}

// postCronRun calls the gateway's POST /cron/run and turns its answer into an error.
func postCronRun(client *http.Client, baseURL, token, jobID string) error {
	body, err := json.Marshal(map[string]string{"job_id": jobID})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/cron/run", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the gateway at %s: %w", baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusAccepted {
		return nil
	}

	var payload struct {
		Error string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
	_ = json.Unmarshal(raw, &payload)

	switch resp.StatusCode {
	case http.StatusNotFound:
		// The gateway only knows the jobs it loaded at startup, not ones added
		// to jobs.json since (e.g. with `picoclaw cron add`).
		return fmt.Errorf(
			"job %s not found in the running gateway (jobs added since it started need a gateway restart)",
			jobID,
		)
	case http.StatusConflict:
		return fmt.Errorf("job %s is already running", jobID)
	case http.StatusUnauthorized:
		return errors.New("gateway rejected the auth token from the pid file (stale pid file or another gateway?)")
	default:
		if payload.Error == "" {
			payload.Error = http.StatusText(resp.StatusCode)
		}
		return fmt.Errorf("gateway returned %d: %s", resp.StatusCode, payload.Error)
	}
}
