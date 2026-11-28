package mail

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/media"
)

func init() { message.CharsetReader = charset.Reader }

// mimePart is a decoded MIME entity of a rendered message.
type mimePart struct {
	ctype    string
	filename string
	body     string
	children []mimePart
}

func readMessage(t *testing.T, raw []byte) (message.Header, mimePart) {
	t.Helper()
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		t.Fatalf("read rendered mail: %v", err)
	}
	return e.Header, readPart(t, e)
}

func readPart(t *testing.T, e *message.Entity) mimePart {
	t.Helper()
	ct, _, _ := e.Header.ContentType()
	_, dp, _ := e.Header.ContentDisposition()
	p := mimePart{ctype: ct, filename: dp["filename"]}
	if mr := e.MultipartReader(); mr != nil {
		for {
			child, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil && !message.IsUnknownCharset(err) {
				t.Fatalf("next part: %v", err)
			}
			p.children = append(p.children, readPart(t, child))
		}
		return p
	}
	b, err := io.ReadAll(e.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	p.body = string(b)
	return p
}

func ctypes(parts []mimePart) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, p.ctype)
	}
	return out
}

func equalStrings(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

func testOutgoing() *outgoing {
	return &outgoing{
		to:         "alice@example.org",
		subject:    "Re: Plans",
		body:       "Hello **world**\n\n| a | b |\n|---|---|\n| 1 | 2 |",
		inReplyTo:  "parent@example.org",
		references: []string{"root@example.org", "parent@example.org"},
	}
}

func TestRender_MultipartAlternative(t *testing.T) {
	c, _ := newTestChannel(t, nil)
	raw, err := c.render("Bot <bot@example.com>", testOutgoing())
	if err != nil {
		t.Fatal(err)
	}
	h, top := readMessage(t, raw)

	if top.ctype != "multipart/alternative" {
		t.Fatalf("top-level type = %q", top.ctype)
	}
	if !equalStrings(ctypes(top.children), []string{"text/plain", "text/html"}) {
		t.Fatalf("parts = %v, want plain then html", ctypes(top.children))
	}
	if !strings.Contains(top.children[0].body, "Hello **world**") {
		t.Errorf("plain part must carry the raw Markdown: %q", top.children[0].body)
	}
	html := top.children[1].body
	if !strings.Contains(html, "<strong>world</strong>") || !strings.Contains(html, "<table>") {
		t.Errorf("html part is not rendered Markdown: %q", html)
	}

	for key, want := range map[string]string{
		"To":             "<alice@example.org>",
		"Subject":        "Re: Plans",
		"In-Reply-To":    "<parent@example.org>",
		"References":     "<root@example.org> <parent@example.org>",
		"Auto-Submitted": "auto-generated",
	} {
		if got := h.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if from := h.Get("From"); !strings.Contains(from, "bot@example.com") {
		t.Errorf("From = %q", from)
	}
	if id := h.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-Id = %q, want <...@example.com>", id)
	}
	if h.Get("Date") == "" {
		t.Error("Date missing")
	}
}

func TestRender_PlainTextOnly(t *testing.T) {
	c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) { s.PlainTextOnly = true })
	raw, err := c.render("bot@example.com", testOutgoing())
	if err != nil {
		t.Fatal(err)
	}
	_, top := readMessage(t, raw)
	if top.ctype != "text/plain" || len(top.children) != 0 {
		t.Fatalf(
			"plain_text_only must give a single text/plain part, got %q with %d children",
			top.ctype,
			len(top.children),
		)
	}
	if !strings.Contains(top.body, "Hello **world**") {
		t.Errorf("body = %q", top.body)
	}
}

func TestRender_MultipartMixedWithAttachments(t *testing.T) {
	c, _ := newTestChannel(t, nil)
	out := testOutgoing()
	blob := []byte{0x00, 0xff, 0x10, '\r', '\n', 0x80}
	out.attachments = []attachment{
		{name: "data.bin", contentType: "application/octet-stream", data: blob},
		{name: "notes.txt", contentType: "text/plain", data: []byte("some notes")},
	}
	raw, err := c.render("bot@example.com", out)
	if err != nil {
		t.Fatal(err)
	}
	_, top := readMessage(t, raw)

	if top.ctype != "multipart/mixed" {
		t.Fatalf("top-level type = %q, want multipart/mixed", top.ctype)
	}
	if !equalStrings(
		ctypes(top.children),
		[]string{"multipart/alternative", "application/octet-stream", "text/plain"},
	) {
		t.Fatalf("children = %v", ctypes(top.children))
	}
	if !equalStrings(ctypes(top.children[0].children), []string{"text/plain", "text/html"}) {
		t.Errorf("alternative = %v", ctypes(top.children[0].children))
	}
	if got := top.children[1]; got.filename != "data.bin" || got.body != string(blob) {
		t.Errorf("binary attachment mangled: name=%q body=%v", got.filename, []byte(got.body))
	}
	if got := top.children[2]; got.filename != "notes.txt" || got.body != "some notes" {
		t.Errorf("text attachment mangled: name=%q body=%q", got.filename, got.body)
	}
}

func TestRender_MixedPlainTextOnly(t *testing.T) {
	c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) { s.PlainTextOnly = true })
	out := testOutgoing()
	out.attachments = []attachment{{name: "a.bin", contentType: "application/octet-stream", data: []byte("x")}}
	raw, err := c.render("bot@example.com", out)
	if err != nil {
		t.Fatal(err)
	}
	_, top := readMessage(t, raw)
	if top.ctype != "multipart/mixed" ||
		!equalStrings(ctypes(top.children), []string{"text/plain", "application/octet-stream"}) {
		t.Fatalf("got %q %v", top.ctype, ctypes(top.children))
	}
}

func TestRender_NoThreadingHeadersWithoutParent(t *testing.T) {
	c, _ := newTestChannel(t, nil)
	raw, err := c.render("bot@example.com", &outgoing{to: "a@example.org", subject: "Hi", body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := readMessage(t, raw)
	if h.Has("In-Reply-To") || h.Has("References") {
		t.Errorf(
			"unexpected threading headers: In-Reply-To=%q References=%q",
			h.Get("In-Reply-To"),
			h.Get("References"),
		)
	}
}

func TestRender_ParentIsAppendedToReferencesOnce(t *testing.T) {
	c, _ := newTestChannel(t, nil)
	// parent already last in the chain: not repeated
	raw, _ := c.render("bot@example.com", &outgoing{
		to: "a@example.org", subject: "s", body: "x",
		inReplyTo: "p@x", references: []string{"r@x", "p@x"},
	})
	if h, _ := readMessage(t, raw); h.Get("References") != "<r@x> <p@x>" {
		t.Errorf("References = %q", h.Get("References"))
	}
	// parent missing from the chain: appended
	raw, _ = c.render("bot@example.com", &outgoing{
		to: "a@example.org", subject: "s", body: "x",
		inReplyTo: "p@x", references: []string{"r@x"},
	})
	if h, _ := readMessage(t, raw); h.Get("References") != "<r@x> <p@x>" {
		t.Errorf("References = %q", h.Get("References"))
	}
	// no chain at all: parent alone
	raw, _ = c.render("bot@example.com", &outgoing{to: "a@example.org", subject: "s", body: "x", inReplyTo: "p@x"})
	if h, _ := readMessage(t, raw); h.Get("References") != "<p@x>" {
		t.Errorf("References = %q", h.Get("References"))
	}
}

func TestNewOutgoing_ThreadingFromInboundContext(t *testing.T) {
	c, _ := newTestChannel(t, nil)

	t.Run("angle brackets are applied exactly once", func(t *testing.T) {
		out, err := c.newOutgoing("alice@example.org", "hi", bus.InboundContext{Raw: map[string]string{
			"subject":    "Plans",
			"message_id": "<parent@example.org>", // already bracketed
			"references": "<root@example.org> parent@example.org",
		}}, "")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := c.render("bot@example.com", out)
		if err != nil {
			t.Fatal(err)
		}
		h, _ := readMessage(t, raw)
		if got := h.Get("In-Reply-To"); got != "<parent@example.org>" {
			t.Errorf("In-Reply-To = %q", got)
		}
		if got := h.Get("References"); got != "<root@example.org> <parent@example.org>" {
			t.Errorf("References = %q", got)
		}
		if got := h.Get("Subject"); got != "Re: Plans" {
			t.Errorf("Subject = %q", got)
		}
	})

	t.Run("synthetic ids never reach the headers", func(t *testing.T) {
		synth := c.syntheticID(7)
		out, err := c.newOutgoing("alice@example.org", "hi", bus.InboundContext{
			MessageID: synth,
			Raw:       map[string]string{"message_id": synth, "references": "real@example.org " + synth},
		}, synth)
		if err != nil {
			t.Fatal(err)
		}
		if out.inReplyTo != "" {
			t.Errorf("inReplyTo = %q, want none", out.inReplyTo)
		}
		raw, _ := c.render("bot@example.com", out)
		if strings.Contains(string(raw), syntheticIDPrefix) {
			t.Errorf("synthetic id leaked into the rendered mail")
		}
		if h, _ := readMessage(t, raw); h.Get("References") != "<real@example.org>" {
			t.Errorf("References = %q", h.Get("References"))
		}
	})

	t.Run("falls back to the reply-to message id", func(t *testing.T) {
		out, err := c.newOutgoing("alice@example.org", "hi", bus.InboundContext{}, "orig@example.org")
		if err != nil {
			t.Fatal(err)
		}
		if out.inReplyTo != "orig@example.org" {
			t.Errorf("inReplyTo = %q", out.inReplyTo)
		}
	})

	t.Run("default subject", func(t *testing.T) {
		out, _ := c.newOutgoing("alice@example.org", "hi", bus.InboundContext{}, "")
		if out.subject != defaultSubject {
			t.Errorf("subject = %q", out.subject)
		}
		c.cfg.DefaultSubject = "Daily report"
		defer func() { c.cfg.DefaultSubject = "" }()
		out, _ = c.newOutgoing("alice@example.org", "hi", bus.InboundContext{}, "")
		if out.subject != "Daily report" {
			t.Errorf("subject = %q", out.subject)
		}
	})
}

func TestRender_HeaderInjectionIsNeutralized(t *testing.T) {
	c, _ := newTestChannel(t, nil)

	out, err := c.newOutgoing("alice@example.org", "hi", bus.InboundContext{Raw: map[string]string{
		"subject": "x\r\nBcc: evil@example.net",
	}}, "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := c.render("bot@example.com", out)
	if err != nil {
		t.Fatal(err)
	}
	h, _ := readMessage(t, raw)
	if h.Has("Bcc") {
		t.Fatalf("injected Bcc header made it into the mail:\n%s", raw)
	}
	if got := h.Get("Subject"); !strings.Contains(got, "Bcc: evil@example.net") || strings.ContainsAny(got, "\r\n") {
		t.Errorf("subject = %q", got)
	}

	if _, err := c.newOutgoing(
		"alice@example.org\r\nBcc: evil@example.net",
		"hi",
		bus.InboundContext{},
		"",
	); err == nil {
		t.Error("recipient with an embedded header was accepted")
	} else if !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Errorf("err = %v, want ErrSendFailed", err)
	}
	if _, err := c.newOutgoing("not an address", "hi", bus.InboundContext{}, ""); err == nil {
		t.Error("invalid recipient accepted")
	}
}

func TestMarkdownToHTML(t *testing.T) {
	html := markdownToHTML(
		"# Title\n\n**bold** ~~gone~~\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1)) [ok](https://example.org)\n\n- a\n- b\n\nline1\nline2",
	)
	for _, want := range []string{"<h1", "<strong>bold</strong>", "<del>gone</del>", "<li>a</li>", `href="https://example.org"`} {
		if !strings.Contains(html, want) {
			t.Errorf("html lacks %q:\n%s", want, html)
		}
	}
	for _, bad := range []string{"<script", "javascript:"} {
		if strings.Contains(html, bad) {
			t.Errorf("html still contains %q:\n%s", bad, html)
		}
	}
}

func TestReplySubjectAndBareMessageID(t *testing.T) {
	for in, want := range map[string]string{"Plans": "Re: Plans", "Re: Plans": "Re: Plans", "RE: Plans": "RE: Plans", "re:x": "re:x", "": "Re: "} {
		if got := replySubject(in); got != want {
			t.Errorf("replySubject(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"<a@b>": "a@b", " a@b ": "a@b", "<<a@b>>": "a@b", "a@b": "a@b", "": ""} {
		if got := bareMessageID(in); got != want {
			t.Errorf("bareMessageID(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- SMTP delivery ---------------------------------------------------------

func runningChannel(t *testing.T, f *fakeSMTP, mutate func(*config.MailSettings)) *MailChannel {
	t.Helper()
	c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) {
		pointSMTP(s, f)
		if mutate != nil {
			mutate(s)
		}
	})
	c.SetRunning(true)
	return c
}

func TestSend_PlainAuth(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, nil)

	_, err := c.Send(context.Background(), bus.OutboundMessage{
		ChatID:  "Alice <alice@example.org>",
		Content: "hello **there**",
		Context: bus.InboundContext{Raw: map[string]string{"subject": "Q", "message_id": "m1@example.org"}},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	rec := f.next(t)
	if rec.authMech != "PLAIN" || !strings.Contains(rec.authPayload, "bot@example.com") ||
		!strings.Contains(rec.authPayload, "secret") {
		t.Errorf("auth = %s %q", rec.authMech, rec.authPayload)
	}
	if rec.from != "bot@example.com" || rec.rcpt != "alice@example.org" {
		t.Errorf("envelope from=%q rcpt=%q", rec.from, rec.rcpt)
	}
	h, top := readMessage(t, []byte(rec.data))
	if h.Get("Subject") != "Re: Q" || h.Get("In-Reply-To") != "<m1@example.org>" ||
		top.ctype != "multipart/alternative" {
		t.Errorf("subject=%q in-reply-to=%q type=%q", h.Get("Subject"), h.Get("In-Reply-To"), top.ctype)
	}
}

func TestSend_SeparateSMTPCredentialsAndFromAddress(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, func(s *config.MailSettings) {
		s.SMTPUsername = "relay-user"
		s.SMTPPassword = *config.NewSecureString("relay-pass")
		s.FromAddress = "Support <support@example.com>"
	})
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatal(err)
	}
	rec := f.next(t)
	if !strings.Contains(rec.authPayload, "relay-user") || !strings.Contains(rec.authPayload, "relay-pass") ||
		strings.Contains(rec.authPayload, "secret") {
		t.Errorf("auth payload = %q", rec.authPayload)
	}
	if rec.from != "support@example.com" {
		t.Errorf("envelope from = %q", rec.from)
	}
	if h, _ := readMessage(t, []byte(rec.data)); !strings.Contains(h.Get("From"), "support@example.com") {
		t.Errorf("From header = %q", h.Get("From"))
	}
}

func TestSend_XOAuth2(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, func(s *config.MailSettings) { s.OAuthToken = *config.NewSecureString("static-token") })

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatal(err)
	}
	rec := f.next(t)
	if rec.authMech != "XOAUTH2" || rec.authPayload != "user=bot@example.com\x01auth=Bearer static-token\x01\x01" {
		t.Errorf("auth = %s %q", rec.authMech, rec.authPayload)
	}

	// A token service is consulted on every send, so rotated tokens are used.
	calls := 0
	c.SetTokenSource(func(context.Context) (string, error) { calls++; return "rotated-" + itoa(calls), nil })
	for i := 1; i <= 2; i++ {
		if _, err := c.Send(
			context.Background(),
			bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
		); err != nil {
			t.Fatal(err)
		}
		if rec := f.next(t); !strings.Contains(rec.authPayload, "Bearer rotated-"+itoa(i)) {
			t.Errorf("send %d auth = %q", i, rec.authPayload)
		}
	}

	// A failing token source aborts the send.
	c.SetTokenSource(func(context.Context) (string, error) { return "", errors.New("token service down") })
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err == nil {
		t.Fatal("send succeeded without a token")
	}
	f.none(t, 200*time.Millisecond)
}

func TestSend_AuthRejected(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	f.rejectAuth = true
	c := runningChannel(t, f, nil)
	_, err := c.Send(context.Background(), bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"})
	if err == nil || !strings.Contains(err.Error(), "535") {
		t.Fatalf("err = %v, want the 535 rejection", err)
	}
}

func TestSend_NoCredentialsMeansNoAuth(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, func(s *config.MailSettings) {
		s.Username, s.Password = "", config.SecureString{}
		s.FromAddress = "bot@example.com"
	})
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatal(err)
	}
	if rec := f.next(t); rec.authMech != "" {
		t.Errorf("authenticated although no credentials are configured: %s", rec.authMech)
	}
}

func TestSend_StartTLS(t *testing.T) {
	serverTLS, clientTLS := testTLS(t)
	f := newFakeSMTP(t, serverTLS, false)
	c := runningChannel(t, f, nil)
	c.tlsOverride = clientTLS

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec := f.next(t); !rec.overTLS || rec.authMech != "PLAIN" {
		t.Errorf("overTLS=%v auth=%q, want STARTTLS and PLAIN", rec.overTLS, rec.authMech)
	}
}

func TestSend_ImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := testTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	c := runningChannel(t, f, func(s *config.MailSettings) { s.SMTPImplicitTLS = true })
	c.tlsOverride = clientTLS

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rec := f.next(t); !rec.overTLS {
		t.Error("message was not delivered over TLS")
	}
}

func TestSend_RejectsUntrustedCertificate(t *testing.T) {
	serverTLS, _ := testTLS(t)
	f := newFakeSMTP(t, serverTLS, true)
	c := runningChannel(t, f, func(s *config.MailSettings) { s.SMTPImplicitTLS = true }) // system roots only
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); err == nil {
		t.Fatal("send succeeded against an untrusted certificate")
	}
}

func TestSend_Guards(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, nil)

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "  \n"},
	); err != nil {
		t.Errorf("empty content: %v", err)
	}
	f.none(t, 100*time.Millisecond)

	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "bogus", Content: "x"},
	); !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Errorf("bad recipient: err = %v", err)
	}

	c.SetRunning(false)
	if _, err := c.Send(
		context.Background(),
		bus.OutboundMessage{ChatID: "alice@example.org", Content: "x"},
	); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Errorf("not running: err = %v", err)
	}
}

func TestSMTPXOAuth2_RefusesCleartext(t *testing.T) {
	a := &smtpXOAuth2{user: "u", token: "t"}
	if _, _, err := a.Start(&smtp.ServerInfo{Name: "mail.example.com", TLS: false}); err == nil {
		t.Error("XOAUTH2 offered over an unencrypted remote connection")
	}
	for _, info := range []*smtp.ServerInfo{{Name: "mail.example.com", TLS: true}, {Name: "localhost"}, {Name: "127.0.0.1"}, {Name: "::1"}} {
		if mech, ir, err := a.Start(
			info,
		); err != nil || mech != "XOAUTH2" ||
			string(ir) != "user=u\x01auth=Bearer t\x01\x01" {
			t.Errorf("Start(%+v) = %q %q %v", info, mech, ir, err)
		}
	}
	if _, err := a.Next([]byte(`{"status":"401"}`), true); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("Next with error challenge: %v", err)
	}
	if resp, err := a.Next(nil, false); err != nil || resp != nil {
		t.Errorf("Next at end: %v %v", resp, err)
	}
}

// --- media -----------------------------------------------------------------

func TestSendMedia(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, func(s *config.MailSettings) { s.MaxAttachmentBytes = 64 })
	store := media.NewFileMediaStore()
	c.SetMediaStore(store)

	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		ref, err := store.Store(
			p,
			media.MediaMeta{
				Filename:      name,
				ContentType:   "application/octet-stream",
				CleanupPolicy: media.CleanupPolicyForgetOnly,
			},
			"scope",
		)
		if err != nil {
			t.Fatal(err)
		}
		return ref
	}
	small := []byte{0, 1, 2, 3, 255}
	okRef := write("report.bin", small)
	bigRef := write("huge.bin", bytes.Repeat([]byte("x"), 65))

	_, err := c.SendMedia(context.Background(), bus.OutboundMediaMessage{
		ChatID:  "alice@example.org",
		Context: bus.InboundContext{Raw: map[string]string{"subject": "Files", "message_id": "m@example.org"}},
		Parts: []bus.MediaPart{
			{Ref: okRef, Caption: "Here is the **report**", ContentType: "application/x-report"},
			{Ref: bigRef, Caption: "too big"},
			{Ref: "media://does-not-exist"},
		},
	})
	if err != nil {
		t.Fatalf("SendMedia: %v", err)
	}
	h, top := readMessage(t, []byte(f.next(t).data))
	if top.ctype != "multipart/mixed" || len(top.children) != 2 {
		t.Fatalf(
			"type=%q children=%v; want mixed with body + the one attachment that fits",
			top.ctype,
			ctypes(top.children),
		)
	}
	if h.Get("Subject") != "Re: Files" || h.Get("In-Reply-To") != "<m@example.org>" {
		t.Errorf("subject=%q in-reply-to=%q", h.Get("Subject"), h.Get("In-Reply-To"))
	}
	if !strings.Contains(top.children[0].children[1].body, "<strong>report</strong>") {
		t.Errorf("caption not rendered as the body")
	}
	att := top.children[1]
	if att.ctype != "application/x-report" || att.filename != "report.bin" || att.body != string(small) {
		t.Errorf("attachment = %q %q %v", att.ctype, att.filename, []byte(att.body))
	}
	if strings.Contains(top.children[0].children[0].body, "too big") {
		t.Errorf("caption of a skipped attachment leaked into the body")
	}

	// Nothing deliverable: nothing sent.
	if _, err := c.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "alice@example.org", Parts: []bus.MediaPart{{Ref: bigRef}}},
	); err != nil {
		t.Fatal(err)
	}
	f.none(t, 100*time.Millisecond)

	// Without a caption the body says so.
	if _, err := c.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "alice@example.org", Parts: []bus.MediaPart{{Ref: okRef}}},
	); err != nil {
		t.Fatal(err)
	}
	_, top = readMessage(t, []byte(f.next(t).data))
	if !strings.Contains(top.children[0].children[0].body, "attached") {
		t.Errorf("default body = %q", top.children[0].children[0].body)
	}
}

func TestSendMedia_Guards(t *testing.T) {
	f := newFakeSMTP(t, nil, false)
	c := runningChannel(t, f, nil)
	if _, err := c.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "alice@example.org"},
	); !errors.Is(
		err,
		channels.ErrSendFailed,
	) {
		t.Errorf("no media store: err = %v", err)
	}
	c.SetMediaStore(media.NewFileMediaStore())
	c.SetRunning(false)
	if _, err := c.SendMedia(
		context.Background(),
		bus.OutboundMediaMessage{ChatID: "alice@example.org"},
	); !errors.Is(
		err,
		channels.ErrNotRunning,
	) {
		t.Errorf("not running: err = %v", err)
	}
}

func TestReadCapped(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("12345"), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readCapped(p, 5); err != nil || string(b) != "12345" {
		t.Errorf("at the limit: %q %v", b, err)
	}
	if _, err := readCapped(p, 4); err == nil {
		t.Error("over the limit accepted")
	}
	if _, err := readCapped(p+"-missing", 4); err == nil {
		t.Error("missing file accepted")
	}
}
