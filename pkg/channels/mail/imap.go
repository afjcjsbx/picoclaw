package mail

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/sipeed/picoclaw/pkg/logger"
)

// runWithBackoff keeps an IMAP session alive until ctx ends, reconnecting with
// exponential backoff (1s doubling up to 1m). The backoff restarts from 1s
// once a session has got as far as selecting the mailbox.
func (c *MailChannel) runWithBackoff(ctx context.Context) {
	backoff := initialBackoff
	for ctx.Err() == nil {
		connected, err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = initialBackoff
		}
		logger.WarnCF("mail", "IMAP session ended, reconnecting", map[string]any{
			"error":   fmt.Sprint(err),
			"backoff": backoff.String(),
		})
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = nextBackoff(backoff)
	}
}

// nextBackoff doubles d, capped at maxBackoff.
func nextBackoff(d time.Duration) time.Duration { return min(d*2, maxBackoff) }

// session runs one IMAP connection. connected reports whether login and
// mailbox selection succeeded.
func (c *MailChannel) session(ctx context.Context) (connected bool, err error) {
	// The channel is a one-slot "something changed" signal fed by EXISTS updates.
	notify := make(chan struct{}, 1)
	cl, err := c.dialIMAP(notify)
	if err != nil {
		return false, err
	}
	defer cl.Close()

	if err = c.loginIMAP(ctx, cl); err != nil {
		return false, fmt.Errorf("imap login: %w", err)
	}
	sel, err := cl.Select(c.folder(), &imap.SelectOptions{ReadOnly: c.cfg.ObserverMode}).Wait()
	if err != nil {
		return false, fmt.Errorf("imap select %q: %w", c.folder(), err)
	}
	if err = c.initCursor(ctx, cl, sel); err != nil {
		return true, err
	}

	for {
		if err = c.fetchNew(ctx, cl); err != nil {
			return true, err
		}
		if err = c.waitForMail(ctx, cl, notify); err != nil {
			return true, err
		}
	}
}

func (c *MailChannel) dialIMAP(notify chan<- struct{}) (*imapclient.Client, error) {
	opts := &imapclient.Options{
		Dialer:    &net.Dialer{Timeout: 30 * time.Second},
		TLSConfig: c.tlsConfig(c.cfg.IMAPHost),
		UnilateralDataHandler: &imapclient.UnilateralDataHandler{
			Mailbox: func(data *imapclient.UnilateralDataMailbox) {
				if data.NumMessages == nil {
					return
				}
				select {
				case notify <- struct{}{}:
				default:
				}
			},
		},
	}
	port := c.cfg.IMAPPort
	if port == 0 {
		port = 993
		if c.cfg.IMAPPlaintext {
			port = 143
		}
	}
	addr := net.JoinHostPort(c.cfg.IMAPHost, strconv.Itoa(port))
	if c.cfg.IMAPPlaintext {
		return imapclient.DialInsecure(addr, opts)
	}
	return imapclient.DialTLS(addr, opts)
}

func (c *MailChannel) loginIMAP(ctx context.Context, cl *imapclient.Client) error {
	if c.cfg.OAuthToken.String() != "" {
		token, err := c.tokenSource(ctx)
		if err != nil {
			return fmt.Errorf("oauth token: %w", err)
		}
		return cl.Authenticate(newXOAuth2(c.cfg.Username, token))
	}
	return cl.Login(c.cfg.Username, c.cfg.Password.String()).Wait()
}

// initCursor sets the UID threshold on the first session (or after the server
// reset UIDVALIDITY). Active mode also drains what is still unseen; observer
// mode starts at UIDNEXT so the backlog is never touched.
func (c *MailChannel) initCursor(ctx context.Context, cl *imapclient.Client, sel *imap.SelectData) error {
	if c.cursorSet && c.uidValidity == sel.UIDValidity {
		return nil
	}
	c.cursorSet = true
	c.uidValidity = sel.UIDValidity
	c.nextUID = sel.UIDNext
	if c.nextUID == 0 {
		// UIDNEXT is optional on SELECT; derive it so observer mode cannot
		// mistake the whole mailbox for new mail.
		data, err := cl.UIDSearch(&imap.SearchCriteria{}, nil).Wait()
		if err != nil {
			return fmt.Errorf("imap uid search: %w", err)
		}
		c.nextUID = 1
		for _, uid := range data.AllUIDs() {
			c.nextUID = max(c.nextUID, uid+1)
		}
	}
	if c.cfg.ObserverMode {
		return nil
	}

	data, err := cl.UIDSearch(&imap.SearchCriteria{NotFlag: []imap.Flag{imap.FlagSeen}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap search unseen: %w", err)
	}
	return c.fetchAndDispatch(ctx, cl, sortedUIDs(data.AllUIDs(), 0))
}

// fetchNew fetches every message with UID >= the threshold and advances it.
func (c *MailChannel) fetchNew(ctx context.Context, cl *imapclient.Client) error {
	var set imap.UIDSet
	set.AddRange(c.nextUID, 0) // nextUID:*
	data, err := cl.UIDSearch(&imap.SearchCriteria{UID: []imap.UIDSet{set}}, nil).Wait()
	if err != nil {
		return fmt.Errorf("imap uid search: %w", err)
	}
	// "n:*" always matches the last message, even when its UID is below n.
	return c.fetchAndDispatch(ctx, cl, sortedUIDs(data.AllUIDs(), c.nextUID))
}

// fetchAndDispatch downloads uids in batches of maxFetchBatch. Active mode uses
// BODY[], which sets \Seen; observer mode uses BODY.PEEK[] and never writes a flag.
func (c *MailChannel) fetchAndDispatch(ctx context.Context, cl *imapclient.Client, uids []imap.UID) error {
	section := &imap.FetchItemBodySection{Peek: c.cfg.ObserverMode}
	opts := &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}

	for start := 0; start < len(uids); start += maxFetchBatch {
		batch := uids[start:min(start+maxFetchBatch, len(uids))]
		msgs, err := cl.Fetch(imap.UIDSetNum(batch...), opts).Collect()
		if err != nil {
			return fmt.Errorf("imap fetch: %w", err)
		}
		sort.Slice(msgs, func(i, j int) bool { return msgs[i].UID < msgs[j].UID })

		for _, m := range msgs {
			if err := ctx.Err(); err != nil {
				return err
			}
			c.nextUID = max(c.nextUID, m.UID+1)
			raw := m.FindBodySection(section)
			if raw == nil {
				continue
			}
			pm, err := parseMail(raw, c.maxAttach)
			if err != nil {
				logger.WarnCF("mail", "Skipping unparsable mail", map[string]any{"uid": m.UID, "error": err.Error()})
				continue
			}
			if err := c.dispatch(ctx, pm, m.UID); err != nil {
				return err
			}
		}
		// A batch can be shorter than asked for when messages were expunged.
		c.nextUID = max(c.nextUID, batch[len(batch)-1]+1)
	}
	return nil
}

// waitForMail blocks until new mail may have arrived: an IDLE notification when
// the server supports IDLE (renewed every idleTimeout), otherwise the poll interval.
func (c *MailChannel) waitForMail(ctx context.Context, cl *imapclient.Client, notify <-chan struct{}) error {
	if !cl.Caps().Has(imap.CapIdle) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-cl.Closed():
			return errors.New("imap connection closed")
		case <-time.After(c.pollInterval):
		}
		// NOOP both reports new mail and detects a dead connection.
		return cl.Noop().Wait()
	}

	// go-imap also re-issues IDLE by itself every 28 minutes, so the effective
	// renewal period is min(28m, idle_timeout); this loop adds a catch-up fetch
	// each time round.
	idle, err := cl.Idle()
	if err != nil {
		return fmt.Errorf("imap idle: %w", err)
	}
	timer := time.NewTimer(c.idleTimeout)
	defer timer.Stop()

	var cause error
	select {
	case <-ctx.Done():
		cause = ctx.Err()
	case <-cl.Closed():
		cause = errors.New("imap connection closed")
	case <-notify:
	case <-timer.C:
	}
	if err := idle.Close(); err != nil && cause == nil {
		cause = fmt.Errorf("imap idle close: %w", err)
	}
	if err := idle.Wait(); err != nil && cause == nil {
		cause = fmt.Errorf("imap idle: %w", err)
	}
	return cause
}

// sortedUIDs returns uids >= from in ascending order.
func sortedUIDs(uids []imap.UID, from imap.UID) []imap.UID {
	out := make([]imap.UID, 0, len(uids))
	for _, uid := range uids {
		if uid >= from {
			out = append(out, uid)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
