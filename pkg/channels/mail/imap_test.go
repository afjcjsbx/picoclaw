package mail

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

const (
	testUser     = "bot@example.com"
	testPassword = "secret"
	testToken    = "good-token"
)

// testIMAP wraps go-imap's in-memory server with the hooks the tests need:
// IDLE and FETCH counters, XOAUTH2, and the ability to drop and revive it.
type testIMAP struct {
	t    *testing.T
	mem  *imapmemserver.Server
	user *imapmemserver.User
	addr string
	tls  *tls.Config // non-nil: implicit TLS

	srv       *imapserver.Server
	idleCalls atomic.Int32
	selects   atomic.Int32 // SELECTs completed, i.e. cursors captured

	mu         sync.Mutex
	fetchSizes []int
}

func newTestIMAP(t *testing.T, tlsCfg *tls.Config) *testIMAP {
	t.Helper()
	s := &testIMAP{t: t, mem: imapmemserver.New(), user: imapmemserver.NewUser(testUser, testPassword), tls: tlsCfg}
	if err := s.user.Create("INBOX", nil); err != nil {
		t.Fatal(err)
	}
	s.mem.AddUser(s.user)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s.addr = ln.Addr().String()
	s.serve(ln)
	t.Cleanup(func() { _ = s.srv.Close() })
	return s
}

func (s *testIMAP) serve(ln net.Listener) {
	if s.tls != nil {
		ln = tls.NewListener(ln, s.tls)
	}
	s.srv = imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return &testSession{Session: s.mem.NewSession(), srv: s}, nil, nil
		},
		InsecureAuth: true,
		Logger:       log.New(io.Discard, "", 0),
	})
	go func() { _ = s.srv.Serve(ln) }()
}

// kill drops every connection and stops listening; revive brings the server
// back on the same address with the mailbox intact.
func (s *testIMAP) kill() { _ = s.srv.Close() }

func (s *testIMAP) revive() {
	s.t.Helper()
	var ln net.Listener
	var err error
	for range 50 {
		if ln, err = net.Listen("tcp", s.addr); err == nil {
			s.serve(ln)
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	s.t.Fatalf("cannot rebind %s: %v", s.addr, err)
}

func atoiPort(addr string) int {
	_, p, _ := net.SplitHostPort(addr)
	var n int
	_, _ = fmt.Sscanf(p, "%d", &n)
	return n
}

type literal struct {
	io.Reader
	size int64
}

func (l literal) Size() int64 { return l.size }

// deliver appends a raw message to INBOX and returns its UID.
func (s *testIMAP) deliver(raw string, flags ...imap.Flag) imap.UID {
	s.t.Helper()
	data, err := s.user.Append(
		"INBOX",
		literal{strings.NewReader(raw), int64(len(raw))},
		&imap.AppendOptions{Flags: flags},
	)
	if err != nil {
		s.t.Fatalf("deliver: %v", err)
	}
	return data.UID
}

// flagsOf reads the flags of uid through an independent connection.
func (s *testIMAP) flagsOf(uid imap.UID) []imap.Flag {
	s.t.Helper()
	cl, err := imapclient.DialInsecure(s.addr, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	defer cl.Close()
	if err = cl.Login(testUser, testPassword).Wait(); err != nil {
		s.t.Fatal(err)
	}
	if _, err = cl.Select("INBOX", nil).Wait(); err != nil {
		s.t.Fatal(err)
	}
	msgs, err := cl.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{UID: true, Flags: true}).Collect()
	if err != nil || len(msgs) != 1 {
		s.t.Fatalf("fetch flags of %d: %v (%d msgs)", uid, err, len(msgs))
	}
	return msgs[0].Flags
}

func (s *testIMAP) seen(uid imap.UID) bool { return slices.Contains(s.flagsOf(uid), imap.FlagSeen) }

func (s *testIMAP) maxFetchSize() (largest, count int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.fetchSizes {
		largest = max(largest, n)
	}
	return largest, len(s.fetchSizes)
}

// testSession counts IDLE and FETCH commands and adds XOAUTH2.
type testSession struct {
	imapserver.Session
	srv *testIMAP
}

func (s *testSession) Select(name string, o *imap.SelectOptions) (*imap.SelectData, error) {
	data, err := s.Session.Select(name, o)
	s.srv.selects.Add(1)
	return data, err
}

func (s *testSession) Idle(w *imapserver.UpdateWriter, stop <-chan struct{}) error {
	s.srv.idleCalls.Add(1)
	return s.Session.Idle(w, stop)
}

func (s *testSession) Fetch(w *imapserver.FetchWriter, set imap.NumSet, o *imap.FetchOptions) error {
	if uids, ok := set.(imap.UIDSet); ok {
		if nums, _ := uids.Nums(); len(nums) > 0 {
			s.srv.mu.Lock()
			s.srv.fetchSizes = append(s.srv.fetchSizes, len(nums))
			s.srv.mu.Unlock()
		}
	}
	return s.Session.Fetch(w, set, o)
}

func (s *testSession) AuthenticateMechanisms() []string { return []string{"XOAUTH2"} }

func (s *testSession) Authenticate(mech string) (sasl.Server, error) {
	return xoauth2Server{func(user, token string) error {
		if token != testToken {
			return &imap.Error{Type: imap.StatusResponseTypeNo, Text: "bad token"}
		}
		return s.Session.Login(user, testPassword)
	}}, nil
}

type xoauth2Server struct {
	login func(user, token string) error
}

func (x xoauth2Server) Next(resp []byte) ([]byte, bool, error) {
	if resp == nil {
		return nil, false, nil // ask for the initial response
	}
	user, rest, ok := strings.Cut(strings.TrimPrefix(string(resp), "user="), "\x01auth=Bearer ")
	token := strings.TrimSuffix(rest, "\x01\x01")
	if !ok || !strings.HasPrefix(string(resp), "user=") {
		return nil, false, &imap.Error{Type: imap.StatusResponseTypeNo, Text: "malformed XOAUTH2"}
	}
	return nil, true, x.login(user, token)
}

// withoutIdle fronts backend with a proxy that deletes IDLE from the server's
// capability list, imitating a server that lacks the extension.
func withoutIdle(t *testing.T, backend string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			client, err := ln.Accept()
			if err != nil {
				return
			}
			server, err := net.Dial("tcp", backend)
			if err != nil {
				client.Close()
				continue
			}
			go func() { _, _ = io.Copy(server, client); server.Close() }()
			go func() {
				buf := make([]byte, 8192)
				for {
					n, err := server.Read(buf)
					if n > 0 {
						_, _ = client.Write(bytes.ReplaceAll(buf[:n], []byte(" IDLE"), nil))
					}
					if err != nil {
						client.Close()
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().String()
}

// --- helpers ---------------------------------------------------------------

// fixture builds a plain-text message. An empty msgID omits the Message-ID header.
func fixture(msgID, from, subject, body string, headers ...string) string {
	var b strings.Builder
	if msgID != "" {
		fmt.Fprintf(&b, "Message-ID: <%s>\r\n", msgID)
	}
	fmt.Fprintf(
		&b,
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: Mon, 02 Jan 2006 15:04:05 +0000\r\n",
		from,
		testUser,
		subject,
	)
	for _, h := range headers {
		b.WriteString(h + "\r\n")
	}
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n\r\n" + body + "\r\n")
	return b.String()
}

const alice = "Alice <alice@example.org>"

// imapChannel builds a channel pointed at srv and the fake SMTP server (if any).
func imapChannel(
	t *testing.T,
	srv *testIMAP,
	mutate func(*config.MailSettings, *config.Channel),
) (*MailChannel, *bus.MessageBus) {
	t.Helper()
	return newTestChannel(t, func(s *config.MailSettings, bc *config.Channel) {
		s.IMAPPort = atoiPort(srv.addr)
		if mutate != nil {
			mutate(s, bc)
		}
	})
}

// waitIdle waits until the channel is parked in IDLE, which means the initial
// drain is over and the cursor is set.
func waitIdle(t *testing.T, srv *testIMAP, atLeast int32) {
	t.Helper()
	eventually(t, "the channel to enter IDLE", func() bool { return srv.idleCalls.Load() >= atLeast })
}

func subjects(msgs []bus.InboundMessage) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		_, rest, _ := strings.Cut(m.Content, "Subject: ")
		line, _, _ := strings.Cut(rest, "\n")
		out = append(out, line)
	}
	return out
}

func collect(t *testing.T, mb *bus.MessageBus, n int) []bus.InboundMessage {
	t.Helper()
	out := make([]bus.InboundMessage, 0, n)
	for range n {
		out = append(out, nextInbound(t, mb))
	}
	return out
}

// --- tests -----------------------------------------------------------------

func TestIMAP_ActiveModeDrainsUnseenAndMarksSeen(t *testing.T) {
	srv := newTestIMAP(t, nil)
	u1 := srv.deliver(fixture("1@x", alice, "first", "one"))
	alreadyRead := srv.deliver(fixture("2@x", alice, "already read", "two"), imap.FlagSeen)
	u3 := srv.deliver(fixture("3@x", alice, "third", "three"))

	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)

	got := collect(t, mb, 2)
	if s := subjects(got); !slices.Equal(s, []string{"first", "third"}) {
		t.Fatalf("drained %v, want the unseen mail only, in UID order", s)
	}
	noInbound(t, mb, 200*time.Millisecond)

	waitIdle(t, srv, 1)
	if !srv.seen(u1) || !srv.seen(u3) {
		t.Error("BODY[] fetch should have set \\Seen on drained mail")
	}
	if !srv.seen(alreadyRead) {
		t.Error("an already-read message lost its flag")
	}

	// Mail arriving later is fetched through the UID threshold, and flagged too.
	u4 := srv.deliver(fixture("4@x", alice, "fourth", "four"))
	if m := nextInbound(t, mb); subjects([]bus.InboundMessage{m})[0] != "fourth" {
		t.Errorf("got %q", m.Content)
	}
	eventually(t, "\\Seen on the live message", func() bool { return srv.seen(u4) })
	noInbound(t, mb, 200*time.Millisecond)
}

func TestIMAP_ObserverModeSkipsBacklogAndNeverTouchesFlags(t *testing.T) {
	srv := newTestIMAP(t, nil)
	backlog := []imap.UID{
		srv.deliver(fixture("b1@x", alice, "old one", "x")),
		srv.deliver(fixture("b2@x", alice, "old two", "x")),
		srv.deliver(fixture("b3@x", alice, "old three", "x")),
	}

	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.ObserverMode = true })
	startChannel(t, c)
	waitIdle(t, srv, 1)
	noInbound(t, mb, 200*time.Millisecond)

	fresh := srv.deliver(fixture("n1@x", alice, "brand new", "hello"))
	if m := nextInbound(t, mb); subjects([]bus.InboundMessage{m})[0] != "brand new" {
		t.Fatalf("got %q", m.Content)
	}
	noInbound(t, mb, 200*time.Millisecond)

	for _, uid := range append(backlog, fresh) {
		if srv.seen(uid) {
			t.Errorf("observer mode set \\Seen on uid %d", uid)
		}
	}

	// A human reading the mail in between must not hide it from the bot.
	srv.deliver(fixture("n2@x", alice, "read by a human first", "x"), imap.FlagSeen)
	if m := nextInbound(t, mb); subjects([]bus.InboundMessage{m})[0] != "read by a human first" {
		t.Fatalf("got %q", m.Content)
	}
}

func TestIMAP_IdleDeliversWithoutWaitingForThePollInterval(t *testing.T) {
	srv := newTestIMAP(t, nil)
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.PollIntervalSecs = 3600 })
	startChannel(t, c)
	waitIdle(t, srv, 1)

	start := time.Now()
	srv.deliver(fixture("i1@x", alice, "pushed", "x"))
	nextInbound(t, mb)
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("took %v: not delivered by IDLE push", d)
	}
}

func TestIMAP_IdleIsRenewed(t *testing.T) {
	srv := newTestIMAP(t, nil)
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.IdleTimeoutSecs = 1 })
	startChannel(t, c)

	// Nothing is delivered, yet IDLE is re-issued every idle_timeout_secs.
	waitIdle(t, srv, 3)

	srv.deliver(fixture("r1@x", alice, "after renewals", "x"))
	if m := nextInbound(t, mb); subjects([]bus.InboundMessage{m})[0] != "after renewals" {
		t.Errorf("got %q", m.Content)
	}
}

func TestIMAP_PollingFallbackWhenServerLacksIdle(t *testing.T) {
	srv := newTestIMAP(t, nil)
	addr := withoutIdle(t, srv.addr)
	// Observer mode: only mail arriving after SELECT counts, so each message
	// below can only be found by a poll, never by the startup drain.
	c, mb := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) {
		s.IMAPPort = atoiPort(addr)
		s.PollIntervalSecs = 1
		s.ObserverMode = true
	})
	startChannel(t, c)
	eventually(t, "the first SELECT", func() bool { return srv.selects.Load() >= 1 })

	for _, subject := range []string{"polled one", "polled two"} {
		start := time.Now()
		srv.deliver(fixture(subject+"@x", alice, subject, "x"))
		if m := nextInbound(t, mb); subjects([]bus.InboundMessage{m})[0] != subject {
			t.Fatalf("got %q, want %q", m.Content, subject)
		}
		if d := time.Since(start); d < 100*time.Millisecond || d > 4*time.Second {
			t.Errorf("%q arrived after %v, expected roughly one poll interval (1s)", subject, d)
		}
	}
	if n := srv.idleCalls.Load(); n != 0 {
		t.Errorf("IDLE was used %d times against a server that does not offer it", n)
	}
}

func TestIMAP_FetchesInBatchesOfTen(t *testing.T) {
	srv := newTestIMAP(t, nil)
	const total = 25
	for i := 1; i <= total; i++ {
		srv.deliver(fixture(fmt.Sprintf("%d@x", i), alice, fmt.Sprintf("m%02d", i), "x"))
	}
	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)

	got := subjects(collect(t, mb, total))
	for i, s := range got {
		if want := fmt.Sprintf("m%02d", i+1); s != want {
			t.Fatalf("message %d = %q, want %q (UID order)", i, s, want)
		}
	}
	largest, n := srv.maxFetchSize()
	if largest != maxFetchBatch {
		t.Errorf("largest FETCH covered %d messages, want exactly %d", largest, maxFetchBatch)
	}
	if n < 3 {
		t.Errorf("only %d FETCH commands for %d messages", n, total)
	}
}

func TestIMAP_DuplicateMessageIDIsDeliveredOnce(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("dup@x", alice, "original", "x"))
	srv.deliver(fixture("dup@x", alice, "copy", "x")) // e.g. the same mail filed twice
	srv.deliver(fixture("other@x", alice, "different", "x"))

	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)
	if s := subjects(collect(t, mb, 2)); !slices.Equal(s, []string{"original", "different"}) {
		t.Fatalf("got %v", s)
	}
	noInbound(t, mb, 200*time.Millisecond)
}

func TestIMAP_MissingMessageIDGetsStableSyntheticID(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("", alice, "no id one", "x"))
	srv.deliver(fixture("", alice, "no id two", "x"))

	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)
	got := collect(t, mb, 2)

	if got[0].MessageID == got[1].MessageID {
		t.Fatalf("both mails got the id %q", got[0].MessageID)
	}
	for _, m := range got {
		if !isSyntheticID(m.MessageID) {
			t.Errorf("message id = %q, want a synthetic one", m.MessageID)
		}
		if _, ok := m.Context.Raw["message_id"]; ok {
			t.Errorf("synthetic id offered for threading: %v", m.Context.Raw)
		}
	}
}

func TestIMAP_AllowListAndReplyTo(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("a@x", "Mallory <mallory@evil.net>", "from a stranger", "x"))
	srv.deliver(fixture("b@x", alice, "redirect attempt", "x", "Reply-To: attacker@evil.net"))
	srv.deliver(fixture("c@x", alice, "internal reply-to", "x", "Reply-To: team@example.org"))
	srv.deliver(fixture("d@x", alice, "auto reply", "x", "Auto-Submitted: auto-replied"))
	srv.deliver(fixture("e@x", alice, "spoofed", "x", "Authentication-Results: mx.example.com; spf=fail"))

	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)
	got := collect(t, mb, 2)
	noInbound(t, mb, 300*time.Millisecond)

	if s := subjects(got); !slices.Equal(s, []string{"redirect attempt", "internal reply-to"}) {
		t.Fatalf("delivered %v", s)
	}
	if got[0].ChatID != "alice@example.org" {
		t.Errorf("disallowed Reply-To redirected the answer to %q", got[0].ChatID)
	}
	if got[1].ChatID != "team@example.org" {
		t.Errorf("allowed Reply-To ignored, chat = %q", got[1].ChatID)
	}
}

func TestIMAP_AttachmentsAreSavedUnderTheSizeCap(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver("From: " + alice + "\r\nTo: " + testUser + "\r\nSubject: files\r\nMessage-ID: <att@x>\r\n" +
		"Content-Type: multipart/mixed; boundary=B\r\n\r\n" +
		"--B\r\nContent-Type: text/plain\r\n\r\nsee attached\r\n" +
		"--B\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"small.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\nAQIDBA==\r\n" +
		"--B\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"big.bin\"\r\nContent-Transfer-Encoding: base64\r\n\r\n" +
		strings.Repeat("QUJDREVGR0g=", 8) + "\r\n--B--\r\n")

	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.MaxAttachmentBytes = 10 })
	startChannel(t, c)
	m := nextInbound(t, mb)

	if len(m.Media) != 1 {
		t.Fatalf("media = %v, want only the attachment under the cap", m.Media)
	}
	if !strings.Contains(m.Content, "[file: small.bin]") || !strings.Contains(m.Content, "1 attachment(s) skipped") {
		t.Errorf("content = %q", m.Content)
	}
	if b, err := os.ReadFile(m.Media[0]); err != nil || !bytes.Equal(b, []byte{1, 2, 3, 4}) {
		t.Errorf("saved file = %v, %v", b, err)
	}
	if !strings.Contains(m.Media[0], c.workspace) {
		t.Errorf("attachment %q not under the workspace %q", m.Media[0], c.workspace)
	}
}

func TestIMAP_ReconnectsWithBackoffAndKeepsItsCursor(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("1@x", alice, "before", "x"))
	c, mb := imapChannel(t, srv, nil)
	startChannel(t, c)
	nextInbound(t, mb)
	waitIdle(t, srv, 1)

	srv.kill()
	srv.deliver(fixture("2@x", alice, "while down", "x")) // arrives while the bot is offline
	time.Sleep(300 * time.Millisecond)
	srv.revive()

	m := nextInbound(t, mb)
	if got := subjects([]bus.InboundMessage{m})[0]; got != "while down" {
		t.Fatalf("after reconnect got %q, want the mail that arrived while offline", got)
	}
	noInbound(t, mb, 300*time.Millisecond) // "before" must not come back

	// ... and the new session works as usual.
	waitIdle(t, srv, 2)
	srv.deliver(fixture("3@x", alice, "after", "x"))
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "after" {
		t.Errorf("got %q", got)
	}
}

func TestIMAP_ObserverReconnectDoesNotReplayTheBacklog(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("old@x", alice, "old", "x"))
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.ObserverMode = true })
	startChannel(t, c)
	waitIdle(t, srv, 1)

	srv.kill()
	srv.deliver(fixture("gap@x", alice, "gap", "x"))
	srv.revive()

	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "gap" {
		t.Fatalf("got %q", got)
	}
	noInbound(t, mb, 300*time.Millisecond)
}

func TestIMAP_UIDValidityChangeRestartsTheCursor(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("u1@x", alice, "unseen one", "x"))
	srv.deliver(fixture("u2@x", alice, "unseen two", "x"))

	c, mb := imapChannel(t, srv, nil)
	// A cursor from a previous incarnation of the mailbox: its UIDs mean nothing now.
	c.cursorSet, c.uidValidity, c.nextUID = true, 424242, 100
	startChannel(t, c)

	if s := subjects(collect(t, mb, 2)); !slices.Equal(s, []string{"unseen one", "unseen two"}) {
		t.Fatalf("got %v, want the mailbox drained again", s)
	}
	if c.uidValidity == 424242 {
		t.Error("uidvalidity not updated")
	}
}

func TestIMAP_ResumesWithMatchingUIDValidity(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("r1@x", alice, "already handled", "x"))

	// Learn the server's UIDVALIDITY from a first run, then resume from it.
	c1, mb1 := imapChannel(t, srv, nil)
	startChannel(t, c1)
	nextInbound(t, mb1)
	waitIdle(t, srv, 1)
	validity := c1.uidValidity
	_ = c1.Stop(context.Background())

	srv.deliver(fixture("r2@x", alice, "second run", "x"), imap.FlagSeen) // seen: only the cursor can find it
	c2, mb2 := imapChannel(t, srv, nil)
	c2.cursorSet, c2.uidValidity, c2.nextUID = true, validity, 2
	startChannel(t, c2)
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb2)})[0]; got != "second run" {
		t.Fatalf("got %q", got)
	}
	noInbound(t, mb2, 200*time.Millisecond)
}

func TestIMAP_MissingUIDNextDoesNotReplayTheMailbox(t *testing.T) {
	srv := newTestIMAP(t, nil)
	for i := 1; i <= 3; i++ {
		srv.deliver(fixture(fmt.Sprintf("m%d@x", i), alice, fmt.Sprintf("old %d", i), "x"))
	}
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.ObserverMode = true })

	cl, err := c.dialIMAP(make(chan struct{}, 1))
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()
	if err = c.loginIMAP(context.Background(), cl); err != nil {
		t.Fatal(err)
	}
	sel, err := cl.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		t.Fatal(err)
	}
	sel.UIDNext = 0 // a server that does not report UIDNEXT
	if err := c.initCursor(context.Background(), cl, sel); err != nil {
		t.Fatal(err)
	}
	if c.nextUID != 4 {
		t.Errorf("nextUID = %d, want 4 (highest UID + 1)", c.nextUID)
	}
	noInbound(t, mb, 100*time.Millisecond)
}

func TestIMAP_XOAuth2(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("o1@x", alice, "oauth mail", "x"))

	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) {
		s.Password = config.SecureString{} // the password must not be needed
		s.OAuthToken = *config.NewSecureString(testToken)
	})
	startChannel(t, c)
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "oauth mail" {
		t.Fatalf("got %q", got)
	}
}

func TestIMAP_XOAuth2TokenSource(t *testing.T) {
	srv := newTestIMAP(t, nil)
	srv.deliver(fixture("o1@x", alice, "via token service", "x"))

	var calls atomic.Int32
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) {
		s.Password = config.SecureString{}
		s.OAuthToken = *config.NewSecureString("stale-static-token")
	})
	c.SetTokenSource(func(context.Context) (string, error) { calls.Add(1); return testToken, nil })
	startChannel(t, c)
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "via token service" {
		t.Fatalf("got %q", got)
	}
	if calls.Load() == 0 {
		t.Error("token source was not consulted")
	}
}

func TestIMAP_LoginFailures(t *testing.T) {
	srv := newTestIMAP(t, nil)

	t.Run("wrong password", func(t *testing.T) {
		c, _ := imapChannel(
			t,
			srv,
			func(s *config.MailSettings, _ *config.Channel) { s.Password = *config.NewSecureString("wrong") },
		)
		connected, err := c.session(context.Background())
		if connected || err == nil || !strings.Contains(err.Error(), "login") {
			t.Fatalf("connected=%v err=%v", connected, err)
		}
	})
	t.Run("wrong oauth token", func(t *testing.T) {
		c, _ := imapChannel(
			t,
			srv,
			func(s *config.MailSettings, _ *config.Channel) { s.OAuthToken = *config.NewSecureString("nope") },
		)
		connected, err := c.session(context.Background())
		if connected || err == nil {
			t.Fatalf("connected=%v err=%v", connected, err)
		}
	})
	t.Run("token source failure", func(t *testing.T) {
		c, _ := imapChannel(
			t,
			srv,
			func(s *config.MailSettings, _ *config.Channel) { s.OAuthToken = *config.NewSecureString("x") },
		)
		c.SetTokenSource(func(context.Context) (string, error) { return "", fmt.Errorf("token service down") })
		if _, err := c.session(
			context.Background(),
		); err == nil ||
			!strings.Contains(err.Error(), "token service down") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("unknown folder", func(t *testing.T) {
		c, _ := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.IMAPFolder = "Nope" })
		connected, err := c.session(context.Background())
		if connected || err == nil || !strings.Contains(err.Error(), "select") {
			t.Fatalf("connected=%v err=%v", connected, err)
		}
	})
	t.Run("nothing listening", func(t *testing.T) {
		c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) { s.IMAPPort = 1 })
		if connected, err := c.session(context.Background()); connected || err == nil {
			t.Fatalf("connected=%v err=%v", connected, err)
		}
	})
}

func TestIMAP_ImplicitTLS(t *testing.T) {
	serverTLS, clientTLS := testTLS(t)
	srv := newTestIMAP(t, serverTLS)
	srv.deliver(fixture("t1@x", alice, "over tls", "x"))

	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.IMAPPlaintext = false })
	c.tlsOverride = clientTLS
	startChannel(t, c)
	if got := subjects([]bus.InboundMessage{nextInbound(t, mb)})[0]; got != "over tls" {
		t.Fatalf("got %q", got)
	}

	// Without trusting the certificate the channel must not connect.
	c2, _ := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { s.IMAPPlaintext = false })
	if connected, err := c2.session(context.Background()); connected || err == nil {
		t.Fatalf("connected to an untrusted server: connected=%v err=%v", connected, err)
	}
}

func TestIMAP_StopWhileIdling(t *testing.T) {
	srv := newTestIMAP(t, nil)
	c, _ := imapChannel(t, srv, nil)
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !c.IsRunning() {
		t.Fatal("not running after Start")
	}
	waitIdle(t, srv, 1)

	done := make(chan error, 1)
	go func() { done <- c.Stop(context.Background()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return while IDLE was running")
	}
	if c.IsRunning() {
		t.Error("still running after Stop")
	}
}

func TestIMAP_StopWhileBackingOffAndWithoutStart(t *testing.T) {
	c, _ := newTestChannel(t, func(s *config.MailSettings, _ *config.Channel) { s.IMAPPort = 1 }) // refuses connections
	if err := c.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond) // first attempt failed, now in backoff
	done := make(chan struct{})
	go func() { _ = c.Stop(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked on the backoff timer")
	}
}

func TestIMAP_ContextCancellationEndsTheLoop(t *testing.T) {
	srv := newTestIMAP(t, nil)
	c, _ := imapChannel(t, srv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitIdle(t, srv, 1)
	cancel()
	done := make(chan struct{})
	go func() { c.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("loop kept running after its context was canceled")
	}
}

// The whole round trip: a mail comes in over IMAP, the agent's answer goes out
// over SMTP as a threaded reply.
func TestEndToEnd_MailInReplyOut(t *testing.T) {
	smtpd := newFakeSMTP(t, nil, false)
	srv := newTestIMAP(t, nil)
	c, mb := imapChannel(t, srv, func(s *config.MailSettings, _ *config.Channel) { pointSMTP(s, smtpd) })
	startChannel(t, c)
	waitIdle(t, srv, 1)

	srv.deliver(fixture("thread-2@example.org", alice, "[picoclaw-sop] Weekly report", "Please summarize.",
		"References: <thread-1@example.org>"))
	in := nextInbound(t, mb)
	if in.Content != "Subject: Weekly report\n\nPlease summarize." {
		t.Fatalf("content = %q (reserved prefix must be stripped)", in.Content)
	}

	// The agent answers with the context it was given.
	_, err := c.Send(context.Background(), bus.OutboundMessage{
		Channel: in.Channel,
		ChatID:  in.ChatID,
		Context: in.Context,
		Content: "Here is the **summary**.",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	rec := smtpd.next(t)
	if rec.rcpt != "alice@example.org" {
		t.Errorf("rcpt = %q", rec.rcpt)
	}
	h, top := readMessage(t, []byte(rec.data))
	if got := h.Get("Subject"); got != "Re: Weekly report" {
		t.Errorf("Subject = %q", got)
	}
	if got := h.Get("In-Reply-To"); got != "<thread-2@example.org>" {
		t.Errorf("In-Reply-To = %q", got)
	}
	if got := h.Get("References"); got != "<thread-1@example.org> <thread-2@example.org>" {
		t.Errorf("References = %q", got)
	}
	if top.ctype != "multipart/alternative" || !strings.Contains(top.children[1].body, "<strong>summary</strong>") {
		t.Errorf("reply is not text+html: %q", top.ctype)
	}

	// Machine-written mail from an allowed sender is ignored (RFC 3834) ...
	foreign := strings.Replace(rec.data, "bot@example.com", "carol@example.org", 1)
	srv.deliver(foreign)
	noInbound(t, mb, 400*time.Millisecond)

	// ... while the very same mail without the marker is processed, which shows
	// the header is what stopped it.
	srv.deliver(strings.Replace(foreign, "Auto-Submitted: auto-generated\r\n", "", 1))
	nextInbound(t, mb)
}
