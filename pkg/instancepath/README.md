# Instance runtime storage

The root is `$PICOCLAW_HOME`, defaulting to `~/.picoclaw`. Storage creation fails
if the home cannot be resolved; it never falls back to cwd or system temporary
storage. Managed directories reject symlinks and, on Unix, foreign ownership.
Directory permissions are tightened to `0700`, files are created with `0600`.
Windows access control also depends on the instance home's inherited ACLs.

| Location under the instance home | Purpose | Lifetime |
| --- | --- | --- |
| `tmp/media/run-*` | Downloaded attachments and generated media | Media scopes and idle TTL; abandoned runs recovered on next use |
| `tmp/work/run-*` | Downloads, recording, extraction, validation | Caller removes files/directories after each operation; abandoned runs recovered on next use |
| `artifacts/tool` | Tool output when workspace artifacts cannot be written | Artifact retention policy |
| `history` | CLI input history | Persistent |
| `wecom/reqid-store.json` | WeCom reply routing state | Persistent, expiring routes |
| `runtime-user-env/tmp` | Isolated subprocess scratch | Existing sandbox boundary |
| `runtime-user-env/tmux` | Default bundled tmux socket directory | Explicit tmux server lifetime |

Each process uses unique run directories and holds their OS locks until exit.
Recovery only deletes published run directories whose lock can be acquired;
it does not delete a live run based on age, PID, or heartbeat. Unknown files,
symlinks, and incomplete `.init-*` directories are left alone. The latter can
remain after a crash during the short interval before a new run is published.

Tools receive read access to their process's media directory and the instance's
fallback tool artifacts, not the whole instance home or scratch tree.

Persistent uploads remain in workspace `.artifacts/uploads`. Atomic writes and
skill activation continue staging on the destination filesystem. Standalone
installation and benchmark scripts may still use their own system scratch.

Changing the working directory or `TMPDIR` does not select PicoClaw storage.
Use a separate `PICOCLAW_HOME` for each independent profile. Existing global
temporary directories are not adopted, chmodded, or removed automatically.

## Storage budget

Host-managed temporary files share a 512 MiB write budget per process and
instance home across `tmp/media/run-*` and `tmp/work/run-*`. Set
`PICOCLAW_TMP_MAX_BYTES` to a positive byte count to change it. Streaming writes
are bounded; when space is unavailable the operation fails and removes partial
output rather than deleting active files. Deleted files free space for later
writes. The limit excludes retained artifacts and persistent state, which have
their own lifetimes. It is not an OS filesystem quota: external subprocesses
and archive extractors writing directly to paths need their own size limits.
