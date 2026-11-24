---
description: "Drive a real Chromium browser from PicoClaw with Playwright MCP: installation, CDP and managed-browser configurations, flag reference, workflows, and security notes."
---
# Browser automation

PicoClaw's small footprint describes the agent runtime, not a limit on what it can do. The Go core stays at 10–20 MB, and capabilities are added through tools and MCP servers. With [Playwright MCP](https://github.com/microsoft/playwright-mcp), PicoClaw can drive a real Chromium browser: navigate JavaScript-heavy pages, click, fill forms, keep cookies and sessions, inspect network requests and the console, and take screenshots or PDFs. That is the same class of browser work heavier agents perform; the browser runs as a separate process, so its memory cost is separate from PicoClaw's own footprint.

Browser automation is especially useful when [`web_fetch`](web.md) is not enough. `web_fetch` is a fast HTTP client with text extraction: it does not execute JavaScript, keep a login session, or interact with a page. A real browser can render the page, run its scripts, and reuse a profile, which is often what a site expects.

## `web_fetch` vs. a real browser

| Need | `web_fetch` | Playwright MCP |
|------|-------------|----------------|
| Fetch static HTML or an API response | Yes, cheapest option | Overkill |
| Execute JavaScript and read the rendered DOM | No | Yes |
| Keep cookies, local storage, or a logged-in session | No | Yes, with a persistent profile or storage state |
| Click, type, scroll, upload files, handle dialogs | No | Yes |
| Inspect XHR/fetch requests, console errors, or network payloads | No | Yes |
| Take screenshots or generate PDFs | No | Yes |
| Handle a cookie/consent wall or a page that only renders client-side | Usually no | Usually yes |
| Solve CAPTCHAs, bypass access controls, or guarantee stealth | No | No; see [Anti-bot reality](#anti-bot-reality) |
| Cost | Very low | Node.js plus a Chromium process; much heavier |

Use `web_fetch` first when a page is server-rendered or a structured endpoint exists. Reach for the browser when the content is client-rendered, the site returns a challenge or a shell page, or the task requires interaction.

## How it fits PicoClaw

Playwright MCP is a stdio MCP server. PicoClaw starts it like any other MCP server and exposes its tools as `mcp_<server>_<tool>`. With the server name `playwright`, the tools are named `mcp_playwright_browser_*` (for example `mcp_playwright_browser_navigate` and `mcp_playwright_browser_snapshot`). Names may receive a short hash suffix when sanitization or truncation would collide with another tool.

There are three common deployment modes:

| Mode | How it works | Best for | Trade-offs |
|------|--------------|----------|------------|
| **Managed browser** | Playwright MCP launches Chromium itself | Desktop use, simple setup, disposable sessions | Each MCP restart launches a new browser; Playwright's browser bundle may need to be installed |
| **Shared Chromium over CDP** | Chromium runs separately; Playwright MCP connects to `http://127.0.0.1:9222` | Low-RAM boards, long-lived sessions, reusing one browser across agent restarts | You own the Chromium process, profile, and its security |
| **Remote browser** | An HTTP MCP server such as Browserbase provides the browser | Machines that cannot run Chromium | Page data leaves the machine; usually paid |

For a Raspberry Pi, a small VPS, or any board where avoiding a second browser installation matters, the CDP mode is usually the best fit: install system Chromium once, run it headless, and point Playwright MCP at it.

## Alternatives

- **Remote browser MCP.** Services such as Browserbase run Chromium elsewhere and expose it over HTTP MCP. This is useful on very small boards, but page data and sessions leave your machine and the service is usually paid.

Playwright MCP is the most portable local option: it works with system Chromium, does not require the heavy image, and can attach to a browser you already run.

## Requirements

- **Node.js 20 or newer** for `@playwright/mcp`.
- **Chromium or Chrome**. Either use a system package, or let Playwright install its own Chromium build.
- **A writable output directory** for screenshots, PDFs, and other automatically named files. Put it inside the PicoClaw workspace so the agent can read the results with its filesystem tools.

Install the MCP server globally rather than running `npx` on every launch. On low-resource hardware, avoiding a package-resolution step at startup is noticeable, and an absolute binary path is reliable under systemd or a service manager:

```bash
npm install -g @playwright/mcp
command -v playwright-mcp
playwright-mcp --help
```

The installed binary is usually `playwright-mcp`. Depending on your npm prefix it may be in `/usr/local/bin`, `/opt/homebrew/bin`, or `~/.npm-global/bin`. Use the path printed by `command -v playwright-mcp` in the PicoClaw config. PicoClaw expands `~` in the MCP `command`, but not inside `args`, so use absolute paths for arguments.

If you prefer not to install globally, set `command` to `npx` and put `-y` and `@playwright/mcp@latest` at the start of `args`. This avoids the global install but adds a package-resolution step to every server start, which is why the global binary is recommended on low-resource hardware.

On Debian and Raspberry Pi OS, install Chromium from the distribution:

```bash
sudo apt update
sudo apt install chromium
```

The package name varies by distribution (`chromium` on Raspberry Pi OS and Debian, sometimes `chromium-browser` elsewhere). If you prefer Playwright's own build instead, run `npx playwright install chromium`; this downloads a browser and is usually unnecessary when you already have system Chromium.

## Configuration

The configuration lives under `tools.mcp.servers` in `config.json`. The `enabled` switch exists both globally and per server.

### Shared Chromium over CDP

This is the configuration to use when Chromium is already running with a remote debugging port. It matches the common Raspberry Pi setup and adapts the paths to the default `root` service install; replace `/root/.picoclaw` with your `PICOCLAW_HOME` when PicoClaw runs as another user.

```json
{
  "tools": {
    "mcp": {
      "enabled": true,
      "servers": {
        "playwright": {
          "enabled": true,
          "type": "stdio",
          "command": "/usr/local/bin/playwright-mcp",
          "args": [
            "--cdp-endpoint",
            "http://127.0.0.1:9222",
            "--headless",
            "--isolated",
            "--user-agent",
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36",
            "--timeout-navigation",
            "45000",
            "--idle-timeout",
            "300000",
            "--output-dir",
            "/root/.picoclaw/workspace/browser"
          ],
          "deferred": false
        }
      }
    }
  }
}
```

Create the output directory before starting the gateway:

```bash
mkdir -p /root/.picoclaw/workspace/browser
```

On a desktop install the path is usually `/home/<user>/.picoclaw/workspace/browser`.

The example keeps `--headless` and `--isolated` so the same argument list also works if you later remove `--cdp-endpoint`. In CDP mode `--headless` is ignored because the Chromium process you started already decides that. `--isolated` still matters: Playwright creates a fresh browser context for the MCP session instead of reusing Chromium's default context, and that is also what makes `--user-agent` apply to the session. Without `--isolated`, the agent shares the browser's existing default context. The Chromium profile itself remains owned by the process you started either way.

If you need a persistent login across runs, remove `--isolated` and use a dedicated Chromium `--user-data-dir` (or `--storage-state` in managed mode). Do not mix that profile with untrusted browsing, and treat it as a credential store.

`deferred` is optional. With `tools.mcp.discovery.enabled` set to `true`, `"deferred": false` keeps the browser tools visible in the model context instead of hiding them behind tool search. Browser automation is usually a deliberate, frequent capability, so keeping it visible is often worth the schema cost. Set `"deferred": true` instead if you are tight on context and only need the browser occasionally; the model can then discover the tools by searching for "browser", "navigate", or "screenshot".

Keep `supports_parallel_tool_calls` at its default (`false`). Browser actions are stateful and order-dependent: clicking, typing, and navigating the same page concurrently can race. If you ever enable parallel calls, use separate browser contexts or serialise the actions yourself.

### Chromium managed by Playwright

If you do not want to run Chromium as a separate service, remove `--cdp-endpoint` and let Playwright MCP launch the browser. `--headless` and `--isolated` now control the browser Playwright creates.

```json
{
  "args": [
    "--headless",
    "--isolated",
    "--user-agent",
    "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/156.0.0.0 Safari/537.36",
    "--timeout-navigation",
    "45000",
    "--idle-timeout",
    "300000",
    "--output-dir",
    "/home/you/.picoclaw/workspace/browser"
  ]
}
```

Use the same `enabled`, `type`, and `command` fields as above. In this mode Playwright needs a browser it can launch: install its own Chromium with `npx playwright install chromium`, or point `--executable-path` at a system browser.

## Starting Chromium for CDP

Start Chromium once, bound to loopback, with a dedicated profile directory:

```bash
chromium \
  --headless=new \
  --remote-debugging-address=127.0.0.1 \
  --remote-debugging-port=9222 \
  --user-data-dir="$HOME/.cache/picoclaw-chromium" \
  --no-first-run \
  --no-default-browser-check \
  about:blank
```

Verify the endpoint before starting PicoClaw:

```bash
curl http://127.0.0.1:9222/json/version
```

The response contains a `webSocketDebuggerUrl` when Chromium is ready. If the connection is refused, check that Chromium is still running and that the port matches `--cdp-endpoint`.

A minimal systemd unit for a dedicated service user looks like this. Adjust `User`, paths, and the Chromium binary for your distribution:

```ini
[Unit]
Description=Headless Chromium for PicoClaw browser automation
After=network-online.target
Wants=network-online.target

[Service]
User=picoclaw
ExecStart=/usr/bin/chromium --headless=new --remote-debugging-address=127.0.0.1 --remote-debugging-port=9222 --user-data-dir=/home/picoclaw/.cache/picoclaw-chromium --no-first-run --no-default-browser-check about:blank
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Then enable and start it with `systemctl enable --now <unit-name>`. Keep the debugging address on `127.0.0.1`; see [Security](#security).

## Parameter reference

These are the flags used in the example configuration.

| Flag | Meaning | Notes |
|------|---------|-------|
| `--cdp-endpoint <url>` | Connect to an existing Chromium-family browser over the Chrome DevTools Protocol | Use `http://127.0.0.1:9222` for a local browser. Without this flag, Playwright launches its own browser. The endpoint is an unauthenticated control channel; never expose it to a network. |
| `--headless` | Run the browser without a window | Used when Playwright launches the browser. It is ignored with `--cdp-endpoint`: the Chromium you started already decides whether it is headless. |
| `--isolated` | Keep the browser profile in memory instead of writing it to disk | Useful for public or untrusted pages: cookies and storage disappear when the browser closes. With `--cdp-endpoint`, Playwright does not launch an isolated browser, but it still creates a fresh browser context for the session; without the flag it reuses Chromium's default context. |
| `--user-agent <string>` | Override the `User-Agent` header | Helps with sites that reject obviously non-browser clients. It does not make automation undetectable: TLS fingerprints, JavaScript behaviour, IP reputation, and browser fingerprints still matter. |
| `--timeout-navigation <ms>` | Maximum time to wait for a navigation | Default is `60000`. The example uses `45000` to fail faster; increase it for slow pages or low-power boards if navigations time out. |
| `--idle-timeout <ms>` | Close the browser after this long without a completed tool call; the next call relaunches it | Default is one hour for a headless browser launched by Playwright and no timeout for headed or attached browsers. An explicit value also applies to an attached browser. `300000` is five minutes; `0` disables it. In CDP mode with `--isolated`, the timer closes the session context and the next call creates a fresh one; the Chromium process itself is still owned by you. |
| `--output-dir <path>` | Directory for automatically named output files, such as a screenshot taken without a filename | Explicit filenames are resolved against the workspace root instead. Put this inside the PicoClaw workspace and make sure the PicoClaw user can write to it. |

Other flags that are often useful:

| Flag | Use |
|------|-----|
| `--viewport-size 1280x720` | Set a deterministic viewport; helpful for responsive layouts and stable screenshots. |
| `--device "iPhone 15"` or `--mobile` | Emulate a mobile device. Mobile pages are often lighter and cheaper in tokens. |
| `--proxy-server <url>` / `--proxy-bypass <domains>` | Route browser traffic through a proxy, for example when PicoClaw itself uses one. |
| `--user-data-dir <path>` | Persistent profile directory in managed mode. Use a dedicated directory, not your personal browser profile. |
| `--storage-state <path>` | Load cookies and local storage from a Playwright storage-state file into an isolated context. Treat the file as a secret. |
| `--executable-path <path>` | Use a specific Chromium/Chrome binary instead of Playwright's bundled browser. |
| `--timeout-action <ms>` | Action timeout; default `5000`. Increase it for slow pages or heavy client-side apps. |
| `--console-level error` | Reduce console noise returned to the model. |
| `--caps vision,pdf,devtools` | Enable extra capabilities such as coordinate-based vision, PDF generation, or DevTools features. Each adds tools and context cost. |
| `--save-session` | Save the session into the output directory for later inspection. |
| `--secrets <path>` / `--redact-url-params <names>` | Redact matching plain text or URL parameters from tool responses. These are convenience features, not a security boundary. |
| `--no-sandbox` | Disable Chromium's sandbox. Only use it in a container where the sandbox cannot run, and understand that it weakens isolation. |
| `--allowed-origins` / `--blocked-origins` | Restrict browser requests by origin. According to Playwright these are not a security boundary and do not affect redirects; use network-level egress rules when you need real enforcement. |
| `--allow-unrestricted-file-access` | Allow file access outside the workspace roots. Avoid it unless you have a specific need and understand the risk. |

Several flags configure a browser context rather than the browser process: `--user-agent`, `--viewport-size`, `--storage-state`, `--proxy-server`, and `--grant-permissions` are examples. In CDP mode they take effect only when Playwright creates a new context, which means combining them with `--isolated`. Without `--isolated`, the existing default context keeps Chromium's settings; set the equivalent options on the Chromium launch command if you need them without context isolation.

Two settings are PicoClaw-specific rather than Playwright flags:

| Config | Meaning |
|--------|---------|
| `deferred` | Per-server override for MCP tool discovery. `false` keeps the browser tools visible; `true` hides them behind tool search. Only effective when `tools.mcp.discovery.enabled` is `true`. |
| `supports_parallel_tool_calls` | Allows several calls to this server to run concurrently. Leave it `false` for browser automation because page actions are stateful. |

If you need proxy variables, credentials, or other environment values for the MCP process, use `env` or `env_file` under the server entry. `inherit_env` is off by default and passes the full PicoClaw environment only when explicitly enabled; keep it off unless the browser launcher genuinely needs it.

## Using the browser tools

A typical browser task follows the same loop Playwright MCP is designed for:

1. **Navigate** with `browser_navigate` to open the URL.
2. **Snapshot** with `browser_snapshot` to get the accessibility tree and element references. This is usually cheaper and more reliable than a screenshot.
3. **Interact** with `browser_click`, `browser_type`, `browser_fill_form`, `browser_select_option`, or `browser_press_key` using the references from the snapshot.
4. **Re-snapshot** after a navigation or DOM change. References from an older snapshot become stale.
5. **Capture** with `browser_take_screenshot` only when visual verification or a downloadable image is needed.
6. **Close** with `browser_close` when the task is finished, especially on memory-constrained hardware.

Common tools exposed by the Playwright server include:

| Tool | Purpose |
|------|---------|
| `browser_navigate` | Open a URL |
| `browser_snapshot` | Read the accessibility tree and obtain element references |
| `browser_click` | Click an element |
| `browser_type` | Type into an editable element |
| `browser_fill_form` | Fill several fields in one call |
| `browser_select_option` | Choose a dropdown option |
| `browser_press_key` | Press a key |
| `browser_take_screenshot` | Capture the page or a specific element |
| `browser_network_requests` | List network requests since the page loaded |
| `browser_console_messages` | Read console output |
| `browser_evaluate` | Run JavaScript in the page |
| `browser_close` | Close the browser and free its memory |

Screenshots are useful, but they are usually the most expensive way to understand a page. Prefer `browser_snapshot` for deciding what to click; use screenshots for visual checks, charts, or evidence the user asked for.

### Example request

A prompt that makes the fallback explicit works well:

> `web_fetch` returned only a JavaScript shell for `https://example.com/pricing`. Use the Playwright browser tools to open the page, accept the cookie banner if it appears, wait for the pricing table, and extract the plan names and prices. Save a full-page screenshot to `browser/pricing.png`. If the page shows a CAPTCHA or a login wall, stop and tell me instead of trying to work around it.

You can also add a short guideline to the workspace `AGENT.md`:

```markdown
When `web_fetch` returns only a JavaScript shell, a 403/429, or a cookie/consent wall,
try the Playwright browser tools before giving up. Do not attempt to solve CAPTCHAs or
bypass login walls; report them to the user.
```

## Anti-bot reality

A real browser solves many practical failures of `web_fetch`: JavaScript rendering, cookie and consent walls, session cookies, and sites that return different content to non-browser clients. A normal `User-Agent` can also fix sites that reject clients which do not look like a browser.

It is not a universal bypass. Sites can still detect automation through browser fingerprints, CDP artefacts, behaviour analysis, IP reputation, and account-level signals. CAPTCHAs, Cloudflare-style challenges, two-factor prompts, paywalls, and login walls remain gates. Use browser automation for legitimate access and respect the site's terms and `robots.txt`; do not use it to evade access controls. When a challenge appears, the right move is to ask the user, use an official API, or stop.

## Security

Browser automation is a powerful capability and deserves the same scrutiny as `exec`:

- **The CDP endpoint is unauthenticated.** Anyone who can reach `127.0.0.1:9222` can control the browser and read its sessions. Keep it on loopback. Do not publish the port in Docker, do not bind it to `0.0.0.0`, and do not expose it through a reverse proxy.
- **The browser profile is a credential store.** Cookies, local storage, and saved sessions can contain access tokens. Use `--isolated` for untrusted pages and a dedicated profile for trusted logins. Never point `--user-data-dir` at your personal browser profile. Treat `--storage-state` files as secrets.
- **MCP servers are outside the PicoClaw workspace sandbox.** `restrict_to_workspace`, `allow_read_paths`, and the exec guard constrain built-in tools; they do not sandbox a separate browser process. Playwright MCP limits file access to workspace roots (or its working directory when no roots are advertised), but its own documentation is explicit that this is a guardrail, not a security boundary. Do not enable `--allow-unrestricted-file-access`.
- **Pages can contain prompt injection.** A page can tell the model to click, buy, send, or reveal something. Do not keep payment credentials or sensitive logged-in accounts in the automation profile. Review consequential actions, and prefer a dedicated account with minimal privileges.
- **Code-execution tools are RCE-equivalent.** The Playwright server exposes `browser_evaluate` and `browser_run_code_unsafe`; the latter runs JavaScript in the Playwright server process. If the agent does not need them, exclude them with an `AGENT.md` tool allowlist (or use a server configuration that does not expose them).
- **Do not weaken Chromium casually.** `--no-sandbox` is for containers where the sandbox cannot run; it reduces isolation. `--allowed-origins` and `--blocked-origins` are request filters, not a security boundary.
- **Use the same threat model as a human browsing the web.** The browser can reach the public internet and, depending on your network, local services. Isolate it at the network level if that matters.

## Resource notes

PicoClaw's 10–20 MB figure is the Go process. Playwright MCP adds a Node.js process, and Chromium adds its own memory and CPU cost; while a page is open, the browser is usually the largest consumer. The core stays small, but plan for the browser process on low-RAM hardware.

Practical choices for a small board:

- Use **shared Chromium over CDP** so one browser is reused instead of launching a new one for each MCP session.
- Keep **one tab and one browser context** at a time; close pages you no longer need.
- Set `--idle-timeout` to release the browser after inactivity, and call `browser_close` when the task is done.
- Use `--headless`; there is no display or GPU to feed.
- Avoid `npx` at startup by installing `@playwright/mcp` globally and using an absolute binary path.
- Consider `deferred: true` when context is tight; browser tool schemas are large. Use `deferred: false` when browser work is frequent.
- Leave `supports_parallel_tool_calls` off: parallel page actions can race and may increase memory spikes.

If Chromium is killed by the OOM killer, reduce the number of open pages, use `--isolated` so profiles do not accumulate, or move the browser to another machine and connect to it over a remote MCP transport.

## Troubleshooting

| Symptom | Check |
|---------|-------|
| MCP server fails to start or the tools do not appear | `tools.mcp.enabled` is `true`, the server entry has `"enabled": true`, and `command` points to the real `playwright-mcp` binary. Run `picoclaw mcp test playwright` and check the gateway log. |
| `command not found` under systemd | Service managers often have a smaller `PATH`. Use the absolute path from `command -v playwright-mcp`. |
| `connect ECONNREFUSED 127.0.0.1:9222` | Chromium is not running, the port differs, or the MCP process is in a different container/network namespace. Start Chromium and test with `curl http://127.0.0.1:9222/json/version`. |
| Browser works, then closes after a few minutes | `--idle-timeout` expired. Increase it, set it to `0`, or accept the relaunch on the next tool call. If you use CDP without an explicit idle timeout, check the Chromium service instead. |
| `Target page, context or browser has been closed` | Chromium crashed or was killed, often from memory pressure. Check the Chromium/service logs and reduce open pages. |
| Screenshots are missing | The `--output-dir` path must exist and be writable by the PicoClaw user. Explicit filenames are resolved against the workspace root, not `--output-dir`. |
| Page is still empty after navigation | Increase `--timeout-navigation` and `--timeout-action`, and use `browser_snapshot` after the page settles. Some apps need a click or a wait before rendering. |
| Site still returns 403, a challenge, or a CAPTCHA | This is not a `User-Agent` problem. Use an official API, authenticate as a human, or stop. Browser automation is not a CAPTCHA solver. |
| Browser tools are hidden | With `tools.mcp.discovery.enabled: true`, either search for "browser" in tool discovery or set `"deferred": false` for the server. |
| High memory use | Close unused pages, use one context, lower the viewport, or move Chromium to another machine. The browser, not the PicoClaw core, is the cost. |

## Verify the setup

```bash
picoclaw mcp list
picoclaw mcp test playwright
picoclaw mcp show playwright
```

`mcp test` performs a connectivity check and lists the tools the server exposes. Then start the gateway and ask for a simple task such as "Open `https://example.com`, take a snapshot, and tell me the page title." If that works, move on to the real workflow.

For the general MCP configuration model, transports, and tool discovery, see [MCP tools](mcp.md). For the lightweight HTTP path, see [Web tools](web.md).
