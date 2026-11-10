---
description: "Create reusable prompt commands from Markdown files in the PicoClaw workspace or global config directory."
---
# Custom Prompt Commands

Custom prompt commands turn a Markdown file into a reusable slash command. The
file name becomes the command name and the file body becomes a prompt template.
When you run the command, PicoClaw expands the template and submits it as a
normal user message in the current session.

## Where Commands Live

PicoClaw discovers `*.md` files in two roots:

| Priority | Root | Scope |
| --- | --- | --- |
| 1 | `<workspace>/commands/` | Current agent workspace |
| 2 | `<PICOCLAW_HOME>/commands/` | All agents and workspaces |

The default global directory is `~/.picoclaw/commands`. Set `PICOCLAW_HOME` to
move the whole PicoClaw data directory, or set a different `workspace` for an
agent in `config.json`.

Workspace commands override global commands with the same name. Built-in
commands such as `/help`, `/clear`, and `/list` always win, so a file named
`help.md` cannot replace `/help`.

Nested paths become names with `/` separators:

```text
<workspace>/commands/team/review.md  ->  /team/review
```

Command names are lowercased. Only Markdown files (`.md`) are discovered; hidden
files and hidden directories are ignored. Changes are picked up on the next
invocation, so you do not need to restart the gateway.

## File Format

A command file may start with YAML frontmatter. Only `description` is used; it
appears in `/help` and in the Web UI command palette.

```md title="<workspace>/commands/review.md"
---
description: Review code for correctness
---

Review $ARGUMENTS for bugs and missing tests. Report bugs first.
```

If the file has no frontmatter, the whole file is the template. Empty templates
are skipped. Files whose name is not a valid command name are skipped and
reported with a warning in the gateway log.

Run the command from any chat channel or the CLI:

```text
/review src/auth.ts
```

PicoClaw submits `Review src/auth.ts for bugs and missing tests. Report bugs
first.` as the user prompt and runs a normal agent turn with the current agent
and model.

## Arguments

### `$ARGUMENTS`

`$ARGUMENTS` expands to the complete argument string exactly as entered,
including quotes and internal spacing:

```md
Review $ARGUMENTS for bugs.
```

```text
/review src/auth.ts "error handling"
```

becomes:

```text
Review src/auth.ts "error handling" for bugs.
```

### Positional arguments

Use `$1`, `$2`, and higher numbers for parsed arguments. Single and double
quotes group words and are removed from the parsed value. The highest-numbered
placeholder consumes its argument and everything after it:

```md title="<workspace>/commands/compare.md"
Compare $1 with $2.
```

```text
/compare api stable branch
```

becomes:

```text
Compare api with stable branch.
```

Missing positions expand to an empty string. With `/compare api`, `$1` is
`api` and `$2` is empty. If the whole template expands to an empty string (for
example a body containing only `$1` invoked without arguments), PicoClaw replies
with an error instead of sending the raw slash text to the model.

### Fallback append

When the template has no placeholder and arguments were supplied, PicoClaw
appends them after a blank line:

```md title="<workspace>/commands/explain.md"
Explain this code clearly.
```

```text
/explain src/cache.ts
```

becomes:

```text
Explain this code clearly.

src/cache.ts
```

## Listing Commands

`/help` lists built-in and custom commands together. Use `/help <name>` for the
command metadata:

```text
/help review
```

The Web UI slash suggestions and command palette also list workspace custom
commands. Reload the page if the Web UI was already open when you added a file.

Telegram's bot command menu includes custom commands whose names are compatible
with Telegram: lowercase letters, digits, and underscores, up to 32 characters.
Nested or punctuated names still work as slash commands but are not shown in the
Telegram menu. Telegram refreshes its menu when the channel starts.

## Limitations

This is deliberately a small prompt-template feature. Command files cannot:

- run shell blocks embedded in the template;
- switch the agent or model;
- spawn a background sub-agent;
- define JSON commands or new built-in handlers.

Use a [skill](skills-authoring.md) for reusable capabilities that the agent
should load while working, or the `spawn` tool for background tasks.

## Security Notes

Command files are trusted configuration. They are read from the agent workspace
and from the PicoClaw home directory, and the expanded prompt runs with the same
permissions as any other user message. Keep write access to those directories
restricted to people who can already control the agent.
