#!/usr/bin/env python3
"""Shared helpers for the PicoClaw runtime benchmark scripts.

Standard library only; no third-party dependencies.
"""
import subprocess


def process_tree(pid):
    """Return the pids in the process tree rooted at pid (inclusive)."""
    try:
        out = subprocess.check_output(["ps", "-axo", "pid=,ppid="], text=True, timeout=8)
    except Exception:
        return [pid]
    children = {}
    for line in out.splitlines():
        fields = line.split()
        if len(fields) != 2:
            continue
        children.setdefault(int(fields[1]), []).append(int(fields[0]))
    seen, stack = [], [pid]
    while stack:
        cur = stack.pop()
        if cur in seen:
            continue
        seen.append(cur)
        stack.extend(children.get(cur, []))
    return seen


def phys_footprint_bytes(pid):
    """Return macOS phys_footprint bytes for pid, or None elsewhere."""
    try:
        out = subprocess.check_output(["footprint", "-p", str(pid)], text=True,
                                      stderr=subprocess.DEVNULL, timeout=20)
    except Exception:
        return None
    for line in out.splitlines():
        line = line.strip()
        if not line.startswith("phys_footprint:"):
            continue
        tokens = line.split(":", 1)[1].strip().split()
        if not tokens:
            continue
        try:
            value = float(tokens[0])
            unit = tokens[1].upper() if len(tokens) > 1 else "MB"
        except ValueError:
            continue
        mult = {"B": 1, "KB": 1024, "MB": 1024 ** 2, "GB": 1024 ** 3}.get(unit)
        if mult:
            return int(value * mult)
    return None
