#!/usr/bin/env python3
"""PicoClaw vs Hermes benchmark harness.

Runs both agents against identical task workspaces, through the same local
logging proxy and the same upstream provider/model, then records wall time,
LLM steps, tool calls, token usage and deterministic task verification.

Usage:
  python3 run_benchmark.py --tasks bug-fix,log-triage --agents picoclaw,hermes
  python3 run_benchmark.py --label full-run
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
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
REPO = HERE.parent.parent
PICO_BIN = REPO / "build" / "picoclaw-bench"
HERMES_ROOT = Path.home() / "hermes-src-0.23.1"
HERMES_BIN = HERMES_ROOT / ".venv" / "bin" / "hermes"
API_KEY = os.environ.get("OPENCODE_GO_API_KEY", "")
MODEL = "glm-5.3-flash"


def utc_stamp() -> str:
    return datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as fh:
        for chunk in iter(lambda: fh.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def tree_hashes(root: Path) -> dict[str, str]:
    out = {}
    for path in sorted(root.rglob("*")):
        if path.is_file() and not any(part.startswith(".git") for part in path.relative_to(root).parts):
            out[str(path.relative_to(root))] = sha256(path)
    return out


def free_port() -> int:
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


def wait_proxy(port: int, proc: subprocess.Popen, timeout: float = 15.0) -> None:
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


def pico_config(home: Path, workspace: Path, port: int, max_iterations: int) -> dict:
    return {
        "version": 3,
        "agents": {
            "defaults": {
                "workspace": str(workspace),
                "restrict_to_workspace": True,
                "model_name": "default",
                "max_tokens": 8192,
                "context_window": 131072,
                "max_tool_iterations": max_iterations,
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
            "read_file": {"enabled": True},
            "write_file": {"enabled": True},
            "append_file": {"enabled": True},
            "edit_file": {"enabled": True},
            "list_dir": {"enabled": True},
            "search_files": {"enabled": True},
            "web": {
                "enabled": True,
                "provider": "duckduckgo",
                "prefer_native": False,
                "duckduckgo": {"enabled": True, "max_results": 5},
            },
            "web_fetch": {"enabled": True},
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
        },
        "gateway": {"host": "127.0.0.1", "port": free_port(), "log_level": "error"},
    }


def hermes_config(home: Path, port: int, max_turns: int) -> str:
    return f"""\
model:
  provider: "custom:bench"
  default: "{MODEL}"
  base_url: "http://127.0.0.1:{port}/openai/v1"
  api_mode: chat_completions

providers:
  bench:
    api: "http://127.0.0.1:{port}/openai/v1"
    api_key: "{API_KEY}"
    transport: chat_completions
    discover_models: false
    extra_body:
      temperature: 0
    models:
      - "{MODEL}"

platform_toolsets:
  cli: [terminal, file, web]

agent:
  max_turns: {max_turns}
  reasoning_effort: "minimal"
  verbose: false

compression:
  enabled: false

memory:
  memory_enabled: false
  user_profile_enabled: false
  nudge_interval: 0

skills:
  auto_load: []

auxiliary:
  title_generation:
    enabled: false
  compression:
    enabled: false

display:
  interface: cli
  cli_layout: modern
  tool_progress: off
  bell_on_complete: false
"""


def prepare_home(agent: str, home: Path, workspace: Path, port: int, max_steps: int) -> None:
    home.mkdir(parents=True, exist_ok=True)
    if agent == "picoclaw":
        (home / "config.json").write_text(json.dumps(pico_config(home, workspace, port, max_steps), indent=2))
    else:
        (home / "config.yaml").write_text(hermes_config(home, port, max_steps))
        (home / ".env").write_text(f"OPENCODE_GO_API_KEY={API_KEY}\nBENCH_API_KEY={API_KEY}\n")
        (home / "logs").mkdir(exist_ok=True)


def agent_command(agent: str, prompt: str) -> list[str]:
    if agent == "picoclaw":
        return [str(PICO_BIN), "--no-color", "agent", "-m", prompt, "-s", "bench"]
    return [str(HERMES_BIN), "chat", "--yolo", "-q", prompt]


def agent_env(agent: str, home: Path, workspace: Path) -> dict:
    env = dict(os.environ)
    env.pop("OPENAI_API_KEY", None)
    env.pop("ANTHROPIC_API_KEY", None)
    env.pop("OPENCODE_GO_API_KEY", None)
    env["PWD"] = str(workspace)
    env["OLDPWD"] = str(workspace)
    if agent == "picoclaw":
        env["PICOCLAW_HOME"] = str(home)
    else:
        env["HERMES_HOME"] = str(home)
        env["HERMES_YOLO_MODE"] = "1"
        env["NO_COLOR"] = "1"
    return env


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


def summarize_proxy_log(path: Path) -> dict:
    records = []
    if path.exists():
        for line in path.read_text().splitlines():
            line = line.strip()
            if line:
                try:
                    records.append(json.loads(line))
                except json.JSONDecodeError:
                    pass
    steps = 0
    errors = 0
    prompt_tokens = 0
    completion_tokens = 0
    total_tokens = 0
    cached_tokens = 0
    peak_prompt = 0
    tool_calls: list[dict] = []
    tool_counts: dict[str, int] = {}
    final_content = ""
    finish_reasons: dict[str, int] = {}
    llm_ms = 0.0
    first_prompt_chars = 0
    tools_offered = 0
    timeline = []
    for rec in records:
        if rec.get("status") != 200:
            errors += 1
            timeline.append({"seq": rec.get("seq"), "error": rec.get("error"), "status": rec.get("status")})
            continue
        steps += 1
        llm_ms += float(rec.get("duration_ms") or 0)
        req = rec.get("request") or {}
        messages = req.get("messages") or []
        if steps == 1:
            first_prompt_chars = sum(len(str(m.get("content") or "")) for m in messages)
            tools_offered = len(req.get("tools") or [])
        norm = normalize_response(rec)
        usage = norm.get("usage") or {}
        pt = int(usage.get("prompt_tokens") or 0)
        ct = int(usage.get("completion_tokens") or 0)
        tt = int(usage.get("total_tokens") or (pt + ct))
        prompt_tokens += pt
        completion_tokens += ct
        total_tokens += tt
        peak_prompt = max(peak_prompt, pt)
        details = usage.get("prompt_tokens_details") or {}
        cached_tokens += int(details.get("cached_tokens") or 0)
        fr = norm.get("finish_reason") or "none"
        finish_reasons[fr] = finish_reasons.get(fr, 0) + 1
        names = []
        for call in norm.get("tool_calls") or []:
            fn = call.get("function") or {}
            name = fn.get("name") or "?"
            names.append(name)
            tool_counts[name] = tool_counts.get(name, 0) + 1
            tool_calls.append(
                {
                    "seq": rec.get("seq"),
                    "name": name,
                    "arguments": (fn.get("arguments") or "")[:2000],
                }
            )
        if norm.get("content"):
            final_content = norm["content"]
        timeline.append(
            {
                "seq": rec.get("seq"),
                "duration_ms": rec.get("duration_ms"),
                "prompt_tokens": pt,
                "completion_tokens": ct,
                "finish_reason": fr,
                "tools": names,
                "content_preview": (norm.get("content") or "")[:300],
            }
        )
    return {
        "llm_calls": steps,
        "llm_errors": errors,
        "prompt_tokens": prompt_tokens,
        "completion_tokens": completion_tokens,
        "total_tokens": total_tokens,
        "cached_prompt_tokens": cached_tokens,
        "peak_prompt_tokens": peak_prompt,
        "tool_calls": len(tool_calls),
        "tool_call_counts": tool_counts,
        "tool_timeline": tool_calls,
        "finish_reasons": finish_reasons,
        "final_content": final_content,
        "llm_duration_ms": round(llm_ms, 1),
        "first_prompt_chars": first_prompt_chars,
        "tools_offered": tools_offered,
        "timeline": timeline,
    }


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


def stop_hermes_gateway(home: Path) -> None:
    try:
        subprocess.run(
            [str(HERMES_BIN), "gateway", "stop"],
            env={**os.environ, "HERMES_HOME": str(home)},
            capture_output=True,
            timeout=30,
        )
    except Exception:  # noqa: BLE001
        pass


def run_one(task: dict, agent: str, run_root: Path, max_steps: int) -> dict:
    task_id = task["id"]
    run_dir = run_root / task_id / agent
    workspace = run_dir / "workspace"
    home = run_dir / "home"
    if run_dir.exists():
        shutil.rmtree(run_dir)
    shutil.copytree(HERE / "tasks" / task_id / "workspace", workspace)
    prepare_home(agent, home, workspace, 0, max_steps)  # port patched below

    port = free_port()
    # Rewrite config with the real port.
    if agent == "picoclaw":
        (home / "config.json").write_text(json.dumps(pico_config(home, workspace, port, max_steps), indent=2))
    else:
        (home / "config.yaml").write_text(hermes_config(home, port, max_steps))

    proxy_log = run_dir / "proxy.jsonl"
    proxy = subprocess.Popen(
        [
            sys.executable,
            str(HERE / "proxy.py"),
            "--port",
            str(port),
            "--log",
            str(proxy_log),
            "--api-key",
            API_KEY,
            "--session",
            f"bench-{task_id}-{agent}",
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
    timeout = int(task.get("timeout_sec", 480))
    started = time.time()
    timed_out = False
    try:
        with open(stdout_path, "w") as out, open(stderr_path, "w") as err:
            proc = subprocess.Popen(
                agent_command(agent, task["prompt"]),
                cwd=workspace,
                env=agent_env(agent, home, workspace),
                stdout=out,
                stderr=err,
                start_new_session=True,
            )
            try:
                exit_code = proc.wait(timeout=timeout)
            except subprocess.TimeoutExpired:
                timed_out = True
                stop_process_tree(proc)
                exit_code = 124
    finally:
        wall_sec = time.time() - started
        stop_process_tree(proxy)
        if agent == "hermes":
            stop_hermes_gateway(home)

    after = tree_hashes(workspace)
    changed = sorted(k for k in after if k not in before or before[k] != after[k])
    deleted = sorted(k for k in before if k not in after)
    created = sorted(k for k in after if k not in before)

    verify_cmd = [sys.executable, str(HERE / "verify.py"), task_id, str(workspace)]
    try:
        verify_proc = subprocess.run(verify_cmd, capture_output=True, text=True, timeout=180)
        verification = json.loads(verify_proc.stdout or "{}")
        if verify_proc.returncode != 0:
            verification = {"error": verify_proc.stderr, "pass": False, "score": 0.0, "checks": []}
    except Exception as exc:  # noqa: BLE001
        verification = {"error": str(exc), "pass": False, "score": 0.0, "checks": []}

    metrics = summarize_proxy_log(proxy_log)
    result = {
        "task": task_id,
        "title": task["title"],
        "agent": agent,
        "wall_sec": round(wall_sec, 2),
        "exit_code": exit_code,
        "timed_out": timed_out,
        "metrics": metrics,
        "verification": verification,
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


def aggregate(results: list[dict]) -> dict:
    by_agent: dict[str, dict] = {}
    for result in results:
        agent = result["agent"]
        bucket = by_agent.setdefault(
            agent,
            {
                "tasks": 0,
                "passed": 0,
                "score_sum": 0.0,
                "wall_sec": 0.0,
                "llm_calls": 0,
                "tool_calls": 0,
                "total_tokens": 0,
                "prompt_tokens": 0,
                "completion_tokens": 0,
                "timeouts": 0,
            },
        )
        bucket["tasks"] += 1
        bucket["passed"] += 1 if result["verification"].get("pass") else 0
        bucket["score_sum"] += float(result["verification"].get("score") or 0)
        bucket["wall_sec"] += result["wall_sec"]
        bucket["llm_calls"] += result["metrics"]["llm_calls"]
        bucket["tool_calls"] += result["metrics"]["tool_calls"]
        bucket["total_tokens"] += result["metrics"]["total_tokens"]
        bucket["prompt_tokens"] += result["metrics"]["prompt_tokens"]
        bucket["completion_tokens"] += result["metrics"]["completion_tokens"]
        bucket["timeouts"] += 1 if result["timed_out"] else 0
    for bucket in by_agent.values():
        n = bucket["tasks"] or 1
        bucket["pass_rate"] = round(bucket["passed"] / n, 3)
        bucket["avg_score"] = round(bucket["score_sum"] / n, 3)
        bucket["avg_wall_sec"] = round(bucket["wall_sec"] / n, 2)
        bucket["avg_llm_calls"] = round(bucket["llm_calls"] / n, 1)
        bucket["avg_tool_calls"] = round(bucket["tool_calls"] / n, 1)
        bucket["avg_total_tokens"] = round(bucket["total_tokens"] / n)
    return by_agent


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--tasks", default="", help="comma-separated task ids (default: all)")
    ap.add_argument("--agents", default="picoclaw,hermes")
    ap.add_argument("--label", default="")
    ap.add_argument("--max-steps", type=int, default=40)
    args = ap.parse_args()
    if not API_KEY:
        print("OPENCODE_GO_API_KEY is required", file=sys.stderr)
        return 2

    tasks = []
    for path in sorted((HERE / "tasks").glob("*/task.json")):
        task = json.loads(path.read_text())
        if args.tasks and task["id"] not in args.tasks.split(","):
            continue
        tasks.append(task)
    agents = [a.strip() for a in args.agents.split(",") if a.strip()]
    if not tasks:
        print("no tasks selected", file=sys.stderr)
        return 2
    if "hermes" in agents and not HERMES_BIN.exists():
        print(f"hermes binary not found at {HERMES_BIN}", file=sys.stderr)
        return 2

    label = args.label or utc_stamp()
    run_root = HERE / "results" / label
    run_root.mkdir(parents=True, exist_ok=True)
    print(json.dumps({"run": label, "tasks": [t["id"] for t in tasks], "agents": agents}), flush=True)

    results = []
    for task in tasks:
        for agent in agents:
            print(json.dumps({"running": task["id"], "agent": agent}), flush=True)
            result = run_one(task, agent, run_root, args.max_steps)
            results.append(result)
            print(
                json.dumps(
                    {
                        "done": task["id"],
                        "agent": agent,
                        "wall_sec": result["wall_sec"],
                        "pass": result["verification"].get("pass"),
                        "score": result["verification"].get("score"),
                        "llm_calls": result["metrics"]["llm_calls"],
                        "tool_calls": result["metrics"]["tool_calls"],
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
        "max_steps": args.max_steps,
        "agents": aggregate(results),
        "results": results,
    }
    (run_root / "summary.json").write_text(json.dumps(summary, indent=2))
    (run_root / "summary.md").write_text(render_markdown(summary))
    print(json.dumps({"summary": str(run_root / "summary.md")}), flush=True)
    return 0


def render_markdown(summary: dict) -> str:
    agents = summary["agents"]
    lines = [
        f"# Agent benchmark — {summary['label']}",
        "",
        f"Model: `{summary['model']}` via `{summary['provider']}`. Max steps: {summary['max_steps']}.",
        "",
        "## Aggregate",
        "",
        "| Agent | Pass | Score | Avg wall (s) | Avg steps | Avg tool calls | Avg tokens | Timeouts |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for agent, b in sorted(agents.items()):
        lines.append(
            f"| {agent} | {b['passed']}/{b['tasks']} | {b['avg_score']:.2f} | {b['avg_wall_sec']:.1f} | "
            f"{b['avg_llm_calls']:.1f} | {b['avg_tool_calls']:.1f} | {b['avg_total_tokens']:,} | {b['timeouts']} |"
        )
    lines += [
        "",
        "## Per task",
        "",
        "| Task | Agent | Pass | Score | Wall (s) | Steps | Tool calls | Prompt tok | Completion tok | Total tok |",
        "| --- | --- | :---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for r in summary["results"]:
        m = r["metrics"]
        v = r["verification"]
        lines.append(
            f"| {r['task']} | {r['agent']} | {'✅' if v.get('pass') else '❌'} | {float(v.get('score') or 0):.2f} | "
            f"{r['wall_sec']:.1f} | {m['llm_calls']} | {m['tool_calls']} | {m['prompt_tokens']:,} | "
            f"{m['completion_tokens']:,} | {m['total_tokens']:,} |"
        )
    lines.append("")
    return "\n".join(lines)


if __name__ == "__main__":
    sys.exit(main())
