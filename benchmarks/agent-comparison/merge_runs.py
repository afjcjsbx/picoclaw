#!/usr/bin/env python3
"""Merge multiple benchmark runs into one comparison table.

Usage: python3 merge_runs.py full-2026-11-10 full-2026-11-10-v2
"""
from __future__ import annotations

import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent


def mean(values):
    return sum(values) / len(values) if values else 0.0


def cost_and_cache(result):
    path = Path(result["paths"]["proxy_log"])
    cost = 0.0
    prompt = 0
    cached = 0
    if path.exists():
        for line in path.read_text().splitlines():
            try:
                rec = json.loads(line)
            except json.JSONDecodeError:
                continue
            if rec.get("status") != 200:
                continue
            usage = (rec.get("response") or {}).get("usage") or {}
            cost += float((usage.get("opencode") or {}).get("list_cost") or 0)
            prompt += int(usage.get("prompt_tokens") or 0)
            cached += int((usage.get("prompt_tokens_details") or {}).get("cached_tokens") or 0)
    return cost, (cached / prompt if prompt else 0.0)


def main() -> int:
    labels = sys.argv[1:]
    if not labels:
        print("usage: merge_runs.py <label> [label ...]", file=sys.stderr)
        return 2
    runs = []
    for label in labels:
        summary = json.loads((HERE / "results" / label / "summary.json").read_text())
        for result in summary["results"]:
            result["_label"] = label
            runs.append(result)

    agents = sorted({r["agent"] for r in runs})
    tasks = sorted({r["task"] for r in runs})

    def cells(agent, task=None):
        selected = [r for r in runs if r["agent"] == agent and (task is None or r["task"] == task)]
        return selected

    lines = [
        "# Combined benchmark",
        "",
        f"Runs: {', '.join(f'`{label}`' for label in labels)}.",
        "",
        "## Overall",
        "",
        "| Agent | Runs | Pass | Score | Avg wall (s) | Avg LLM (s) | Avg overhead (s) | Avg steps | Avg tool calls | Avg tokens | Avg cost | Cache |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for agent in agents:
        rs = cells(agent)
        costs = [cost_and_cache(r) for r in rs]
        lines.append(
            f"| {agent} | {len(rs)} | {sum(1 for r in rs if r['verification'].get('pass'))}/{len(rs)} | "
            f"{mean([float(r['verification'].get('score') or 0) for r in rs]):.3f} | "
            f"{mean([r['wall_sec'] for r in rs]):.1f} | "
            f"{mean([r['metrics']['llm_duration_ms'] / 1000 for r in rs]):.1f} | "
            f"{mean([r['wall_sec'] - r['metrics']['llm_duration_ms'] / 1000 for r in rs]):.1f} | "
            f"{mean([r['metrics']['llm_calls'] for r in rs]):.1f} | "
            f"{mean([r['metrics']['tool_calls'] for r in rs]):.1f} | "
            f"{mean([r['metrics']['total_tokens'] for r in rs]):,.0f} | "
            f"${mean([c[0] for c in costs]):.5f} | "
            f"{mean([c[1] for c in costs]) * 100:.0f}% |"
        )

    lines += [
        "",
        "## Per task (averaged over runs)",
        "",
        "| Task | Agent | Pass | Wall (s) | LLM (s) | Steps | Tool calls | Tokens |",
        "| --- | --- | :---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for task in tasks:
        for agent in agents:
            rs = cells(agent, task)
            if not rs:
                continue
            lines.append(
                f"| {task} | {agent} | {sum(1 for r in rs if r['verification'].get('pass'))}/{len(rs)} | "
                f"{mean([r['wall_sec'] for r in rs]):.1f} | "
                f"{mean([r['metrics']['llm_duration_ms'] / 1000 for r in rs]):.1f} | "
                f"{mean([r['metrics']['llm_calls'] for r in rs]):.1f} | "
                f"{mean([r['metrics']['tool_calls'] for r in rs]):.1f} | "
                f"{mean([r['metrics']['total_tokens'] for r in rs]):,.0f} |"
            )

    lines += [
        "",
        "## Head-to-head deltas (PicoClaw vs Hermes, task averages)",
        "",
        "| Task | Wall Δ | Steps Δ | Tool calls Δ | Tokens Δ | Pass P / H |",
        "| --- | ---: | ---: | ---: | ---: | :---: |",
    ]
    for task in tasks:
        p = cells("picoclaw", task)
        h = cells("hermes", task)
        if not p or not h:
            continue
        def avg(rs, key):
            return mean([r[key] for r in rs])
        pw, hw = avg(p, "wall_sec"), avg(h, "wall_sec")
        ps = mean([r["metrics"]["llm_calls"] for r in p])
        hs = mean([r["metrics"]["llm_calls"] for r in h])
        pc = mean([r["metrics"]["tool_calls"] for r in p])
        hc = mean([r["metrics"]["tool_calls"] for r in h])
        pt = mean([r["metrics"]["total_tokens"] for r in p])
        ht = mean([r["metrics"]["total_tokens"] for r in h])
        pp = sum(1 for r in p if r["verification"].get("pass"))
        hp = sum(1 for r in h if r["verification"].get("pass"))
        lines.append(
            f"| {task} | {pw - hw:+.1f}s ({(pw / hw - 1) * 100:+.0f}%) | {ps - hs:+.1f} | "
            f"{pc - hc:+.1f} | {pt - ht:+,.0f} ({(pt / ht - 1) * 100:+.0f}%) | {pp}/{len(p)} / {hp}/{len(h)} |"
        )
    lines.append("")
    out = HERE / "results" / ("combined-" + "-".join(labels) + ".md")
    out.write_text("\n".join(lines))
    (HERE / "results" / ("combined-" + "-".join(labels) + ".json")).write_text(
        json.dumps({"labels": labels, "runs": runs}, indent=2)
    )
    print("\n".join(lines))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
