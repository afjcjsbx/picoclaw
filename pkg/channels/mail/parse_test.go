package mail

import (
	"encoding/base64"
	"strings"
	"testing"
)

// crlf turns a readable test fixture into an RFC 5322 message.
func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimLeft(s, "\n"), "\n", "\r\n"))
}

func mustParse(t *testing.T, raw string, maxAttach int) *parsedMail {
	t.Helper()
	pm, err := parseMail(crlf(raw), maxAttach)
	if err != nil {
		t.Fatalf("parseMail: %v", err)
	}
	return pm
}

func TestParseMail_PlainText(t *testing.T) {
	pm := mustParse(t, `
From: Alice <Alice@Example.COM>
To: bot@example.com
Subject: Hello there
Message-ID: <abc.123@example.com>
Reply-To: Replies <Replies@Example.com>
Content-Type: text/plain; charset=utf-8

First line
Second line
`, 1<<20)

	if pm.sender != "alice@example.com" {
		t.Errorf("sender = %q", pm.sender)
	}
	if pm.senderName != "Alice" {
		t.Errorf("senderName = %q", pm.senderName)
	}
	if pm.replyTo != "replies@example.com" {
		t.Errorf("replyTo = %q", pm.replyTo)
	}
	if pm.messageID != "abc.123@example.com" {
		t.Errorf("messageID = %q (angle brackets must be stripped)", pm.messageID)
	}
	if pm.subject != "Hello there" {
		t.Errorf("subject = %q", pm.subject)
	}
	if pm.body != "First line\r\nSecond line" {
		t.Errorf("body = %q", pm.body)
	}
	if pm.automated || pm.authFailed {
		t.Errorf("plain mail flagged automated=%v authFailed=%v", pm.automated, pm.authFailed)
	}
}

func TestParseMail_MissingMessageID(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: x

body
`, 1<<20)
	if pm.messageID != "" {
		t.Errorf("messageID = %q, want empty", pm.messageID)
	}
}

func TestParseMail_References(t *testing.T) {
	tests := []struct {
		name    string
		headers string
		want    string
	}{
		{"references header wins", "References: <a@x> <b@x>\nIn-Reply-To: <b@x>", "a@x b@x"},
		{"single in-reply-to stands in", "In-Reply-To: <p@x>", "p@x"},
		{"ambiguous in-reply-to ignored", "In-Reply-To: <p@x> <q@x>", ""},
		{"none", "X-Other: 1", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pm := mustParse(t, "From: a@example.com\nSubject: s\n"+tt.headers+"\n\nbody\n", 1<<20)
			if got := strings.Join(pm.references, " "); got != tt.want {
				t.Errorf("references = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMail_HTMLOnlyIsStrippedOfTags(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: html
Content-Type: text/html; charset=utf-8

<html><body><p>Hello <b>world</b></p><script>alert(1)</script><style>p{}</style></body></html>
`, 1<<20)

	for _, bad := range []string{"<p>", "<b>", "<html", "alert", "p{}"} {
		if strings.Contains(pm.body, bad) {
			t.Errorf("body %q still contains %q", pm.body, bad)
		}
	}
	if !strings.Contains(pm.body, "Hello") || !strings.Contains(pm.body, "world") {
		t.Errorf("body lost its text: %q", pm.body)
	}
}

func TestParseMail_PlainPreferredOverHTML(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: alt
Content-Type: multipart/alternative; boundary=BOUND

--BOUND
Content-Type: text/html; charset=utf-8

<p>html version</p>
--BOUND
Content-Type: text/plain; charset=utf-8

plain version
--BOUND--
`, 1<<20)
	if pm.body != "plain version" {
		t.Errorf("body = %q, want the plain part", pm.body)
	}
}

func TestParseMail_TextAttachmentIsBodyFallback(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: only attachment
Content-Type: multipart/mixed; boundary=BOUND

--BOUND
Content-Type: text/plain; charset=utf-8
Content-Disposition: attachment; filename="notes.txt"

hello from a file
--BOUND--
`, 1<<20)

	if want := "[Attachment: notes.txt]\nhello from a file"; pm.body != want {
		t.Errorf("body = %q, want %q", pm.body, want)
	}
	if len(pm.attachments) != 0 {
		t.Errorf("the attachment used as body must not be listed again: %+v", pm.attachments)
	}
}

func TestParseMail_TextAttachmentKeptWhenBodyExists(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: body and file
Content-Type: multipart/mixed; boundary=BOUND

--BOUND
Content-Type: text/plain; charset=utf-8

the body
--BOUND
Content-Type: text/csv; charset=utf-8
Content-Disposition: attachment; filename="data.csv"

a,b
1,2
--BOUND--
`, 1<<20)

	if pm.body != "the body" {
		t.Errorf("body = %q", pm.body)
	}
	if len(pm.attachments) != 1 || pm.attachments[0].name != "data.csv" {
		t.Fatalf("attachments = %+v, want data.csv", pm.attachments)
	}
	if got := string(pm.attachments[0].data); got != "a,b\r\n1,2" {
		t.Errorf("data = %q", got)
	}
}

func TestParseMail_Attachments(t *testing.T) {
	payload := []byte{0x00, 0x01, 0xfe, 0xff, 'P', 'N', 'G'}
	b64 := base64.StdEncoding.EncodeToString(payload)
	pm := mustParse(t, `
From: a@example.com
Subject: files
Content-Type: multipart/mixed; boundary=BOUND

--BOUND
Content-Type: text/plain

see files
--BOUND
Content-Type: image/png
Content-Transfer-Encoding: base64
Content-Disposition: attachment; filename="pic.png"

`+b64+`
--BOUND
Content-Type: application/pdf; name="report.pdf"
Content-Transfer-Encoding: base64

`+b64+`
--BOUND
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="../../etc/passwd"
Content-Transfer-Encoding: base64

`+b64+`
--BOUND
Content-Type: image/png
Content-Transfer-Encoding: base64

`+b64+`
--BOUND--
`, 1<<20)

	if pm.body != "see files" {
		t.Errorf("body = %q", pm.body)
	}
	if len(pm.attachments) != 4 {
		t.Fatalf("attachments = %d, want 4: %+v", len(pm.attachments), pm.attachments)
	}
	want := []struct{ name, ctype string }{
		{"pic.png", "image/png"},
		{"report.pdf", "application/pdf"}, // name from the Content-Type parameter
		{"passwd", "application/octet-stream"},
		{"attachment-4.png", "image/png"}, // no name at all
	}
	for i, w := range want {
		a := pm.attachments[i]
		if a.name != w.name || a.contentType != w.ctype {
			t.Errorf("attachment %d = %q (%s), want %q (%s)", i, a.name, a.contentType, w.name, w.ctype)
		}
		if string(a.data) != string(payload) {
			t.Errorf("attachment %d data = %v, want %v", i, a.data, payload)
		}
	}
}

func TestParseMail_HostileAttachmentNames(t *testing.T) {
	part := func(disp string) string {
		return "--B\nContent-Type: application/octet-stream\nContent-Disposition: " + disp + "\nContent-Transfer-Encoding: base64\n\nAQID\n"
	}
	raw := "From: a@example.com\nSubject: names\nContent-Type: multipart/mixed; boundary=B\n\n--B\nContent-Type: text/plain\n\nbody\n" +
		part(
			`attachment; filename="..."`,
		) +
		part(
			`attachment; filename="."`,
		) +
		part(
			`attachment; filename="..\\..\\windows\\system32\\evil.dll"`,
		) +
		part(
			`attachment; filename="sub/dir/file.txt"`,
		) +
		part(
			`attachment; filename="a\r\nb.txt"`,
		) + "--B--\n"
	pm := mustParse(t, raw, 1<<20)

	for _, a := range pm.attachments {
		if a.name == "" || a.name == "." || a.name == ".." || strings.ContainsAny(a.name, "/\\\r\n") ||
			strings.Contains(a.name, "..") {
			t.Errorf("unsafe attachment name %q", a.name)
		}
	}
	if len(pm.attachments) != 5 {
		t.Fatalf("attachments = %d, want 5", len(pm.attachments))
	}
	if pm.attachments[0].name != "attachment-1.bin" || pm.attachments[1].name != "attachment-2.bin" {
		t.Errorf(
			"empty names must fall back to attachment-N<ext>: %q %q",
			pm.attachments[0].name,
			pm.attachments[1].name,
		)
	}
}

func TestParseMail_AttachmentSizeCap(t *testing.T) {
	part := func(name string) string {
		return "--B\nContent-Type: application/octet-stream\nContent-Disposition: attachment; filename=\"" + name + "\"\n" +
			"Content-Transfer-Encoding: base64\n\n" + base64.StdEncoding.EncodeToString(
			[]byte("123456"),
		) + "\n"
	}
	raw := "From: a@example.com\nSubject: big\nContent-Type: multipart/mixed; boundary=B\n\n" +
		"--B\nContent-Type: text/plain\n\nbody\n" + part("one.bin") + part("two.bin") + part("three.bin") + "--B--\n"

	// 6 bytes each, cap of 10 bytes in total: only the first fits.
	pm := mustParse(t, raw, 10)
	if len(pm.attachments) != 1 || pm.attachments[0].name != "one.bin" {
		t.Fatalf("attachments = %+v, want only one.bin", pm.attachments)
	}
	if pm.droppedAttachments != 2 {
		t.Errorf("droppedAttachments = %d, want 2", pm.droppedAttachments)
	}

	// A cap of 6 still fits exactly one; 5 fits none.
	if pm := mustParse(t, raw, 6); len(pm.attachments) != 1 {
		t.Errorf("cap 6: attachments = %d, want 1", len(pm.attachments))
	}
	if pm := mustParse(t, raw, 5); len(pm.attachments) != 0 || pm.droppedAttachments != 3 {
		t.Errorf("cap 5: attachments = %d dropped = %d, want 0 and 3", len(pm.attachments), pm.droppedAttachments)
	}
}

func TestParseMail_NoReadableContent(t *testing.T) {
	pm := mustParse(t, "From: a@example.com\nSubject: empty\nContent-Type: text/plain\n\n   \n", 1<<20)
	if pm.body != "(no readable content)" {
		t.Errorf("body = %q", pm.body)
	}
}

func TestParseMail_NonUTF8Charset(t *testing.T) {
	pm := mustParse(t, `
From: a@example.com
Subject: =?ISO-8859-1?Q?Caf=E9?=
Content-Type: text/plain; charset=iso-8859-1
Content-Transfer-Encoding: quoted-printable

caff=E8 e citt=E0
`, 1<<20)
	if pm.subject != "Café" {
		t.Errorf("subject = %q", pm.subject)
	}
	if pm.body != "caffè e città" {
		t.Errorf("body = %q", pm.body)
	}
}

func TestSanitizeSubject(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello", "Hello"},
		{"  spaced   out  ", "spaced out"},
		{"[picoclaw-sop] run backup", "run backup"},
		{"[PicoClaw-SOP] run backup", "run backup"},
		{"[picoclaw-sop][picoclaw-sop]   nested", "nested"},
		{"[picoclaw-sop]", ""},
		{"Re: [picoclaw-sop] not leading", "Re: [picoclaw-sop] not leading"},
		{"[picoclaw-s", "[picoclaw-s"},
		{"line one\r\nline two", "line one line two"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := sanitizeSubject(tt.in); got != tt.want {
			t.Errorf("sanitizeSubject(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParseMail_SubjectCannotInjectHeaders(t *testing.T) {
	// An RFC 2047 word can decode to a string containing CR LF.
	evil := base64.StdEncoding.EncodeToString([]byte("hi\r\nBcc: evil@example.com"))
	pm := mustParse(t, "From: a@example.com\nSubject: =?UTF-8?B?"+evil+"?=\n\nbody\n", 1<<20)
	if strings.ContainsAny(pm.subject, "\r\n") {
		t.Fatalf("subject kept a line break: %q", pm.subject)
	}
	if pm.subject != "hi Bcc: evil@example.com" {
		t.Errorf("subject = %q", pm.subject)
	}
}

func TestParseMail_StripsReservedPrefixFromEncodedSubject(t *testing.T) {
	enc := base64.StdEncoding.EncodeToString([]byte("[picoclaw-sop] do it"))
	pm := mustParse(t, "From: a@example.com\nSubject: =?UTF-8?B?"+enc+"?=\n\nbody\n", 1<<20)
	if pm.subject != "do it" {
		t.Errorf("subject = %q", pm.subject)
	}
}

func TestIsAutomated(t *testing.T) {
	tests := []struct {
		name                string
		autoSubmitted, prec string
		sender              string
		want                bool
	}{
		{"human", "", "", "alice@example.com", false},
		{"auto-submitted no", "no", "", "alice@example.com", false},
		{"auto-replied", "auto-replied", "", "alice@example.com", true},
		{"auto-generated", "Auto-Generated", "", "alice@example.com", true},
		{"precedence bulk", "", "bulk", "alice@example.com", true},
		{"precedence list", "", "List", "alice@example.com", true},
		{"precedence junk", "", "junk", "alice@example.com", true},
		{"precedence first-class", "", "first-class", "alice@example.com", false},
		{"bounce", "", "", "mailer-daemon@example.com", true},
		{"postmaster", "", "", "postmaster@example.com", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isAutomated(tt.autoSubmitted, tt.prec, tt.sender); got != tt.want {
				t.Errorf("isAutomated = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAuthenticationFailed(t *testing.T) {
	tests := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"mx.example.com; spf=pass smtp.mailfrom=a@b.c; dkim=pass header.d=b.c; dmarc=pass", false},
		{"mx.example.com; spf=none; dkim=none; dmarc=none", false},
		{"mx.example.com; spf=softfail smtp.mailfrom=a@b.c", false},
		{"mx.example.com; spf=fail smtp.mailfrom=a@b.c", true},
		{"mx.example.com;\r\n dkim=fail reason=\"bad sig\"", true},
		{"mx.example.com; spf=pass; DMARC=FAIL (p=reject)", true},
	}
	for _, tt := range tests {
		if got := authenticationFailed(tt.header); got != tt.want {
			t.Errorf("authenticationFailed(%q) = %v, want %v", tt.header, got, tt.want)
		}
	}
}

func TestParseMail_FlagsFromHeaders(t *testing.T) {
	pm := mustParse(t, `
From: MAILER-DAEMON@example.com
Subject: bounce
Auto-Submitted: auto-replied
Authentication-Results: mx.example.com; spf=fail

body
`, 1<<20)
	if !pm.automated {
		t.Error("automated not set")
	}
	if !pm.authFailed {
		t.Error("authFailed not set")
	}
}
