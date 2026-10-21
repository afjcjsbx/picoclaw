package mail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

func TestMatchAllowed(t *testing.T) {
	tests := []struct {
		name  string
		allow []string
		addr  string
		want  bool
	}{
		{"exact address", []string{"alice@example.org"}, "alice@example.org", true},
		{"address is case-insensitive", []string{"alice@example.org"}, "Alice@Example.ORG", true},
		{"other address", []string{"alice@example.org"}, "bob@example.org", false},
		{"address is not a prefix match", []string{"alice@example.org"}, "malice@example.org", false},
		{"@domain", []string{"@example.org"}, "anyone@example.org", true},
		{"@domain rejects subdomain", []string{"@example.org"}, "anyone@sub.example.org", false},
		{"@domain rejects lookalike", []string{"@example.org"}, "anyone@evilexample.org", false},
		{"@domain rejects suffix trick", []string{"@example.org"}, "anyone@example.org.evil.net", false},
		{"bare domain", []string{"example.org"}, "anyone@example.org", true},
		{"bare domain rejects subdomain", []string{"example.org"}, "anyone@sub.example.org", false},
		{"bare domain rejects lookalike", []string{"example.org"}, "anyone@badexample.org", false},
		{"bare domain is not the local part", []string{"example.org"}, "example.org@evil.net", false},
		{"wildcard", []string{"*"}, "anyone@anywhere.test", true},
		{"several entries", []string{"x@y.z", "@example.org"}, "q@example.org", true},
		{"empty list", nil, "alice@example.org", false},
		{"empty address", []string{"*"}, "", false},
		{"whitespace address", []string{"*"}, "  ", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// entries are normalized by the constructor; mimic that.
			var allow []string
			for _, a := range tt.allow {
				allow = append(allow, strings.ToLower(a))
			}
			if got := matchAllowed(allow, tt.addr); got != tt.want {
				t.Errorf("matchAllowed(%v, %q) = %v, want %v", tt.allow, tt.addr, got, tt.want)
			}
		})
	}
}

func TestNewMailChannel_Validation(t *testing.T) {
	base := func() (*config.MailSettings, *config.Channel) {
		return &config.MailSettings{
				IMAPHost: "imap.example.com",
				SMTPHost: "smtp.example.com",
				Username: "bot@example.com",
			},
			&config.Channel{
				AllowFrom: config.FlexibleStringSlice{"@example.org"},
			}
	}
	tests := []struct {
		name    string
		mutate  func(*config.MailSettings, *config.Channel)
		wantErr string
	}{
		{"valid", func(*config.MailSettings, *config.Channel) {}, ""},
		{"missing imap host", func(s *config.MailSettings, _ *config.Channel) { s.IMAPHost = "" }, "imap_host"},
		{"missing smtp host", func(s *config.MailSettings, _ *config.Channel) { s.SMTPHost = "" }, "smtp_host"},
		{
			"username is not an address",
			func(s *config.MailSettings, _ *config.Channel) { s.Username = "bot" },
			"from_address",
		},
		{"from_address rescues a plain username", func(s *config.MailSettings, _ *config.Channel) {
			s.Username, s.FromAddress = "bot", "bot@example.com"
		}, ""},
		{"no allow_from", func(_ *config.MailSettings, bc *config.Channel) { bc.AllowFrom = nil }, "allow_from"},
		{
			"blank allow_from entries",
			func(_ *config.MailSettings, bc *config.Channel) { bc.AllowFrom = config.FlexibleStringSlice{"", "  "} },
			"allow_from",
		},
		{
			"explicit wildcard is accepted",
			func(_ *config.MailSettings, bc *config.Channel) { bc.AllowFrom = config.FlexibleStringSlice{"*"} },
			"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, bc := base()
			tt.mutate(s, bc)
			c, err := NewMailChannel(bc, s, bus.NewMessageBus(), "")
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if c.Name() != "mail" || c.IsRunning() {
					t.Errorf("name=%q running=%v", c.Name(), c.IsRunning())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

func TestNewMailChannel_Defaults(t *testing.T) {
	c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) { s.PollIntervalSecs = 0 })
	if c.pollInterval != 60*time.Second {
		t.Errorf("poll interval = %v, want 60s", c.pollInterval)
	}
	if c.idleTimeout != 29*time.Minute {
		t.Errorf("idle timeout = %v, want 29m (RFC 2177)", c.idleTimeout)
	}
	if c.maxAttach != 10<<20 {
		t.Errorf("max attachment = %d", c.maxAttach)
	}
	if c.folder() != "INBOX" {
		t.Errorf("folder = %q", c.folder())
	}

	c, _ = newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) {
		s.PollIntervalSecs, s.IdleTimeoutSecs, s.MaxAttachmentBytes, s.IMAPFolder = 5, 90, 123, "Bot"
	})
	if c.pollInterval != 5*time.Second || c.idleTimeout != 90*time.Second || c.maxAttach != 123 || c.folder() != "Bot" {
		t.Errorf("overrides ignored: %v %v %d %q", c.pollInterval, c.idleTimeout, c.maxAttach, c.folder())
	}
}

func TestChannelInterfaceAllowList(t *testing.T) {
	c, _ := newTestChannel(t, func(_ *config.MailSettings, bc *config.Channel) {
		bc.AllowFrom = config.FlexibleStringSlice{"@Example.org", " carol@other.net "}
	})
	for addr, want := range map[string]bool{"a@example.org": true, "carol@other.net": true, "dave@other.net": false} {
		if got := c.IsAllowed(addr); got != want {
			t.Errorf("IsAllowed(%q) = %v", addr, got)
		}
		if got := c.IsAllowedSender(bus.SenderInfo{PlatformID: addr}); got != want {
			t.Errorf("IsAllowedSender(%q) = %v", addr, got)
		}
	}
}

func TestSeenSet(t *testing.T) {
	s := newSeenSet(3)
	for _, id := range []string{"a", "b", "c"} {
		if !s.add(id) {
			t.Fatalf("%q reported as seen", id)
		}
	}
	if s.add("b") {
		t.Error("duplicate accepted")
	}
	if !s.add("d") { // evicts "a"
		t.Error("new id rejected")
	}
	if len(s.ids) != 3 || len(s.order) != 3 {
		t.Errorf("set not bounded: %d ids, %d order", len(s.ids), len(s.order))
	}
	if !s.add("a") {
		t.Error("evicted id should be new again")
	}
	if s.add("d") {
		t.Error("recent id should still be known")
	}
}

func TestSyntheticID(t *testing.T) {
	c, _ := newTestChannel(t, nil)
	c.uidValidity = 5
	id := c.syntheticID(42)

	if !strings.HasPrefix(id, "mail-imap-") || !isSyntheticID(id) {
		t.Fatalf("id = %q", id)
	}
	if !strings.HasSuffix(id, "-42") {
		t.Errorf("id %q does not end with the uid", id)
	}
	if c.syntheticID(42) != id {
		t.Error("synthetic id is not stable")
	}
	if c.syntheticID(43) == id {
		t.Error("uid not part of the id")
	}
	c.uidValidity = 6
	if c.syntheticID(42) == id {
		t.Error("uidvalidity not part of the id")
	}
	c.uidValidity = 5
	c.cfg.IMAPFolder = "Other"
	if c.syntheticID(42) == id {
		t.Error("folder not part of the id")
	}
	c.cfg.IMAPFolder = ""
	c.cfg.Username = "other@example.com"
	if c.syntheticID(42) == id {
		t.Error("user not part of the id")
	}
	if isSyntheticID("abc@example.org") {
		t.Error("real id classified as synthetic")
	}
}

func TestNextBackoff(t *testing.T) {
	d := initialBackoff
	got := make([]time.Duration, 0, 9)
	for range 9 {
		got = append(got, d)
		d = nextBackoff(d)
	}
	want := []int{1, 2, 4, 8, 16, 32, 60, 60, 60} // seconds
	for i := range want {
		if got[i] != time.Duration(want[i])*time.Second {
			t.Fatalf("backoff sequence = %v, want 1s doubling up to 1m", got)
		}
	}
}

func TestSafeDirNameAndUniqueName(t *testing.T) {
	for in, want := range map[string]string{
		"abc.123@example.com": "abc.123_example.com",
		"../../etc/passwd":    "_.._etc_passwd", // one path element, never a traversal
		"a/b\\c":              "a_b_c",
		"":                    "mail",
		"...":                 "mail",
	} {
		if got := safeDirName(in); got != want {
			t.Errorf("safeDirName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeDirName(strings.Repeat("a", 500)); len(got) != 100 {
		t.Errorf("long id not truncated: %d", len(got))
	}

	used := map[string]bool{}
	got := []string{
		uniqueName("a.txt", used),
		uniqueName("a.txt", used),
		uniqueName("a.txt", used),
		uniqueName("b", used),
		uniqueName("b", used),
	}
	if strings.Join(got, ",") != "a.txt,a-2.txt,a-3.txt,b,b-2" {
		t.Errorf("uniqueName = %v", got)
	}
}

// --- dispatch --------------------------------------------------------------

func parsed(sender, subject, body string) *parsedMail {
	return &parsedMail{
		sender:     sender,
		senderName: "Sender",
		subject:    subject,
		body:       body,
		messageID:  subject + "@example.org",
	}
}

func TestDispatch_PublishesInboundMessage(t *testing.T) {
	c, mb := newTestChannel(t, nil)
	pm := parsed("alice@example.org", "Plans", "Lunch at noon?")
	pm.references = []string{"root@example.org"}

	if err := c.dispatch(context.Background(), pm, 9); err != nil {
		t.Fatal(err)
	}
	m := nextInbound(t, mb)

	if m.Content != "Subject: Plans\n\nLunch at noon?" {
		t.Errorf("content = %q", m.Content)
	}
	if m.Channel != "mail" || m.ChatID != "alice@example.org" || m.SenderID != "alice@example.org" {
		t.Errorf("channel=%q chat=%q sender=%q", m.Channel, m.ChatID, m.SenderID)
	}
	if m.MessageID != "Plans@example.org" || m.Context.ChatType != "direct" {
		t.Errorf("message id=%q chat type=%q", m.MessageID, m.Context.ChatType)
	}
	raw := m.Context.Raw
	if raw["subject"] != "Plans" || raw["message_id"] != "Plans@example.org" ||
		raw["references"] != "root@example.org Plans@example.org" {
		t.Errorf("raw = %v", raw)
	}
	if m.Sender.CanonicalID != "mail:alice@example.org" || m.Sender.DisplayName != "Sender" {
		t.Errorf("sender = %+v", m.Sender)
	}
}

func TestDispatch_BlockedAndFiltered(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*parsedMail)
	}{
		{"sender not in allow_from", func(p *parsedMail) { p.sender = "mallory@evil.net" }},
		{"no sender", func(p *parsedMail) { p.sender = "" }},
		{"automated", func(p *parsedMail) { p.automated = true }},
		{"failed authentication", func(p *parsedMail) { p.authFailed = true }},
		{"mail from ourselves", func(p *parsedMail) { p.sender = "bot@example.com" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mb := newTestChannel(t, func(_ *config.MailSettings, bc *config.Channel) {
				bc.AllowFrom = config.FlexibleStringSlice{"@example.org", "bot@example.com"}
			})
			pm := parsed("alice@example.org", "x", "body")
			tt.mutate(pm)
			if err := c.dispatch(context.Background(), pm, 1); err != nil {
				t.Fatal(err)
			}
			noInbound(t, mb, 50*time.Millisecond)
		})
	}
}

func TestDispatch_ReplyTo(t *testing.T) {
	tests := []struct {
		name, replyTo, wantChat string
	}{
		{"allowed reply-to is used", "assistant@example.org", "assistant@example.org"},
		{"disallowed reply-to is ignored", "attacker@evil.net", "alice@example.org"},
		{"no reply-to", "", "alice@example.org"},
		{"same as sender", "alice@example.org", "alice@example.org"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, mb := newTestChannel(t, nil)
			pm := parsed("alice@example.org", "x", "body")
			pm.replyTo = tt.replyTo
			if err := c.dispatch(context.Background(), pm, 1); err != nil {
				t.Fatal(err)
			}
			m := nextInbound(t, mb)
			if m.ChatID != tt.wantChat {
				t.Errorf("chat = %q, want %q", m.ChatID, tt.wantChat)
			}
			if m.SenderID != "alice@example.org" {
				t.Errorf("sender = %q, the From address must stay the sender", m.SenderID)
			}
		})
	}
}

func TestDispatch_DedupsByMessageID(t *testing.T) {
	c, mb := newTestChannel(t, nil)
	ctx := context.Background()
	for uid := imap.UID(1); uid <= 3; uid++ {
		if err := c.dispatch(ctx, parsed("alice@example.org", "same", "body"), uid); err != nil {
			t.Fatal(err)
		}
	}
	nextInbound(t, mb)
	noInbound(t, mb, 50*time.Millisecond)

	// A blocked sender must not poison the set: the same id from an allowed one passes.
	c2, mb2 := newTestChannel(t, nil)
	evil := parsed("mallory@evil.net", "same", "body")
	_ = c2.dispatch(ctx, evil, 1)
	_ = c2.dispatch(ctx, parsed("alice@example.org", "same", "body"), 2)
	nextInbound(t, mb2)
}

func TestDispatch_SyntheticIDWhenMessageIDMissing(t *testing.T) {
	c, mb := newTestChannel(t, nil)
	c.uidValidity = 3
	pm := parsed("alice@example.org", "x", "body")
	pm.messageID = ""
	pm.references = []string{"root@example.org"}

	if err := c.dispatch(context.Background(), pm, 77); err != nil {
		t.Fatal(err)
	}
	m := nextInbound(t, mb)
	if m.MessageID != c.syntheticID(77) || !isSyntheticID(m.MessageID) {
		t.Errorf("message id = %q, want %q", m.MessageID, c.syntheticID(77))
	}
	if _, ok := m.Context.Raw["message_id"]; ok {
		t.Errorf("synthetic id exposed for threading: %v", m.Context.Raw)
	}
	if m.Context.Raw["references"] != "root@example.org" {
		t.Errorf("references = %q", m.Context.Raw["references"])
	}

	// The same position gives the same id, so a redelivered UID is a duplicate.
	if err := c.dispatch(context.Background(), pm, 77); err != nil {
		t.Fatal(err)
	}
	noInbound(t, mb, 50*time.Millisecond)
}

func TestDispatch_SavesAttachmentsInWorkspace(t *testing.T) {
	workspace := t.TempDir()
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	bc := &config.Channel{AllowFrom: config.FlexibleStringSlice{"@example.org"}}
	c, err := NewMailChannel(
		bc,
		&config.MailSettings{IMAPHost: "h", SMTPHost: "h", Username: "bot@example.com"},
		mb,
		workspace,
	)
	if err != nil {
		t.Fatal(err)
	}
	store := media.NewFileMediaStore()
	c.SetMediaStore(store)

	pm := parsed("alice@example.org", "Files", "see attached")
	pm.messageID = "id/with:odd<chars>@example.org"
	pm.attachments = []attachment{
		{name: "a.png", contentType: "image/png", data: []byte("PNG1")},
		{name: "a.png", contentType: "image/png", data: []byte("PNG2")}, // same name
		{name: "b.pdf", contentType: "application/pdf", data: []byte("PDF")},
	}
	pm.droppedAttachments = 2

	if err := c.dispatch(context.Background(), pm, 1); err != nil {
		t.Fatal(err)
	}
	m := nextInbound(t, mb)

	if len(m.Media) != 3 {
		t.Fatalf("media refs = %v", m.Media)
	}
	for _, want := range []string{"[file: a.png]", "[file: a-2.png]", "[file: b.pdf]", "2 attachment(s) skipped"} {
		if !strings.Contains(m.Content, want) {
			t.Errorf("content lacks %q: %q", want, m.Content)
		}
	}

	contents := make([]string, 0, len(m.Media))
	for _, ref := range m.Media {
		path, meta, err := store.ResolveWithMeta(ref)
		if err != nil {
			t.Fatalf("ref %q does not resolve: %v", ref, err)
		}
		if !strings.HasPrefix(path, filepath.Join(workspace, "attachments")+string(filepath.Separator)) {
			t.Errorf("attachment stored outside <workspace>/attachments: %s", path)
		}
		if strings.Contains(strings.TrimPrefix(path, workspace), "<") || meta.Source != "mail" {
			t.Errorf("path=%q meta=%+v", path, meta)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, string(b))
		if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
			t.Errorf("attachment mode = %v, want 0600", info.Mode().Perm())
		}
	}
	if strings.Join(contents, ",") != "PNG1,PNG2,PDF" {
		t.Errorf("contents = %v", contents)
	}

	// Workspace files outlive the media scope: the store only forgets them.
	paths := make([]string, 0, len(m.Media))
	for _, ref := range m.Media {
		p, _ := store.Resolve(ref)
		paths = append(paths, p)
	}
	if err := store.ReleaseAll(m.MediaScope); err != nil {
		t.Fatal(err)
	}
	for i, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("file removed by the media store: %v", err)
		}
		if _, err := store.Resolve(m.Media[i]); err == nil {
			t.Errorf("ref %q still resolves after ReleaseAll", m.Media[i])
		}
	}
}

func TestDispatch_AttachmentsWithoutMediaStoreOrWorkspace(t *testing.T) {
	c, mb := newTestChannel(t, nil)
	c.workspace = "" // temp dir fallback
	c.SetMediaStore(nil)

	pm := parsed("alice@example.org", "NoStore", "body")
	pm.attachments = []attachment{{name: "x.txt", contentType: "text/plain", data: []byte("hi")}}
	if err := c.dispatch(context.Background(), pm, 1); err != nil {
		t.Fatal(err)
	}
	m := nextInbound(t, mb)
	if len(m.Media) != 1 || !strings.HasPrefix(m.Media[0], media.TempDir()) {
		t.Fatalf("media = %v, want a local path under %s", m.Media, media.TempDir())
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(m.Media[0])) })
	if b, _ := os.ReadFile(m.Media[0]); string(b) != "hi" {
		t.Errorf("file content = %q", b)
	}
}

func TestDispatch_BlockedSenderWritesNothingToDisk(t *testing.T) {
	c, mb := newTestChannel(t, nil)
	pm := parsed("mallory@evil.net", "Evil", "body")
	pm.attachments = []attachment{{name: "x.bin", contentType: "application/octet-stream", data: []byte("x")}}
	_ = c.dispatch(context.Background(), pm, 1)
	noInbound(t, mb, 50*time.Millisecond)
	if _, err := os.Stat(filepath.Join(c.workspace, "attachments")); !os.IsNotExist(err) {
		t.Errorf("attachments directory created for a blocked sender: %v", err)
	}
}
