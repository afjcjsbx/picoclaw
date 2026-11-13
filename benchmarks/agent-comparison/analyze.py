#!/usr/bin/env python3
"""Derive qualitative signals from a benchmark run's proxy logs.

Reads results/<label>/summary.json and each run's proxy.jsonl, then reports:
  - tool-result errors and self-corrections
  - repeated identical tool calls
  - per-step token growth
  - orchestration overhead (wall time minus LLM time)
"""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

HERE = Path(__file__).resolve().parent

ERROR_MARKERS = (
    "tool execution failed",
    "command blocked by safety guard",
    "traceback (most recent call last)",
    "assertionerror",
    "failed (failures=",
    "no such file or directory",
    "command not found",
    "permission denied",
    "fatal:",
)


def tool_messages(request: dict) -> list[str]:
    out = []
    for msg in request.get("messages") or []:
        if msg.get("role") == "tool":
            content = msg.get("content")
            if isinstance(content, str):
                out.append(content)
            elif isinstance(content, list):
                out.append(" ".join(str(part.get("text", "")) for part in content if isinstance(part, dict)))
    return out


def is_error(text: str) -> bool:
    low = text.lower()
    return any(marker in low for marker in ERROR_MARKERS)


def analyze_run(result: dict) -> dict:
    path = Path(result["paths"]["proxy_log"])
    records = []
    if path.exists():
        for line in path.read_text().splitlines():
            if line.strip():
                try:
                    records.append(json.loads(line))
                except json.JSONDecodeError:
                    pass

    tool_errors = []
    seen_tool_hashes = set()
    repeated = []
    call_counts: dict[str, int] = {}
    step_tokens = []
    for rec in records:
        if rec.get("status") != 200:
            continue
        req = rec.get("request") or {}
        norm = rec.get("response") or {}
        usage = norm.get("usage") or {}
        step_tokens.append(
            {
                "seq": rec.get("seq"),
                "prompt": usage.get("prompt_tokens"),
                "completion": usage.get("completion_tokens"),
                "cached": (usage.get("prompt_tokens_details") or {}).get("cached_tokens"),
            }
        )
        # Tool calls from the response (stream vs non-stream).
        calls = norm.get("tool_calls") or []
        if not calls and norm.get("choices"):
            calls = (norm["choices"][0].get("message") or {}).get("tool_calls") or []
        for call in calls:
            fn = call.get("function") or {}
            key = f"{fn.get('name')}|{fn.get('arguments')}"
            call_counts[key] = call_counts.get(key, 0) + 1
            if call_counts[key] == 2:
                repeated.append({"tool": fn.get("name"), "arguments": (fn.get("arguments") or "")[:300]})
        # Tool results are the last tool-role messages in the request.
        for text in tool_messages(req):
            digest = hashlib.sha1(text.encode()).hexdigest()
            if digest in seen_tool_hashes:
                continue
            seen_tool_hashes.add(digest)
            if is_error(text):
                tool_errors.append({"seq": rec.get("seq"), "snippet": text[:500]})

    m = result["metrics"]
    return {
        "task": result["task"],
        "agent": result["agent"],
        "wall_sec": result["wall_sec"],
        "llm_sec": round(m["llm_duration_ms"] / 1000, 2),
        "overhead_sec": round(result["wall_sec"] - m["llm_duration_ms"] / 1000, 2),
        "steps": m["llm_calls"],
        "tool_calls": m["tool_calls"],
        "tool_counts": m["tool_call_counts"],
        "total_tokens": m["total_tokens"],
        "peak_prompt_tokens": m["peak_prompt_tokens"],
        "cached_tokens": m["cached_prompt_tokens"],
        "step_tokens": step_tokens,
        "repeated_calls": repeated,
        "tool_errors": tool_errors,
        "tool_error_count": len(tool_errors),
        "pass": result["verification"].get("pass"),
        "score": result["verification"].get("score"),
        "failed_checks": [c["name"] for c in result["verification"].get("checks", []) if not c["ok"]],
        "final_content": m["final_content"],
        "files": result["files"],
        "timed_out": result["timed_out"],
        "exit_code": result["exit_code"],
    }


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("label")
    args = ap.parse_args()
    run_root = HERE / "results" / args.label
    summary = json.loads((run_root / "summary.json").read_text())
    analyses = [analyze_run(r) for r in summary["results"]]
    (run_root / "analysis.json").write_text(json.dumps(analyses, indent=2))
    lines = [f"# Qualitative analysis — {args.label}", ""]
    for a in analyses:
        lines += [
            f"## {a['task']} / {a['agent']}",
            "",
            f"- pass: {a['pass']} (score {a['score']}); failed checks: {', '.join(a['failed_checks']) or 'none'}",
            f"- wall {a['wall_sec']}s = llm {a['llm_sec']}s + overhead {a['overhead_sec']}s",
            f"- steps {a['steps']}, tool calls {a['tool_calls']} {a['tool_counts']}",
            f"- tokens {a['total_tokens']:,} (peak prompt {a['peak_prompt_tokens']:,}, cached {a['cached_tokens']:,})",
            f"- tool errors {a['tool_error_count']}; repeated calls {len(a['repeated_calls'])}",
        ]
        for err in a["tool_errors"][:5]:
            lines.append(f"  - error @step {err['seq']}: {err['snippet'][:160]!r}")
        for rep in a["repeated_calls"][:5]:
            lines.append(f"  - repeated: {rep['tool']} {rep['arguments'][:100]!r}")
        lines += [f"- final: {a['final_content'][:500]!r}", ""]
    (run_root / "analysis.md").write_text("\n".join(lines))
    print(json.dumps({"analysis": str(run_root / "analysis.md")}))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
