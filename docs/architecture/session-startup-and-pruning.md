# Session Startup Cost and Automatic Pruning

> Back to [README](../README.md) · Related: [Session System](session-system.md)

## Symptom

After a restart, the launcher web UI is reachable within a second, but the
gateway does not accept chat connections for a long time — on slow boards
(single-core RISC-V, eMMC/SD storage) it can stay unresponsive for **several
minutes**. The launcher already reports the gateway as `running` (the process
and its PID file exist), so the UI keeps retrying until the Pico channel comes
up.

## Cause

When the default context manager is `seahorse`, `newSeahorseContextManager`
(`pkg/agent/context_seahorse.go`) does the following **synchronously during
gateway startup, before the HTTP/health server is ready**:

1. Opens (and, if needed, migrates) the SQLite database at
   `<workspace>/sessions/seahorse.db`. The schema uses FTS5 tables with the
   `trigram` tokenizer.
2. Bootstraps **every known session** into SQLite:

   ```go
   for _, sessionKey := range agent.Sessions.ListSessions() {
       mgr.bootstrapSession(ctx, sessionKey)
   }
   ```

   Each bootstrap reads the session's append-only JSONL history, reconciles it
   with the rows already in SQLite, and writes the delta back (message rows,
   message parts and FTS index entries).

The startup cost therefore grows with:

- the **number of sessions**,
- the **size of each session's history** (tool output makes JSONL lines large),
- and the **database size / FTS maintenance**.

On a single-core board with a few dozen sessions, tens of MB of JSONL and a
tens-of-MB `seahorse.db`, this easily takes minutes. During that time the
process is CPU-bound and the gateway logs are silent, so the only visible
effect is the delayed readiness.

### How it looks in the logs

`~/.picoclaw/logs/gateway.log` shows a gap between the last per-agent tool
registration and the shared tools / readiness messages:

```
18:12:14  wrote pid file ... / Registered core tool (short_expand)
18:12:18  ... (no log output) ...
18:15:53  web_search registered / Agent initialized
18:15:55  Shared HTTP server listening      <- /ready becomes true
```

The gap between `short_expand` and `web_search` is the seahorse bootstrap.

### Why the reconciliation bootstrap exists

`seahorseContextManager` treats the JSONL session store (`agent.Sessions`) as
the **canonical source of conversation history** and the SQLite database
(`sessions/seahorse.db`) as an **FTS5 index / retrieval cache**. The two can
diverge — for example after a crash, manual edits to the JSONL files, or a
downgrade/migration — so at startup the manager reconciles every known session:

```go
for _, sessionKey := range agent.Sessions.ListSessions() {
    mgr.bootstrapSession(ctx, sessionKey)
}
```

`Engine.Bootstrap` is **incremental**:

- If the messages already stored in SQLite match the JSONL history, it is a
  no-op.
- If the JSONL has a longer tail, it ingests **only the delta**.
- It rebuilds a session from scratch only when a mismatch is detected (for
  example an edited or reordered history).

Running this reconciliation at startup — rather than lazily on the first
`Ingest` — also prevents duplicate messages: a session that is written to disk
but not yet indexed is brought back in sync before any new turn can append to
it. The trade-off is that the work is proportional to the total number of known
sessions and the size of their histories, which is exactly why startup slows
down as history grows and why pruning matters.

### How to measure it on a board

```sh
# history size and session count
du -sh ~/.picoclaw/workspace/sessions
ls ~/.picoclaw/workspace/sessions/*.jsonl | wc -l

# database size
ls -la ~/.picoclaw/workspace/sessions/seahorse.db*

# time until the gateway is actually ready after a restart
time while ! wget -qO- http://127.0.0.1:18791/ready | grep -q ready; do sleep 1; done
```

## Automatic pruning

PicoClaw can prune old sessions automatically. Pruning removes the session's
JSONL files and its seahorse data, then reclaims disk space, which keeps both
the startup bootstrap and the database bounded.

Pruning is **opt-in** (`enabled: false` by default) because it deletes data.

### Configuration

```json
"session": {
  "prune": {
    "enabled": true,
    "max_age_days": 30,
    "max_sessions": 100,
    "max_db_size_mb": 512,
    "check_interval_minutes": 60,
    "vacuum": true
  }
}
```

| Field | Meaning |
| --- | --- |
| `enabled` | Master switch. When `false`, nothing is ever deleted. |
| `max_age_days` | Delete sessions whose last activity is older than this. `0` disables the check. |
| `max_sessions` | Keep only the most recently active N sessions. `0` disables the check. |
| `max_db_size_mb` | Safety net: when `seahorse.db` (+ WAL) exceeds this, delete the oldest remaining sessions until it fits (the most recent session is always kept, so a limit below the minimum database size cannot wipe everything). `0` disables the check. |
| `check_interval_minutes` | How often the background prune pass runs. Defaults to `60`. |
| `vacuum` | Give freed space back to the filesystem after a pass that deleted sessions. Default `true`. See [Disk space and VACUUM](#disk-space-and-vacuum). |

Environment overrides are available with the `PICOCLAW_SESSION_PRUNE_*`
prefix (for example `PICOCLAW_SESSION_PRUNE_MAX_AGE_DAYS`).

The prune settings are read once at startup. Changing them (or the matching
environment variables) takes effect after a gateway restart.

### Recommended values

For an always-on small device the most effective and predictable combination
is **age + session count** as the primary triggers, with the database size as
a guard:

```json
{
  "max_age_days": 30,
  "max_sessions": 100,
  "max_db_size_mb": 512
}
```

- `max_age_days` bounds staleness without touching active chats.
- `max_sessions` bounds the startup bootstrap directly (its cost is roughly
  proportional to the number of sessions).
- `max_db_size_mb` protects the disk if a few sessions grow unusually large;
  it is a coarse guard because SQLite only frees file space after a vacuum.

### Semantics

- A session's **last activity** is the newest of its last seahorse message time
  and the last write recorded by its JSONL store. The JSONL store is the
  canonical history and the seahorse database only an index that can lag behind
  it, so a session that was written recently but not yet indexed (or never
  indexed) is not mistaken for a stale one.
- Sessions are always deleted **oldest-first** by last activity.
- The **most recently active session is never deleted**, whatever the
  thresholds say (for example when every session is older than `max_age_days`
  after a long idle period, or when `max_db_size_mb` is smaller than the
  smallest possible database).
- A session with **no usable timestamp is never deleted**. It still counts
  toward `max_sessions`, but the sessions dropped to honor the cap are always the
  oldest dated ones.
- **Every agent's session store is pruned.** The seahorse database is shared by
  all agents while each agent keeps its own JSONL store; a session is removed
  from the database and from the store that lists it. If any agent's store
  cannot delete sessions, pruning is skipped entirely and a warning is logged,
  because removing only the database side would not shrink anything (the
  startup bootstrap re-imports the JSONL history) and would drop the retrieval
  context of those sessions.
- A prune pass runs once during startup **before** the seahorse bootstrap, so
  a device that has grown too large recovers on the next restart. It then
  repeats every `check_interval_minutes` while the gateway runs, and stops
  cleanly when the gateway shuts down.
- `max_sessions` counts every session, including those created by cron jobs
  and subagents, not only interactive chats. On a busy device a small cap can
  therefore evict real chats in favor of recent automated sessions.

### Clock requirements

`max_age_days` compares message timestamps with the system clock. Boards
without a battery-backed RTC boot with the clock at the epoch (or a stale build
date) until NTP syncs, so:

- timestamps earlier than **2024-01-01** are ignored (the session is treated as
  having no timestamp and is left alone), and
- age-based pruning is skipped altogether while the current clock reads earlier
  than that.

A clock that is wrong but still after 2024 cannot be detected: messages written
while the clock lagged will look older than they are once it is corrected. Keep
NTP working on such devices, or rely on `max_sessions` instead of
`max_age_days`. The "most recent session is never deleted" rule limits the
damage in the worst case.

### Disk space and VACUUM

Deleting rows frees pages inside `seahorse.db` but does not shrink the file;
only `VACUUM` does, by rewriting the whole database. Keep in mind on small
devices:

- `VACUUM` temporarily needs free disk space about the size of the database.
- It **blocks writers** while it runs, which can be long on slow storage. Live
  chat messages wait for it (up to the 5 second SQLite busy timeout, which now
  applies to every database connection) and can fail to be indexed if it takes
  longer.
- The first pass at startup is **synchronous**: it runs before the gateway is
  ready, so a large `VACUUM` adds to the startup time the pruning is meant to
  reduce.

To limit this, a pass vacuums only when it would give back at least 1 MiB **and**
at least a tenth of the file; otherwise it just truncates the WAL. Free pages
inside the file are reused by later writes, so the file does not keep growing.
The `max_db_size_mb` guard always vacuums, because it needs the file to shrink
to observe progress. Set `vacuum` to `false` to never vacuum; the size guard
then stops as soon as a pass makes no progress, rather than deleting everything.

## Limitations and future fix

Pruning bounds growth but does not remove the startup cost entirely: with many
small sessions the bootstrap still scales with their count. The structural fix
is to bootstrap a session **lazily** (on first use) instead of iterating all
sessions at startup. Until that lands, pruning is the available mitigation.
