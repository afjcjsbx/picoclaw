# Plugin CLI

> Back to [README](../README.md)

PicoClaw includes a `plugin` CLI command group for managing [Agent Plugin](../guides/plugins.md)
entries in `config.json` and for checking plugin packages while you write them.

This CLI acts as a **configuration manager and linter**:

- `add`, `remove`, `enable` and `disable` edit `plugins.entries` in `config.json`
- `validate`, `init`, `show` and `list` read packages and never start anything
- `test`, `show --connect` and `list --status` load plugins, which **starts their MCP servers**
- the gateway still loads the plugins; restart it after changing the config

## Where It Writes

The CLI updates the same config file used by the rest of PicoClaw:

- `PICOCLAW_CONFIG` if set
- otherwise `~/.picoclaw/config.json`

It loads the config, changes `plugins`, and saves the whole file atomically with
the standard 2-space JSON formatting, exactly like `picoclaw mcp add`.

Behavior notes:

- enabling an entry (`add --enable`, `enable`) also sets `plugins.enabled`, because an
  enabled entry does nothing while the global switch is off
- removing the last entry with `remove` sets `plugins.enabled` back to `false`
- `add` without `--enable` never turns the global switch on
- package files and `plugin-data/<id>` are never deleted by the CLI

## Quick Start

Scaffold, check, enable and test a plugin:

```bash
picoclaw plugin init weather --dir ./plugins
picoclaw plugin validate ./plugins/weather
picoclaw plugin add weather --path ./plugins/weather --enable
picoclaw plugin test weather
```

See what is configured and what is merely present on disk:

```bash
picoclaw plugin list
picoclaw plugin list --status
```

Inspect one plugin:

```bash
picoclaw plugin show weather
picoclaw plugin show weather --connect
```

Turn it off, or remove the entry:

```bash
picoclaw plugin disable weather
picoclaw plugin remove weather
```

## Command Summary

| Command | Purpose |
|---------|---------|
| `picoclaw plugin list [--status]` | List configured entries and packages found under the plugin directories |
| `picoclaw plugin show <id> [--connect]` | Show manifest, capabilities and diagnostics for one plugin |
| `picoclaw plugin add <id> [--path <dir>] [--enable]` | Add or update `plugins.entries.<id>` |
| `picoclaw plugin remove <id>` | Remove an entry from config |
| `picoclaw plugin enable <id>` / `disable <id>` | Toggle `plugins.entries.<id>.enabled` |
| `picoclaw plugin validate <path\|id> [--strict]` | Lint a package without starting anything |
| `picoclaw plugin init <name> [--dir <dir>]` | Scaffold a new package |
| `picoclaw plugin test <id> [--strict]` | Dry-run load and report what the plugin publishes |
| `picoclaw plugin edit` | Open `config.json` in `$EDITOR` |

## Plugin IDs and Paths

An `<id>` is a key of `plugins.entries`. Without a `path`, it selects a package whose
manifest `name` equals the id, found under the plugin directories (see
[discovery](../guides/plugins.md#discovery-installation-and-activation)). With a `path`, the id is an
alias and may differ from the manifest name.

`list`, `show`, `test` and `validate` also accept ids of packages that are on disk but
have no entry yet; their state is `not enabled`.

If `validate` or `add` fails because a downloaded repository has no root `plugin.json`, see
[Installing a repository that is not a plugin](../guides/plugins.md#installing-a-repository-that-is-not-a-plugin).

## `picoclaw plugin list`

```bash
picoclaw plugin list [--status] [--timeout 30s]
```

Without `--status`, the output reflects configuration and disk only:

| State | Meaning |
|-------|---------|
| `disabled` | Entry exists but is disabled, or `plugins.enabled` is `false` |
| `pending` | Entry is enabled; it has not been loaded by this command |
| `not enabled` | Package found on disk, no entry activates it |

With `--status`, every enabled entry is loaded and the state becomes the runtime state
(`ready`, `degraded` or `failed`) together with the number of diagnostics. This starts the
plugins' MCP servers in a **separate process** from the gateway; if the gateway is
already running, its own state is not reported. `--timeout` bounds the wait.

On a wide terminal the output is a bordered panel; otherwise it is an ASCII table.

## `picoclaw plugin show`

```bash
picoclaw plugin show <id> [--connect] [--timeout 30s]
```

Prints the manifest metadata, root, state and `allow_hooks`, then the capabilities found by
reading the package: skills, MCP servers (including `mcp_overrides` from the entry) and
hooks, followed by diagnostics. A package with a rejected manifest is an error; use
`validate` to see why.

If the plugin declares hooks while `allow_hooks` is `false`, they are listed as inactive.

`--connect` loads the plugin instead and prints what it actually published, including MCP
tools as `server/tool`. It starts the plugin's MCP servers.

## `picoclaw plugin add`

```bash
picoclaw plugin add <id> [--path <dir>] [--enable] [--force]
```

| Flag | Meaning |
|------|---------|
| `--path` | Package directory. A relative path is resolved against the **current directory** and saved as an absolute path. |
| `--enable` | Enable the entry. Without it, a new entry is saved disabled. |
| `--force`, `-f` | Overwrite an existing entry without prompting. |

The package is resolved the same way the host will resolve it and its manifest is checked
before anything is saved. An invalid manifest, a missing directory, or an id that matches
no single package is an error and leaves the config untouched. Warnings (for example an
ignored manifest field) are printed and do not block the save.

When the entry already exists, you are asked to confirm. Only the fields you pass change:
`allow_hooks`, `agents`, `skill_names`, `config`, `mcp_overrides` and the rest are kept, and
omitting `--enable` does not disable an enabled entry.

Note that `entries.<id>.path` written by hand is relative to the **workspace**, not to the
shell. `add` avoids the difference by always saving an absolute path.

## `picoclaw plugin remove`, `enable`, `disable`

```bash
picoclaw plugin remove <id>
picoclaw plugin enable <id>
picoclaw plugin disable <id>
```

All three fail if `plugins.entries.<id>` does not exist. `enable` also sets
`plugins.enabled`. Restart the gateway to apply.

## `picoclaw plugin validate`

```bash
picoclaw plugin validate <path|id> [--strict]
```

Checks a package the way the host would load it, without starting any process or
contacting any server:

- `plugin.json`: schema, name, field types
- `skills/*/SKILL.md`: frontmatter, name matching the directory, description
- `mcp.json` and the entry's `mcp_overrides`: schema, transports, commands, URLs, headers
- hooks, inline or in `com.sipeed.picoclaw/hooks.json`: fields, intercept stages, result modes,
  and that an MCP action names a server that exists

Hooks are checked even when `allow_hooks` is `false`, so you can lint them before granting
the permission. A hook command that is not on `PATH` is reported, as the host would.

The argument is a directory, or an id. An id takes precedence over a directory with the same
name; write `./name` to force the directory.

**Exit status**

| Status | Cause |
|--------|-------|
| `0` | The manifest is accepted. Warnings may have been printed. |
| `1` | The manifest is rejected, the directory is unreadable, or `--strict` found any warning. |

Component problems and ignored manifest fields are warnings by default, because the host
loads the rest of the plugin anyway (the plugin runs `degraded`). Use `--strict` in CI to
fail on them:

```bash
picoclaw plugin validate . --strict
```

## `picoclaw plugin init`

```bash
picoclaw plugin init <name> [--dir <dir>]
```

Creates `<dir>/<name>/` (default `./plugins/<name>/`, relative to the current directory) with:

```text
plugin.json                    # $schema, name, version 0.1.0, description
skills/<name>/SKILL.md         # starter skill
README.md
```

The package passes `validate --strict` as generated. If the name contains `.`, the skill is
named with `-` instead, because skill names do not allow periods. `init` refuses to write into a
non-empty directory and never touches `config.json`; enable the plugin with `plugin add`.

## `picoclaw plugin test`

```bash
picoclaw plugin test <id> [--timeout 30s] [--strict]
```

Loads one plugin exactly as the gateway would, even if its entry is disabled or does not
exist yet, and prints the final state, the skills, MCP tools and hooks it publishes, and its
diagnostics. Nothing is registered with an agent and the config is not changed.

This **starts the plugin's MCP servers** and creates its `plugin-data/<id>` directory, as the
gateway does. Only test packages you trust. Hooks are published only when the entry has
`allow_hooks` set.

| Status | Cause |
|--------|-------|
| `0` | The plugin is `ready`, or `degraded` without `--strict` |
| `1` | The plugin `failed`, did not finish within `--timeout`, or is `degraded` with `--strict` |

## `picoclaw plugin edit`

```bash
picoclaw plugin edit
```

Opens `config.json` in `$EDITOR`. It fails if `$EDITOR` is not set.

## Related

- [Plugin Installation](../guides/plugins.md): package format, host configuration, hooks, isolation
- [Writing a Plugin](../guides/writing-a-plugin.md): step-by-step authoring tutorial
- [MCP Server CLI](mcp-cli.md): the plugin CLI manages packages, not individual MCP servers
