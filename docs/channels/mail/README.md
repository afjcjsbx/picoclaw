# ✉️ Mail Channel

PicoClaw can talk over e-mail. It reads a mailbox over IMAP (IDLE push when the server supports it, polling otherwise) and answers over SMTP, threading each reply onto the mail it answers. It works with Gmail, Outlook, Fastmail, or any self-hosted IMAP/SMTP server.

## 🚀 Quick Start

**1. Add the channel to `~/.picoclaw/config.json`:**

```json
{
  "channel_list": {
    "mail": {
      "enabled": true,
      "type": "mail",
      "allow_from": ["alice@example.org", "@example.com"],
      "settings": {
        "imap_host": "imap.example.com",
        "smtp_host": "smtp.example.com",
        "username": "bot@example.com",
        "password": "app-password"
      }
    }
  }
}
```

**2. Start the gateway:**

```bash
picoclaw gateway
```

**3. Send a mail** from an allowed address to the mailbox. The reply arrives in the same thread. Each correspondent has one conversation (session), whatever the subject.

> `allow_from` is **mandatory**. The `From` header is trivially forged, so an open mailbox would let anyone drive the agent. Use `"*"` only if you really mean it.

---

## ⚙️ Settings

| Field | Default | Description |
|-------|---------|-------------|
| `imap_host` | – | IMAP server (required) |
| `imap_port` | `993` (`143` with `imap_plaintext`) | IMAP port; implicit TLS |
| `imap_folder` | `INBOX` | Mailbox folder to watch |
| `imap_plaintext` | `false` | Talk IMAP without TLS (local bridges and tests only) |
| `smtp_host` | – | SMTP server (required) |
| `smtp_port` | `587` | `465` or `smtp_implicit_tls` use implicit TLS; otherwise STARTTLS is used whenever the server offers it |
| `smtp_implicit_tls` | `false` | Force implicit TLS on a non-465 port |
| `username` | – | IMAP login; also the sender address when it is an address |
| `password` | – | IMAP password (secret) |
| `smtp_username`, `smtp_password` | `username`, `password` | Separate SMTP credentials (secret) |
| `oauth_token` | – | OAuth2 access token. When set, XOAUTH2 replaces the passwords (secret) |
| `from_address` | `username` | Sender address, e.g. `Support <support@example.com>` |
| `default_subject` | `PicoClaw` | Subject for mails that do not answer another mail |
| `poll_interval_secs` | `60` | Polling period when the server has no IDLE |
| `idle_timeout_secs` | `1740` | IDLE is re-issued this often (29 min, per RFC 2177). The IMAP library additionally renews it every 28 min, so the effective period is the smaller of the two |
| `observer_mode` | `false` | See below |
| `plain_text_only` | `false` | Send `text/plain` only, no HTML part |
| `max_attachment_bytes` | `10485760` | Cap, in total, for attachments in either direction |

Secrets can also live in `.security.yml` (`channel_list.mail.settings.password`, `smtp_password`, `oauth_token`) or in `PICOCLAW_CHANNELS_MAIL_*` environment variables.

---

## 📥 Reading the mailbox

- **IDLE with polling fallback.** The server's capabilities are checked after login; without `IDLE` the channel polls every `poll_interval_secs`.
- **Reconnection.** A dropped connection is retried with exponential backoff, 1 s doubling up to 1 min. Mail that arrived meanwhile is fetched on reconnect.
- **UID threshold.** On the first connection the channel records `UIDNEXT` and from then on only fetches `UID threshold:*`, advancing the threshold. If the server changes `UIDVALIDITY` the cursor starts over.
- **Deduplication.** Messages are also de-duplicated by `Message-ID` in memory. A mail without one gets a stable synthetic ID (SHA-256 over host, user, folder, `UIDVALIDITY` and UID); synthetic IDs are never put in reply headers.
- **Batches.** Mail is downloaded 10 messages at a time, so a large backlog does not exhaust memory.

### Active mode (default) and `observer_mode`

| | Active | `observer_mode` |
|-|--------|-----------------|
| At start | Reads every **unseen** mail | Ignores the whole backlog, starts at `UIDNEXT` |
| Fetch | `BODY[]`, which sets `\Seen` | `BODY.PEEK[]`; the mailbox is selected read-only and no flag is ever written |
| Use it when | The mailbox belongs to the bot | A person also reads the mailbox |

### What the agent sees

```
Subject: <subject>

<body>
```

The body is the `text/plain` part; failing that the HTML part with its markup removed; failing that the first text attachment. Other attachments are saved under `<workspace>/attachments/<message-id>/`, announced as `[file: name]` and handed to the agent as media. Attachments beyond `max_attachment_bytes` are skipped and the agent is told how many.

---

## 🔐 Who may write

`allow_from` entries are matched case-insensitively:

| Entry | Matches |
|-------|---------|
| `alice@example.org` | that address |
| `@example.org` or `example.org` | any address at exactly that domain (not subdomains) |
| `*` | everyone |

Further guards, all applied before anything reaches the agent:

- **`Reply-To` is honored only if it is allowed too**; otherwise the answer goes to `From`. A permitted sender cannot redirect replies to an arbitrary address.
- **Automated mail is dropped**: `Auto-Submitted` other than `no`, `Precedence: bulk|list|junk`, and bounces from `mailer-daemon`/`postmaster`. Replies carry `Auto-Submitted: auto-generated` so other bots do the same. This prevents mail loops.
- **Failed authentication is dropped**: when the receiving MTA's topmost `Authentication-Results` records `spf=fail`, `dkim=fail` or `dmarc=fail`.
- **The subject cannot impersonate internal messages**: a leading `[picoclaw-sop]` is stripped, and line breaks in subjects are flattened so a sender cannot inject headers into replies.

---

## 📤 Replies

Replies are `multipart/alternative` with the agent's Markdown as plain text and rendered HTML (raw HTML in the Markdown is dropped), wrapped in `multipart/mixed` when files are attached. `In-Reply-To` and `References` use `<angle-bracketed>` IDs, and the subject becomes `Re: …`.

---

## 🔑 OAuth2 (XOAUTH2)

Set `oauth_token` and the channel authenticates with XOAUTH2 on both IMAP and SMTP. The token is static in the config; to rotate it, embed the channel and call `SetTokenSource` with a function that returns a fresh token, which is consulted on every IMAP login and SMTP send.

Gmail no longer accepts the account password: use an **app password** (requires 2-step verification) or OAuth2.

## Not included

Gmail push notifications (Pub/Sub), OAuth2 token refresh and acquisition, and the web UI form.
