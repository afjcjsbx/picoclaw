package tools

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"mime"
	"net"
	"net/smtp"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-message"
	"github.com/sipeed/picoclaw/pkg/config"
)

// SMTPSender defines the interface for sending emails via SMTP.
type SMTPSender interface {
	SendMail(addr string, a smtp.Auth, from string, to []string, msg []byte) error
}

// IMAPClient defines the subset of methods we use from the IMAP client.
type IMAPClient interface {
	Login(username, password string) error
	Logout() error
	Select(mbox string, readonly bool) (*imap.MailboxStatus, error)
	Fetch(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error
	Search(criteria *imap.SearchCriteria) ([]uint32, error)
}

// IMAPConnector is a function type that creates a new IMAP connection.
type IMAPConnector func(addr string) (IMAPClient, error)

// Real implementation of SMTPSender
type realSMTPSender struct{}

func (s *realSMTPSender) SendMail(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid SMTP address: %w", err)
	}

	// Implicit TLS (Port 465)
	// smtp.SendMail hangs on this port, so we must handle it manually.
	if port == "465" {
		// TLS Connection
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
		if err != nil {
			return err
		}
		defer conn.Close()

		// SMTP Client
		c, err := smtp.NewClient(conn, host)
		if err != nil {
			return err
		}
		defer c.Quit()

		// Auth
		if a != nil {
			if ok, _ := c.Extension("AUTH"); ok {
				if err = c.Auth(a); err != nil {
					return err
				}
			}
		}

		// Send Data
		if err = c.Mail(from); err != nil {
			return err
		}
		for _, rcpt := range to {
			if err = c.Rcpt(rcpt); err != nil {
				return err
			}
		}
		w, err := c.Data()
		if err != nil {
			return err
		}
		_, err = w.Write(msg)
		if err != nil {
			return err
		}
		err = w.Close()
		if err != nil {
			return err
		}

		return nil
	}

	// STARTTLS (Port 587 or 25)
	// The standard library handles this automatically.
	return smtp.SendMail(addr, a, from, to, msg)
}

// Adapter for the real IMAP client to satisfy the IMAPClient interface.
// This solves the "Cannot use c (type *Client) as type IMAPClient" error.
type imapClientAdapter struct {
	c *client.Client
}

func (a *imapClientAdapter) Login(username, password string) error {
	return a.c.Login(username, password)
}

func (a *imapClientAdapter) Logout() error {
	return a.c.Logout()
}

func (a *imapClientAdapter) Select(mbox string, readonly bool) (*imap.MailboxStatus, error) {
	return a.c.Select(mbox, readonly)
}

func (a *imapClientAdapter) Fetch(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error {
	return a.c.Fetch(seqset, items, ch)
}

func (a *imapClientAdapter) Search(criteria *imap.SearchCriteria) ([]uint32, error) {
	return a.c.Search(criteria)
}

// Default connector using the adapter
func defaultIMAPConnector(addr string) (IMAPClient, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address format: %w", err)
	}

	// Create a Dialer with a strict timeout
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}

	var c *client.Client

	// Connection strategy
	if port == "143" {
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, fmt.Errorf("TCP connection failed: %w", err)
		}

		c, err = client.New(conn)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("IMAP handshake failed: %w", err)
		}

		if ok, _ := c.SupportStartTLS(); ok {
			tlsConfig := &tls.Config{ServerName: host}
			if err := c.StartTLS(tlsConfig); err != nil {
				c.Logout()
				return nil, fmt.Errorf("STARTTLS upgrade failed: %w", err)
			}
		}
	} else {
		tlsConfig := &tls.Config{ServerName: host}
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, tlsConfig)
		if err != nil {
			return nil, fmt.Errorf("TLS connection failed: %w", err)
		}

		c, err = client.New(conn)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("IMAP handshake failed: %w", err)
		}
	}

	// Set the command timeout for future operations
	// This covers "Read" and "Write" timeouts for actual commands like FETCH or SEARCH.
	c.Timeout = 30 * time.Second

	// Wrap the real client in our adapter
	return &imapClientAdapter{c: c}, nil
}

type EmailTool struct {
	cfg           config.EmailToolConfig
	smtpSender    SMTPSender
	imapConnector IMAPConnector
}

type SearchEmailArgs struct {
	Query string
	Limit int
}

func NewEmailTool(cfg config.EmailToolConfig) *EmailTool {
	return &EmailTool{
		cfg:           cfg,
		smtpSender:    &realSMTPSender{},    // Default to real implementation
		imapConnector: defaultIMAPConnector, // Default to real implementation
	}
}

func (t *EmailTool) Name() string {
	return "email"
}

func (t *EmailTool) Description() string {
	return "Manages emails (IMAP/SMTP). Actions: 'list_accounts' (show configured accounts), 'read' (read last N emails), 'search' (search by subject/sender), 'send' (send email). Supports multiple accounts via aliases."
}

func (t *EmailTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"list_accounts", "read", "search", "send"},
				"description": "Action to perform.",
			},
			"account": map[string]interface{}{
				"type":        "string",
				"description": "Account alias to use (e.g., 'work', 'personal'). If omitted, uses the first available.",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Number of emails to read (for 'read' or 'search' actions). Default 5.",
			},
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query string for 'search' action (searches in subject and sender).",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "Recipient for 'send' action.",
			},
			"subject": map[string]interface{}{
				"type":        "string",
				"description": "Subject for 'send' action.",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Message body for 'send' action.",
			},
		},
		"required": []string{"action"},
	}
}

func (t *EmailTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !t.cfg.Enabled {
		return ErrorResult("Email tool is disabled in configuration.")
	}

	action, ok := args["action"].(string)
	if !ok {
		return ErrorResult("action is required")
	}

	// Account Selection
	accountAlias, _ := args["account"].(string)
	account, err := t.getAccount(accountAlias)
	if err != nil {
		return ErrorResult(err.Error())
	}

	switch action {
	case "list_accounts":
		return t.listAccounts()
	case "send":
		return t.sendEmail(account, args)
	case "read":
		return t.readEmails(account, args)
	case "search":
		return t.searchEmails(account, args)
	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s", action))
	}
}

// Helper to get the account from configuration
func (t *EmailTool) getAccount(alias string) (config.EmailAccountConfig, error) {
	if len(t.cfg.Accounts) == 0 {
		return config.EmailAccountConfig{}, fmt.Errorf("no email accounts configured")
	}

	// If alias is specified, look for it
	if alias != "" {
		if acc, ok := t.cfg.Accounts[alias]; ok {
			return acc, nil
		}
		return config.EmailAccountConfig{}, fmt.Errorf("account alias '%s' not found", alias)
	}

	keys := make([]string, 0, len(t.cfg.Accounts))
	for k := range t.cfg.Accounts {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	return t.cfg.Accounts[keys[0]], nil
}

func (t *EmailTool) listAccounts() *ToolResult {
	var aliases []string
	for k := range t.cfg.Accounts {
		aliases = append(aliases, k)
	}
	return SilentResult(fmt.Sprintf("Configured email accounts: %s", strings.Join(aliases, ", ")))
}

func (t *EmailTool) sendEmail(acc config.EmailAccountConfig, args map[string]interface{}) *ToolResult {
	to, _ := args["to"].(string)
	subject, _ := args["subject"].(string)
	body, _ := args["body"].(string)

	if to == "" || subject == "" || body == "" {
		return ErrorResult("to, subject, and body are required for sending email")
	}

	if strings.ContainsAny(subject, "\r\n") || strings.ContainsAny(to, "\r\n") {
		return ErrorResult("Invalid characters in email headers")
	}

	encodedSubject := mime.QEncoding.Encode("utf-8", subject)

	dateHeader := time.Now().Format(time.RFC1123Z)

	// Message-ID: <unique-id@domain>
	// We try to extract the domain from the username, defaulting to "localhost"
	domain := "localhost"
	if parts := strings.Split(acc.Username, "@"); len(parts) > 1 {
		domain = parts[1]
	}

	// Using nanoseconds ensures uniqueness
	msgID := fmt.Sprintf("<%d.%s@%s>", time.Now().UnixNano(), "picoclaw", domain)

	// Construct base message using the ENCODED subject
	msg := []byte(fmt.Sprintf("To: %s\r\n"+
		"Subject: %s\r\n"+
		"Date: %s\r\n"+
		"Message-ID: %s\r\n"+
		"Content-Type: text/plain; charset=UTF-8\r\n"+
		"\r\n"+
		"%s\r\n", to, encodedSubject, dateHeader, msgID, body))

	auth := smtp.PlainAuth("", acc.Username, acc.Password, acc.SMTPServer)
	addr := fmt.Sprintf("%s:%d", acc.SMTPServer, acc.SMTPPort)

	err := t.smtpSender.SendMail(addr, auth, acc.Username, []string{to}, msg)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to send email: %v", err))
	}

	return SilentResult(fmt.Sprintf("Email sent successfully to %s", to))
}

func (t *EmailTool) connectIMAP(acc config.EmailAccountConfig) (IMAPClient, error) {
	addr := fmt.Sprintf("%s:%d", acc.IMAPServer, acc.IMAPPort)

	c, err := t.imapConnector(addr)
	if err != nil {
		return nil, fmt.Errorf("IMAP connection failed: %v", err)
	}

	if err := c.Login(acc.Username, acc.Password); err != nil {
		c.Logout()
		return nil, fmt.Errorf("IMAP login failed: %v", err)
	}

	return c, nil
}

// extractBodies recursively extracts plain text and HTML bodies from a MIME entity.
// It handles nested multipart structures (e.g., multipart/mixed > multipart/alternative > text/plain).
func extractBodies(entity *message.Entity, plainBody, htmlBody *string) {
	if mr := entity.MultipartReader(); mr != nil {
		// Multipart: recurse into each part
		for {
			part, err := mr.NextPart()
			if err != nil {
				break
			}
			extractBodies(part, plainBody, htmlBody)
		}
		return
	}

	// Leaf part (non-multipart): read and classify
	contentType, _, err := entity.Header.ContentType()
	if err != nil {
		return
	}

	const maxBodySize = 512 * 1024 // 512 KB

	b, err := io.ReadAll(io.LimitReader(entity.Body, maxBodySize))
	if err != nil {
		return
	}

	switch contentType {
	case "text/plain":
		if *plainBody == "" {
			*plainBody = string(b)
		}
	case "text/html":
		if *htmlBody == "" {
			*htmlBody = string(b)
		}
	}
}

func stripHTMLTags(html string) string {
	var (
		reScript = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
		reStyle  = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
		reTags   = regexp.MustCompile(`<[^>]+>`)
		reSpaces = regexp.MustCompile(`\s+`)
	)

	html = reScript.ReplaceAllString(html, "")
	html = reStyle.ReplaceAllString(html, "")
	html = reTags.ReplaceAllString(html, " ")
	html = reSpaces.ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}

func (t *EmailTool) fetchMessages(c IMAPClient, seqSet *imap.SeqSet, limit int) (string, error) {
	section := &imap.BodySectionName{Peek: true} // Peek: true = do not mark as read
	items := []imap.FetchItem{section.FetchItem(), imap.FetchEnvelope, imap.FetchUid}

	messages := make(chan *imap.Message, limit)

	done := make(chan error, 1)
	go func() {
		defer close(messages)
		done <- c.Fetch(seqSet, items, messages)
	}()

	var sb strings.Builder
	for msg := range messages {
		if msg == nil {
			continue
		}
		sb.WriteString(fmt.Sprintf("--- Email UID: %d ---\n", msg.Uid))

		// Guard against nil Envelope (prevents panic)
		if msg.Envelope == nil {
			sb.WriteString("[Error: Message envelope is missing or malformed]\n\n")
			continue
		}

		sb.WriteString(fmt.Sprintf("Subject: %s\n", msg.Envelope.Subject))
		if len(msg.Envelope.From) > 0 {
			sb.WriteString(fmt.Sprintf("From: %s <%s>\n",
				msg.Envelope.From[0].PersonalName,
				msg.Envelope.From[0].MailboxName+"@"+msg.Envelope.From[0].HostName,
			))
		}
		sb.WriteString(fmt.Sprintf("Date: %s\n", msg.Envelope.Date))

		// Body parsing
		r := msg.GetBody(section)
		if r == nil {
			sb.WriteString("\n[Body not available: section not found in fetch]\n\n")
			continue
		}

		// Use the lower-level message package which exposes entity.Body
		// for non-multipart emails
		entity, err := message.Read(r)
		if err != nil && entity == nil {
			sb.WriteString(fmt.Sprintf("\n[Could not parse body: %v]\n\n", err))
			continue
		}

		var plainBody, htmlBody string
		extractBodies(entity, &plainBody, &htmlBody)

		sb.WriteString("\nBody:\n")
		switch {
		case plainBody != "":
			sb.WriteString(plainBody)
		case htmlBody != "":
			sb.WriteString(stripHTMLTags(htmlBody))
		default:
			sb.WriteString("[No readable text body found]")
		}

		sb.WriteString("\n\n")
	}

	if err := <-done; err != nil {
		return "", err
	}

	return sb.String(), nil
}

func (t *EmailTool) readEmails(acc config.EmailAccountConfig, args map[string]interface{}) *ToolResult {
	limit := 5
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if limit <= 0 {
		return ErrorResult("limit must be a positive integer")
	}

	c, err := t.connectIMAP(acc)
	if err != nil {
		return ErrorResult(err.Error())
	}
	defer c.Logout()

	mbox, err := c.Select("INBOX", false)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to select INBOX: %v", err))
	}

	if mbox.Messages == 0 {
		return SilentResult("No messages in INBOX.")
	}

	// Calculate range: from the last message back by 'limit'
	from := uint32(1)
	if mbox.Messages > uint32(limit) {
		from = mbox.Messages - uint32(limit) + 1
	}
	to := mbox.Messages

	seqSet := new(imap.SeqSet)
	seqSet.AddRange(from, to)

	content, err := t.fetchMessages(c, seqSet, limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("fetch error: %v", err))
	}

	return &ToolResult{
		ForLLM:  content,
		ForUser: fmt.Sprintf("Read %d recent emails from %s", limit, acc.Username),
	}
}

func parseSearchArgs(args map[string]interface{}) (SearchEmailArgs, error) {
	var out SearchEmailArgs

	q, ok := args["query"].(string)
	if !ok || strings.TrimSpace(q) == "" {
		return out, fmt.Errorf("query is required")
	}

	if len(q) > 200 {
		return out, fmt.Errorf("query too long")
	}

	out.Query = strings.TrimSpace(q)

	limit := 5
	if l, ok := args["limit"].(float64); ok {
		limit = int(l)
	}

	if limit <= 0 {
		return out, fmt.Errorf("limit must be positive")
	}
	if limit > 50 {
		limit = 50
	}

	out.Limit = limit
	return out, nil
}

func (t *EmailTool) searchUIDs(c IMAPClient, query string) ([]uint32, error) {
	criteria := imap.NewSearchCriteria()
	criteria.Text = []string{query}
	return c.Search(criteria)
}

func limitAndSort(uids []uint32, limit int) []uint32 {
	sort.Slice(uids, func(i, j int) bool {
		return uids[i] > uids[j]
	})

	if len(uids) > limit {
		return uids[:limit]
	}

	return uids
}

func (t *EmailTool) searchEmails(acc config.EmailAccountConfig, args map[string]interface{}) *ToolResult {

	parsed, err := parseSearchArgs(args)
	if err != nil {
		return ErrorResult(err.Error())
	}

	c, err := t.connectIMAP(acc)
	if err != nil {
		return ErrorResult(err.Error())
	}

	defer c.Logout()

	if _, err := c.Select("INBOX", false); err != nil {
		return ErrorResult(fmt.Sprintf("failed to select INBOX: %v", err))
	}

	uids, err := t.searchUIDs(c, parsed.Query)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	if len(uids) == 0 {
		return SilentResult("No emails found matching query.")
	}

	uids = limitAndSort(uids, parsed.Limit)

	seqSet := new(imap.SeqSet)
	seqSet.AddNum(uids...)

	content, err := t.fetchMessages(c, seqSet, parsed.Limit)
	if err != nil {
		return ErrorResult(fmt.Sprintf("fetch error: %v", err))
	}

	return &ToolResult{
		ForLLM:  content,
		ForUser: fmt.Sprintf("Found %d emails matching '%s'", len(uids), parsed.Query),
	}
}
