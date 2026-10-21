// Package mail implements a channel that reads mail over IMAP and replies over SMTP.
package mail

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/channels"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/identity"
	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/media"
	"github.com/sipeed/picoclaw/pkg/utils"
)

const (
	channelName = "mail"

	defaultIMAPFolder         = "INBOX"
	defaultPollInterval       = 60 * time.Second
	defaultIdleTimeout        = 29 * time.Minute // RFC 2177: re-issue IDLE before the server's 30 minute limit
	defaultMaxAttachmentBytes = 10 << 20
	defaultSubject            = "PicoClaw"

	// maxFetchBatch bounds how many messages are held in memory per FETCH.
	maxFetchBatch = 10

	initialBackoff = time.Second
	maxBackoff     = time.Minute

	// maxSeenIDs bounds the in-memory set of processed Message-IDs.
	maxSeenIDs = 4096

	// syntheticIDPrefix marks IDs derived from the mailbox position when a
	// message has no Message-ID header. They are never sent in mail headers.
	syntheticIDPrefix = "mail-imap-"
)

// TokenSource returns a current OAuth2 access token for XOAUTH2.
type TokenSource func(ctx context.Context) (string, error)

// MailChannel implements channels.Channel on top of an IMAP mailbox and an SMTP relay.
type MailChannel struct {
	*channels.BaseChannel
	cfg       *config.MailSettings
	allowFrom []string
	workspace string

	// tokenSource, when set, supplies XOAUTH2 tokens (default: the static
	// oauth_token setting). It is consulted on every IMAP and SMTP login so
	// refreshed tokens are picked up.
	tokenSource TokenSource

	// tlsOverride lets tests trust a self-signed certificate; nil means the
	// system roots.
	tlsOverride *tls.Config

	pollInterval time.Duration
	idleTimeout  time.Duration
	maxAttach    int

	cancel context.CancelFunc
	wg     sync.WaitGroup

	// IMAP cursor. It outlives reconnects so mail that arrives while the
	// connection is down is still fetched.
	cursorSet   bool
	uidValidity uint32
	nextUID     imap.UID

	seen *seenSet
}

// NewMailChannel creates a mail channel. workspace is the directory under which
// inbound attachments are stored; when empty a temp directory is used.
func NewMailChannel(
	bc *config.Channel,
	cfg *config.MailSettings,
	messageBus *bus.MessageBus,
	workspace string,
) (*MailChannel, error) {
	if cfg.IMAPHost == "" {
		return nil, fmt.Errorf("mail imap_host is required")
	}
	if cfg.SMTPHost == "" {
		return nil, fmt.Errorf("mail smtp_host is required")
	}
	if _, err := mail.ParseAddress(fromAddress(cfg)); err != nil {
		return nil, fmt.Errorf("mail from_address (or an address as username) is required: %w", err)
	}

	// Anyone can put any address in From, so an open mailbox would let the
	// world drive the agent. Require an explicit allow-list ("*" opts out).
	var allow []string
	for _, a := range bc.AllowFrom {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			allow = append(allow, a)
		}
	}
	if len(allow) == 0 {
		return nil, fmt.Errorf(
			`mail allow_from is required: list addresses, "@domain" or "domain" ("*" allows everyone)`,
		)
	}

	// The allow-list is enforced here, not by BaseChannel: its matcher knows
	// nothing about "@domain" entries. Handing it "*" keeps it out of the way
	// and silences its open-by-default warning.
	base := channels.NewBaseChannel(channelName, cfg, messageBus, []string{"*"},
		channels.WithReasoningChannelID(bc.ReasoningChannelID),
	)

	c := &MailChannel{
		BaseChannel:  base,
		cfg:          cfg,
		allowFrom:    allow,
		workspace:    workspace,
		pollInterval: durationOr(cfg.PollIntervalSecs, defaultPollInterval),
		idleTimeout:  durationOr(cfg.IdleTimeoutSecs, defaultIdleTimeout),
		maxAttach:    cfg.MaxAttachmentBytes,
		seen:         newSeenSet(maxSeenIDs),
	}
	if c.maxAttach <= 0 {
		c.maxAttach = defaultMaxAttachmentBytes
	}
	c.tokenSource = func(context.Context) (string, error) { return cfg.OAuthToken.String(), nil }
	return c, nil
}

// SetTokenSource replaces the XOAUTH2 token provider. Call before Start.
func (c *MailChannel) SetTokenSource(ts TokenSource) { c.tokenSource = ts }

// Start begins reading the mailbox in the background. Connection problems are
// retried with exponential backoff rather than failing Start.
func (c *MailChannel) Start(ctx context.Context) error {
	logger.InfoCF("mail", "Starting mail channel", map[string]any{
		"imap":     c.cfg.IMAPHost,
		"folder":   c.folder(),
		"observer": c.cfg.ObserverMode,
	})
	runCtx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	c.SetRunning(true)

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		c.runWithBackoff(runCtx)
	}()
	return nil
}

// Stop ends the IMAP session and waits for the background loop to exit.
func (c *MailChannel) Stop(ctx context.Context) error {
	logger.InfoC("mail", "Stopping mail channel")
	c.SetRunning(false)
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
	return nil
}

// IsAllowed implements channels.Channel using the mail allow-list.
func (c *MailChannel) IsAllowed(senderID string) bool { return c.allowed(senderID) }

// IsAllowedSender implements channels.Channel using the mail allow-list.
func (c *MailChannel) IsAllowedSender(sender bus.SenderInfo) bool {
	return c.allowed(sender.PlatformID)
}

// allowed reports whether addr matches an allow_from entry: a full address,
// "@domain" or a bare "domain" (exact domain, subdomains do not match).
func (c *MailChannel) allowed(addr string) bool {
	return matchAllowed(c.allowFrom, addr)
}

func matchAllowed(allowFrom []string, addr string) bool {
	addr = strings.ToLower(strings.TrimSpace(addr))
	if addr == "" {
		return false
	}
	for _, entry := range allowFrom {
		switch {
		case entry == "*":
			return true
		case strings.HasPrefix(entry, "@"):
			if strings.HasSuffix(addr, entry) {
				return true
			}
		case strings.Contains(entry, "@"):
			if addr == entry {
				return true
			}
		default:
			if strings.HasSuffix(addr, "@"+entry) {
				return true
			}
		}
	}
	return false
}

// dispatch filters one parsed message and publishes it to the bus.
func (c *MailChannel) dispatch(ctx context.Context, pm *parsedMail, uid imap.UID) error {
	if !c.allowed(pm.sender) {
		logger.WarnCF("mail", "Blocked mail from sender not in allow_from", map[string]any{"sender": pm.sender})
		return nil
	}
	if pm.automated || pm.sender == strings.ToLower(fromAddress(c.cfg)) {
		logger.DebugCF("mail", "Ignored automated or self-sent mail", map[string]any{"sender": pm.sender})
		return nil
	}
	if pm.authFailed {
		logger.WarnCF("mail", "Dropped mail that failed SPF/DKIM/DMARC", map[string]any{"sender": pm.sender})
		return nil
	}

	id := pm.messageID
	if id == "" {
		id = c.syntheticID(uid)
	}
	if !c.seen.add(id) {
		return nil
	}

	// Reply-To is honored only when it is allowed too; otherwise a permitted
	// sender could redirect our answers to an arbitrary address.
	chatID := pm.sender
	if pm.replyTo != "" && pm.replyTo != pm.sender {
		if c.allowed(pm.replyTo) {
			chatID = pm.replyTo
		} else {
			logger.WarnCF("mail", "Ignoring Reply-To that is not in allow_from", map[string]any{"reply_to": pm.replyTo})
		}
	}

	scope := channels.BuildMediaScope(channelName, chatID, id)
	refs, notes := c.saveAttachments(pm, id, scope)
	if pm.droppedAttachments > 0 {
		notes = append(
			notes,
			fmt.Sprintf("[%d attachment(s) skipped: over max_attachment_bytes]", pm.droppedAttachments),
		)
	}

	content := "Subject: " + pm.subject + "\n\n" + pm.body
	if len(notes) > 0 {
		content += "\n\n" + strings.Join(notes, "\n")
	}

	// Replies carry the parent's chain plus the parent itself (RFC 5322 3.6.4).
	// Synthetic IDs would mean nothing to a mail client, so they are left out.
	raw := map[string]string{"platform": channelName, "subject": pm.subject}
	chain := append([]string(nil), pm.references...)
	if pm.messageID != "" {
		raw["message_id"] = pm.messageID
		chain = append(chain, pm.messageID)
	}
	if len(chain) > 0 {
		raw["references"] = strings.Join(chain, " ")
	}

	sender := bus.SenderInfo{
		Platform:    channelName,
		PlatformID:  pm.sender,
		CanonicalID: identity.BuildCanonicalID(channelName, pm.sender),
		Username:    pm.sender,
		DisplayName: pm.senderName,
	}
	inboundCtx := bus.InboundContext{
		Channel:   c.Name(),
		ChatID:    chatID,
		ChatType:  "direct",
		SenderID:  pm.sender,
		MessageID: id,
		Raw:       raw,
	}
	logger.DebugCF("mail", "Received mail", map[string]any{
		"sender":  pm.sender,
		"subject": utils.Truncate(pm.subject, 50),
		"files":   len(refs),
	})
	return c.HandleInboundContext(ctx, chatID, content, refs, inboundCtx, sender)
}

// syntheticID derives a stable ID from the mailbox position for messages that
// lack a Message-ID header.
func (c *MailChannel) syntheticID(uid imap.UID) string {
	h := sha256.New()
	for _, part := range []string{c.cfg.IMAPHost, c.cfg.Username, c.folder()} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	var tail [8]byte
	binary.BigEndian.PutUint32(tail[:4], c.uidValidity)
	binary.BigEndian.PutUint32(tail[4:], uint32(uid))
	h.Write(tail[:])
	return fmt.Sprintf("%s%x-%d", syntheticIDPrefix, h.Sum(nil)[:16], uid)
}

func isSyntheticID(id string) bool { return strings.HasPrefix(id, syntheticIDPrefix) }

// saveAttachments writes the attachments under <workspace>/attachments/<mail>/
// and registers them with the media store. It returns the media refs plus the
// "[file: name]" notes that tell the agent about them.
func (c *MailChannel) saveAttachments(pm *parsedMail, id, scope string) (refs, notes []string) {
	if len(pm.attachments) == 0 {
		return nil, nil
	}
	dir, policy := c.attachmentDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		logger.ErrorCF("mail", "Cannot create attachment dir", map[string]any{"dir": dir, "error": err.Error()})
		return nil, nil
	}

	used := map[string]bool{}
	for _, a := range pm.attachments {
		name := uniqueName(a.name, used)
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, a.data, 0o600); err != nil {
			logger.ErrorCF("mail", "Cannot save attachment", map[string]any{"file": name, "error": err.Error()})
			continue
		}
		ref := path
		if store := c.GetMediaStore(); store != nil {
			if r, err := store.Store(path, media.MediaMeta{
				Filename:      name,
				ContentType:   a.contentType,
				Source:        channelName,
				CleanupPolicy: policy,
			}, scope); err == nil {
				ref = r
			}
		}
		refs = append(refs, ref)
		notes = append(notes, "[file: "+name+"]")
	}
	return refs, notes
}

// attachmentDir returns where a mail's attachments live. Workspace files are
// kept (the media store only forgets them); the temp fallback is cleaned up.
func (c *MailChannel) attachmentDir(id string) (string, media.CleanupPolicy) {
	sub := safeDirName(id)
	if c.workspace != "" {
		return filepath.Join(c.workspace, "attachments", sub), media.CleanupPolicyForgetOnly
	}
	return filepath.Join(media.TempDir(), channelName, sub), media.CleanupPolicyDeleteOnCleanup
}

// safeDirName turns a Message-ID into a single, safe path element.
func safeDirName(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	s := strings.Trim(b.String(), ".")
	if s == "" {
		return "mail"
	}
	if len(s) > 100 {
		s = s[:100]
	}
	return s
}

// uniqueName avoids overwriting two attachments that share a filename.
func uniqueName(name string, used map[string]bool) string {
	candidate := name
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := 2; used[candidate]; i++ {
		candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
	used[candidate] = true
	return candidate
}

// tlsConfig returns the TLS settings for talking to host.
func (c *MailChannel) tlsConfig(host string) *tls.Config {
	cfg := &tls.Config{}
	if c.tlsOverride != nil {
		cfg = c.tlsOverride.Clone()
	}
	cfg.ServerName = host
	return cfg
}

func (c *MailChannel) folder() string {
	if c.cfg.IMAPFolder != "" {
		return c.cfg.IMAPFolder
	}
	return defaultIMAPFolder
}

func durationOr(secs int, def time.Duration) time.Duration {
	if secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return def
}

// fromAddress is the envelope and header sender: from_address, else a username
// that is itself an address.
func fromAddress(cfg *config.MailSettings) string {
	if cfg.FromAddress != "" {
		return cfg.FromAddress
	}
	return cfg.Username
}

// seenSet is a bounded FIFO set of processed message IDs.
type seenSet struct {
	mu    sync.Mutex
	limit int
	ids   map[string]struct{}
	order []string
}

func newSeenSet(limit int) *seenSet {
	return &seenSet{limit: limit, ids: make(map[string]struct{}, limit)}
}

// add records id and reports whether it was new.
func (s *seenSet) add(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.ids[id]; dup {
		return false
	}
	s.ids[id] = struct{}{}
	s.order = append(s.order, id)
	if len(s.order) > s.limit {
		delete(s.ids, s.order[0])
		s.order = s.order[1:]
	}
	return true
}
