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

## Concurrency

`FileMediaStore` is safe for concurrent use. Deletion is two-phase: mappings are
removed under the lock, then files are deleted without holding it. Shared paths
are reference-counted so the file is removed only when the last owning ref is
released or expires.

## Temp directory

`TempDir()` returns `os.TempDir()/picoclaw_media`, the shared location where
channels and tools are expected to place downloaded or generated media before
registering it.
