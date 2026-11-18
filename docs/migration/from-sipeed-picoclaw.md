---
title: "Migrating from sipeed/picoclaw to this fork"
description: "Step-by-step guide to back up, install, and verify the community-maintained PicoClaw fork without losing configuration or session data."
---

# Migrating from sipeed/picoclaw to this fork

This guide is for installations that currently run the upstream [`sipeed/picoclaw`](https://github.com/sipeed/picoclaw) binary and want to switch to the community-maintained fork in this repository.

The fork keeps the same `~/.picoclaw` data layout by default, so you normally keep your configuration, workspace, sessions, skills, and scheduled tasks. Back up first and verify after the switch.

## 1. Back up the current installation

Stop the running service before copying data. The default data directory is `~/.picoclaw`; if you use `PICOCLAW_HOME`, back up that directory instead.

```bash
# Stop the service first. The unit name depends on how you installed it.
sudo systemctl stop picoclaw   # or: launchctl unload ~/Library/LaunchAgents/...

cp -a ~/.picoclaw "$HOME/.picoclaw.backup-$(date +%Y%m%d-%H%M%S)"
```

Record the current version and configuration path:

```bash
picoclaw version
printf '%s\n' "${PICOCLAW_HOME:-$HOME/.picoclaw}"
```

## 2. Install the fork

Download the release asset for your platform from the [latest release](https://github.com/afjcjsbx/picoclaw/releases/latest), or use the installer:

```bash
curl -fsSL https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.sh | sh
```

On Windows PowerShell:

```powershell
irm https://raw.githubusercontent.com/afjcjsbx/picoclaw/main/scripts/install.ps1 | iex
```

If you build from source, follow the [build instructions](https://github.com/afjcjsbx/picoclaw#build-from-source) and install the resulting `picoclaw` binary in place of the old one.

## 3. Verify the switch

```bash
picoclaw version
picoclaw status
```

`picoclaw version` should report the fork release you installed. `picoclaw status` should load the same configuration and default model as before.

Start the gateway or agent and check the channels you use:

```bash
picoclaw gateway
# or, for a one-shot check:
picoclaw agent -m "Reply with OK"
```

If you run the Web UI, start the launcher and confirm that the dashboard loads:

```bash
picoclaw-launcher
```

## 4. Configuration compatibility

The fork uses the same `config.json` format as the upstream project. If your configuration still uses the legacy `providers` block, follow the [providers to `model_list` migration guide](model-list-migration.md) before upgrading further.

If a release changes a configuration field, the release notes call it out. Check the [releases page](https://github.com/afjcjsbx/picoclaw/releases) before upgrading a production install.

## 5. Roll back

If the new binary does not start or a channel behaves differently, stop the service and restore the backup you created in step 1. Confirm the backup exists before removing the current data directory.

```bash
# Stop the fork service first.
BACKUP_DIR="$HOME/.picoclaw.backup-20261107-120000"  # use your actual backup path
test -d "$BACKUP_DIR" && rm -rf "$HOME/.picoclaw" && mv "$BACKUP_DIR" "$HOME/.picoclaw"
```

Then reinstall the previous binary. Keep it until you have verified the new installation. Do not delete the backup until the gateway, channels, and scheduled tasks have all been checked.

## Related documentation

- [Configuration guide](../guides/configuration.md)
- [CLI reference](../reference/cli.md)
- [Troubleshooting](../operations/troubleshooting.md)
- [Maintenance status](../project/maintenance-status.md)
