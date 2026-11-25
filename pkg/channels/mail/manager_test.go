package mail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
)

func TestRegisteredWithTheChannelManager(t *testing.T) {
	if !slices.Contains(channels.GetRegisteredFactoryNames(), config.ChannelMail) {
		t.Fatalf("the %q factory is not registered", config.ChannelMail)
	}
}

// The channel is built from JSON settings by the real manager, exactly as it
// is at runtime: factory lookup, settings decoding, secret handling, start-up,
// then mail in over IMAP and an agent answer out over SMTP.
func TestManagerRunsTheMailChannelFromConfig(t *testing.T) {
	smtpd := newFakeSMTP(t, nil, false)
	srv := newTestIMAP(t, nil)
	workspace := t.TempDir()

	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	bc := &config.Channel{
		Enabled:   true,
		Type:      config.ChannelMail,
		AllowFrom: config.FlexibleStringSlice{"@example.org"},
	}
	bc.SetName(config.ChannelMail)
	bc.Settings = config.RawNode(fmt.Sprintf(`{
		"imap_host": "127.0.0.1", "imap_port": %d, "imap_plaintext": true,
		"smtp_host": "127.0.0.1", "smtp_port": %d,
		"username": "bot@example.com", "password": "secret",
		"poll_interval_secs": 1
	}`, atoiPort(srv.addr), smtpd.port()))
	cfg.Channels = config.ChannelsConfig{config.ChannelMail: bc}

	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	m, err := channels.NewManager(cfg, mb, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err = m.StartAll(context.Background()); err != nil {
		t.Fatalf("StartAll: %v", err)
	}
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })

	ch, ok := m.GetChannel(config.ChannelMail)
	if !ok {
		t.Fatal("manager did not create the mail channel")
	}
	mc, ok := ch.(*MailChannel)
	if !ok {
		t.Fatalf("channel is %T", ch)
	}
	if mc.workspace != workspace {
		t.Errorf("workspace = %q, want %q (attachments belong under it)", mc.workspace, workspace)
	}
	waitIdle(t, srv, 1)

	srv.deliver(fixture("m1@example.org", alice, "Via the manager", "Hello bot"))
	in := nextInbound(t, mb)
	if in.Channel != "mail" || in.Content != "Subject: Via the manager\n\nHello bot" {
		t.Fatalf("inbound = %q on %q", in.Content, in.Channel)
	}

	err = m.SendMessage(context.Background(), bus.OutboundMessage{
		Channel: in.Channel, ChatID: in.ChatID, Context: in.Context, Content: "Hello **human**",
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	rec := smtpd.next(t)
	if rec.authMech != "PLAIN" || !strings.Contains(rec.authPayload, "secret") {
		t.Errorf("SMTP login = %s %q: the password did not survive config decoding", rec.authMech, rec.authPayload)
	}
	h, _ := readMessage(t, []byte(rec.data))
	if h.Get("Subject") != "Re: Via the manager" || h.Get("In-Reply-To") != "<m1@example.org>" {
		t.Errorf("subject=%q in-reply-to=%q", h.Get("Subject"), h.Get("In-Reply-To"))
	}
}

func TestManagerSkipsAMailChannelWithoutHosts(t *testing.T) {
	cfg := config.DefaultConfig()
	bc := &config.Channel{Enabled: true, Type: config.ChannelMail, AllowFrom: config.FlexibleStringSlice{"*"}}
	bc.SetName(config.ChannelMail)
	bc.Settings = config.RawNode(`{"username": "bot@example.com"}`)
	cfg.Channels = config.ChannelsConfig{config.ChannelMail: bc}

	m, err := channels.NewManager(cfg, bus.NewMessageBus(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.StartAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.StopAll(context.Background()) })
	time.Sleep(50 * time.Millisecond)
	if _, ok := m.GetChannel(config.ChannelMail); ok {
		t.Error("a mail channel with no imap_host/smtp_host must not be started")
	}
}

// The documented ways of supplying secrets: .security.yml next to the config,
// and PICOCLAW_CHANNELS_MAIL_* environment variables.
func TestSecretsLoadFromSecurityFileAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("config.json", fmt.Sprintf(`{
		"version": %d,
		"channel_list": {"mail": {"enabled": true, "type": "mail", "allow_from": ["@example.org"],
			"settings": {
				"imap_host": "imap.example.com", "smtp_host": "smtp.example.com",
				"username": "bot@example.com", "observer_mode": true, "poll_interval_secs": 7,
				"idle_timeout_secs": 100, "max_attachment_bytes": 2048, "plain_text_only": true,
				"imap_folder": "Bot", "smtp_username": "relay", "from_address": "Bot <bot@example.com>"
			}}}
	}`, config.CurrentVersion))
	write(".security.yml", `channel_list:
  mail:
    settings:
      password: "imap-secret"
      smtp_password: "smtp-secret"
      oauth_token: "oauth-secret"
`)

	load := func() *config.MailSettings {
		t.Helper()
		cfg, err := config.LoadConfig(cfgPath)
		if err != nil {
			t.Fatalf("LoadConfig: %v", err)
		}
		bc := cfg.Channels[config.ChannelMail]
		if bc == nil {
			t.Fatal("mail channel missing from the loaded config")
		}
		decoded, err := bc.GetDecoded()
		if err != nil {
			t.Fatal(err)
		}
		s, ok := decoded.(*config.MailSettings)
		if !ok {
			t.Fatalf("decoded %T", decoded)
		}
		return s
	}

	s := load()
	if s.Password.String() != "imap-secret" || s.SMTPPassword.String() != "smtp-secret" ||
		s.OAuthToken.String() != "oauth-secret" {
		t.Errorf(
			"secrets from .security.yml: %q %q %q",
			s.Password.String(),
			s.SMTPPassword.String(),
			s.OAuthToken.String(),
		)
	}
	if s.IMAPHost != "imap.example.com" || s.SMTPHost != "smtp.example.com" || s.Username != "bot@example.com" ||
		!s.ObserverMode || !s.PlainTextOnly || s.PollIntervalSecs != 7 || s.IdleTimeoutSecs != 100 ||
		s.MaxAttachmentBytes != 2048 || s.IMAPFolder != "Bot" || s.SMTPUsername != "relay" || s.FromAddress != "Bot <bot@example.com>" {
		t.Errorf("settings not decoded as written: %+v", s)
	}

	t.Setenv("PICOCLAW_CHANNELS_MAIL_PASSWORD", "env-secret")
	t.Setenv("PICOCLAW_CHANNELS_MAIL_IMAP_HOST", "imap.env.example.com")
	s = load()
	if s.Password.String() != "env-secret" {
		t.Errorf("env did not override the password: %q", s.Password.String())
	}
	if s.IMAPHost != "imap.env.example.com" {
		t.Errorf("env did not override imap_host: %q", s.IMAPHost)
	}
}

// A secret may be a file:// reference instead of a literal, e.g. a mounted
// Docker/Kubernetes secret. The password never appears in any config file.
func TestPasswordFromFileReference(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mail-password"), []byte("password-from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgJSON := fmt.Sprintf(`{"version": %d, "channel_list": {"mail": {"enabled": true, "type": "mail",
		"allow_from": ["@example.org"],
		"settings": {"imap_host": "imap.example.com", "smtp_host": "smtp.example.com", "username": "bot@example.com"}}}}`,
		config.CurrentVersion)
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	security := "channel_list:\n  mail:\n    settings:\n      password: \"file://mail-password\"\n" // relative to the config directory
	if err := os.WriteFile(filepath.Join(dir, ".security.yml"), []byte(security), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.LoadConfig(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := cfg.Channels[config.ChannelMail].GetDecoded()
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.(*config.MailSettings).Password.String(); got != "password-from-file" {
		t.Errorf("password = %q, want the file content", got)
	}
}

// Saving the config must never write the secret back into config.json.
func TestPasswordIsNeverWrittenToConfigJSON(t *testing.T) {
	s := &config.MailSettings{IMAPHost: "h", SMTPHost: "h", Username: "bot@example.com"}
	s.Password = *config.NewSecureString("super-secret-password")
	s.SMTPPassword = *config.NewSecureString("super-secret-smtp")
	s.OAuthToken = *config.NewSecureString("super-secret-token")

	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-password", "super-secret-smtp", "super-secret-token"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("config JSON leaks %q: %s", secret, b)
		}
	}
}
