---
title: "Backup and restore"
description: "Create, encrypt, transfer, and restore a PicoClaw snapshot with picoclaw backup and picoclaw restore."
---

# Backup and restore

`picoclaw backup` packages the migratable state of an installation into a
single archive. `picoclaw restore` verifies that archive and writes it to
another instance, so moving a PicoClaw installation is one command per side.

```bash
picoclaw backup -o picoclaw-backup.tar.gz
picoclaw restore picoclaw-backup.tar.gz
```

The default archive name is `picoclaw-backup-<YYYYMMDD-HHMMSS>.tar.gz` in the
current directory. Encrypted archives get a `.enc` suffix automatically.

## What is included

The archive contains:

- `PICOCLAW_HOME` (default `~/.picoclaw`): `config.json`, `.security.yml`,
  `auth.json`, `auth/mcp`, `channels`, `plugin-data`, `launcher-auth.db`,
  `state`, global `skills`, and the whole `workspace` tree.
- The workspace: sessions (`sessions/`, including `seahorse.db`), memory,
  state, cron jobs, custom commands, skills, `AGENT.md`, `SOUL.md`, `USER.md`,
  `HEARTBEAT.md`, and user files.
- The config file and `.security.yml` when they live outside the home.
- The credential SSH key used by `enc://` API keys
  (`PICOCLAW_SSH_KEY_PATH` or `~/.ssh/picoclaw_ed25519.key`).

Workspace, plugin-data, and channel stores must live under `PICOCLAW_HOME` to be
included. If a custom path points outside it, move it under the home or copy it
separately; the backup command does not scan arbitrary external paths.

## What is skipped

Only state that is machine-specific, recreated automatically, or unsafe to
archive is skipped:

| Path | Reason |
| --- | --- |
| `cache/` | cache, recreated automatically |
| `logs/` | machine-specific logs |
| `runtime-user-env/` | ephemeral sandbox `HOME`/`TMPDIR`/XDG directories |
| `.artifacts/`, `workspace/.artifacts/` | spilled tool results |
| `*.log`, `*.log.N`, `*.log.gz` | log files |
| `node_modules/`, `__pycache__/`, `.pytest_cache/`, `.mypy_cache/`, `.ruff_cache/`, `.venv/`, `venv/`, `.tox/` | reinstallable dependencies, virtual environments, and tool caches |
| `.DS_Store`, `._*`, `Thumbs.db`, `desktop.ini` | OS metadata |
| `*.tmp`, `.tmp-*`, `*.pid`, `*.sock`, `*.swp`, `*~` | temporary/runtime files |
| absolute symlinks and symlinks escaping their root | unsafe outside the tree |

The manifest records the skipped count and the first skipped entries. Plugin
packages and executables inside the standard `plugins/` directories are included;
binaries built for a different CPU architecture must be rebuilt on the target.
External plugin directories and other paths outside `PICOCLAW_HOME` should be
copied or reinstalled separately.

## Manifest and verification

The archive is a gzip-compressed tar. `MANIFEST.json` is the last entry and
contains:

- format and manifest version, creation time, PicoClaw version;
- the config schema version and original config path;
- the source home, platform, and hostname;
- every archived file with size, mode, modification time, and SHA-256;
- directories, safe relative symlinks, and skipped entries;
- the encryption parameters (algorithm and scrypt cost).

`picoclaw restore` extracts to a staging directory, checks every size and
SHA-256 against the manifest, rejects duplicate, absolute, or `..` paths and
unsafe symlinks, and only then writes to the target. A checksum mismatch aborts
the restore.

## Encryption

Pass `--encrypt` to protect the archive with a passphrase:

```bash
picoclaw backup --encrypt -o picoclaw-backup.tar.gz
```

The passphrase is read from the terminal (twice), from
`PICOCLAW_BACKUP_PASSPHRASE`, or from `--passphrase-file`. It is never placed on
the command line and never stored in the archive. New passphrases must be at
least 8 characters.

Encryption is AES-256-GCM in 1 MiB chunks with a key derived by scrypt
(N=2^15, r=8, p=1) and a random salt. The archive is compressed first, then
encrypted. Restore detects the encrypted format from the file header and prompts
for the passphrase; the file extension is not used for detection. On very small
devices, set `PICOCLAW_BACKUP_KDF_LOG_N` (for example `14` for about 16 MiB) to
lower the scrypt cost; the value used is recorded in the manifest.

This archive passphrase is independent from `PICOCLAW_KEY_PASSPHRASE`, which
decrypts `enc://` API keys inside the config. Keep both, and keep the restored
SSH key, when moving an installation that uses encrypted credentials.

## Restoring

```bash
picoclaw restore picoclaw-backup.tar.gz
```

Target paths default to the current `PICOCLAW_HOME` (or `~/.picoclaw`) and the
config, security, and SSH key paths derived from it. Use `--home`, `--config`,
and `--ssh-key` to override them.

Behavior:

1. The archive is verified against its manifest before writing to the target.
2. The target gateway must be stopped; `--allow-running` overrides this.
3. If the target already contains an installation, `--force` is required. A
   pre-restore archive named `<home>.pre-restore-<timestamp>.tar.gz` is created
   automatically before any file is written. If the source archive was
   encrypted, the safety archive uses the same passphrase; otherwise it is
   written in plain form with mode `0600`.
4. Restored files overwrite their counterparts. Files already present in the
   target are **not deleted**, so unrelated data survives.
5. An existing SSH key with different content is replaced only with `--force`;
   the old key is renamed to `<key>.pre-restore-<timestamp>`. Use `--no-ssh-key`
   to keep the target key. A key stored inside `PICOCLAW_HOME` is part of the
   home tree and cannot be omitted separately.
6. Restoring an archive with a newer config schema is refused unless `--force`
   is given. Older schemas are restored as-is and migrated on the next start.

Use `--dry-run` to read the manifest, check the config version and target, and
print the plan without writing anything. `--no-safety-backup` disables the
automatic pre-restore archive.

## Moving to another device

Stop the source service first so SQLite files and session JSONL are consistent:

```bash
sudo systemctl stop picoclaw        # or: launchctl unload ...
picoclaw backup --encrypt -o /tmp/picoclaw.tar.gz
```

Copy the archive to the target, install the PicoClaw binary for the target
architecture, and restore with the same service stopped:

```bash
sudo systemctl stop picoclaw
picoclaw restore /tmp/picoclaw.tar.gz.enc
sudo chown -R picoclaw:picoclaw /home/picoclaw/.picoclaw
picoclaw status
picoclaw agent -m "Reply with OK"
```

If the target uses a different user or home directory, pass `--home` and
`--config`. Absolute paths inside the JSON config that point under the source
home are rewritten to the target home. `.security.yml` values are not rewritten.
Keep the same layout when in doubt.

## Operational notes

- Restore extracts to a staging directory next to the target before validating
  hashes, so keep free space for the uncompressed snapshot and the restored
  files (roughly twice the snapshot size).
- Run the backup and restore with the gateway stopped. `--allow-running` is a
  best-effort option and can capture a database mid-write.
- Stop the Web UI launcher too if it is running; `launcher-auth.db` is a SQLite
  file.
- Archives contain API keys, tokens, sessions, and the credential SSH key. Keep
  them at mode `0600` and prefer `--encrypt` when they leave the machine.
  Restored config, token files, and the SSH key are forced to `0600`, and
  `auth/`, `auth/mcp`, WeChat token directories, and `plugin-data/` to `0700`.
- Unencrypted archives have no authenticity: the manifest detects accidental
  corruption, not an attacker who can rewrite both files and manifest.
- The snapshot is intentionally portable across PicoClaw versions through the
  config schema check. The binary itself is not included; install the build for
  the target OS/architecture.
- The manifest limits extraction to 1,000,000 entries and 100 GiB to avoid
  unbounded disk use from a malformed archive.

## Related documentation

- [CLI reference](../reference/cli.md)
- [Configuration](../guides/configuration.md)
- [Migrating from sipeed/picoclaw to this fork](../migration/from-sipeed-picoclaw.md)
