package updater

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"time"
)

// matchesMagic checks whether the file at path looks like a platform binary
// by inspecting magic bytes (ELF for linux, MZ for windows).
func matchesMagic(path, platform string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	buf := make([]byte, 4)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return false, err
	}
	if n >= 4 && buf[0] == 0x7f && buf[1] == 'E' && buf[2] == 'L' && buf[3] == 'F' {
		return strings.Contains(platform, "linux"), nil
	}
	if n >= 2 && buf[0] == 'M' && buf[1] == 'Z' {
		return strings.Contains(platform, "windows"), nil
	}
	return false, nil
}

type testReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest,omitempty"`
}

type testReleasePayload struct {
	TagName string             `json:"tag_name"`
	Assets  []testReleaseAsset `json:"assets"`
}

const testReleaseAPIPath = "/api.github.com/repos/afjcjsbx/picoclaw/releases/latest"

// TestDownloadAndExtractRelease_IntegrationLatestRelease downloads the latest
// public release for a single platform as an opt-in smoke test.
func TestDownloadAndExtractRelease_IntegrationLatestRelease(t *testing.T) {
	if os.Getenv("PICOCLAW_INTEGRATION_TESTS") == "" {
		t.Skip("skipping integration test (set PICOCLAW_INTEGRATION_TESTS=1 to enable)")
	}
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	const platform = "linux"
	const arch = "amd64"
	apiURL := GetProdReleaseAPIURL()
	assetURL, checksum, err := findAssetInfo(apiURL, platform, arch)
	if err != nil {
		t.Fatalf("findAssetInfo failed for %s/%s: %v", platform, arch, err)
	}
	t.Logf("asset URL: %s checksum: %s", assetURL, checksum)

	dir, err := DownloadAndExtractRelease(apiURL, platform, arch)
	if err != nil {
		t.Fatalf("DownloadAndExtractRelease failed for %s/%s: %v", platform, arch, err)
	}
	defer os.RemoveAll(dir)

	var found bool
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() < 64 {
			return nil
		}
		ok, err := matchesMagic(path, platform)
		if err != nil {
			return err
		}
		if ok {
			found = true
			t.Logf("found artifact: %s (size=%d)", path, info.Size())
		}
		return nil
	})
	if !found {
		t.Fatalf("no binary-like artifact found for %s/%s", platform, arch)
	}
}

func TestFindAssetInfo_SelectsPreferredAsset(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testReleaseAPIPath:
			writeReleasePayload(w, testReleasePayload{
				TagName: "v0.2.6",
				Assets: []testReleaseAsset{
					{
						Name:               "picoclaw_Linux_x86_64.zip",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_x86_64.zip",
						Digest:             "sha256:" + strings.Repeat("1", 64),
					},
					{
						Name:               "picoclaw_Linux_x86_64.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_x86_64.tar.gz",
						Digest:             "sha256:" + strings.Repeat("2", 64),
					},
					{
						Name:               "picoclaw_Windows_x86_64.zip",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Windows_x86_64.zip",
						Digest:             "sha256:" + strings.Repeat("3", 64),
					},
					{
						Name:               "picoclaw_Windows_arm64.zip",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Windows_arm64.zip",
						Digest:             "sha256:" + strings.Repeat("4", 64),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestHTTPClient(t, server.Client())

	tests := []struct {
		name         string
		platform     string
		arch         string
		wantURL      string
		wantChecksum string
	}{
		{
			name:         "linux prefers tar.gz over zip",
			platform:     "linux",
			arch:         "amd64",
			wantURL:      server.URL + "/assets/picoclaw_Linux_x86_64.tar.gz",
			wantChecksum: strings.Repeat("2", 64),
		},
		{
			name:         "windows amd64 matches x86_64 zip",
			platform:     "windows",
			arch:         "amd64",
			wantURL:      server.URL + "/assets/picoclaw_Windows_x86_64.zip",
			wantChecksum: strings.Repeat("3", 64),
		},
		{
			name:         "windows arm64 matches arm64 zip",
			platform:     "windows",
			arch:         "arm64",
			wantURL:      server.URL + "/assets/picoclaw_Windows_arm64.zip",
			wantChecksum: strings.Repeat("4", 64),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotURL, gotChecksum, err := findAssetInfo(server.URL+testReleaseAPIPath, tc.platform, tc.arch)
			if err != nil {
				t.Fatalf(
					"findAssetInfo(%q, %q, %q) error: %v",
					server.URL+testReleaseAPIPath,
					tc.platform,
					tc.arch,
					err,
				)
			}
			if gotURL != tc.wantURL {
				t.Fatalf("assetURL = %q, want %q", gotURL, tc.wantURL)
			}
			if gotChecksum != tc.wantChecksum {
				t.Fatalf("checksum = %q, want %q", gotChecksum, tc.wantChecksum)
			}
		})
	}
}

func TestFindAssetInfo_UsesChecksumAssetWhenDigestMissing(t *testing.T) {
	const checksum = "77b564f36da6d1e02169d0ecc837728eecb9ef983c317d9186ac9651798b924c"

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testReleaseAPIPath:
			writeReleasePayload(w, testReleasePayload{
				TagName: "v0.2.6",
				Assets: []testReleaseAsset{
					{
						Name:               "picoclaw_Windows_x86_64.zip",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Windows_x86_64.zip",
					},
					{
						Name:               "checksums.txt",
						BrowserDownloadURL: server.URL + "/assets/checksums.txt",
					},
				},
			})
		case "/assets/checksums.txt":
			_, _ = io.WriteString(w, checksum+"  picoclaw_Windows_x86_64.zip\n")
		case "/assets/picoclaw_Windows_x86_64.zip":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestHTTPClient(t, server.Client())

	gotURL, gotChecksum, err := findAssetInfo(server.URL+testReleaseAPIPath, "windows", "amd64")
	if err != nil {
		t.Fatalf("findAssetInfo returned error: %v", err)
	}
	if gotURL != server.URL+"/assets/picoclaw_Windows_x86_64.zip" {
		t.Fatalf("assetURL = %q, want %q", gotURL, server.URL+"/assets/picoclaw_Windows_x86_64.zip")
	}
	if gotChecksum != checksum {
		t.Fatalf("checksum = %q, want %q", gotChecksum, checksum)
	}
}

// TestFindAssetInfo_SelectsArchVariant covers 32-bit ARM and 386 selection.
// Linux_arm64 is listed first, as in real releases; it must never be picked
// for arch "arm" (it used to match the "arm" substring).
func TestFindAssetInfo_SelectsArchVariant(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testReleaseAPIPath:
			writeReleasePayload(w, testReleasePayload{
				TagName: "v0.3.1",
				Assets: []testReleaseAsset{
					{
						Name:               "picoclaw_Linux_arm64.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_arm64.tar.gz",
						Digest:             "sha256:" + strings.Repeat("a", 64),
					},
					{
						Name:               "picoclaw_Linux_armv6.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_armv6.tar.gz",
						Digest:             "sha256:" + strings.Repeat("b", 64),
					},
					{
						Name:               "picoclaw_Linux_armv7.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_armv7.tar.gz",
						Digest:             "sha256:" + strings.Repeat("c", 64),
					},
					{
						Name:               "picoclaw_Linux_i386.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_i386.tar.gz",
						Digest:             "sha256:" + strings.Repeat("d", 64),
					},
					{
						Name:               "picoclaw_Linux_x86_64.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_x86_64.tar.gz",
						Digest:             "sha256:" + strings.Repeat("e", 64),
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestHTTPClient(t, server.Client())

	origGOARM := runningGOARM
	t.Cleanup(func() { runningGOARM = origGOARM })

	tests := []struct {
		name      string
		arch      string
		goarm     string
		wantAsset string // empty means an error is expected
	}{
		{name: "arm GOARM=7 picks armv7", arch: "arm", goarm: "7", wantAsset: "picoclaw_Linux_armv7.tar.gz"},
		{name: "arm GOARM=6 picks armv6", arch: "arm", goarm: "6", wantAsset: "picoclaw_Linux_armv6.tar.gz"},
		{name: "arm unknown GOARM defaults to armv6", arch: "arm", goarm: "", wantAsset: "picoclaw_Linux_armv6.tar.gz"},
		{name: "arm GOARM=5 has no asset", arch: "arm", goarm: "5", wantAsset: ""},
		{name: "arm64 still picks arm64", arch: "arm64", wantAsset: "picoclaw_Linux_arm64.tar.gz"},
		{name: "amd64 still picks x86_64", arch: "amd64", wantAsset: "picoclaw_Linux_x86_64.tar.gz"},
		{name: "386 picks i386, not x86_64", arch: "386", wantAsset: "picoclaw_Linux_i386.tar.gz"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			runningGOARM = func() string { return tc.goarm }

			gotURL, _, err := findAssetInfo(server.URL+testReleaseAPIPath, "linux", tc.arch)
			if tc.wantAsset == "" {
				if err == nil {
					t.Fatalf("findAssetInfo(arch=%q) = %q, want error", tc.arch, gotURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("findAssetInfo(arch=%q) error: %v", tc.arch, err)
			}
			want := server.URL + "/assets/" + tc.wantAsset
			if gotURL != want {
				t.Fatalf("assetURL = %q, want %q", gotURL, want)
			}
		})
	}
}

func TestAssetHasArchSuffix(t *testing.T) {
	tests := []struct {
		name  string
		asset string
		alias string
		want  bool
	}{
		{name: "arm does not match arm64", asset: "picoclaw_Linux_arm64.tar.gz", alias: "arm", want: false},
		{name: "armv7 matches armv7", asset: "picoclaw_Linux_armv7.tar.gz", alias: "armv7", want: true},
		{name: "x86 does not match x86_64", asset: "picoclaw_Linux_x86_64.tar.gz", alias: "x86", want: false},
		{name: "x86_64 matches x86_64", asset: "picoclaw_Linux_x86_64.zip", alias: "x86_64", want: true},
		{name: "386 does not match i386", asset: "picoclaw_Linux_i386.tgz", alias: "386", want: false},
		{name: "i386 matches i386", asset: "picoclaw_Linux_i386.tgz", alias: "i386", want: true},
		{name: "dash separator matches", asset: "picoclaw-Linux-arm64.tar", alias: "arm64", want: true},
		{
			name:  "checksum file is not an archive",
			asset: "picoclaw_Linux_arm64.tar.gz.sha256",
			alias: "arm64",
			want:  false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := assetHasArchSuffix(tc.asset, tc.alias); got != tc.want {
				t.Fatalf("assetHasArchSuffix(%q, %q) = %v, want %v", tc.asset, tc.alias, got, tc.want)
			}
		})
	}
}

func TestArmAliases(t *testing.T) {
	tests := []struct {
		goarm string
		want  []string
	}{
		{goarm: "7", want: []string{"armv7", "armv6"}},
		{goarm: "6", want: []string{"armv6"}},
		{goarm: "", want: []string{"armv6"}},
		{goarm: "5", want: nil},
	}
	for _, tc := range tests {
		got := armAliases(tc.goarm)
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Fatalf("armAliases(%q) = %v, want %v", tc.goarm, got, tc.want)
		}
	}
}

func TestGoarmFromSettings(t *testing.T) {
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{name: "plain GOARM", settings: []debug.BuildSetting{{Key: "GOARM", Value: "7"}}, want: "7"},
		{name: "GOARM with softfloat", settings: []debug.BuildSetting{{Key: "GOARM", Value: "7,softfloat"}}, want: "7"},
		{name: "no GOARM setting", settings: []debug.BuildSetting{{Key: "GOAMD64", Value: "v1"}}, want: ""},
		{name: "no settings", settings: nil, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := goarmFromSettings(tc.settings); got != tc.want {
				t.Fatalf("goarmFromSettings() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDownloadAndExtractRelease_ExtractsTarGz(t *testing.T) {
	tarGzContent := buildTestTarGz(t, map[string]string{
		"picoclaw_Linux_x86_64/picoclaw": "test linux binary payload",
	})
	sum := sha256.Sum256(tarGzContent)
	checksum := hex.EncodeToString(sum[:])

	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testReleaseAPIPath:
			writeReleasePayload(w, testReleasePayload{
				TagName: "v0.2.6",
				Assets: []testReleaseAsset{
					{
						Name:               "picoclaw_Linux_x86_64.tar.gz",
						BrowserDownloadURL: server.URL + "/assets/picoclaw_Linux_x86_64.tar.gz",
						Digest:             "sha256:" + checksum,
					},
				},
			})
		case "/assets/picoclaw_Linux_x86_64.tar.gz":
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(tarGzContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestHTTPClient(t, server.Client())

	dir, err := DownloadAndExtractRelease(server.URL+testReleaseAPIPath, "linux", "amd64")
	if err != nil {
		t.Fatalf("DownloadAndExtractRelease returned error: %v", err)
	}
	defer os.RemoveAll(dir)

	binPath, err := findBinaryInDir(dir, "picoclaw")
	if err != nil {
		t.Fatalf("findBinaryInDir returned error: %v", err)
	}

	bs, err := os.ReadFile(binPath)
	if err != nil {
		t.Fatalf("ReadFile extracted asset: %v", err)
	}
	if got := string(bs); got != "test linux binary payload" {
		t.Fatalf("extracted content = %q, want %q", got, "test linux binary payload")
	}
}

func TestDownloadAndExtractRelease_RetriesTransientAssetFailure(t *testing.T) {
	zipContent := buildTestZip(t, map[string]string{
		"picoclaw.exe": "test windows binary payload",
	})
	sum := sha256.Sum256(zipContent)
	checksum := hex.EncodeToString(sum[:])

	var assetAttempts int
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api.github.com/repos/afjcjsbx/picoclaw/releases/latest":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(
				w,
				`{"tag_name":"v0.2.6","assets":[{"name":"picoclaw_Windows_x86_64.zip","browser_download_url":%q,"digest":"sha256:%s"}]}`,
				server.URL+"/assets/picoclaw_Windows_x86_64.zip",
				checksum,
			)
		case "/assets/picoclaw_Windows_x86_64.zip":
			assetAttempts++
			if assetAttempts == 1 {
				w.WriteHeader(http.StatusGatewayTimeout)
				return
			}
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	withTestHTTPClient(t, server.Client())

	dir, err := DownloadAndExtractRelease(
		server.URL+"/api.github.com/repos/afjcjsbx/picoclaw/releases/latest",
		"windows",
		"amd64",
	)
	if err != nil {
		t.Fatalf("DownloadAndExtractRelease returned error: %v", err)
	}
	defer os.RemoveAll(dir)

	if assetAttempts != 2 {
		t.Fatalf("asset attempts = %d, want 2", assetAttempts)
	}

	bs, err := os.ReadFile(filepath.Join(dir, "picoclaw.exe"))
	if err != nil {
		t.Fatalf("ReadFile extracted asset: %v", err)
	}
	if got := string(bs); got != "test windows binary payload" {
		t.Fatalf("extracted content = %q, want %q", got, "test windows binary payload")
	}
}

func buildTestZip(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("Create zip entry %q: %v", name, err)
		}
		if _, err := io.WriteString(w, content); err != nil {
			t.Fatalf("Write zip entry %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("Close zip writer: %v", err)
	}
	return buf.Bytes()
}

func buildTestTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()

	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)

	for name, content := range files {
		if err := tw.WriteHeader(&tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(content)),
		}); err != nil {
			t.Fatalf("Write tar header %q: %v", name, err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatalf("Write tar entry %q: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close tar writer: %v", err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatalf("Close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func writeReleasePayload(w http.ResponseWriter, payload testReleasePayload) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func withTestHTTPClient(t *testing.T, client *http.Client) {
	t.Helper()

	origClient := httpClient
	httpClient = client
	httpClient.Timeout = 5 * time.Second
	t.Cleanup(func() {
		httpClient = origClient
	})
}
