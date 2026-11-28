package mail

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	gomail "github.com/emersion/go-message/mail"
	"github.com/gomarkdown/markdown"
	mdhtml "github.com/gomarkdown/markdown/html"
	mdparser "github.com/gomarkdown/markdown/parser"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/logger"
)

const smtpTimeout = 2 * time.Minute

// outgoing is a mail ready to be rendered.
type outgoing struct {
	to          string
	subject     string
	body        string // Markdown
	inReplyTo   string
	references  []string
	attachments []attachment
}

// Send replies to msg.ChatID. The Markdown body goes out as multipart/alternative
// (plain text plus rendered HTML) and the mail is threaded onto the one it answers.
func (c *MailChannel) Send(ctx context.Context, msg bus.OutboundMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	if strings.TrimSpace(msg.Content) == "" {
		return nil, nil
	}
	out, err := c.newOutgoing(msg.ChatID, msg.Content, msg.Context, msg.ReplyToMessageID)
	if err != nil {
		return nil, err
	}
	return nil, c.deliver(ctx, out)
}

// SendMedia implements channels.MediaSender: the files travel as attachments of
// a multipart/mixed mail whose body is the parts' captions.
func (c *MailChannel) SendMedia(ctx context.Context, msg bus.OutboundMediaMessage) ([]string, error) {
	if !c.IsRunning() {
		return nil, channels.ErrNotRunning
	}
	store := c.GetMediaStore()
	if store == nil {
		return nil, fmt.Errorf("no media store available: %w", channels.ErrSendFailed)
	}

	var captions []string
	var atts []attachment
	for _, part := range msg.Parts {
		path, meta, err := store.ResolveWithMeta(part.Ref)
		if err != nil {
			logger.WarnCF("mail", "Cannot resolve media ref", map[string]any{"ref": part.Ref, "error": err.Error()})
			continue
		}
		data, err := readCapped(path, c.maxAttach)
		if err != nil {
			logger.WarnCF("mail", "Skipping attachment", map[string]any{"file": path, "error": err.Error()})
			continue
		}
		name := cmp.Or(part.Filename, meta.Filename, filepath.Base(path))
		ct := cmp.Or(part.ContentType, meta.ContentType, "application/octet-stream")
		atts = append(atts, attachment{name: cleanHeaderValue(name), contentType: ct, data: data})
		if part.Caption != "" {
			captions = append(captions, part.Caption)
		}
	}
	if len(atts) == 0 {
		return nil, nil
	}

	body := strings.Join(captions, "\n\n")
	if body == "" {
		body = "See the attached file(s)."
	}
	out, err := c.newOutgoing(msg.ChatID, body, msg.Context, "")
	if err != nil {
		return nil, err
	}
	out.attachments = atts
	return nil, c.deliver(ctx, out)
}

// newOutgoing derives subject and threading headers from the mail being answered.
func (c *MailChannel) newOutgoing(chatID, body string, inbound bus.InboundContext, replyTo string) (*outgoing, error) {
	addr, err := mail.ParseAddress(strings.TrimSpace(chatID))
	if err != nil {
		return nil, fmt.Errorf("invalid recipient %q: %w", chatID, channels.ErrSendFailed)
	}

	out := &outgoing{to: addr.Address, body: body, subject: defaultSubject}
	if subj := cleanHeaderValue(inbound.Raw["subject"]); subj != "" {
		out.subject = replySubject(subj)
	} else if c.cfg.DefaultSubject != "" {
		out.subject = cleanHeaderValue(c.cfg.DefaultSubject)
	}

	for _, id := range strings.Fields(inbound.Raw["references"]) {
		if id = bareMessageID(id); id != "" && !isSyntheticID(id) {
			out.references = append(out.references, id)
		}
	}
	parent := bareMessageID(inbound.Raw["message_id"])
	if parent == "" {
		parent = bareMessageID(replyTo)
	}
	if parent != "" && !isSyntheticID(parent) {
		out.inReplyTo = parent
	}
	return out, nil
}

func (c *MailChannel) deliver(ctx context.Context, out *outgoing) error {
	from := fromAddress(c.cfg)
	raw, err := c.render(from, out)
	if err != nil {
		return fmt.Errorf("render mail: %w", err)
	}
	if err := c.smtpSend(ctx, from, out.to, raw); err != nil {
		return channels.ClassifyNetError(fmt.Errorf("smtp send: %w", err))
	}
	logger.InfoCF("mail", "Mail sent", map[string]any{"to": out.to, "attachments": len(out.attachments)})
	return nil
}

// render builds the RFC 5322 message:
//
//	multipart/alternative            (no attachments)
//	multipart/mixed
//	  multipart/alternative          (attachments present)
//	  attachment...
//
// With plain_text_only the alternative collapses to a single text/plain part.
func (c *MailChannel) render(from string, out *outgoing) ([]byte, error) {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return nil, err
	}

	var h gomail.Header
	h.SetDate(time.Now())
	h.SetAddressList("From", []*gomail.Address{{Name: fromAddr.Name, Address: fromAddr.Address}})
	h.SetAddressList("To", []*gomail.Address{{Address: out.to}})
	h.SetSubject(cleanHeaderValue(out.subject))
	domain := fromAddr.Address[strings.LastIndex(fromAddr.Address, "@")+1:]
	if err := h.GenerateMessageIDWithHostname(domain); err != nil {
		return nil, err
	}
	// Angle brackets are added by SetMsgIDList; IDs are kept bare until here.
	if out.inReplyTo != "" {
		h.SetMsgIDList("In-Reply-To", []string{out.inReplyTo})
	}
	refs := out.references
	if out.inReplyTo != "" && (len(refs) == 0 || refs[len(refs)-1] != out.inReplyTo) {
		refs = append(append([]string(nil), refs...), out.inReplyTo)
	}
	h.SetMsgIDList("References", refs)
	// RFC 3834: lets other bots recognize this as machine-written and not answer.
	h.Set("Auto-Submitted", "auto-generated")

	var buf bytes.Buffer
	switch {
	case len(out.attachments) > 0:
		mw, err := gomail.CreateWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if c.cfg.PlainTextOnly {
			w, err := mw.CreateSingleInline(textHeader("text/plain"))
			if err != nil {
				return nil, err
			}
			if err := writeClose(w, out.body); err != nil {
				return nil, err
			}
		} else {
			iw, err := mw.CreateInline()
			if err != nil {
				return nil, err
			}
			if err := writeAlternatives(iw, out.body); err != nil {
				return nil, err
			}
		}
		for _, a := range out.attachments {
			var ah gomail.AttachmentHeader
			ah.Set("Content-Type", a.contentType)
			ah.SetFilename(a.name)
			w, err := mw.CreateAttachment(ah)
			if err != nil {
				return nil, err
			}
			if err := writeClose(w, string(a.data)); err != nil {
				return nil, err
			}
		}
		if err := mw.Close(); err != nil {
			return nil, err
		}
	case c.cfg.PlainTextOnly:
		h.Set("Content-Type", "text/plain; charset=utf-8")
		w, err := gomail.CreateSingleInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if err := writeClose(w, out.body); err != nil {
			return nil, err
		}
	default:
		iw, err := gomail.CreateInlineWriter(&buf, h)
		if err != nil {
			return nil, err
		}
		if err := writeAlternatives(iw, out.body); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

// writeAlternatives writes the plain-text part first, then the HTML rendering
// of the same Markdown (RFC 2046 puts the preferred format last), and closes iw.
func writeAlternatives(iw *gomail.InlineWriter, body string) error {
	for _, p := range []struct{ ctype, text string }{
		{"text/plain", body},
		{"text/html", markdownToHTML(body)},
	} {
		w, err := iw.CreatePart(textHeader(p.ctype))
		if err != nil {
			return err
		}
		if err := writeClose(w, p.text); err != nil {
			return err
		}
	}
	return iw.Close()
}

func writeClose(w io.WriteCloser, text string) error {
	if _, err := io.WriteString(w, text); err != nil {
		return err
	}
	return w.Close()
}

func textHeader(ctype string) gomail.InlineHeader {
	var ih gomail.InlineHeader
	ih.Set("Content-Type", ctype+"; charset=utf-8")
	return ih
}

// markdownToHTML renders the agent's Markdown. Raw HTML in it is dropped and
// unsafe link schemes are neutralized, since the text can echo untrusted input.
func markdownToHTML(md string) string {
	p := mdparser.NewWithExtensions(mdparser.CommonExtensions)
	r := mdhtml.NewRenderer(mdhtml.RendererOptions{Flags: mdhtml.CommonFlags | mdhtml.SkipHTML | mdhtml.Safelink})
	return string(markdown.ToHTML([]byte(md), p, r))
}

// smtpSend delivers raw over SMTP: implicit TLS on port 465 (or smtp_implicit_tls),
// otherwise STARTTLS whenever the server offers it.
func (c *MailChannel) smtpSend(ctx context.Context, from, to string, raw []byte) error {
	fromAddr, err := mail.ParseAddress(from)
	if err != nil {
		return err
	}
	port := c.cfg.SMTPPort
	if port == 0 {
		port = 587
	}
	implicit := c.cfg.SMTPImplicitTLS || port == 465
	host := c.cfg.SMTPHost

	conn, err := (&net.Dialer{Timeout: 30 * time.Second}).DialContext(
		ctx,
		"tcp",
		net.JoinHostPort(host, strconv.Itoa(port)),
	)
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(smtpTimeout))
	if implicit {
		tc := tls.Client(conn, c.tlsConfig(host))
		if err = tc.HandshakeContext(ctx); err != nil {
			conn.Close()
			return err
		}
		conn = tc
	}
	cl, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return err
	}
	defer cl.Close()

	if !implicit {
		if ok, _ := cl.Extension("STARTTLS"); ok {
			if err = cl.StartTLS(c.tlsConfig(host)); err != nil {
				return err
			}
		}
	}
	auth, err := c.smtpAuth(ctx)
	if err != nil {
		return err
	}
	if auth != nil {
		if err = cl.Auth(auth); err != nil {
			return err
		}
	}
	if err = cl.Mail(fromAddr.Address); err != nil {
		return err
	}
	if err = cl.Rcpt(to); err != nil {
		return err
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(raw); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	return cl.Quit()
}

// smtpAuth picks XOAUTH2 when an OAuth token is configured, else PLAIN. It
// returns nil for an unauthenticated relay. net/smtp refuses to send either
// credential over an unencrypted non-local connection.
func (c *MailChannel) smtpAuth(ctx context.Context) (smtp.Auth, error) {
	user := cmp.Or(c.cfg.SMTPUsername, c.cfg.Username)
	if c.cfg.OAuthToken.String() != "" {
		token, err := c.tokenSource(ctx)
		if err != nil {
			return nil, fmt.Errorf("oauth token: %w", err)
		}
		return &smtpXOAuth2{user: user, token: token}, nil
	}
	pass := cmp.Or(c.cfg.SMTPPassword.String(), c.cfg.Password.String())
	if user == "" || pass == "" {
		return nil, nil
	}
	return smtp.PlainAuth("", user, pass, c.cfg.SMTPHost), nil
}

// smtpXOAuth2 implements smtp.Auth for the XOAUTH2 mechanism.
type smtpXOAuth2 struct{ user, token string }

func (a *smtpXOAuth2) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && !isLocalhost(server.Name) {
		return "", nil, errors.New("refusing XOAUTH2 over an unencrypted connection")
	}
	return "XOAUTH2", xoauth2Response(a.user, a.token), nil
}

func (a *smtpXOAuth2) Next(fromServer []byte, more bool) ([]byte, error) {
	if more { // the server sent a JSON error description
		return nil, fmt.Errorf("xoauth2 rejected: %s", fromServer)
	}
	return nil, nil
}

func isLocalhost(name string) bool {
	return name == "localhost" || name == "127.0.0.1" || name == "::1"
}

// readCapped reads a file, refusing anything larger than limit bytes.
func readCapped(path string, limit int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("larger than max_attachment_bytes (%d)", limit)
	}
	return data, nil
}

// replySubject prefixes "Re: " unless the subject already is a reply.
func replySubject(subject string) string {
	if len(subject) >= 3 && strings.EqualFold(subject[:3], "re:") {
		return subject
	}
	return "Re: " + subject
}

// bareMessageID strips whitespace and angle brackets so IDs are stored and
// compared uniformly; brackets are re-added when headers are written.
func bareMessageID(id string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(id), "<>"))
}
