---
title: "Mail Channel"
description: "Connect PicoClaw to a mailbox over IMAP and send threaded replies over SMTP, with optional attachments and OAuth2 authentication."
---
# ✉️ Mail Channel

PicoClaw can talk over e-mail. It reads a mailbox over IMAP (IDLE push when the server supports it, polling otherwise) and answers over SMTP, threading each reply onto the mail it answers. It works with Gmail, Outlook, Fastmail, or any self-hosted IMAP/SMTP server.

## 🛠️ Build

Mail is **opt-in** and is not included in the standard binary. Build a custom
binary with the `channel_mail` tag:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_mail'
```

This example registers only the mail driver. Add any other `channel_<name>` tags
you need to the same `GO_BUILD_TAGS` list; see
[Custom Builds](../../guides/custom-builds).

## 🚀 Quick Start

**1. Add the channel to `~/.picoclaw/config.json`** (no secrets here):

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
        "username": "bot@example.com"
      }
    }
  }
}
```

**2. Put the password in `~/.picoclaw/.security.yml`** (keep it out of `config.json` and out of version control, and restrict it with `chmod 600`):

```yaml
channel_list:
  mail:
    settings:
      password: "app-password"
```

Alternatively export it as an environment variable, which takes precedence: `PICOCLAW_CHANNELS_MAIL_PASSWORD`. The same applies to `smtp_password` and `oauth_token`.

The value in `.security.yml` can also be a reference instead of the literal secret, for instance `password: "file://mail-password"` to read it from a file (the path is relative to the config directory, handy for mounted Docker/Kubernetes secrets). Encrypted `enc://` values are resolved by the same mechanism as for the other channels; see the [security configuration guide](../../security/security_configuration). Saving the config never writes these fields back to `config.json`.

**3. Start the gateway:**

```bash
picoclaw gateway
```

**4. Send a mail** from an allowed address to the mailbox. The reply arrives in the same thread. Each correspondent has one conversation (session), whatever the subject.

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
| `password` | – | IMAP password. **Secret: never in `config.json`** |
| `smtp_username`, `smtp_password` | `username`, `password` | Separate SMTP credentials. `smtp_password` is a **secret** |
| `oauth_token` | – | Static OAuth2 access token. When set, XOAUTH2 replaces the passwords. **Secret** |
| `oauth_provider` | – | Credential key from `picoclaw auth login --provider gmail`, e.g. `gmail:bot@example.com`. Enables automatic token refresh; mutually exclusive with `oauth_token` |
| `from_address` | `username` | Sender address, e.g. `Support <support@example.com>` |
| `default_subject` | `PicoClaw` | Subject for mails that do not answer another mail |
| `poll_interval_secs` | `60` | Polling period when the server has no IDLE |
| `idle_timeout_secs` | `1740` | IDLE is re-issued this often (29 min, per RFC 2177). The IMAP library additionally renews it every 28 min, so the effective period is the smaller of the two |
| `observer_mode` | `false` | See below |
| `plain_text_only` | `false` | Send `text/plain` only, no HTML part |
| `max_attachment_bytes` | `10485760` | Cap, in total, for attachments in either direction |

`password`, `smtp_password` and `oauth_token` are secrets: supply them through `.security.yml` (`channel_list.mail.settings.*`) or the `PICOCLAW_CHANNELS_MAIL_PASSWORD`, `..._SMTP_PASSWORD` and `..._OAUTH_TOKEN` environment variables. `oauth_provider` is only a credential-store key and is not a secret. See the [security configuration guide](../../security/security_configuration).

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

### Gmail with automatic token refresh

Gmail no longer accepts the normal account password for IMAP/SMTP. Besides an app password, you can use OAuth2 with automatic refresh:

1. In [Google Cloud Console](https://console.cloud.google.com/), create or select a project and enable **Gmail API**.
2. Configure the OAuth consent screen and create an OAuth client of type **Desktop app**.
3. Log in with that client's credentials:

   ```bash
   export PICOCLAW_GMAIL_CLIENT_ID="your-client-id.apps.googleusercontent.com"
   export PICOCLAW_GMAIL_CLIENT_SECRET="your-client-secret"
   picoclaw auth login --provider gmail --no-browser
   ```

   Open the printed URL, authorize access, and paste the final redirect URL (or authorization code) back into the terminal. `--no-browser` is useful on a headless server; omit it to open the local browser automatically.

   The command stores the access token, refresh token, and OAuth client credentials in `~/.picoclaw/auth.json` with mode `0600`, then prints a key such as `gmail:bot@example.com`.

   Prefer the environment variables over `--client-secret`: values passed in argv can appear in shell history and process listings.

4. Set that key in `config.json`; no password or token belongs in the channel settings:

   ```json
   {
     "channel_list": {
       "mail": {
         "enabled": true,
         "type": "mail",
         "allow_from": ["alice@example.org"],
         "settings": {
           "imap_host": "imap.gmail.com",
           "imap_port": 993,
           "smtp_host": "smtp.gmail.com",
           "smtp_port": 587,
           "username": "bot@example.com",
           "oauth_provider": "gmail:bot@example.com",
           "observer_mode": true
         }
       }
     }
   }
   ```

The channel reloads the credential before each IMAP login and SMTP send, refreshes the access token five minutes before expiry, and persists any rotated refresh token. Refreshes and auth-store writes are serialized across PicoClaw processes. If the refresh token is revoked or expires, the logs ask you to run `picoclaw auth login --provider gmail` again.

The requested scope is `https://mail.google.com/`, plus `userinfo.email` to identify the account. Google may require OAuth app verification for this restricted scope. If the OAuth client remains in **Testing**, Google expires refresh tokens after seven days.

To revoke access, remove the credential with `picoclaw auth logout --provider gmail:bot@example.com` and revoke the app's access in your Google Account.

### Static token or custom token source

Set `oauth_token` to use a static access token with XOAUTH2 on both IMAP and SMTP. It does not refresh automatically and is mutually exclusive with `oauth_provider`. When embedding the channel, `SetTokenSource` can supply tokens programmatically; it is consulted on every IMAP login and SMTP send.

## Not included

Gmail push notifications (Pub/Sub) and the web UI form.
