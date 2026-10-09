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
| `max_db_size_mb` | Safety net: when `seahorse.db` (+ WAL) exceeds this, delete the oldest remaining sessions until it fits. `0` disables the check. |
| `check_interval_minutes` | How often the background prune pass runs. Defaults to `60`. |
| `vacuum` | Run SQLite `VACUUM` + WAL checkpoint after a pass that deleted sessions. Default `true`. |

Environment overrides are available with the `PICOCLAW_SESSION_PRUNE_*`
prefix (for example `PICOCLAW_SESSION_PRUNE_MAX_AGE_DAYS`).

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

- Sessions are always deleted **oldest-first** (by last message activity).
- Age-based deletion uses the last message timestamp. Sessions with no known
  activity are never age-deleted; the session-count cap may still drop them.
- A prune pass runs once during startup **before** the seahorse bootstrap, so
  a device that has grown too large recovers on the next restart. It then
  repeats every `check_interval_minutes` while the gateway runs.
- Deletion is applied to both the seahorse database and the JSONL session
  files; the conversation rows are removed so no orphans accumulate.

## Limitations and future fix

Pruning bounds growth but does not remove the startup cost entirely: with many
small sessions the bootstrap still scales with their count. The structural fix
is to bootstrap a session **lazily** (on first use) instead of iterating all
sessions at startup. Until that lands, pruning is the available mitigation.
