---
description: "Skills are modular capabilities you add to your agent by dropping a SKILL.md folder into a skills directory."
---
# Skills Authoring Guide

Skills are modular capabilities you add to your agent by dropping a `SKILL.md` folder into a skills directory. This guide covers writing a skill from scratch. To install existing skills or Agent Plugin packages, see [Plugin Installation](plugins.md) and the [`picoclaw skills` commands](../reference/cli.md#skills).

A plain skill is just a folder the agent reads with `read_file`. If you need packaged MCP servers, scripts, or shareable bundles, consider [Agent Plugins](plugins.md) instead. MCP servers are a separate mechanism for external tools — see [MCP tools](../tools/mcp.md).

## Where Skills Live

Skills are discovered one level deep: each direct subdirectory of a skills root must contain a `SKILL.md`. When names collide, the first root wins, in this order:

| Priority | Root | Source |
| --- | --- | --- |
| 1 | Plugin-provided skills (see [Plugins](plugins.md)) | `plugin` |
| 2 | `~/.picoclaw/workspace/skills` (or your configured workspace) | `workspace` |
| 3 | `~/.picoclaw/skills` | `global` |
| 4 | Builtin skills embedded at build time (`picoclaw skills list-builtin`) | `builtin` |

Copy builtin skills into your workspace to customize them:

```bash
picoclaw skills list-builtin
picoclaw skills install-builtin
```

A skill folder that fails validation is skipped, and the Gateway emits a warning in its logs (for example `invalid skill from workspace ...`) — check the [logs](../operations/debug.md) when a skill does not appear.

## SKILL.md Format

A skill is a Markdown file with YAML frontmatter:

```markdown
---
name: my-skill
description: What it does and when to use it — this is the trigger.
---

# My Skill

1. Step one.
2. For details read [the API reference](references/api.md).
```

### Frontmatter fields

| Field | Required | Rules |
| --- | --- | --- |
| `name` | Yes | Non-empty; ≤ 64 characters; letters, digits, and hyphens only (`^[a-zA-Z0-9]+(-[a-zA-Z0-9]+)*$`) — no underscores or spaces |
| `description` | Yes | Non-empty; ≤ 1024 bytes |

The `description` is the most important line you will write: it is the only text the agent sees **before** deciding to use the skill, so state both what the skill does and when to use it (include trigger words a user would say). Unknown fields such as `license` or `metadata` are tolerated but ignored by the loader.

If frontmatter is missing, the loader falls back to the directory name and the first body paragraph; a leading H1 can override the name when it matches the naming rules. Relying on fallbacks is not recommended — always add explicit frontmatter.

### Body conventions

- Write in the imperative and keep it concise. The agent is already capable; only add context it cannot infer.
- Keep the body under ~500 lines. Metadata is always in context; the body is loaded only after the skill triggers, so **when to use** belongs in `description`, not the body.
- Prefer short examples over long explanations, and match specificity to the task: free-form guidance for judgment calls, exact steps or scripts for fragile sequences.
- Link additional files relatively and keep references one level deep, e.g. `[the example](references/example.md)`. The agent reads them from the skill folder when needed.

## Bundled Resources

```
my-skill/
├── SKILL.md          (required)
├── references/       (optional documentation, loaded on demand)
├── scripts/          (optional executables for deterministic tasks)
└── assets/           (optional templates, icons, files used in output)
```

`references/` files are read with `skills_read` and a `relativePath` such as `references/api.md` (limited to the skill's own directory; symlinks that leave it are refused). Plugin-provided skills are surfaced differently: their resources are read through the plugin resource tool instead.

## How Skills Reach the Agent

The system prompt does **not** list skills, so it stays the same size with 10 or 10,000 skills installed. It only explains two tools:

- `skills_search` ranks installed skills against a natural-language request (BM25 over `name` and `description`; an exact name always comes first) and returns `name`, `description` and `location`.
- `skills_read` returns the complete `SKILL.md` (frontmatter removed) or a supporting file. Files over 256 KiB are refused with an error, never truncated.

Skills that are forced active by configuration or a turn profile are still inlined as an **Active Skills** block. A turn profile with `skills.mode` `off` or `custom` also limits what both tools can find and read.

This means two things for authors:

- `name` and `description` decide whether your skill is ever found: write the description for the request that should trigger it. Only the first 8 KiB of `SKILL.md` is read to index it, so keep the frontmatter at the top.
- The body cost is paid only on use — keep it focused on execution, not on justification.

Installing or removing a skill is picked up on the next search; an edit to `name` or `description` within about 10 seconds. `skills_read` always reads the file from disk.

## Testing a Skill

1. Create the folder and `SKILL.md` in `~/.picoclaw/workspace/skills/<name>/`.
2. Verify it is discovered:

   ```bash
   picoclaw skills list
   picoclaw skills show <name>
   ```

3. Start the Gateway (or the Web UI) and ask something that should trigger the skill; inspect the tool calls and reads in `/logs` or with `picoclaw gateway --debug`.

Two ready-to-read examples ship in the repository:

- [`examples/plugins/demo-plugin/skills/greet/SKILL.md`](https://github.com/afjcjsbx/picoclaw/blob/main/examples/plugins/demo-plugin/skills/greet/SKILL.md) — minimal skill with a `references/` file.
- The builtin `skill-creator` skill (`picoclaw skills list-builtin`) — a detailed authoring process you can ask the agent to use directly.

## Sharing Skills

- **GitHub**: publish the skill folder in a repository and install it with `picoclaw skills install <owner>/<repo>/<path-to-skill>`.
- **Registries**: configure `tools.skills.registries` in `config.json` (the default `clawhub` and `github` registries are documented in [Skills tools](../tools/skills.md)) and install with `picoclaw skills install --registry <name> <slug>`.
- **Plugins**: to ship skills together with MCP servers as one package, follow the [Plugin Installation](plugins.md) guide and the [demo plugin](https://github.com/afjcjsbx/picoclaw/tree/main/examples/plugins/demo-plugin) layout.
