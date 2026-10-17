# Filesystem tools

PicoClaw ships a filesystem tool family for reading, writing, and searching files. Relative paths are resolved from the agent workspace.

| Tool | What it does | Switch (default) |
|------|--------------|------------------|
| `read_file` | Read file contents, in byte-offset or line-numbered mode | `tools.read_file.enabled` (`true`) |
| `write_file` | Create a file; replacing an existing file requires `overwrite=true` | `tools.write_file.enabled` (`true`) |
| `edit_file` | Replace one exact text occurrence in a file | `tools.edit_file.enabled` (`true`) |
| `append_file` | Append content to a file (creates it if missing) | `tools.append_file.enabled` (`true`) |
| `list_dir` | List directory entries | `tools.list_dir.enabled` (`true`) |
| `search_files` | Regex content search or glob file search | `tools.search_files.enabled` (`true`) |

Every switch follows the environment variable pattern `PICOCLAW_TOOLS_<TOOL>_ENABLED` (for example `PICOCLAW_TOOLS_SEARCH_FILES_ENABLED=false`), except `read_file`, whose settings are configured in JSON only. See the [tool switch table](../reference/tools_configuration.md#tool-switches) for the full list of tools.

## Sandbox and path policy

The filesystem tools are always bound to the agent workspace and the readable/writable sandbox:

- `agents.defaults.restrict_to_workspace` (default `true`) keeps reads and writes inside the workspace.
- `agents.defaults.allow_read_outside_workspace` (default `false`) lets read-only tools access paths outside the workspace while keeping the restriction for writes and for `exec`.
- `tools.allow_read_paths` and `tools.allow_write_paths` are regular-expression allowlists evaluated against absolute paths, granting access that the workspace restriction would otherwise deny.
- The credential/protected-file policy always wins over allow paths. See [Protected local files](../reference/tools_configuration.md#protected-local-files) for the full deny list and symlink behavior.

These settings are global. There is no per-tool path configuration beyond the switches above.

## `read_file`

The tool has two modes, selected with `tools.read_file.mode`:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.read_file.enabled` | bool | `true` | Register the `read_file` tool |
| `tools.read_file.mode` | string | `bytes` | `bytes` = byte-offset pagination; `lines` = line-numbered output |
| `tools.read_file.max_read_file_size` | int | `65536` | Maximum bytes read per call (byte mode), and the default `length` |
| `tools.read_file.max_lines` | int | `2000` | Upper bound on a single line-mode read; `max_lines` requests above it are clamped |
| `tools.read_file.max_line_length` | int | `2000` | Maximum characters per line in the line-numbered view; the remainder is replaced by a ` ... [truncated]` marker |

**Byte mode** (`mode: "bytes"`, default) reads raw content and supports pagination:

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the file to read |
| `offset` | integer | no | Byte offset to start reading from. Default: `0` |
| `length` | integer | no | Maximum number of bytes to read. Default: `max_read_file_size` |

**Line mode** (`mode: "lines"`) returns line-numbered output (`LINE_NUMBER|LINE_CONTENT`, 1-indexed) and supports partial reads of large text files:

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the file to read |
| `start_line` | integer | no | Line number to start from (1-indexed, inclusive). Default: `1` |
| `max_lines` | integer | no | Maximum number of lines to read. Requests above `tools.read_file.max_lines` are clamped |

Using `offset`/`length` in line mode (or `limit`) is rejected with an error: each mode only accepts its own pagination arguments.

Lines longer than `tools.read_file.max_line_length` characters are cut after that many characters with a ` ... [truncated]` marker and the read continues with the next line, so a single long line cannot consume the whole byte budget. If the byte budget itself runs out mid-line, the header reports the mid-line cut instead.

Results larger than `tools.max_inline_result_chars` are spilled to a workspace artifact with a head+tail preview, exactly like every other tool result; see [Large tool results](../reference/tools_configuration.md#large-tool-results).

## `write_file`

Creates a file with the full content in one call. If the file already exists, the call fails unless `overwrite=true` is passed, which replaces the entire file and discards its current contents.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to write |
| `content` | string | yes | Full file content. Standard JSON escaping applies: `\n` for newline, `\\n` for a literal backslash-n |
| `overwrite` | boolean | no | Set to `true` to replace an existing file in full. Default: `false` |

For targeted changes, prefer `edit_file` or `append_file`: PicoClaw advertises those alternatives in the tool description and in the "file already exists" error only when they are enabled. Writes to protected credential paths (and to `write_file`-specific denies such as `/etc/shadow` or the workspace `sessions` directory) are refused; write allow paths do not override the policy.

## `edit_file`

Replaces a single, exact occurrence of `old_text` with `new_text` and returns a diff of the change.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the file to edit |
| `old_text` | string | yes | Exact text to replace; must appear exactly once |
| `new_text` | string | yes | Replacement text |

The call fails when `old_text` is not found, or when it appears more than once (add more context to make the match unique). The file is rewritten in place.

## `append_file`

Appends content to the end of a file. The file is created if it does not exist.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path of the file to append to |
| `content` | string | yes | Content to append |

## `list_dir`

Lists the entries of a directory. Protected entries (for example `.ssh` and credential files) are hidden by the policy.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Directory to list (relative paths resolve from the workspace) |

## `search_files`

Searches file contents with a regular expression, or finds file names by glob or substring. It is the intended replacement for `grep`, `rg`, `find`, and `ls` in the terminal tool.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `pattern` | string | yes | Regex for content search (an empty pattern matches every line), or a filename pattern for `target: "files"` (see below; an empty pattern lists all files) |
| `target` | string | no | `content` (default) or `files` |
| `path` | string | no | File or directory to search. Default: `.` |
| `file_glob` | string | no | Optional filename pattern applied to content searches (same syntax as `pattern` for `target: "files"`) |
| `limit` | integer | no | Maximum results to return. Default: `50`, maximum: `200` |
| `offset` | integer | no | Number of results to skip (pagination); a result is a match line, or a merged context block when `context` > 0. Default: `0` |
| `order` | string | no | `discovery` (default) or `modified` (newest modification time first; scans the full tree) |
| `output_mode` | string | no | Content searches: `content` (default), `files_only`, or `count` |
| `context` | integer | no | Lines before and after each content match (maximum: `20`). Default: `0` |

Filename patterns accept three forms:

* **Empty pattern** — matches every file, so `{"pattern": "", "target": "files"}` is an `ls -R`-style listing.
* **Plain text** — a pattern without `*`, `?`, `[`, or `{` matches as a substring of the path relative to the search root, so `docker-compose` finds both `docker-compose.yml` and `deploy/docker-compose.prod.yml`.
* **Glob** — a pattern with metacharacters is matched as a glob. A glob containing `/` is matched against the whole relative path; one without `/` is matched against the base name. `*` and `?` do not cross `/`, `**/` matches any number of directories (`src/**/*.ts` also matches `src/main.ts`), and `{a,b}` alternates (`*.{js,ts}`).

Output lines are capped at 240 bytes each. When a match lies beyond that cut, the displayed window moves to keep the matched text visible, with `…` marking both cut sides, so reported matches are never silently dropped from the output.

With `context` greater than zero, matches whose context ranges overlap or touch are merged into a single block (as ripgrep does) instead of reprinting shared lines, and blocks are split at 50 lines so each result row stays small. `offset` and `limit` count these blocks, which makes pagination duplicate-free.

Content searches are bounded by a scan budget (5,000 files and 64 MiB of file content). Files are streamed line by line, so there is no per-file size limit: larger files are searched in full as long as they fit the global budget. When a file no longer fits in the remaining byte budget it is skipped on its own and the walk continues with the files that still fit, instead of ending the search early. Binary detection looks only at the first 8 KiB: a NUL byte there marks the file as binary and it is skipped, while text in other encodings (Latin-1, mixed byte sequences) is searched as byte text. Result lines are additionally scrubbed of credentials before they reach the model: well-known token formats (Bearer headers, JWTs, `sk-`/`rk-` keys, GitHub tokens, AWS key IDs, Slack and Google keys) and long machine-generated high-entropy strings are replaced with `[FILTERED]`, while ordinary identifiers, prose, and hex hashes stay visible. This redaction works without configuration and is independent of the configured sensitive-data filter.

Searches run inside the readable sandbox. On macOS, searches above your home directory skip TCC-protected folders; target one directly to include it.

Like `ripgrep`, directory walks honor `.gitignore` and `.ignore` files, including nested ones: patterns are gitignore-style (`!` negation, trailing `/` for directories, a leading or embedded `/` anchors to the declaring directory), `.ignore` overrides `.gitignore`, and a deeper file overrides a shallower one. `.git` is always skipped. An explicitly targeted file or directory (via `path`) is searched even when its ancestors are ignored.
