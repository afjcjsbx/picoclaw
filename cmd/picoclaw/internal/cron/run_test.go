package cron

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/pid"
)

func TestNewRunSubcommand(t *testing.T) {
	cmd := newRunCommand()

	require.NotNil(t, cmd)
	assert.Equal(t, "run", cmd.Name())
	assert.True(t, cmd.HasExample())
	require.Error(t, cmd.Args(cmd, nil), "run requires a job id")
	require.NoError(t, cmd.Args(cmd, []string{"1"}))
	require.Error(t, cmd.Args(cmd, []string{"1", "2"}))
}

func TestPostCronRun(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantErr string
	}{
		{"accepted", http.StatusAccepted, `{"status":"triggered"}`, ""},
		{"not found", http.StatusNotFound, `{"error":"job not found"}`, "not found in the running gateway"},
		{"already running", http.StatusConflict, `{"error":"job is already running"}`, "job j1 is already running"},
		{"unauthorized", http.StatusUnauthorized, `{"error":"unauthorized"}`, "rejected the auth token"},
		{
			"unavailable", http.StatusServiceUnavailable,
			`{"error":"cron service is not running"}`, "503: cron service is not running",
		},
		{"no error body", http.StatusBadGateway, ``, "502: Bad Gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/cron/run", r.URL.Path)
				assert.Equal(t, "Bearer tok", r.Header.Get("Authorization"))
				var req map[string]string
				require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
				assert.Equal(t, "j1", req["job_id"])
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			err := postCronRun(srv.Client(), srv.URL, "tok", "j1")
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestPostCronRun_GatewayUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	require.ErrorContains(t, postCronRun(http.DefaultClient, url, "tok", "j1"), "could not reach the gateway")
}

func TestGatewayURL(t *testing.T) {
	assert.Equal(t, "http://127.0.0.1:18790", gatewayURL(&pid.PidFileData{Host: "127.0.0.1", Port: 18790}))
	assert.Equal(t, "http://[::1]:9", gatewayURL(&pid.PidFileData{Host: "::1", Port: 9}))
}
