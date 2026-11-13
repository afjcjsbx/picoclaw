#!/usr/bin/env python3
"""A/B chaos benchmark: read_file bytes mode vs auto mode.

Runs the real Picoclaw CLI against a real OpenCode Go model (glm-5.3-flash),
through a local logging proxy, against identical deterministic task workspaces.
The baseline binary is built from origin/main and runs with read_file mode
"bytes"; the candidate binary is built from the branch under test and runs
with read_file mode "auto".

Usage:
  python3 run.py --baseline-bin /path/to/old --candidate-bin /path/to/new \
      --reps 3 --label read-file-chaos-<stamp>
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import re
import shutil
import signal
import socket
import subprocess
import sys
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path

HERE = Path(__file__).resolve().parent
REPO = Path("/Users/afjcjsbx/GolandProjects/picoclaw")
PROXY = REPO / "benchmarks" / "agent-comparison" / "proxy.py"
API_KEY = os.environ.get("OPENCODE_GO_API_KEY", "")
MODEL = "glm-5.3-flash"

from rf_tasks import TASKS, TASK_INDEX  # noqa: E402

IGNORE_DIRS = {".artifacts", "sessions", "state", "logs", "memory", ".picoclaw", "__pycache__"}


def utc_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_proxy(port: int, proc: subprocess.Popen, timeout: float = 20.0) -> None:
    deadline = time.time() + timeout
    while time.time() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"proxy exited early with {proc.returncode}")
        try:
            with urllib.request.urlopen(f"http://127.0.0.1:{port}/health", timeout=0.5) as resp:
                if resp.status == 200:
                    return
        except Exception:  # noqa: BLE001
            time.sleep(0.1)
    raise RuntimeError("proxy did not become ready")


def kill_port(port: int) -> None:
    """Kill any listener on a benchmark port so runs cannot contaminate each other."""
    for sig in (signal.SIGTERM, signal.SIGKILL):
        try:
            out = subprocess.check_output(["lsof", "-ti", f":{port}"], text=True)
        except Exception:  # noqa: BLE001
            return
        pids = [int(pid) for pid in out.split() if pid.strip().isdigit()]
        if not pids:
            return
        for pid in pids:
            try:
                os.kill(pid, sig)
            except Exception:  # noqa: BLE001
                pass
        time.sleep(0.2)


def stop_process_tree(proc: subprocess.Popen) -> None:
    if proc.poll() is not None:
        return
    try:
        os.killpg(os.getpgid(proc.pid), signal.SIGTERM)
    except Exception:  # noqa: BLE001
        try:
            proc.terminate()
        except Exception:  # noqa: BLE001
            pass
    try:
        proc.wait(timeout=10)
    except subprocess.TimeoutExpired:
        try:
            os.killpg(os.getpgid(proc.pid), signal.SIGKILL)
        except Exception:  # noqa: BLE001
            try:
                proc.kill()
            except Exception:  # noqa: BLE001
                pass


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def tree_hashes(root: Path) -> dict[str, str]:
    out = {}
    for path in sorted(root.rglob("*")):
        rel = path.relative_to(root)
        if any(part in IGNORE_DIRS for part in rel.parts):
            continue
        if path.is_file():
            out[str(rel)] = sha256(path)
    return out


def pico_config(workspace: Path, port: int, max_steps: int, read_mode: str) -> dict:
    return {
        "version": 3,
        "agents": {
            "defaults": {
                "workspace": str(workspace),
                "restrict_to_workspace": True,
                "model_name": "default",
                "max_tokens": 4096,
                "context_window": 131072,
                "max_tool_iterations": max_steps,
                "temperature": 0,
                "memory": {"memory_enabled": False, "daily_notes_enabled": False},
            }
        },
        "model_list": [
            {
                "model_name": "default",
                "provider": "opencode-go",
                "model": MODEL,
                "enabled": True,
                "api_keys": [API_KEY],
                "api_base": f"http://127.0.0.1:{port}",
                "request_timeout": 600,
                "streaming": {"enabled": True},
                "extra_body": {"reasoning_effort": "minimal"},
            }
        ],
        "channel_list": {},
        "tools": {
            "exec": {"enabled": True, "allow_remote": False},
            "read_file": {"enabled": True, "mode": read_mode},
            "write_file": {"enabled": True},
            "append_file": {"enabled": True},
            "edit_file": {"enabled": True},
            "list_dir": {"enabled": True},
            "search_files": {"enabled": True},
            "todo": {"enabled": False},
            "skills": {"enabled": False},
            "find_skills": {"enabled": False},
            "install_skill": {"enabled": False},
            "subagent": {"enabled": False},
            "spawn": {"enabled": False},
            "spawn_status": {"enabled": False},
            "message": {"enabled": False},
            "send_file": {"enabled": False},
            "send_tts": {"enabled": False},
            "cron": {"enabled": False},
            "mcp": {"enabled": False},
            "load_image": {"enabled": False},
            "image_generate": {"enabled": False},
            "web": {"enabled": False},
            "web_fetch": {"enabled": False},
        },
        "gateway": {"host": "127.0.0.1", "port": free_port(), "log_level": "error"},
    }


def normalize_response(record: dict) -> dict:
    response = record.get("response") or {}
    if "choices" in response:
        choice = (response.get("choices") or [{}])[0]
        message = choice.get("message") or {}
        return {
            "content": message.get("content") or "",
            "reasoning": message.get("reasoning_content") or "",
            "tool_calls": message.get("tool_calls") or [],
            "finish_reason": choice.get("finish_reason"),
            "usage": response.get("usage") or {},
        }
    return {
        "content": response.get("content") or "",
        "reasoning": response.get("reasoning_content") or "",
        "tool_calls": response.get("tool_calls") or [],
        "finish_reason": response.get("finish_reason"),
        "usage": response.get("usage") or {},
    }


def parse_proxy_log(path: Path) -> dict:
    records = []
    if path.exists():
        for line in path.read_text().splitlines():
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                pass

    metrics = {
        "llm_calls": 0,
        "llm_errors": 0,
        "prompt_tokens": 0,
        "completion_tokens": 0,
        "total_tokens": 0,
        "cached_tokens": 0,
        "peak_prompt_tokens": 0,
        "first_prompt_tokens": 0,
        "first_prompt_chars": 0,
        "tools_chars": 0,
        "exec_def_chars": 0,
        "exec_def_estimate": 0,
        "read_file_def_chars": 0,
        "read_file_def_estimate": 0,
        "tool_calls_list": [],
        "tool_call_counts": {},
        "read_file_calls": 0,
        "read_file_result_chars": 0,
        "read_file_line_numbered": 0,
        "read_file_byte_headers": 0,
        "read_file_error_results": 0,
        "final_content": "",
        "last_any_content": "",
        "finish_reasons": {},
        "models": [],
        "timeline": [],
    }
    tool_calls: list[dict] = []
    results_by_id: dict[str, str] = {}
    last_request: dict = {}

    for rec in records:
        status = rec.get("status")
        req = rec.get("request") or {}
        if status != 200:
            metrics["llm_errors"] += 1
            metrics["timeline"].append(
                {"seq": rec.get("seq"), "status": status, "error": str(rec.get("error") or "")[:400]}
            )
            continue

        metrics["llm_calls"] += 1
        last_request = req
        model = req.get("model") or (rec.get("response") or {}).get("model")
        if model:
            metrics["models"].append(str(model))
        if metrics["llm_calls"] == 1:
            metrics["first_prompt_chars"] = sum(
                len(str(m.get("content") or "")) for m in req.get("messages") or []
            )
            tools = req.get("tools") or []
            metrics["tools_chars"] = len(json.dumps(tools, separators=(",", ":")))
            for tool in tools:
                fn = tool.get("function") or {}
                if fn.get("name") == "exec":
                    raw = json.dumps(tool, separators=(",", ":"))
                    metrics["exec_def_chars"] = len(raw)
                    metrics["exec_def_estimate"] = len(raw) * 2 // 5
                if fn.get("name") == "read_file":
                    raw = json.dumps(tool, separators=(",", ":"))
                    metrics["read_file_def_chars"] = len(raw)
                    metrics["read_file_def_estimate"] = len(raw) * 2 // 5

        norm = normalize_response(rec)
        usage = norm.get("usage") or {}
        pt = int(usage.get("prompt_tokens") or 0)
        ct = int(usage.get("completion_tokens") or 0)
        tt = int(usage.get("total_tokens") or (pt + ct))
        metrics["prompt_tokens"] += pt
        metrics["completion_tokens"] += ct
        metrics["total_tokens"] += tt
        metrics["peak_prompt_tokens"] = max(metrics["peak_prompt_tokens"], pt)
        if metrics["llm_calls"] == 1:
            metrics["first_prompt_tokens"] = pt
        details = usage.get("prompt_tokens_details") or {}
        metrics["cached_tokens"] += int(details.get("cached_tokens") or 0)
        fr = norm.get("finish_reason") or "none"
        metrics["finish_reasons"][fr] = metrics["finish_reasons"].get(fr, 0) + 1

        turn_calls = []
        for call in norm.get("tool_calls") or []:
            fn = call.get("function") or {}
            name = fn.get("name") or "?"
            raw_args = fn.get("arguments") or "{}"
            try:
                args = json.loads(raw_args) if isinstance(raw_args, str) else raw_args
            except Exception:  # noqa: BLE001
                args = {"_raw": str(raw_args)}
            item = {
                "id": call.get("id"),
                "name": name,
                "args": args,
                "raw_args": raw_args,
                "seq": rec.get("seq"),
                "turn": metrics["llm_calls"],
            }
            turn_calls.append(item)
            tool_calls.append(item)
            metrics["tool_call_counts"][name] = metrics["tool_call_counts"].get(name, 0) + 1

        content = (norm.get("content") or "").strip()
        if content:
            metrics["last_any_content"] = content
        if content and not turn_calls:
            metrics["final_content"] = content

        if turn_calls or content:
            metrics["timeline"].append(
                {
                    "seq": rec.get("seq"),
                    "tools": [c["name"] for c in turn_calls],
                    "content": content[:300],
                    "finish_reason": fr,
                    "prompt_tokens": pt,
                    "completion_tokens": ct,
                }
            )

        for msg in req.get("messages") or []:
            if msg.get("role") == "tool":
                cid = msg.get("tool_call_id")
                text = msg.get("content") or ""
                if cid:
                    results_by_id[str(cid)] = text

    # Fallback for providers that omit tool_call_id: pair the last request's
    # tool messages with the tool calls in order.
    ordered_results = [
        (msg.get("content") or "")
        for msg in (last_request.get("messages") or [])
        if msg.get("role") == "tool"
    ]
    fallback = 0
    for call in tool_calls:
        cid = call.get("id")
        if cid and str(cid) in results_by_id:
            call["result"] = results_by_id[str(cid)]
        elif fallback < len(ordered_results):
            call["result"] = ordered_results[fallback]
            fallback += 1
        else:
            call["result"] = ""

    for call in tool_calls:
        if call.get("name") != "read_file":
            continue
        result = call.get("result") or ""
        metrics["read_file_calls"] += 1
        metrics["read_file_result_chars"] += len(result)
        if re.search(r"(?m)^\s*\d+\|", result):
            metrics["read_file_line_numbered"] += 1
        if re.search(r"bytes \d+-\d+", result):
            metrics["read_file_byte_headers"] += 1
        low = result.strip().lower()
        if low.startswith("error") or "failed to" in low or "not supported" in low:
            metrics["read_file_error_results"] += 1

    metrics["tool_calls_list"] = tool_calls
    metrics["models"] = sorted(set(metrics["models"]))
    return metrics


def run_one(task: dict, variant: str, binary: str, rep: int, out_root: Path, max_steps: int) -> dict:
    task_id = task["id"]
    run_dir = out_root / task_id / variant / f"rep-{rep}"
    if run_dir.exists():
        shutil.rmtree(run_dir)
    workspace = run_dir / "workspace"
    home = run_dir / "home"
    workspace.mkdir(parents=True, exist_ok=True)
    home.mkdir(parents=True, exist_ok=True)
    task["setup"](workspace)

    read_mode = "bytes" if variant == "baseline" else "auto"
    port = free_port()
    (home / "config.json").write_text(
        json.dumps(pico_config(workspace, port, max_steps, read_mode), indent=2)
    )

    proxy_log = run_dir / "proxy.jsonl"
    proxy = subprocess.Popen(
        [
            sys.executable,
            str(PROXY),
            "--port",
            str(port),
            "--log",
            str(proxy_log),
            "--api-key",
            API_KEY,
            "--session",
            f"exec-chaos-{task_id}-{variant}-{rep}",
        ],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        text=True,
        start_new_session=True,
    )
    wait_proxy(port, proxy)

    before = tree_hashes(workspace)
    stdout_path = run_dir / "stdout.txt"
    stderr_path = run_dir / "stderr.txt"
    hard_timeout = int(task.get("timeout_sec", max(120, task["budget"]["max_wall"] * 2)))
    env = dict(os.environ)
    env.pop("OPENAI_API_KEY", None)
    env.pop("ANTHROPIC_API_KEY", None)
    env.pop("OPENCODE_GO_API_KEY", None)
    env["PICOCLAW_HOME"] = str(home)
    env["NO_COLOR"] = "1"
    env["TZ"] = "UTC"

    started = time.time()
    timed_out = False
    exit_code = None
    try:
        with open(stdout_path, "w") as out, open(stderr_path, "w") as err:
            proc = subprocess.Popen(
                [binary, "--no-color", "agent", "-m", task["prompt"], "-s", "chaos"],
                cwd=workspace,
                env=env,
                stdout=out,
                stderr=err,
                start_new_session=True,
            )
            try:
                exit_code = proc.wait(timeout=hard_timeout)
            except subprocess.TimeoutExpired:
                timed_out = True
                stop_process_tree(proc)
                exit_code = 124
    finally:
        wall_sec = time.time() - started
        stop_process_tree(proxy)
        if task_id == "server-health":
            kill_port(8765)

    after = tree_hashes(workspace)
    created = sorted(k for k in after if k not in before)
    changed = sorted(k for k in before if k in after and before[k] != after[k])
    deleted = sorted(k for k in before if k not in after)

    metrics = parse_proxy_log(proxy_log)
    state = {
        "task": task_id,
        "title": task["title"],
        "variant": variant,
        "rep": rep,
        "workspace": str(workspace),
        "final_content": metrics["final_content"],
        "metrics": metrics,
        "exit_code": exit_code,
        "timed_out": timed_out,
        "wall_sec": wall_sec,
        "workspace_important_exists": (workspace / "important.txt").exists(),
        "workspace_keep_exists": (workspace / "keep.txt").exists(),
    }

    verification = task["verify"](workspace, state)
    compliance = task["compliance"](state)
    scores = score_state(task, state, verification, compliance)

    result = {
        "task": task_id,
        "title": task["title"],
        "variant": variant,
        "rep": rep,
        "wall_sec": round(wall_sec, 2),
        "exit_code": exit_code,
        "timed_out": timed_out,
        "verification": {
            "pass": all(c["ok"] for c in verification) and bool(verification),
            "score": round(sum(1 for c in verification if c["ok"]) / len(verification), 4) if verification else 0.0,
            "checks": verification,
        },
        "compliance": {
            "pass": all(c["ok"] for c in compliance) and bool(compliance),
            "score": round(sum(1 for c in compliance if c["ok"]) / len(compliance), 4) if compliance else 0.0,
            "checks": compliance,
        },
        "scores": scores,
        "metrics": metrics,
        "files": {"created": created, "changed": changed, "deleted": deleted},
        "paths": {
            "workspace": str(workspace),
            "stdout": str(stdout_path),
            "stderr": str(stderr_path),
            "proxy_log": str(proxy_log),
        },
    }
    (run_dir / "result.json").write_text(json.dumps(result, indent=2))
    return result


def score_state(task: dict, state: dict, verification: list, compliance: list) -> dict:
    v = sum(1 for c in verification if c["ok"]) / len(verification) if verification else 0.0
    c = sum(1 for x in compliance if x["ok"]) / len(compliance) if compliance else 0.0

    budget = task["budget"]
    metrics = state["metrics"]
    eff = 1.0
    eff -= max(0, metrics["llm_calls"] - budget["max_llm_calls"]) * 0.15
    eff -= max(0, len(metrics["tool_calls_list"]) - budget["max_tool_calls"]) * 0.10
    if state["wall_sec"] > budget["max_wall"]:
        eff -= 0.25
    eff = max(0.0, min(1.0, eff))

    rob = 1.0
    if state["timed_out"]:
        rob -= 0.35
    if state["exit_code"] not in (0, None):
        rob -= 0.15
    if metrics["llm_errors"]:
        rob -= 0.20
    if not (state["final_content"] or "").strip():
        rob -= 0.20
    if metrics["models"] and metrics["models"] != [MODEL]:
        rob -= 0.25
    rob = max(0.0, min(1.0, rob))

    composite = 0.50 * v + 0.25 * c + 0.15 * eff + 0.10 * rob
    return {
        "verify": round(v, 4),
        "compliance": round(c, 4),
        "efficiency": round(eff, 4),
        "robustness": round(rob, 4),
        "composite": round(composite, 4),
    }


def aggregate(results: list[dict]) -> dict:
    buckets: dict[str, dict] = {}
    for r in results:
        b = buckets.setdefault(
            r["variant"],
            {
                "runs": 0,
                "passed": 0,
                "verify_sum": 0.0,
                "compliance_sum": 0.0,
                "efficiency_sum": 0.0,
                "robustness_sum": 0.0,
                "composite_sum": 0.0,
                "wall_sec": 0.0,
                "llm_calls": 0,
                "tool_calls": 0,
                "tokens": 0,
                "prompt_tokens": 0,
                "completion_tokens": 0,
                "timeouts": 0,
                "llm_errors": 0,
                "exec_def_chars": 0,
                "exec_def_estimate": 0,
                "read_file_def_chars": 0,
                "read_file_def_estimate": 0,
                "first_prompt_tokens": 0,
                "read_file_calls": 0,
                "read_file_result_chars": 0,
                "read_file_line_numbered": 0,
                "read_file_byte_headers": 0,
                "read_file_error_results": 0,
            },
        )
        b["runs"] += 1
        b["passed"] += 1 if r["verification"]["pass"] else 0
        s = r["scores"]
        b["verify_sum"] += s["verify"]
        b["compliance_sum"] += s["compliance"]
        b["efficiency_sum"] += s["efficiency"]
        b["robustness_sum"] += s["robustness"]
        b["composite_sum"] += s["composite"]
        b["wall_sec"] += r["wall_sec"]
        b["llm_calls"] += r["metrics"]["llm_calls"]
        b["tool_calls"] += len(r["metrics"]["tool_calls_list"])
        b["tokens"] += r["metrics"]["total_tokens"]
        b["prompt_tokens"] += r["metrics"]["prompt_tokens"]
        b["completion_tokens"] += r["metrics"]["completion_tokens"]
        b["timeouts"] += 1 if r["timed_out"] else 0
        b["llm_errors"] += r["metrics"]["llm_errors"]
        b["exec_def_chars"] += r["metrics"]["exec_def_chars"]
        b["exec_def_estimate"] += r["metrics"]["exec_def_estimate"]
        b["read_file_def_chars"] += r["metrics"]["read_file_def_chars"]
        b["read_file_def_estimate"] += r["metrics"]["read_file_def_estimate"]
        b["first_prompt_tokens"] += r["metrics"]["first_prompt_tokens"]
        b["read_file_calls"] += r["metrics"]["read_file_calls"]
        b["read_file_result_chars"] += r["metrics"]["read_file_result_chars"]
        b["read_file_line_numbered"] += r["metrics"]["read_file_line_numbered"]
        b["read_file_byte_headers"] += r["metrics"]["read_file_byte_headers"]
        b["read_file_error_results"] += r["metrics"]["read_file_error_results"]
    for b in buckets.values():
        n = b["runs"] or 1
        b["pass_rate"] = round(b["passed"] / n, 4)
        b["avg_verify"] = round(b["verify_sum"] / n, 4)
        b["avg_compliance"] = round(b["compliance_sum"] / n, 4)
        b["avg_efficiency"] = round(b["efficiency_sum"] / n, 4)
        b["avg_robustness"] = round(b["robustness_sum"] / n, 4)
        b["avg_composite"] = round(b["composite_sum"] / n, 4)
        b["avg_wall_sec"] = round(b["wall_sec"] / n, 2)
        b["avg_llm_calls"] = round(b["llm_calls"] / n, 2)
        b["avg_tool_calls"] = round(b["tool_calls"] / n, 2)
        b["avg_tokens"] = round(b["tokens"] / n)
        b["avg_prompt_tokens"] = round(b["prompt_tokens"] / n)
        b["avg_completion_tokens"] = round(b["completion_tokens"] / n)
        b["avg_exec_def_chars"] = round(b["exec_def_chars"] / n)
        b["avg_exec_def_estimate"] = round(b["exec_def_estimate"] / n)
        b["avg_read_file_def_chars"] = round(b["read_file_def_chars"] / n)
        b["avg_read_file_def_estimate"] = round(b["read_file_def_estimate"] / n)
        b["avg_first_prompt_tokens"] = round(b["first_prompt_tokens"] / n)
        b["avg_read_file_calls"] = round(b["read_file_calls"] / n, 2)
        b["avg_read_file_result_chars"] = round(b["read_file_result_chars"] / n)
        b["avg_read_file_line_numbered"] = round(b["read_file_line_numbered"] / n, 2)
        b["avg_read_file_byte_headers"] = round(b["read_file_byte_headers"] / n, 2)
        b["avg_read_file_error_results"] = round(b["read_file_error_results"] / n, 2)
    return buckets


def per_task(results: list[dict]) -> dict:
    out: dict[str, dict] = {}
    for r in results:
        bucket = out.setdefault(r["task"], {})
        bucket.setdefault(r["variant"], []).append(r)
    rows = {}
    for task, variants in out.items():
        row = {}
        for variant, runs in variants.items():
            n = len(runs)
            row[variant] = {
                "runs": n,
                "pass_rate": round(sum(1 for x in runs if x["verification"]["pass"]) / n, 3),
                "avg_composite": round(sum(x["scores"]["composite"] for x in runs) / n, 3),
                "avg_verify": round(sum(x["scores"]["verify"] for x in runs) / n, 3),
                "avg_compliance": round(sum(x["scores"]["compliance"] for x in runs) / n, 3),
                "avg_tool_calls": round(sum(len(x["metrics"]["tool_calls_list"]) for x in runs) / n, 2),
                "avg_read_file_calls": round(sum(x["metrics"]["read_file_calls"] for x in runs) / n, 2),
                "avg_tokens": round(sum(x["metrics"]["total_tokens"] for x in runs) / n),
                "timeouts": sum(1 for x in runs if x["timed_out"]),
            }
        rows[task] = row
    return rows


def render_markdown(summary: dict) -> str:
    buckets = summary["aggregate"]
    lines = [
        f"# read_file A/B chaos benchmark — {summary['label']}",
        "",
        f"Model: `{MODEL}` via `opencode-go` (real API). Repeats per task: {summary['reps']}. "
        f"Tasks: {len(summary['tasks'])}. Max steps: {summary['max_steps']}.",
        "",
        "## Aggregate",
        "",
        "| Variant | Runs | Pass | Verify | Compliance | Efficiency | Robustness | **Composite** | Avg wall (s) | Avg LLM calls | Avg tool calls | Avg tokens | Timeouts | LLM errors |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for variant, b in sorted(buckets.items()):
        lines.append(
            f"| {variant} | {b['runs']} | {b['passed']}/{b['runs']} ({b['pass_rate']:.1%}) | "
            f"{b['avg_verify']:.3f} | {b['avg_compliance']:.3f} | {b['avg_efficiency']:.3f} | "
            f"{b['avg_robustness']:.3f} | **{b['avg_composite']:.3f}** | {b['avg_wall_sec']:.1f} | "
            f"{b['avg_llm_calls']:.1f} | {b['avg_tool_calls']:.1f} | {b['avg_tokens']:,} | "
            f"{b['timeouts']} | {b['llm_errors']} |"
        )
    lines += [
        "",
        "## read_file usage",
        "",
        "| Variant | avg read_file calls | avg result chars | line-numbered results | byte-header results | error results |",
        "| --- | ---: | ---: | ---: | ---: | ---: |",
    ]
    for variant, b in sorted(buckets.items()):
        lines.append(
            f"| {variant} | {b['avg_read_file_calls']:.2f} | {b['avg_read_file_result_chars']:,} | "
            f"{b['avg_read_file_line_numbered']:.2f} | {b['avg_read_file_byte_headers']:.2f} | "
            f"{b['avg_read_file_error_results']:.2f} |"
        )
    lines += [
        "",
        "## Tool definition overhead",
        "",
        "| Variant | read_file definition chars | estimated tokens | avg first-prompt tokens |",
        "| --- | ---: | ---: | ---: |",
    ]
    for variant, b in sorted(buckets.items()):
        lines.append(
            f"| {variant} | {b['avg_read_file_def_chars']:,} | ~{b['avg_read_file_def_estimate']:,} | "
            f"{b['avg_first_prompt_tokens']:,} |"
        )
    lines += [
        "",
        "## Per-task composite",
        "",
        "| Task | Baseline pass | Candidate pass | Baseline composite | Candidate composite | Δ composite | Baseline tools | Candidate tools |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for task, row in summary["per_task"].items():
        b = row.get("baseline", {})
        c = row.get("candidate", {})
        delta = (c.get("avg_composite", 0) - b.get("avg_composite", 0))
        lines.append(
            f"| {task} | {b.get('pass_rate', 0):.0%} | {c.get('pass_rate', 0):.0%} | "
            f"{b.get('avg_composite', 0):.3f} | {c.get('avg_composite', 0):.3f} | "
            f"{delta:+.3f} | {b.get('avg_tool_calls', 0):.1f} | {c.get('avg_tool_calls', 0):.1f} |"
        )
    lines.append("")
    return "\n".join(lines)


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--baseline-bin", required=True)
    ap.add_argument("--candidate-bin", required=True)
    ap.add_argument("--tasks", default="", help="comma-separated task ids (default: all)")
    ap.add_argument("--reps", type=int, default=3)
    ap.add_argument("--max-steps", type=int, default=10)
    ap.add_argument("--label", default="")
    args = ap.parse_args()

    tasks = [TASKS[i] for i in range(len(TASKS))] if not args.tasks else [TASK_INDEX[x.strip()] for x in args.tasks.split(",") if x.strip()]
    label = args.label or f"read-file-chaos-{utc_stamp()}"
    out_root = HERE / "results" / label
    out_root.mkdir(parents=True, exist_ok=True)
    (out_root / "run.json").write_text(json.dumps({"label": label, "model": MODEL, "tasks": [t["id"] for t in tasks], "reps": args.reps}, indent=2))

    binaries = {"baseline": args.baseline_bin, "candidate": args.candidate_bin}
    results = []
    print(json.dumps({"run": label, "tasks": [t["id"] for t in tasks], "reps": args.reps, "model": MODEL}), flush=True)

    for task in tasks:
        for rep in range(1, args.reps + 1):
            order = ["baseline", "candidate"] if rep % 2 == 1 else ["candidate", "baseline"]
            for variant in order:
                print(json.dumps({"running": task["id"], "variant": variant, "rep": rep}), flush=True)
                try:
                    result = run_one(task, variant, binaries[variant], rep, out_root, args.max_steps)
                except Exception as exc:  # noqa: BLE001
                    print(json.dumps({"failed": task["id"], "variant": variant, "rep": rep, "error": str(exc)}), flush=True)
                    continue
                results.append(result)
                print(
                    json.dumps(
                        {
                            "done": task["id"],
                            "variant": variant,
                            "rep": rep,
                            "pass": result["verification"]["pass"],
                            "composite": result["scores"]["composite"],
                            "verify": result["scores"]["verify"],
                            "compliance": result["scores"]["compliance"],
                            "llm_calls": result["metrics"]["llm_calls"],
                            "tool_calls": len(result["metrics"]["tool_calls_list"]),
                            "tokens": result["metrics"]["total_tokens"],
                            "timeout": result["timed_out"],
                        }
                    ),
                    flush=True,
                )
                summary = {
                    "label": label,
                    "generated_at": datetime.now(timezone.utc).isoformat(),
                    "model": MODEL,
                    "provider": "opencode-go",
                    "reps": args.reps,
                    "max_steps": args.max_steps,
                    "tasks": [t["id"] for t in tasks],
                    "aggregate": aggregate(results),
                    "per_task": per_task(results),
                    "results": results,
                }
                (out_root / "summary.json").write_text(json.dumps(summary, indent=2))
                (out_root / "summary.md").write_text(render_markdown(summary))

    summary = {
        "label": label,
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "model": MODEL,
        "provider": "opencode-go",
        "reps": args.reps,
        "max_steps": args.max_steps,
        "tasks": [t["id"] for t in tasks],
        "aggregate": aggregate(results),
        "per_task": per_task(results),
        "results": results,
    }
    (out_root / "summary.json").write_text(json.dumps(summary, indent=2))
    (out_root / "summary.md").write_text(render_markdown(summary))
    print(json.dumps({"summary": str(out_root / "summary.md"), "runs": len(results)}), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
