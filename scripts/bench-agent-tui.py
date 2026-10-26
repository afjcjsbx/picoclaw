#!/usr/bin/env python3
"""PTY benchmark harness for interactive terminal AI agents.

Drives a TUI in a pseudo-terminal of a fixed size (default 200x50) and records:
  - install/exec info is measured separately by the caller
  - first_byte_ms   : spawn -> first byte of output
  - first_paint_ms  : spawn -> first substantial frame (cumulative output > paint_bytes)
  - settled_ms      : spawn -> UI stopped changing (no output for quiet_ms after paint)
  - idle_rss_kib    : resident set after settle + extra settle
  - idle_footprint  : macOS phys_footprint (empty elsewhere)
  - processes       : number of OS processes in the tree (main + descendants)

Portable: RSS/tree use `ps`; phys_footprint is macOS-only and omitted elsewhere.
Standard library only; no third-party dependencies.

Example:
  python3 scripts/bench-agent-tui.py --cmd "opencode" --runs 3 \\
      --keys '\\x03' --out /tmp/opencode.json
"""
import argparse
import fcntl
import json
import os
import select
import signal
import struct
import subprocess
import sys
import termios
import threading
import time

from bench_common import phys_footprint_bytes, process_tree


DEBUG = os.environ.get("TUI_BENCH_DEBUG") == "1"


def dprint(*a):
    if DEBUG:
        print("[dbg]", *a, file=sys.stderr, flush=True)


def set_winsize(fd, rows, cols):
    fcntl.ioctl(fd, termios.TIOCSWINSZ, struct.pack("HHHH", rows, cols, 0, 0))


def ps_field(pid, field):
    try:
        out = subprocess.check_output(["ps", "-o", f"{field}=", "-p", str(pid)], text=True,
                                      timeout=5).strip()
        return out or None
    except Exception:
        return None


def run(cmd, cwd, env, rows, cols, paint_bytes, quiet_ms, settle, max_s, keys,
        prekeys=(), prekeys_delay=2.5):
    master, slave = os.openpty()
    os.set_blocking(master, False)
    set_winsize(slave, rows, cols)
    t0 = time.perf_counter()
    proc = subprocess.Popen(cmd, cwd=cwd, env=env, stdin=slave, stdout=slave,
                            stderr=slave, preexec_fn=os.setsid, close_fds=True)
    os.close(slave)

    chunks = []          # (t, nbytes)
    raw = []
    total = [0]
    stop = threading.Event()
    first_byte = [None]
    first_paint = [None]
    last_out = [t0]

    def reader():
        while not stop.is_set():
            try:
                r, _, _ = select.select([master], [], [], 0.05)
            except Exception:
                break
            if master in r:
                try:
                    data = os.read(master, 65536)
                except BlockingIOError:
                    continue
                except OSError:
                    break
                if not data:
                    break
                now = time.perf_counter()
                total[0] += len(data)
                chunks.append((now, len(data)))
                raw.append(data)
                last_out[0] = now
                if first_byte[0] is None:
                    first_byte[0] = now
                if first_paint[0] is None and total[0] >= paint_bytes:
                    first_paint[0] = now

    th = threading.Thread(target=reader, daemon=True)
    th.start()

    settled = [None]
    peak_rss = [0]
    prekeys_sent = False
    deadline = t0 + max_s
    while time.perf_counter() < deadline:
        if proc.poll() is not None:
            break
        if prekeys and not prekeys_sent and (time.perf_counter() - t0) >= prekeys_delay:
            for k in prekeys:
                try:
                    os.write(master, k)
                    time.sleep(0.05)
                except OSError:
                    pass
            prekeys_sent = True
            last_out[0] = time.perf_counter()  # restart the quiet timer
        if first_paint[0] is not None and settled[0] is None:
            if (time.perf_counter() - last_out[0]) * 1000 >= quiet_ms:
                settled[0] = last_out[0]
        r = ps_field(proc.pid, "rss")
        if r:
            peak_rss[0] = max(peak_rss[0], int(r))
        time.sleep(0.02)

    exited_early = proc.poll() is not None
    dprint("loop done, exited_early=", exited_early)
    # let it idle a bit more before sampling memory
    if not exited_early:
        time.sleep(settle)
    rss = ps_field(proc.pid, "rss")
    dprint("rss=", rss)
    footprint = phys_footprint_bytes(proc.pid) if not exited_early else None
    dprint("footprint=", footprint)
    tree = process_tree(proc.pid) if not exited_early else []
    dprint("tree=", len(tree))
    nproc = len(tree)
    desc = max(0, nproc - 1)

    # capture any trailing output, then shut down
    stop.set()
    dprint("writing keys")
    for k in keys:
        try:
            os.write(master, k)
            time.sleep(0.1)
        except OSError:
            break

    # Bounded shutdown: never block indefinitely on a stubborn child.
    def shutdown():
        if proc.poll() is not None:
            return
        for sig in (signal.SIGTERM, signal.SIGKILL, signal.SIGKILL):
            try:
                os.killpg(os.getpgid(proc.pid), sig)
            except Exception:
                try:
                    os.kill(proc.pid, sig)
                except Exception:
                    pass
            try:
                proc.wait(timeout=4)
                return
            except subprocess.TimeoutExpired:
                continue
        dprint("WARNING: child did not reap; leaving it")

    dprint("waiting")
    shutdown()
    dprint("done waiting")
    try:
        os.close(master)
    except OSError:
        pass
    th.join(timeout=1)

    def ms(t):
        return round((t - t0) * 1000, 1) if t else None

    return {
        "cmd": cmd,
        "exited_early": exited_early,
        "exit_code": proc.returncode,
        "first_byte_ms": ms(first_byte[0]),
        "first_paint_ms": ms(first_paint[0]),
        "settled_ms": ms(settled[0]),
        "idle_rss_kib": int(rss) if rss else None,
        "idle_footprint_bytes": footprint,
        "processes": nproc,
        "descendants": desc,
        "peak_rss_kib": peak_rss[0] or None,
        "output_bytes": total[0],
        "_raw": b"".join(raw),
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cmd", required=True, help="shell-free command line; use -- to separate")
    ap.add_argument("--cwd", default=os.getcwd())
    ap.add_argument("--rows", type=int, default=50)
    ap.add_argument("--cols", type=int, default=200)
    ap.add_argument("--paint-bytes", type=int, default=256)
    ap.add_argument("--quiet-ms", type=int, default=400)
    ap.add_argument("--settle", type=float, default=3.0)
    ap.add_argument("--max-s", type=float, default=30.0)
    ap.add_argument("--keys", default="q")
    ap.add_argument("--prekeys", default="", help="comma-separated escape sequences sent after --prekeys-delay s")
    ap.add_argument("--prekeys-delay", type=float, default=2.5)
    ap.add_argument("--out", default=None)
    ap.add_argument("--term", default="xterm-256color")
    ap.add_argument("--runs", type=int, default=1)
    ap.add_argument("--dump", default=None, help="write raw + ANSI-stripped screen output here")
    args = ap.parse_args()

    env = dict(os.environ)
    env.setdefault("TERM", args.term)
    env.setdefault("COLORTERM", "truecolor")

    keys = [k.encode().decode("unicode_escape").encode("latin1") for k in args.keys.split(",")]
    prekeys = [k.encode().decode("unicode_escape").encode("latin1")
               for k in args.prekeys.split(",") if k]

    results = []
    for i in range(args.runs):
        r = run(args.cmd.split(), args.cwd, env, args.rows, args.cols,
                args.paint_bytes, args.quiet_ms, args.settle, args.max_s, keys,
                prekeys, args.prekeys_delay)
        raw = r.pop("_raw", b"")
        if args.dump:
            base = args.dump if args.runs == 1 else f"{args.dump}.{i}"
            with open(base + ".raw", "wb") as f:
                f.write(raw)
            import re
            text = re.sub(rb"\x1b\[[0-9;?]*[A-Za-z]", b"", raw)
            text = re.sub(rb"\x1b\][^\x07]*\x07", b"", text)
            text = re.sub(rb"\x1b[()][A-Z0-9]", b"", text)
            with open(base + ".txt", "wb") as f:
                f.write(text)
        results.append(r)
        print(json.dumps(r), flush=True)

    if args.out:
        with open(args.out, "w") as f:
            json.dump(results if args.runs > 1 else results[0], f, indent=2)


if __name__ == "__main__":
    main()
