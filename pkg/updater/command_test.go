package updater

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteTransport sends every request to the test server while keeping the
// original path, so tests can exercise the code that builds GitHub API URLs.
type rewriteTransport struct {
	target *url.URL
}

func (rt rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = rt.target.Scheme
	clone.URL.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(clone)
}

func useReleaseTestServer(t *testing.T, tag string) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeReleasePayload(w, testReleasePayload{TagName: tag})
	}))
	t.Cleanup(server.Close)

	target, err := url.Parse(server.URL)
	require.NoError(t, err)

	withTestHTTPClient(t, &http.Client{Transport: rewriteTransport{target: target}})
}

func stubUpdateSelf(t *testing.T) *bool {
	t.Helper()

	applied := new(bool)
	original := updateSelfFn
	updateSelfFn = func(_ *releaseInfo, _, _, _ string) error {
		*applied = true
		return nil
	}
	t.Cleanup(func() { updateSelfFn = original })
	return applied
}

func stubStdinTerminal(t *testing.T, terminal bool) {
	t.Helper()

	original := stdinIsTerminalFn
	stdinIsTerminalFn = func(io.Reader) bool { return terminal }
	t.Cleanup(func() { stdinIsTerminalFn = original })
}

func executeUpdate(t *testing.T, opts updateOptions, stdin string) (string, error) {
	t.Helper()

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetIn(strings.NewReader(stdin))

	err := runUpdate(cmd, opts)
	return out.String(), err
}

func TestUpdateCheckOnly(t *testing.T) {
	tests := []struct {
		name     string
		current  string
		latest   string
		contains []string
		absent   []string
	}{
		{
			name:     "update available",
			current:  "v0.5.4",
			latest:   "v0.5.6",
			contains: []string{"Update available: v0.5.4 -> v0.5.6", "Run without --check"},
		},
		{
			name:     "git describe is behind latest release",
			current:  "v0.5.4-128-ge5915850",
			latest:   "v0.5.6",
			contains: []string{"Update available: v0.5.4-128-ge5915850 -> v0.5.6"},
		},
		{
			name:     "already up to date",
			current:  "v0.5.6",
			latest:   "v0.5.6",
			contains: []string{"Already up to date."},
			absent:   []string{"Update available", "Run without --check"},
		},
		{
			name:     "release binary without v prefix is already up to date",
			current:  "0.5.6",
			latest:   "v0.5.6",
			contains: []string{"Already up to date."},
			absent:   []string{"Update available", "Run without --check"},
		},
		{
			name:     "development build ahead of latest release",
			current:  "v0.5.6-12-gabc1234",
			latest:   "v0.5.6",
			contains: []string{"Current version is newer than the latest release"},
			absent:   []string{"Update available"},
		},
		{
			name:     "dev build cannot be compared",
			current:  "dev",
			latest:   "v0.5.6",
			contains: []string{"The versions cannot be compared automatically."},
			absent:   []string{"Update available", "Already up to date"},
		},
		{
			name:     "nightly tag cannot be compared",
			current:  "v0.5.6",
			latest:   "nightly",
			contains: []string{"The versions cannot be compared automatically."},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			useReleaseTestServer(t, tc.latest)
			applied := stubUpdateSelf(t)

			out, err := executeUpdate(t, updateOptions{
				binaryName: "picoclaw",
				checkOnly:  true,
				current:    tc.current,
				display:    tc.current,
			}, "")
			require.NoError(t, err)

			for _, want := range tc.contains {
				assert.Contains(t, out, want)
			}
			for _, unwanted := range tc.absent {
				assert.NotContains(t, out, unwanted)
			}
			assert.False(t, *applied, "--check must not apply the update")
		})
	}
}

func TestUpdateDoesNotApplyWhenUpToDate(t *testing.T) {
	useReleaseTestServer(t, "v0.5.6")
	applied := stubUpdateSelf(t)
	stubStdinTerminal(t, true)

	out, err := executeUpdate(t, updateOptions{
		binaryName: "picoclaw",
		current:    "0.5.6",
		display:    "0.5.6 (git: abc1234)",
	}, "y\n")
	require.NoError(t, err)

	assert.Contains(t, out, "Already up to date.")
	assert.NotContains(t, out, "[y/N]")
	assert.NotContains(t, out, "Update available")
	assert.False(t, *applied)
}

func TestUpdateAppliesAcceptedConfirmation(t *testing.T) {
	useReleaseTestServer(t, "v0.5.6")
	applied := stubUpdateSelf(t)
	stubStdinTerminal(t, true)

	out, err := executeUpdate(t, updateOptions{
		binaryName: "picoclaw",
		current:    "v0.5.4",
		display:    "v0.5.4",
	}, "y\n")
	require.NoError(t, err)

	assert.Contains(t, out, "Update to v0.5.6? [y/N]:")
	assert.Contains(t, out, "Warning: do not restart or power off the device")
	assert.True(t, *applied)
	assert.Contains(t, out, "Update applied; restart to use the new version.")
}

func TestUpdateCancelsRejectedConfirmation(t *testing.T) {
	useReleaseTestServer(t, "v0.5.6")
	applied := stubUpdateSelf(t)
	stubStdinTerminal(t, true)

	out, err := executeUpdate(t, updateOptions{
		binaryName: "picoclaw",
		current:    "v0.5.4",
		display:    "v0.5.4",
	}, "n\n")
	require.NoError(t, err)

	assert.Contains(t, out, "Update canceled.")
	assert.False(t, *applied)
}

func TestUpdateAppliesWithoutPromptWhenNotInteractive(t *testing.T) {
	useReleaseTestServer(t, "v0.5.6")
	applied := stubUpdateSelf(t)
	stubStdinTerminal(t, false)

	out, err := executeUpdate(t, updateOptions{
		binaryName: "picoclaw",
		current:    "v0.5.4",
		display:    "v0.5.4",
	}, "")
	require.NoError(t, err)

	assert.Contains(t, out, "Applying update without confirmation")
	assert.True(t, *applied)
}

func TestUpdateYesSkipsConfirmation(t *testing.T) {
	useReleaseTestServer(t, "v0.5.6")
	applied := stubUpdateSelf(t)
	stubStdinTerminal(t, true)

	out, err := executeUpdate(t, updateOptions{
		binaryName: "picoclaw",
		assumeYes:  true,
		current:    "v0.5.4",
		display:    "v0.5.4",
	}, "")
	require.NoError(t, err)

	assert.NotContains(t, out, "[y/N]")
	assert.True(t, *applied)
}

func TestUpdateCustomURLAllowsOlderRelease(t *testing.T) {
	useReleaseTestServer(t, "v0.5.4")
	applied := stubUpdateSelf(t)

	out, err := executeUpdate(t, updateOptions{
		releaseURL: "https://github.com/afjcjsbx/picoclaw/releases/tag/v0.5.4",
		binaryName: "picoclaw",
		checkOnly:  true,
		current:    "v0.5.6",
		display:    "v0.5.6",
	}, "")
	require.NoError(t, err)

	assert.Contains(t, out, "Requested release v0.5.4 is older than the current version.")
	assert.False(t, *applied)
}

func TestConfirmUpdate(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes", input: "y\n", want: true},
		{name: "yes word", input: "YES\n", want: true},
		{name: "yes with spaces", input: " yes \n", want: true},
		{name: "no", input: "n\n", want: false},
		{name: "blank line", input: "\n", want: false},
		{name: "eof", input: "", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer

			got, err := confirmUpdate(strings.NewReader(tc.input), &out, "v0.5.6")
			require.NoError(t, err)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, "Update to v0.5.6? [y/N]: ", out.String())
		})
	}
}
