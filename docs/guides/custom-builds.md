# Custom Builds

PicoClaw's standard builds keep the full built-in channel set and Seahorse
support. Go build tags let you produce a smaller `picoclaw` executable when
you know at build time which channels and context manager you will use.

These tags affect the executable being built. They do not remove packages from
the source tree or from `go.mod`, and they do not change other binaries such as
`cmd/membench`.

## Default Build

The regular Makefile targets use `GO_BUILD_TAGS=goolm,stdjson`. Without
`custom_channels`:

- All channel drivers supported by the target are registered.
- Seahorse is included on supported targets.
- Matrix may be unavailable on targets where its SQLite/crypto dependencies
  do not build.
- The native WhatsApp implementation is not included unless the separate
  `whatsapp_native` tag is set. The WhatsApp Native channel package otherwise
  provides a stub that reports this at runtime.

For the ordinary build, leave `GO_BUILD_TAGS` at its default:

```bash
make build
```

## Select Channels

Add `custom_channels` to switch from the default full set to an explicit set.
Then add one `channel_<name>` tag for every channel to include. For example:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,channel_discord'
```

Available channel tags:

| Tag | Channel |
| --- | --- |
| `channel_deltachat` | Delta Chat |
| `channel_dingtalk` | DingTalk |
| `channel_discord` | Discord |
| `channel_feishu` | Feishu |
| `channel_irc` | IRC |
| `channel_line` | LINE |
| `channel_matrix` | Matrix |
| `channel_maixcam` | MaixCam |
| `channel_mqtt` | MQTT |
| `channel_onebot` | OneBot |
| `channel_pico` | Pico |
| `channel_qq` | QQ |
| `channel_slack` | Slack |
| `channel_slack_webhook` | Slack Webhook |
| `channel_teams_webhook` | Teams Webhook |
| `channel_telegram` | Telegram |
| `channel_vk` | VK |
| `channel_wecom` | WeCom |
| `channel_weixin` | Weixin |
| `channel_whatsapp` | WhatsApp Bridge |
| `channel_whatsapp_native` | WhatsApp Native |

If `custom_channels` is set without any `channel_<name>` tags, no built-in
channel drivers are registered. The channel configuration schema remains in
the binary, but an omitted driver cannot be enabled by changing configuration
at runtime.

### Platform and WhatsApp Notes

Matrix is not compiled on `mipsle`, NetBSD, Android, or FreeBSD/ARM because of
upstream SQLite and crypto dependency limitations. Selecting `channel_matrix`
does not make it available on those targets.

In a `custom_channels` build, the native WhatsApp implementation requires
**both** `channel_whatsapp_native` and `whatsapp_native`. The first includes
its channel registration; the second selects the real `whatsmeow`
implementation instead of the lightweight stub. In the standard full-channel
build, registration is already included, so only `whatsapp_native` is needed
to select the real implementation. For example:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,channel_whatsapp_native,whatsapp_native'
```

## Omit Seahorse

The `no_seahorse` tag excludes the Seahorse context manager from the main
`picoclaw` executable. This also removes the `pkg/seahorse` import and its
`modernc.org/sqlite` dependency from that executable when no other selected
feature needs SQLite.

For a Telegram-only build without Seahorse:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,no_seahorse'
```

When Seahorse is omitted, PicoClaw still recognizes `context_manager: seahorse`
as a configured manager, but its factory reports that Seahorse is absent from
the build. The existing context-manager resolver logs a warning and falls back
to the legacy context manager. This avoids silently behaving as if the
SQLite-backed Seahorse manager were active.

The default build does not set `no_seahorse` and keeps the current behavior.
The tag does not delete the Seahorse package or remove SQLite from `go.mod`;
other packages and tools in the repository still use them.

## SQLite Dependencies

`no_seahorse` removes SQLite only when Seahorse is the remaining SQLite user in
the selected executable. The standard full-channel build still includes
Matrix, which uses SQLite independently. WhatsApp Native also uses SQLite when
its real implementation is built.

To omit SQLite from the main executable, use a custom channel set without
Matrix or the real WhatsApp Native implementation, and add `no_seahorse`. For
example:

```bash
make build GO_BUILD_TAGS='goolm,stdjson,custom_channels,channel_telegram,no_seahorse'
```

If Matrix is selected, or if the real WhatsApp Native implementation is
enabled, SQLite remains linked even when `no_seahorse` is set.

You can inspect the main executable's dependency list for a given build profile:

```bash
go list -deps -tags 'goolm,stdjson,custom_channels,channel_telegram,no_seahorse' ./cmd/picoclaw \
  | rg 'pkg/seahorse|modernc.org/sqlite'
```

For the example above, the command should produce no output. `go.mod` still
declares SQLite because the default build and other repository targets need it.

## Measured Binary Sizes

These reference measurements use Go 1.27.1 on macOS ARM64, with
`CGO_ENABLED=0` and linker flags `-s -w` to strip symbols and DWARF debug data.
All builds used the same target and linker settings:

| Profile | Build tags | Size |
| --- | --- | ---: |
| Full default channels and Seahorse | `goolm,stdjson` | 37,078,802 bytes (35.36 MiB) |
| Telegram only, with Seahorse | `goolm,stdjson,custom_channels,channel_telegram` | 28,806,514 bytes (27.47 MiB) |
| Telegram only, without Seahorse | `goolm,stdjson,custom_channels,channel_telegram,no_seahorse` | 24,692,146 bytes (23.55 MiB) |

In this measurement, selecting Telegram alone reduced the binary by 7.89 MiB
(22.3%). Omitting Seahorse from that profile saved a further 3.92 MiB (14.3%).
The final profile was 11.81 MiB (33.4%) smaller than the full default build.

Sizes vary with Go version, operating system, architecture, linker settings,
and selected features. Use these figures as a reproducible reference, not as a
size guarantee for other targets.

The profiles can be reproduced with these commands:

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
  -tags 'goolm,stdjson' -ldflags '-s -w' \
  -o /tmp/picoclaw-full ./cmd/picoclaw

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
  -tags 'goolm,stdjson,custom_channels,channel_telegram' -ldflags '-s -w' \
  -o /tmp/picoclaw-telegram ./cmd/picoclaw

CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
  -tags 'goolm,stdjson,custom_channels,channel_telegram,no_seahorse' -ldflags '-s -w' \
  -o /tmp/picoclaw-telegram-no-seahorse ./cmd/picoclaw
```
