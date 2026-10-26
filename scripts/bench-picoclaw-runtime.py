#!/usr/bin/env python3
"""Reproducible runtime benchmark for the PicoClaw core binary (CLI/gateway).

Measures, per cold start of `picoclaw gateway --allow-empty`:
  - startup: wall time from exec() to the /ready endpoint answering 200
  - peak RSS: max resident set observed while starting
  - idle RSS: resident set after the process has been idle for `--settle` s
  - phys_footprint: macOS "memory footprint" (what Activity Monitor shows)
  - cpu: cumulative CPU time when /ready answered and after settle
  - processes: number of OS processes in the gateway tree (main + descendants)

Portable: RSS/CPU come from `ps`, so Linux and macOS both work. The
phys_footprint field is macOS-only (uses `footprint(1)`) and is simply omitted
elsewhere. Standard library only; no third-party dependencies.

Example:
  CGO_ENABLED=0 go build -tags 'goolm,stdjson' -ldflags '-s -w' -o /tmp/picoclaw ./cmd/picoclaw
  python3 scripts/bench-picoclaw-runtime.py --binary /tmp/picoclaw --runs 15
"""
import argparse
import json
import os
import shutil
import socket
import statistics
import subprocess
import sys
import tempfile
import threading
import time
import urllib.request

from bench_common import phys_footprint_bytes, process_tree

BIN = os.environ.get("PICOCLAW_BIN", "")


def free_port():
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    p = s.getsockname()[1]
    s.close()
    return p


def ps_rss_kib(pid):
    try:
        out = subprocess.check_output(["ps", "-o", "rss=", "-p", str(pid)], text=True).strip()
        return int(out) if out else None
    except Exception:
        return None


def ps_vsz_kib(pid):
    try:
        out = subprocess.check_output(["ps", "-o", "vsz=", "-p", str(pid)], text=True).strip()
        return int(out) if out else None
    except Exception:
        return None


def ps_cputime_seconds(pid):
    """Cumulative CPU time (user+sys) as reported by ps."""
    try:
        out = subprocess.check_output(["ps", "-o", "time=", "-p", str(pid)], text=True).strip()
    except Exception:
        return None
    if not out:
        return None
    # formats: MM:SS or HH:MM:SS or MM:SS.ss
    parts = [float(x) for x in out.split(":")]
    secs = 0.0
    for p in parts:
        secs = secs * 60 + p
    return secs


def descendants_count(pid):
    return max(0, len(process_tree(pid)) - 1)


def run_once(settle, long_idle=0.0):
    port = free_port()
    d = tempfile.mkdtemp(prefix="pcbench-")
    keep = False
    log = None
    try:
        cfg = os.path.join(d, "config.json")
        with open(cfg, "w") as f:
            json.dump({"version": 1, "gateway": {"host": "127.0.0.1", "port": port, "log_level": "error"}}, f)
        env = dict(os.environ, PICOCLAW_HOME=os.path.join(d, "home"), PICOCLAW_CONFIG=cfg)
        log = open(os.path.join(d, "gw.log"), "w")

        peak = {"rss": 0}
        stop = threading.Event()

        t0 = time.perf_counter()
        proc = subprocess.Popen([BIN, "--no-color", "gateway", "--allow-empty"],
                                env=env, stdout=log, stderr=subprocess.STDOUT)
        pid = proc.pid

        def sampler():
            while not stop.is_set():
                r = ps_rss_kib(pid)
                if r:
                    peak["rss"] = max(peak["rss"], r)
                time.sleep(0.005)

        th = threading.Thread(target=sampler, daemon=True)
        th.start()

        url = f"http://127.0.0.1:{port}/ready"
        ready = False
        deadline = t0 + 60
        while time.perf_counter() < deadline:
            if proc.poll() is not None:
                break
            try:
                with urllib.request.urlopen(url, timeout=0.5) as resp:
                    if resp.status == 200:
                        ready = True
                        break
            except Exception:
                pass
            time.sleep(0.002)
        t_ready = time.perf_counter()

        if not ready:
            stop.set()
            proc.kill()
            proc.wait()
            keep = True
            raise RuntimeError("gateway did not become ready; see " + os.path.join(d, "gw.log"))

        cpu_ready = ps_cputime_seconds(pid)
        rss_ready = ps_rss_kib(pid)
        time.sleep(settle)
        cpu_settled = ps_cputime_seconds(pid)
        rss_idle = ps_rss_kib(pid)
        vsz_idle = ps_vsz_kib(pid)
        fp = phys_footprint_bytes(pid)
        nproc = len(process_tree(pid))
        ndesc = descendants_count(pid)

        if long_idle:
            time.sleep(long_idle)
            rss_long = ps_rss_kib(pid)
            cpu_long = ps_cputime_seconds(pid)
        else:
            rss_long = cpu_long = None

        stop.set()
        proc.terminate()
        try:
            proc.wait(timeout=10)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()

        return {
            "startup_ms": (t_ready - t0) * 1000.0,
            "peak_rss_kib": peak["rss"],
            "rss_ready_kib": rss_ready,
            "rss_idle_kib": rss_idle,
            "vsz_idle_kib": vsz_idle,
            "footprint_idle_bytes": fp,
            "cpu_ready_s": cpu_ready,
            "cpu_settled_s": cpu_settled,
            "rss_long_kib": rss_long,
            "cpu_long_s": cpu_long,
            "processes": nproc,
            "descendants": ndesc,
        }
    finally:
        if log is not None:
            log.close()
        if not keep:
            shutil.rmtree(d, ignore_errors=True)


def cli_coldstart(n, args):
    """Wall time for a short-lived non-interactive CLI invocation (exec -> exit)."""
    samples = []
    for _ in range(n):
        env = dict(os.environ, PICOCLAW_HOME="/tmp/pc-bench/home")
        t0 = time.perf_counter()
        subprocess.run([BIN, "--no-color", *args], env=env,
                       stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        samples.append((time.perf_counter() - t0) * 1000.0)
    return samples


def stats(xs):
    xs = [x for x in xs if x is not None]
    if not xs:
        return None
    return {
        "n": len(xs),
        "median": statistics.median(xs),
        "mean": statistics.mean(xs),
        "stdev": statistics.stdev(xs) if len(xs) > 1 else 0.0,
        "min": min(xs),
        "max": max(xs),
    }


def main():
    global BIN
    ap = argparse.ArgumentParser()
    ap.add_argument("--runs", type=int, default=12)
    ap.add_argument("--settle", type=float, default=2.0)
    ap.add_argument("--long-idle", type=float, default=8.0)
    ap.add_argument("--binary", default=BIN,
                    help="path to the picoclaw binary to benchmark (required)")
    ap.add_argument("--out", default=None)
    ap.add_argument("--version-args", default="version")
    args = ap.parse_args()

    BIN = args.binary
    if not BIN:
        ap.error("--binary is required (or set PICOCLAW_BIN)")
    if not (os.path.isfile(BIN) and os.access(BIN, os.X_OK)):
        ap.error(f"--binary {BIN!r} is not an executable file")

    # warm-up (excluded)
    run_once(args.settle, 0.0)

    rows = []
    for i in range(args.runs):
        r = run_once(args.settle, args.long_idle if i == 0 else 0.0)
        rows.append(r)
        print(f"run {i+1}/{args.runs}: startup={r['startup_ms']:.1f}ms "
              f"peakRSS={r['peak_rss_kib']/1024:.1f}MiB idleRSS={r['rss_idle_kib']/1024:.1f}MiB "
              f"procs={r['processes']}", flush=True)

    cold = cli_coldstart(5, args.version_args.split())

    summary = {
        "binary": BIN,
        "binary_size_bytes": os.path.getsize(BIN),
        "startup_ms": stats([r["startup_ms"] for r in rows]),
        "peak_rss_kib": stats([r["peak_rss_kib"] for r in rows]),
        "rss_idle_kib": stats([r["rss_idle_kib"] for r in rows]),
        "vsz_idle_kib": stats([r["vsz_idle_kib"] for r in rows]),
        "footprint_idle_bytes": stats([r["footprint_idle_bytes"] for r in rows]),
        "cpu_ready_s": stats([r["cpu_ready_s"] for r in rows]),
        "cpu_settled_s": stats([r["cpu_settled_s"] for r in rows]),
        "processes": stats([r["processes"] for r in rows]),
        "descendants": stats([r["descendants"] for r in rows]),
        "cli_coldstart_ms": stats(cold),
        "long_idle_sample": next(({k: r[k] for k in ("rss_long_kib", "cpu_long_s")} for r in rows if r["rss_long_kib"]), None),
        "long_idle_seconds": args.long_idle,
    }
    print(json.dumps(summary, indent=2))
    if args.out:
        with open(args.out, "w") as f:
            json.dump(summary, f, indent=2)


if __name__ == "__main__":
    main()
