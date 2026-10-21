package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
)

const testWait = 10 * time.Second

// testTLS returns a server config with a self-signed certificate valid for
// 127.0.0.1, and a client config that trusts it.
func testTLS(t *testing.T) (server, client *tls.Config) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(ts.Close)
	pool := x509.NewCertPool()
	pool.AddCert(ts.Certificate())
	return &tls.Config{Certificates: ts.TLS.Certificates}, &tls.Config{RootCAs: pool}
}

// newTestChannel builds a mail channel against 127.0.0.1 with sensible test
// settings. mutate may adjust the settings and the generic channel config.
func newTestChannel(t *testing.T, mutate func(*config.MailSettings, *config.Channel)) (*MailChannel, *bus.MessageBus) {
	t.Helper()
	settings := &config.MailSettings{
		IMAPHost:         "127.0.0.1",
		IMAPPlaintext:    true,
		SMTPHost:         "127.0.0.1",
		Username:         "bot@example.com",
		Password:         *config.NewSecureString("secret"),
		PollIntervalSecs: 1,
	}
	bc := &config.Channel{
		Type:      config.ChannelMail,
		Enabled:   true,
		AllowFrom: config.FlexibleStringSlice{"@example.org"},
	}
	if mutate != nil {
		mutate(settings, bc)
	}
	mb := bus.NewMessageBus()
	t.Cleanup(mb.Close)
	c, err := NewMailChannel(bc, settings, mb, t.TempDir())
	if err != nil {
		t.Fatalf("NewMailChannel: %v", err)
	}
	return c, mb
}

// startChannel starts c and stops it when the test ends.
func startChannel(t *testing.T, c *MailChannel) {
	t.Helper()
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = c.Stop(context.Background()) })
}

// nextInbound waits for one inbound message.
func nextInbound(t *testing.T, mb *bus.MessageBus) bus.InboundMessage {
	t.Helper()
	select {
	case m := <-mb.InboundChan():
		return m
	case <-time.After(testWait):
		t.Fatal("timed out waiting for an inbound message")
		return bus.InboundMessage{}
	}
}

// noInbound asserts nothing is published within d.
func noInbound(t *testing.T, mb *bus.MessageBus, d time.Duration) {
	t.Helper()
	select {
	case m := <-mb.InboundChan():
		t.Fatalf("unexpected inbound message: %q", m.Content)
	case <-time.After(d):
	}
}

// eventually polls cond until it holds or testWait elapses.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// smtpRecord is one message accepted by fakeSMTP.
type smtpRecord struct {
	authMech    string
	authPayload string
	from, rcpt  string
	data        string
	overTLS     bool
}

// fakeSMTP is a minimal SMTP server that records what it receives. It
// supports AUTH PLAIN/XOAUTH2, STARTTLS and implicit TLS.
type fakeSMTP struct {
	ln         net.Listener
	tlsCfg     *tls.Config // nil: no TLS at all
	implicit   bool        // TLS from the first byte instead of STARTTLS
	rejectAuth bool
	records    chan smtpRecord

	mu    sync.Mutex
	conns []net.Conn
}

func newFakeSMTP(t *testing.T, tlsCfg *tls.Config, implicit bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{ln: ln, tlsCfg: tlsCfg, implicit: implicit, records: make(chan smtpRecord, 16)}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns = append(f.conns, conn)
			f.mu.Unlock()
			go f.handle(conn)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		f.mu.Lock()
		for _, c := range f.conns {
			c.Close()
		}
		f.mu.Unlock()
	})
	return f
}

func (f *fakeSMTP) port() int { return f.ln.Addr().(*net.TCPAddr).Port }

func (f *fakeSMTP) handle(conn net.Conn) {
	defer func() { conn.Close() }()
	overTLS := false
	if f.implicit {
		conn = tls.Server(conn, f.tlsCfg)
		overTLS = true
	}
	r := bufio.NewReader(conn)
	reply := func(s string) { _, _ = io.WriteString(conn, s+"\r\n") }

	reply("220 fake ESMTP")
	var rec smtpRecord
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply("250-fake")
			if f.tlsCfg != nil && !overTLS {
				reply("250-STARTTLS")
			}
			reply("250-AUTH PLAIN XOAUTH2")
			reply("250 8BITMIME")
		case upper == "STARTTLS":
			reply("220 go ahead")
			conn = tls.Server(conn, f.tlsCfg)
			r = bufio.NewReader(conn)
			overTLS = true
		case strings.HasPrefix(upper, "AUTH "):
			fields := strings.Fields(line)
			rec.authMech = strings.ToUpper(fields[1])
			if len(fields) > 2 {
				b, _ := base64.StdEncoding.DecodeString(fields[2])
				rec.authPayload = string(b)
			}
			if f.rejectAuth {
				reply("535 5.7.8 rejected")
			} else {
				reply("235 2.7.0 ok")
			}
		case strings.HasPrefix(upper, "MAIL FROM:"):
			rec.from = angle(line)
			reply("250 ok")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rec.rcpt = angle(line)
			reply("250 ok")
		case upper == "DATA":
			reply("354 end with <CRLF>.<CRLF>")
			var sb strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				sb.WriteString(strings.TrimPrefix(l, ".")) // undo dot-stuffing
			}
			rec.data, rec.overTLS = sb.String(), overTLS
			reply("250 queued")
			f.records <- rec
			rec = smtpRecord{}
		case upper == "QUIT":
			reply("221 bye")
			return
		default:
			reply("250 ok")
		}
	}
}

// next waits for the next accepted message.
func (f *fakeSMTP) next(t *testing.T) smtpRecord {
	t.Helper()
	select {
	case rec := <-f.records:
		return rec
	case <-time.After(testWait):
		t.Fatal("timed out waiting for an SMTP message")
		return smtpRecord{}
	}
}

// none asserts that no message is accepted within d.
func (f *fakeSMTP) none(t *testing.T, d time.Duration) {
	t.Helper()
	select {
	case rec := <-f.records:
		t.Fatalf("unexpected SMTP message to %s", rec.rcpt)
	case <-time.After(d):
	}
}

func angle(line string) string {
	i, j := strings.Index(line, "<"), strings.LastIndex(line, ">")
	if i < 0 || j < i {
		return ""
	}
	return line[i+1 : j]
}

// pointSMTP aims the channel at the fake server.
func pointSMTP(s *config.MailSettings, f *fakeSMTP) { s.SMTPPort = f.port() }

func itoa(n int) string { return strconv.Itoa(n) }
