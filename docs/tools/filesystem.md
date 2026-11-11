---
description: "PicoClaw ships a filesystem tool family for reading, writing, and searching files. Relative paths are resolved from the agent workspace."
---
# Filesystem tools

PicoClaw ships a filesystem tool family for reading, writing, and searching files. Relative paths are resolved from the agent workspace.

| Tool | What it does | Switch (default) |
|------|--------------|------------------|
| `read_file` | Read file contents, in byte-offset or line-numbered mode | `tools.read_file.enabled` (`true`) |
| `write_file` | Create a file; identical and append-shaped rewrites are safe, other replacements require `overwrite=true` | `tools.write_file.enabled` (`true`) |
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

The tool selects one of three modes with `tools.read_file.mode`:

| Config | Type | Default | Description |
|--------|------|---------|-------------|
| `tools.read_file.enabled` | bool | `true` | Register the `read_file` tool |
| `tools.read_file.mode` | string | `auto` | `auto` = line-numbered text with raw-byte fallback for binary files; `lines` = line-numbered output only; `bytes` = raw byte-offset pagination only |
| `tools.read_file.max_read_file_size` | int | `65536` | Maximum bytes read per call (byte mode), and the default `length` |
| `tools.read_file.max_lines` | int | `2000` | Upper bound on a single line-mode read; `max_lines` requests above it are clamped |
| `tools.read_file.max_line_length` | int | `2000` | Maximum characters per line in the line-numbered view; the remainder is replaced by a ` ... [truncated]` marker |

**Auto mode** (`mode: "auto"`, default) inspects the first 512 bytes of the file. Text files are read in line mode, while binary-looking files fall back to byte mode instead of being rejected. The tool exposes both pagination styles, and passing `offset` or `length` explicitly forces byte mode, which is useful for minified text files with very long lines:

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the file to read |
| `start_line` | integer | no | Text files: line number to start from (1-indexed, inclusive). Default: `1` |
| `max_lines` | integer | no | Text files: maximum number of lines to read. Requests above `tools.read_file.max_lines` are clamped |
| `offset` | integer | no | Binary files or raw reads: byte offset to start reading from. Default: `0` |
| `length` | integer | no | Binary files or raw reads: maximum number of bytes to read. Default: `max_read_file_size` |

Passing both line and byte pagination arguments in the same call is rejected. Passing line arguments for a binary file returns an error that points at `offset`/`length`; omitting all pagination arguments lets auto mode pick the right one.

**Byte mode** (`mode: "bytes"`) reads raw content and supports pagination:

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

Using `offset`/`length` in line mode (or `limit`) is rejected with an error, and byte mode rejects line arguments: each fixed mode only accepts its own pagination arguments. Line mode rejects binary-looking files with guidance to switch to `bytes`. Auto mode does not: it falls back to raw bytes automatically.

Lines longer than `tools.read_file.max_line_length` characters are cut after that many characters with a ` ... [truncated]` marker and the read continues with the next line, so a single long line cannot consume the whole byte budget. If the byte budget itself runs out mid-line, the header reports the mid-line cut instead.

Both modes scrub credential-looking values from the rendered content before it reaches the model: well-known token formats and long machine-generated high-entropy strings are replaced with `[FILTERED]`, while ordinary identifiers, prose, and hex hashes stay visible. Headers and line numbers still refer to the raw file, so reads continue normally after a masked value. See [Built-in redaction in search results, file reads, and edit diffs](../security/sensitive_data_filtering.md#built-in-redaction-in-search-results-file-reads-and-edit-diffs).

Results larger than `tools.max_inline_result_chars` are spilled to a workspace artifact with a head+tail preview, exactly like every other tool result; see [Large tool results](../reference/tools_configuration.md#large-tool-results).

## `write_file`

Creates a file with the full content in one call. Writing a new path needs no flags. When the file already exists:

- identical content is a successful no-op and the file is not rewritten;
- content that begins with the entire existing file is treated as an append: only the new suffix is written, so no data is lost and `overwrite` is not needed;
- any other replacement fails unless `overwrite=true` is passed, which replaces the entire file and discards its current contents.

The failure result keeps the human-readable "file already exists" line and adds a `structured:` JSON object with `error` (`file_exists`), `path`, `resolved_path`, `recommended_action`, `available_actions`, `existing_bytes`, `new_bytes`, and `overwrite_required`. The model can therefore pick the next call from the path and recommended action without probing the filesystem.

On success the tool returns a compact JSON object instead of a prose line (the result is silent, so only the model sees it):

| Field | Description |
|-------|-------------|
| `success` | `true` for a completed write |
| `action` | `created`, `overwritten`, `appended` (append-shaped content), or `unchanged` (identical content) |
| `resolved_path` | Absolute path the write targeted. Relative paths resolve from the workspace in restricted mode and from the process working directory otherwise |
| `bytes_written` | Bytes this call wrote: the full content for create/overwrite, only the new suffix for an append-shaped write, `0` for unchanged |
| `verified` | `true` when the file content was confirmed byte-for-byte: by a read-back after a write, or by the equality comparison that recognizes `unchanged` (no read-back is needed because nothing is written) |
| `syntax` | `ok` when the quick syntax check ran and passed; omitted for unknown extensions, known JSONC files, and skipped checks |
| `syntax_error` | Advisory problem found by the quick check; the write still succeeded |

The read-back streams the file through a fixed 32 KiB buffer, so verification does not allocate a second copy of the content; an append-shaped write seeks past the old end and compares only the suffix. For `unchanged`, the equality check that detected the no-op is the verification, so no read-back is performed. If the read-back fails or does not match, the tool returns an error JSON with `success: false`, `action`, `resolved_path`, `bytes_written`, `verified: false`, `error` (`read_back_failed` or `read_back_mismatch`), `message`, and, when the backend provides one, `detail`. `syntax` and `syntax_error` are only present on successful writes; they are not attached to errors. The `file_exists` conflict keeps its own payload documented above. The file may still be on disk, so inspect it before retrying. The check verifies the bytes the filesystem returns; `WriteFileAtomic` already fsyncs the file and its directory, so `verified` is an integrity check rather than a durability guarantee.

The syntax check is advisory and best-effort: `.json` uses the standard JSON validator, except for known JSONC files (`tsconfig*.json`, `jsconfig.json`, `devcontainer.json`, and `.vscode/*.json`) which are left unchecked, while `.go`, `.c`, `.h`, `.cc`, `.cpp`, `.cxx`, `.hpp`, and `.hh` use a delimiter/quote/comment scanner where C-family digit separators such as `1'000` are not treated as character literals, and `.py`, `.pyi`, and `.toml` additionally understand triple-quoted strings and `#` comments. It runs in O(n) time with a fixed 256-entry stack and no allocations, but it is not a compiler: extensions whose syntax needs a real parser (JavaScript, Rust, Java, YAML) are left unchecked. It is skipped when nesting exceeds the stack cap, when a C-family file uses conditional compilation (`#if`/`#else`) and its braces do not balance, or when a check bails out for another reason; a skipped check omits `syntax` instead of claiming `ok`. For an append-shaped write, the check covers the full resulting content, not only the appended suffix.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to write |
| `content` | string | yes | Full file content. Standard JSON escaping applies: `\n` for newline, `\\n` for a literal backslash-n |
| `overwrite` | boolean | no | Set to `true` to replace an existing file in full, discarding its current contents. Not needed for new files, identical content, or an append-shaped write. Default: `false` |

For targeted changes, prefer `edit_file` or `append_file`: PicoClaw advertises those alternatives, and makes them the structured `recommended_action`, only when they are enabled. Writes to protected credential paths (and to `write_file`-specific denies such as `/etc/shadow` or the workspace `sessions` directory) are refused; write allow paths do not override the policy.

## `edit_file`

Edits an existing file by replacing a single, exact occurrence of `old_text` with `new_text`, or by applying a V4A patch, and returns a unified diff of the change. Provide either `old_text` + `new_text` or `patch`, never both.

| Argument | Type | Required | Description |
|----------|------|----------|-------------|
| `path` | string | yes | Path to the file to edit |
| `old_text` | string | when `patch` is absent | Exact text to replace; must appear exactly once |
| `new_text` | string | when `patch` is absent | Replacement text |
| `patch` | string | when `old_text`/`new_text` are absent | V4A patch body for this file (see below) |

With `old_text`/`new_text`, the call fails when `old_text` is not found, or when it appears more than once (add more context to make the match unique).

The result carries the unified diff (three context lines, `a/` and `b/` paths) to both the model and the chat UI, so a follow-up turn can inspect the exact context lines the edit matched, including EOF newline transitions, without re-reading the file. The model-facing context lines pass through the same credential scrubber as `read_file`; added and removed lines and the chat preview keep the verbatim text. The diff is only built for files under 64 KiB and 2,000 lines; larger files return the one-line summary plus a note to call `read_file`. To keep a single edit from flooding the model context, the model-facing diff is bounded to 12,288 characters with a head-and-tail window and an explicit omission notice, which stays under the default `tools.max_inline_result_chars` (16 KiB) so the result is not spilled to an artifact; the user-facing preview is bounded to 16 KiB. A cut diff is reported as truncated, and `read_file` returns the full contents.

With `patch`, `edit_file` applies one or more hunks to the file. The patch may be wrapped in `*** Begin Patch` / `*** End Patch` and may include a `*** Update File: <path>` header; the header path must match `path`. Each hunk starts with `@@` (its text is informational) and uses lines prefixed with a space (context), `-` (removed), or `+` (added). `*** End of File` anchors the preceding hunk to the end of the file. Context matching starts after the previous hunk, falls back to the whole file when hunks are not in file order, and must be unique; trailing whitespace differences are tolerated, and matched context lines keep the bytes already in the file. A line containing only a single space is an empty context line; blank lines immediately before a closing directive are treated as formatting. A hunk with no context or removed lines must end with `*** End of File`, which appends its added lines at the end of the file; otherwise the call is rejected because the insertion point is ambiguous. Add, delete, move, and multi-file sections are rejected because `edit_file` edits exactly one existing file. Each matched line keeps its original LF or CRLF ending; inserted lines follow the replaced or adjacent line, so files with mixed line endings are preserved.

```text
*** Begin Patch
*** Update File: internal/server.go
@@ func Start(
 func Start(cfg Config) error {
-	 return oldServer(cfg)
+	 return newServer(cfg)
 }
*** End Patch
```

The file is rewritten in place for both modes.

## `append_file`

Appends content to the end of a file. The file is created if it does not exist, and missing parent directories are created.

The write is an in-place append (`O_APPEND`, then `fsync`), not an atomic rewrite: it keeps the existing file mode, follows symbolic links to their target, and leaves the file in place. `write_file` and `edit_file` replace the file atomically instead. The read policy runs before the append and the write policy after it, so the set of refused paths is the same as for `write_file`.

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

Content searches are bounded by a scan budget (5,000 files and 64 MiB of file content). Files are streamed line by line, so there is no per-file size limit: larger files are searched in full as long as they fit the global budget. A single line is retained up to 1 MiB; longer lines are truncated before matching, so a minified file cannot allocate the whole budget with one line. When a file no longer fits in the remaining byte budget it is skipped on its own and the walk continues with the files that still fit, instead of ending the search early. Binary detection looks only at the first 8 KiB: a NUL byte there marks the file as binary and it is skipped, while text in other encodings (Latin-1, mixed byte sequences) is still scanned. Matching runs on UTF-8, so a non-ASCII byte does not match a literal accented character. Matched and context lines are additionally scrubbed of credentials before they reach the model: well-known token formats (Bearer headers, JWTs, `sk-`/`rk-` keys, GitHub tokens, AWS key IDs, Slack and Google keys) and long machine-generated high-entropy strings are replaced with `[FILTERED]`, while ordinary identifiers, prose, and hex hashes stay visible. This redaction works without configuration and is independent of the configured sensitive-data filter.

Searches run inside the readable sandbox. On macOS, searches above your home directory skip TCC-protected folders; target one directly to include it.

Like `ripgrep`, directory walks honor `.gitignore` and `.ignore` files, including nested ones: patterns are gitignore-style (`!` negation, trailing `/` for directories, a leading or embedded `/` anchors to the declaring directory), `.ignore` overrides `.gitignore`, and a deeper file overrides a shallower one. `.git` is always skipped. An explicitly targeted file or directory (via `path`) is searched even when its ancestors are ignored.
