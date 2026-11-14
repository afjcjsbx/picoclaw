# `pkg/media`

`pkg/media` manages the lifecycle of media files associated with a processing
scope (an inbound message, a tool output, etc.).

Files live on disk; the store only keeps an in-memory mapping from a
`media://<uuid>` ref to the local path plus metadata. `Store` does **not** move
or copy the file: the path must already exist.

## Cleanup policies

Each entry carries a `CleanupPolicy`:

- `delete_on_cleanup` (default when the field is empty): the store owns the
  file and may delete it once the final ref for that path is gone.
- `forget_only`: the store only drops the mapping and never deletes the file
  (used for externally-owned files).

Any other value is rejected by `Store` with an error. Unknown policies are
**not** silently coerced to deletion, so a caller typo cannot delete a file the
store does not own.

When the same path is stored under both policies, the store is conservative:
once any ref borrows the path, the lifecycle never auto-deletes it.

## Scope lifecycle

Channels register inbound media under a scope built by
`channels.BuildMediaScope(channel, chatID, messageID)`, which is unique per
message (a random id is used when the message id is empty).

The agent releases the scope with `ReleaseAll(scope)` from its worker goroutine
**after** the turn that consumes the media completes. Releasing earlier (for
example in the dispatch loop) would delete the files before the LLM reads them.

## TTL cleanup

`NewFileMediaStoreWithCleanup` enables an optional background cleaner
(`Start`/`Stop`). Expiry is **idle-based**: `Resolve` and `ResolveWithMeta`
refresh the entry's last-access time, so a file that is still being read is not
removed by `CleanExpired` even if it was stored longer ago than `MaxAge`.
Reader pins protect inbound turns and outbound delivery from expiration, including
long retries. Failed file removals remain queued for subsequent cleanup.

## Concurrency

`FileMediaStore` is safe for concurrent use. Registration and deletion share
the store lock so cleanup cannot delete a path while it is being re-registered.
Shared paths are reference-counted and pinned during active reads.

## Temp directory

`TempDir()` returns a private directory beneath `<PICOCLAW_HOME>/tmp/media`
(default home: `~/.picoclaw`) and an error if storage cannot be prepared.
Channels and tools use `CreateTemp` or `WriteTemp` to create media exclusively
with owner-only permissions before registering it. System `TMPDIR` and the
working directory do not control this location.

Each process holds an OS lock on its own `tmp/media/run-*` directory. On the
next start, storage recovery removes abandoned run directories only when their
lock can be acquired. It skips live processes, unknown files, and symlinks.
This recovers files left before registration or after crashes without relying
on PID reuse or file age. `tmp/work/run-*` uses the same recovery mechanism.
Files intentionally retained for later turns belong in workspace artifacts,
not these disposable run directories.
