package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sipeed/picoclaw/pkg/config"
)

func TestCreateRestoreRoundTrip(t *testing.T) {
	source := newTestInstallation(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")

	created, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true,
		Now: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC),
	})
	require.NoError(t, err)
	require.Equal(t, config.CurrentVersion, created.Manifest.ConfigVersion)
	require.NotEmpty(t, created.Manifest.Entries)
	require.NotEmpty(t, created.Manifest.Files)
	require.FileExists(t, archive)

	manifest, err := Inspect(archive, "")
	require.NoError(t, err)
	require.Equal(t, FormatName, manifest.Format)
	require.Equal(t, source.home, manifest.Home)
	require.NotEmpty(t, manifest.Entries)
	foundHash := false
	for _, entry := range manifest.Entries {
		if entry.Path == "workspace/memory/MEMORY.md" {
			foundHash = entry.SHA256 != ""
		}
	}
	require.True(t, foundHash)

	target := newEmptyTarget(t)
	result, err := Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true,
	})
	require.NoError(t, err)
	require.Empty(t, result.SafetyBackup)
	require.Positive(t, result.Files)
	require.Greater(t, result.Dirs, 0)

	require.Equal(t, filepath.Join(target.home, "workspace"), configWorkspace(t, target.config))
	assertFileContent(t, filepath.Join(target.home, ".security.yml"), "telegram:\n  token: secret\n")
	assertFileContent(t, filepath.Join(target.home, "workspace", "memory", "MEMORY.md"), "remember me\n")
	assertFileContent(t, filepath.Join(target.home, "workspace", "sessions", "chat.jsonl"), `{"role":"user"}`+"\n")
	assertFileContent(t, target.sshKey, source.sshKeyContent)

	if runtime.GOOS != "windows" {
		requireFileMode(t, target.config, 0o600)
		requireFileMode(t, filepath.Join(target.home, ".security.yml"), 0o600)
		requireFileMode(t, target.sshKey, 0o600)
	}

	link, err := os.Readlink(filepath.Join(target.home, "workspace", "AGENT-link.md"))
	require.NoError(t, err)
	require.Equal(t, "AGENT.md", link)

	for _, path := range []string{
		"cache/junk.txt",
		"logs/gateway.log",
		"runtime-user-env/tmp/x",
		"workspace/.artifacts/tool/big.txt",
		"workspace/session.log",
		"workspace/tmp.swp",
		".DS_Store",
		"workspace/outside-link",
	} {
		_, err := os.Lstat(filepath.Join(target.home, path))
		require.True(t, os.IsNotExist(err), "%s should be excluded", path)
	}
}

func TestEncryptedRoundTrip(t *testing.T) {
	previous := scryptLogN
	scryptLogN = 10
	t.Cleanup(func() { scryptLogN = previous })

	source := newTestInstallation(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	const passphrase = "correct horse battery staple"

	created, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, Encrypt: true, Passphrase: passphrase, AllowRunning: true,
	})
	require.NoError(t, err)
	require.True(t, created.Encrypted)
	require.Equal(t, ".enc", filepath.Ext(created.OutputPath))
	archive = created.OutputPath

	encrypted, err := IsEncrypted(archive)
	require.NoError(t, err)
	require.True(t, encrypted)
	_, err = Inspect(archive, "")
	require.ErrorIs(t, err, ErrPassphraseRequired)
	_, err = Inspect(archive, "wrong passphrase")
	require.ErrorIs(t, err, ErrDecrypt)
	manifest, err := Inspect(archive, passphrase)
	require.NoError(t, err)
	require.NotNil(t, manifest.Encryption)

	target := newEmptyTarget(t)
	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, Passphrase: "wrong passphrase", AllowRunning: true,
	})
	require.ErrorIs(t, err, ErrDecrypt)
	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, Passphrase: passphrase, AllowRunning: true,
	})
	require.NoError(t, err)
	assertFileContent(t, filepath.Join(target.home, "workspace", "memory", "MEMORY.md"), "remember me\n")
}

func TestRestoreRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "evil.tar.gz")
	writeRawArchive(t, archive, []rawEntry{
		{name: "../evil.txt", body: "owned"},
		{name: ManifestName, body: minimalManifestJSON(t)},
	})
	target := newEmptyTarget(t)
	_, err := Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "outside the PicoClaw tree")
	_, statErr := os.Stat(filepath.Join(filepath.Dir(target.home), "evil.txt"))
	require.True(t, os.IsNotExist(statErr))
}

func TestValidateEntriesRejectsChecksumMismatch(t *testing.T) {
	manifest := &Manifest{Entries: []Entry{{
		Path: "file.txt", Type: EntryFile, Size: 4, SHA256: "deadbeef",
	}}}
	extracted := map[string]extractedInfo{
		"home/file.txt": {kind: EntryFile, size: 4, sha256: "cafebabe"},
	}
	err := validateEntries(manifest, extracted)
	require.Error(t, err)
	require.Contains(t, err.Error(), "checksum mismatch")
}

func TestRestoreRejectsNewerConfigVersion(t *testing.T) {
	source := newTestInstallation(t)
	source.writeConfig(t, config.CurrentVersion+1)
	archive := filepath.Join(t.TempDir(), "newer.tar.gz")
	_, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true,
	})
	require.NoError(t, err)

	target := newEmptyTarget(t)
	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "newer")

	result, err := Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true, Force: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Warnings)
	require.Equal(t, filepath.Join(target.home, "workspace"), configWorkspace(t, target.config))
}

func TestRestoreForceCreatesSafetyBackup(t *testing.T) {
	source := newTestInstallation(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	_, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true,
	})
	require.NoError(t, err)

	target := newEmptyTarget(t)
	require.NoError(t, os.MkdirAll(filepath.Join(target.home, "workspace"), 0o700))
	writeTestFile(t, filepath.Join(target.home, "workspace", "old.txt"), "old", 0o600)
	writeTestFile(t, target.config, `{"version":3}`, 0o600)
	writeTestFile(t, target.sshKey, "old-key", 0o600)

	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already contains")

	result, err := Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true, Force: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.SafetyBackup)
	require.FileExists(t, result.SafetyBackup)
	assertFileContent(t, filepath.Join(target.home, "workspace", "old.txt"), "old")
	require.Equal(t, filepath.Join(target.home, "workspace"), configWorkspace(t, target.config))
	assertFileContent(t, target.sshKey, source.sshKeyContent)
}

func TestDryRunWritesNothing(t *testing.T) {
	source := newTestInstallation(t)
	archive := filepath.Join(t.TempDir(), "planned.tar.gz")
	result, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true, DryRun: true,
	})
	require.NoError(t, err)
	require.True(t, result.DryRun)
	require.Positive(t, result.Manifest.FileCount)
	_, statErr := os.Stat(archive)
	require.True(t, os.IsNotExist(statErr))

	created, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true,
	})
	require.NoError(t, err)
	target := newEmptyTarget(t)
	restored, err := Restore(RestoreOptions{
		ArchivePath: created.OutputPath, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true, DryRun: true,
	})
	require.NoError(t, err)
	require.True(t, restored.DryRun)
	_, statErr = os.Stat(target.home)
	require.True(t, os.IsNotExist(statErr))
}

func TestNoSSHKeySkipsExistingKey(t *testing.T) {
	source := newTestInstallation(t)
	archive := filepath.Join(t.TempDir(), "snapshot.tar.gz")
	_, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: archive, AllowRunning: true,
	})
	require.NoError(t, err)

	target := newEmptyTarget(t)
	writeTestFile(t, target.sshKey, "different-key", 0o600)
	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "already exists")

	target = newEmptyTarget(t)
	writeTestFile(t, target.sshKey, "different-key", 0o600)
	_, err = Restore(RestoreOptions{
		ArchivePath: archive, Home: target.home, ConfigPath: target.config,
		SSHKeyPath: target.sshKey, AllowRunning: true, NoSSHKey: true,
	})
	require.NoError(t, err)
	assertFileContent(t, target.sshKey, "different-key")
}

func TestCreateRefusesRunningGateway(t *testing.T) {
	source := newTestInstallation(t)
	writeTestFile(t, filepath.Join(source.home, ".picoclaw.pid"),
		fmt.Sprintf(`{"pid":%d,"version":"test"}`, os.Getpid()), 0o600)
	_, err := Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: filepath.Join(t.TempDir(), "snapshot.tar.gz"),
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "gateway is running")
	_, err = Create(CreateOptions{
		Home: source.home, ConfigPath: source.config, SSHKeyPath: source.sshKey,
		OutputPath: filepath.Join(t.TempDir(), "snapshot.tar.gz"), AllowRunning: true,
	})
	require.NoError(t, err)
}

func TestExclusionPolicy(t *testing.T) {
	tests := []struct {
		path   string
		isDir  bool
		reason bool
	}{
		{"cache", true, true},
		{"cache/data.bin", false, true},
		{"logs", true, true},
		{"logs/gateway.log", false, true},
		{"runtime-user-env/tmp/x", false, true},
		{"workspace/.artifacts", true, true},
		{"workspace/.artifacts/tool/result.txt", false, true},
		{"workspace/session.log", false, true},
		{"workspace/session.log.1", false, true},
		{"workspace/tmp.swp", false, true},
		{".DS_Store", false, true},
		{"workspace/._resource", false, true},
		{"workspace/node_modules", true, true},
		{"workspace/AGENT.md", false, false},
		{"workspace/sessions/chat.jsonl", false, false},
		{"workspace/skills/demo/SKILL.md", false, false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			_, excluded := excluded(test.path, test.isDir)
			require.Equal(t, test.reason, excluded)
		})
	}
}

func TestSecureArchivePath(t *testing.T) {
	for _, valid := range []string{"home/config.json", "extra/ssh/key", "a/../b"} {
		_, err := secureArchivePath(valid)
		require.NoError(t, err, valid)
	}
	for _, invalid := range []string{"", "..", "../evil", "/etc/passwd", `home\config.json`, "home/\x00x", "C:/x"} {
		_, err := secureArchivePath(invalid)
		require.Error(t, err, invalid)
	}
}

func TestSafeLinkTarget(t *testing.T) {
	require.NoError(t, safeLinkTarget("workspace/link", "AGENT.md"))
	require.NoError(t, safeLinkTarget("workspace/sub/link", "../AGENT.md"))
	require.Error(t, safeLinkTarget("workspace/link", "/etc/passwd"))
	require.Error(t, safeLinkTarget("workspace/link", "../../outside"))
	require.Error(t, safeLinkTarget("foo/bar", "../../outside"))
	require.Error(t, safeLinkTarget("workspace/link", "C:/outside"))
	require.Error(t, safeLinkTarget("workspace/link", ""))
}

type testInstallation struct {
	home          string
	config        string
	sshKey        string
	sshKeyContent string
}

func newTestInstallation(t *testing.T) *testInstallation {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, ".picoclaw")
	for _, dir := range []string{
		"workspace/sessions", "workspace/memory", "workspace/skills/demo",
		"workspace/state", "workspace/cron", "workspace/.artifacts/tool",
		"cache", "logs", "runtime-user-env/tmp",
	} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, dir), 0o700))
	}
	sshDir := filepath.Join(base, "ssh")
	require.NoError(t, os.MkdirAll(sshDir, 0o700))
	sshKey := filepath.Join(sshDir, "picoclaw_ed25519.key")
	sshKeyContent := "-----BEGIN OPENSSH PRIVATE KEY-----\ntest\n-----END OPENSSH PRIVATE KEY-----\n"
	writeTestFile(t, sshKey, sshKeyContent, 0o600)

	installation := &testInstallation{
		home: home, config: filepath.Join(home, "config.json"),
		sshKey: sshKey, sshKeyContent: sshKeyContent,
	}
	installation.writeConfig(t, config.CurrentVersion)

	writeTestFile(t, filepath.Join(home, ".security.yml"), "telegram:\n  token: secret\n", 0o600)
	writeTestFile(t, filepath.Join(home, "auth.json"), `{"token":"x"}`+"\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "AGENT.md"), "# Agent\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "memory", "MEMORY.md"), "remember me\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "sessions", "chat.jsonl"), `{"role":"user"}`+"\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "skills", "demo", "SKILL.md"), "# Demo\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "state", "state.json"), `{"last":"telegram"}`+"\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "cron", "jobs.json"), `{"jobs":[]}`+"\n", 0o600)
	writeTestFile(t, filepath.Join(home, "cache", "junk.txt"), "cache\n", 0o600)
	writeTestFile(t, filepath.Join(home, "logs", "gateway.log"), "log\n", 0o600)
	writeTestFile(t, filepath.Join(home, "runtime-user-env", "tmp", "x"), "tmp\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", ".artifacts", "tool", "big.txt"), "artifact\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "session.log"), "log\n", 0o600)
	writeTestFile(t, filepath.Join(home, "workspace", "tmp.swp"), "swap\n", 0o600)
	writeTestFile(t, filepath.Join(home, ".DS_Store"), "metadata\n", 0o600)
	if err := os.Symlink("AGENT.md", filepath.Join(home, "workspace", "AGENT-link.md")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks are not available: %v", err)
		}
		require.NoError(t, err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(home, "workspace", "outside-link")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlinks are not available: %v", err)
		}
		require.NoError(t, err)
	}
	return installation
}

func (i *testInstallation) writeConfig(t *testing.T, version int) {
	t.Helper()
	content := fmt.Sprintf(
		`{"version":%d,"agents":{"defaults":{"workspace":%q}},"gateway":{"port":18999}}`+"\n",
		version, filepath.Join(i.home, "workspace"),
	)
	writeTestFile(t, i.config, content, 0o600)
}

type emptyTarget struct{ home, config, sshKey string }

func newEmptyTarget(t *testing.T) emptyTarget {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, ".picoclaw")
	return emptyTarget{
		home: home, config: filepath.Join(home, "config.json"),
		sshKey: filepath.Join(base, "ssh", "picoclaw_ed25519.key"),
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	require.NoError(t, os.Chmod(path, mode))
}

func assertFileContent(t *testing.T, path, content string) {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, content, string(data))
}

func requireFileMode(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, mode, info.Mode().Perm())
}

func configWorkspace(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var raw struct {
		Agents struct {
			Defaults struct {
				Workspace string `json:"workspace"`
			} `json:"defaults"`
		} `json:"agents"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	return raw.Agents.Defaults.Workspace
}

type rawEntry struct{ name, body string }

func writeRawArchive(t *testing.T, archive string, entries []rawEntry) {
	t.Helper()
	file, err := os.Create(archive)
	require.NoError(t, err)
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		require.NoError(t, tw.WriteHeader(&tar.Header{
			Name: entry.name, Mode: 0o600, Size: int64(len(entry.body)),
			Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}))
		_, err := tw.Write([]byte(entry.body))
		require.NoError(t, err)
	}
	require.NoError(t, tw.Close())
	require.NoError(t, gz.Close())
	require.NoError(t, file.Close())
}

func minimalManifestJSON(t *testing.T) string {
	t.Helper()
	manifest := Manifest{
		Format: FormatName, FormatVersion: FormatVersion,
		Home: t.TempDir(), ConfigVersion: config.CurrentVersion,
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	return string(data)
}

func TestInspectRejectsMissingManifest(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "empty.tar.gz")
	writeRawArchive(t, archive, []rawEntry{{name: "home/config.json", body: "{}"}})
	_, err := Inspect(archive, "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no MANIFEST")
}

func TestDecryptDetectsTruncation(t *testing.T) {
	previous := scryptLogN
	scryptLogN = 10
	t.Cleanup(func() { scryptLogN = previous })

	var buffer bytes.Buffer
	writer, _, err := newEncryptWriter(&buffer, "a-long-enough-passphrase")
	require.NoError(t, err)
	_, err = writer.Write([]byte("payload"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	truncated := buffer.Bytes()[:buffer.Len()-5]
	reader, err := newDecryptReader(bytes.NewReader(truncated), "a-long-enough-passphrase")
	require.NoError(t, err)
	_, err = io.ReadAll(reader)
	require.ErrorIs(t, err, ErrDecrypt)
}

func TestParseManifestRejectsUnsupportedVersion(t *testing.T) {
	manifest := Manifest{
		Format: FormatName, FormatVersion: FormatVersion + 1, Home: t.TempDir(),
	}
	data, err := json.Marshal(manifest)
	require.NoError(t, err)
	_, err = parseManifest(data)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")
}
