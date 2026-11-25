package mail

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // decode non-UTF-8 bodies and headers
	gomail "github.com/emersion/go-message/mail"

	"github.com/sipeed/picoclaw/pkg/utils"
)

// reservedSubjectPrefix marks subjects that picoclaw reserves for internally
// generated messages. It is stripped from inbound subjects so a sender cannot
// make a mail look like one.
const reservedSubjectPrefix = "[picoclaw-sop]"

// attachment is a binary (or non-body text) MIME part.
type attachment struct {
	name        string
	contentType string
	data        []byte
}

// parsedMail is the part of an RFC 5322 message the channel cares about.
type parsedMail struct {
	messageID  string // without angle brackets, empty when the header is missing
	sender     string // lowercased address from From
	senderName string
	replyTo    string // lowercased address from Reply-To, empty when absent
	references []string
	subject    string
	body       string

	// automated is set for auto-generated mail (RFC 3834), bulk mail and bounces.
	automated bool
	// authFailed is set when the receiving MTA recorded a failed SPF, DKIM or
	// DMARC check in its Authentication-Results header.
	authFailed bool

	attachments []attachment
	// droppedAttachments counts parts skipped because of the size limit.
	droppedAttachments int
}

// parseMail parses a raw message. Attachments are collected up to maxAttachBytes
// in total; the rest is counted in droppedAttachments.
func parseMail(raw []byte, maxAttachBytes int) (*parsedMail, error) {
	mr, err := gomail.CreateReader(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) {
		return nil, fmt.Errorf("parse mail: %w", err)
	}
	defer mr.Close()

	pm := &parsedMail{}
	pm.fillHeaders(&mr.Header)

	var plain, htmlBody string
	var havePlain, haveHTML bool
	total := 0

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) {
			break // keep what was parsed so far
		}

		var mh message.Header
		isInline := false
		switch ph := part.Header.(type) {
		case *gomail.InlineHeader:
			mh, isInline = ph.Header, true
		case *gomail.AttachmentHeader:
			mh = ph.Header
		}
		ct, ctParams, _ := mh.ContentType()
		name := partFilename(mh, ctParams)
		inlineText := isInline && (ct == "text/plain" || ct == "text/html" || ct == "")

		if inlineText {
			data, _ := io.ReadAll(part.Body)
			switch {
			case ct == "text/html" && !haveHTML:
				htmlBody, haveHTML = string(data), true
			case ct != "text/html" && !havePlain:
				plain, havePlain = string(data), true
			}
			continue
		}

		// Attachment or non-text inline part.
		if total >= maxAttachBytes {
			pm.droppedAttachments++
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(part.Body, int64(maxAttachBytes-total)+1))
		if len(data) == 0 {
			continue
		}
		if total+len(data) > maxAttachBytes {
			pm.droppedAttachments++
			continue
		}
		total += len(data)
		if name == "" {
			name = fmt.Sprintf("attachment-%d%s", len(pm.attachments)+1, extensionFor(ct))
		}
		pm.attachments = append(pm.attachments, attachment{name: name, contentType: ct, data: data})
	}

	switch {
	case havePlain && strings.TrimSpace(plain) != "":
		pm.body = strings.TrimSpace(plain)
	case haveHTML:
		md, herr := utils.HtmlToMarkdown(htmlBody)
		if herr == nil {
			pm.body = strings.TrimSpace(md)
		}
	}
	if pm.body == "" {
		pm.body = pm.takeTextAttachment()
	}
	if pm.body == "" {
		pm.body = "(no readable content)"
	}
	return pm, nil
}

// takeTextAttachment removes the first text attachment from the list and
// returns it as the message body.
func (pm *parsedMail) takeTextAttachment() string {
	for i, a := range pm.attachments {
		if !strings.HasPrefix(a.contentType, "text/") {
			continue
		}
		pm.attachments = append(pm.attachments[:i], pm.attachments[i+1:]...)
		return fmt.Sprintf("[Attachment: %s]\n%s", a.name, strings.TrimSpace(string(a.data)))
	}
	return ""
}

func (pm *parsedMail) fillHeaders(h *gomail.Header) {
	if from, _ := h.AddressList("From"); len(from) > 0 {
		pm.sender = strings.ToLower(strings.TrimSpace(from[0].Address))
		pm.senderName = cleanHeaderValue(from[0].Name)
	}
	if rt, _ := h.AddressList("Reply-To"); len(rt) > 0 {
		pm.replyTo = strings.ToLower(strings.TrimSpace(rt[0].Address))
	}
	pm.messageID, _ = h.MessageID()

	// RFC 5322 3.6.4: a reply's chain is the parent's References, falling back
	// to a single-id In-Reply-To.
	if refs, _ := h.MsgIDList("References"); len(refs) > 0 {
		pm.references = refs
	} else if irt, _ := h.MsgIDList("In-Reply-To"); len(irt) == 1 {
		pm.references = irt
	}

	subject, _ := h.Subject()
	pm.subject = sanitizeSubject(subject)

	pm.automated = isAutomated(h.Get("Auto-Submitted"), h.Get("Precedence"), pm.sender)
	pm.authFailed = authenticationFailed(h.Get("Authentication-Results"))
}

// partFilename returns the attachment filename, preferring Content-Disposition
// and falling back to the Content-Type name parameter.
func partFilename(h message.Header, ctParams map[string]string) string {
	_, dispParams, _ := h.ContentDisposition()
	name := dispParams["filename"]
	if name == "" {
		name = ctParams["name"]
	}
	if name = utils.SanitizeFilename(cleanHeaderValue(name)); name == "." {
		return ""
	}
	return name
}

func extensionFor(contentType string) string {
	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
		return exts[0]
	}
	return ""
}

// cleanHeaderValue collapses every run of whitespace, CR and LF included, to a
// single space. RFC 2047 decoding can reintroduce line breaks, which would
// otherwise let a sender inject headers into our replies.
func cleanHeaderValue(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// sanitizeSubject normalizes whitespace and strips the reserved prefix.
func sanitizeSubject(subject string) string {
	s := cleanHeaderValue(subject)
	for {
		if len(s) < len(reservedSubjectPrefix) ||
			!strings.EqualFold(s[:len(reservedSubjectPrefix)], reservedSubjectPrefix) {
			break
		}
		s = strings.TrimSpace(s[len(reservedSubjectPrefix):])
	}
	return s
}

// isAutomated reports mail a bot must not answer: RFC 3834 auto-generated
// messages, bulk/list/junk precedence and bounces. Answering them risks mail loops.
func isAutomated(autoSubmitted, precedence, sender string) bool {
	if as := strings.ToLower(strings.TrimSpace(autoSubmitted)); as != "" && as != "no" {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(precedence)) {
	case "bulk", "list", "junk":
		return true
	}
	local, _, _ := strings.Cut(sender, "@")
	return local == "mailer-daemon" || local == "postmaster"
}

// authenticationFailed reports whether the topmost Authentication-Results
// header (added by our own MTA) records a failed SPF, DKIM or DMARC check.
// A missing header means nothing was checked and is not treated as a failure.
func authenticationFailed(results string) bool {
	for _, field := range strings.FieldsFunc(strings.ToLower(results), func(r rune) bool {
		return r == ';' || r == ' ' || r == '\t' || r == '\r' || r == '\n'
	}) {
		switch field {
		case "spf=fail", "dkim=fail", "dmarc=fail":
			return true
		}
	}
	return false
}
