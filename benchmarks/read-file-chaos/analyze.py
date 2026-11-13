#!/usr/bin/env python3
"""Turn a read-file-chaos summary.json into a compact report.

Usage: python3 analyze.py <results-dir>
"""
from __future__ import annotations

import json
import statistics
import sys
from collections import Counter, defaultdict
from pathlib import Path


def pct(v: float) -> str:
    return f"{v * 100:.1f}%"


def main() -> int:
    root = Path(sys.argv[1]).resolve()
    summary = json.loads((root / "summary.json").read_text())
    buckets = summary["aggregate"]
    per_task = summary["per_task"]
    results = summary["results"]

    base = buckets.get("baseline", {})
    cand = buckets.get("candidate", {})

    lines = [
        f"# read_file bytes-vs-auto A/B benchmark — {summary['label']}",
        "",
        f"Model: `{summary['model']}` via `{summary['provider']}`. "
        f"Tasks: {len(summary['tasks'])}, repeats per task: {summary['reps']}, "
        f"runs: {len(results)}.",
        "",
        "## Scoring",
        "",
        "Composite score = **50% task verification + 25% tool-description compliance + "
        "15% efficiency + 10% robustness**. Pass means every task verifier check passed.",
        "",
        "## Aggregate",
        "",
        "| Variant | Runs | Pass | Verify | Compliance | Efficiency | Robustness | Composite | Avg wall | Avg LLM calls | Avg tool calls | Avg tokens | Timeouts | LLM errors |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for name in ("baseline", "candidate"):
        b = buckets.get(name)
        if not b:
            continue
        lines.append(
            f"| {name} | {b['runs']} | {b['passed']}/{b['runs']} ({pct(b['pass_rate'])}) | "
            f"{b['avg_verify']:.3f} | {b['avg_compliance']:.3f} | {b['avg_efficiency']:.3f} | "
            f"{b['avg_robustness']:.3f} | **{b['avg_composite']:.3f}** | {b['avg_wall_sec']:.1f}s | "
            f"{b['avg_llm_calls']:.1f} | {b['avg_tool_calls']:.1f} | {b['avg_tokens']:,} | "
            f"{b['timeouts']} | {b['llm_errors']} |"
        )

    def prompt_stats(variant: str) -> tuple[float, float, int]:
        rs = [r for r in results if r["variant"] == variant]
        calls = sum(r["metrics"]["llm_calls"] for r in rs)
        prompt = sum(r["metrics"]["prompt_tokens"] for r in rs)
        cached = sum(r["metrics"]["cached_tokens"] for r in rs)
        return (prompt / calls if calls else 0.0, cached / prompt if prompt else 0.0, calls)

    base_ppc, base_cache, base_calls = prompt_stats("baseline")
    cand_ppc, cand_cache, cand_calls = prompt_stats("candidate")

    lines += [
        "",
        "## Overhead",
        "",
        "| Metric | Baseline | Candidate | Delta |",
        "| --- | ---: | ---: | ---: |",
        f"| read_file definition (chars) | {base.get('avg_read_file_def_chars', 0):,} | {cand.get('avg_read_file_def_chars', 0):,} | "
        f"{cand.get('avg_read_file_def_chars', 0) - base.get('avg_read_file_def_chars', 0):+,} |",
        f"| read_file definition (estimated tokens) | {base.get('avg_read_file_def_estimate', 0):,} | {cand.get('avg_read_file_def_estimate', 0):,} | "
        f"{cand.get('avg_read_file_def_estimate', 0) - base.get('avg_read_file_def_estimate', 0):+,} |",
        f"| first prompt tokens | {base.get('avg_first_prompt_tokens', 0):,} | {cand.get('avg_first_prompt_tokens', 0):,} | "
        f"{cand.get('avg_first_prompt_tokens', 0) - base.get('avg_first_prompt_tokens', 0):+,} |",
        f"| read_file calls per run | {base.get('avg_read_file_calls', 0):.2f} | {cand.get('avg_read_file_calls', 0):.2f} | "
        f"{cand.get('avg_read_file_calls', 0) - base.get('avg_read_file_calls', 0):+.2f} |",
        f"| read_file result chars per run | {base.get('avg_read_file_result_chars', 0):,} | {cand.get('avg_read_file_result_chars', 0):,} | "
        f"{cand.get('avg_read_file_result_chars', 0) - base.get('avg_read_file_result_chars', 0):+,} |",
        f"| line-numbered read results per run | {base.get('avg_read_file_line_numbered', 0):.2f} | {cand.get('avg_read_file_line_numbered', 0):.2f} | "
        f"{cand.get('avg_read_file_line_numbered', 0) - base.get('avg_read_file_line_numbered', 0):+.2f} |",
        f"| raw-byte read results per run | {base.get('avg_read_file_byte_headers', 0):.2f} | {cand.get('avg_read_file_byte_headers', 0):.2f} | "
        f"{cand.get('avg_read_file_byte_headers', 0) - base.get('avg_read_file_byte_headers', 0):+.2f} |",
        f"| prompt tokens per LLM call | {base_ppc:,.1f} | {cand_ppc:,.1f} | "
        f"{cand_ppc - base_ppc:+,.1f} |",
        f"| cached prompt share | {base_cache:.1%} | {cand_cache:.1%} | "
        f"{(cand_cache - base_cache) * 100:+.1f} pp |",
        f"| total tokens per run | {base.get('avg_tokens', 0):,} | {cand.get('avg_tokens', 0):,} | "
        f"{cand.get('avg_tokens', 0) - base.get('avg_tokens', 0):+,} |",
    ]

    wins = ties = losses = 0
    task_rows = []
    for task, row in per_task.items():
        b = row.get("baseline")
        c = row.get("candidate")
        if not b or not c:
            continue
        delta = c["avg_composite"] - b["avg_composite"]
        if delta > 0.01:
            wins += 1
        elif delta < -0.01:
            losses += 1
        else:
            ties += 1
        task_rows.append((task, b, c, delta))

    pairs: dict[tuple[str, int], dict[str, float]] = defaultdict(dict)
    for r in results:
        pairs[(r["task"], r["rep"])][r["variant"]] = r["scores"]["composite"]
    paired = [
        (key[0], key[1], vals["candidate"] - vals["baseline"])
        for key, vals in pairs.items()
        if "baseline" in vals and "candidate" in vals
    ]
    paired_deltas = [d for _, _, d in paired]
    paired_wins = sum(1 for d in paired_deltas if d > 0.01)
    paired_losses = sum(1 for d in paired_deltas if d < -0.01)
    paired_ties = len(paired_deltas) - paired_wins - paired_losses
    mean_delta = statistics.mean(paired_deltas) if paired_deltas else 0.0
    stdev_delta = statistics.pstdev(paired_deltas) if len(paired_deltas) > 1 else 0.0

    lines += [
        "",
        "## Paired comparison",
        "",
        f"- Pairs (task × repeat): **{len(paired_deltas)}**.",
        f"- Candidate wins/losses/ties: **{paired_wins}/{paired_losses}/{paired_ties}** "
        "(delta threshold ±0.01).",
        f"- Mean paired composite delta: **{mean_delta:+.3f}** "
        f"(stdev {stdev_delta:.3f}).",
        "",
        "## Head-to-head",
        "",
        f"- Candidate better on **{wins}** tasks, worse on **{losses}**, tied on **{ties}**.",
        f"- Baseline pass rate: **{pct(base.get('pass_rate', 0))}**; candidate: "
        f"**{pct(cand.get('pass_rate', 0))}**.",
        f"- Composite delta (candidate - baseline): "
        f"**{cand.get('avg_composite', 0) - base.get('avg_composite', 0):+.3f}**.",
        f"- Token delta per run: **{cand.get('avg_tokens', 0) - base.get('avg_tokens', 0):+,}** "
        f"({(cand.get('avg_tokens', 0) - base.get('avg_tokens', 0)) / max(1, base.get('avg_tokens', 1)) * 100:+.1f}%).",
        "",
        "## Per-task",
        "",
        "| Task | Baseline pass | Candidate pass | Baseline comp. | Candidate comp. | Δ | Baseline tools | Candidate tools | Baseline read_file | Candidate read_file |",
        "| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for task, b, c, delta in task_rows:
        lines.append(
            f"| {task} | {pct(b['pass_rate'])} | {pct(c['pass_rate'])} | "
            f"{b['avg_composite']:.3f} | {c['avg_composite']:.3f} | {delta:+.3f} | "
            f"{b['avg_tool_calls']:.1f} | {c['avg_tool_calls']:.1f} | "
            f"{b.get('avg_read_file_calls', 0):.1f} | {c.get('avg_read_file_calls', 0):.1f} |"
        )

    models = sorted({m for r in results for m in (r["metrics"].get("models") or [])})
    lines += [
        "",
        "## Model audit",
        "",
        f"- Models observed in proxy logs: `{', '.join(models) if models else 'none'}`.",
        f"- Expected: `{summary['model']}` only.",
        "",
    ]

    # Failure signatures
    failures: dict[str, Counter] = defaultdict(Counter)
    for r in results:
        for c in r["verification"]["checks"]:
            if not c["ok"]:
                failures[r["variant"]]["verify: " + c["name"]] += 1
        for c in r["compliance"]["checks"]:
            if not c["ok"]:
                failures[r["variant"]]["compliance: " + c["name"]] += 1

    lines += ["", "## Failed checks", ""]
    for variant in ("baseline", "candidate"):
        lines.append(f"### {variant}")
        if not failures[variant]:
            lines.append("No failed checks.")
        else:
            for name, count in failures[variant].most_common(20):
                lines.append(f"- {count}× {name}")
        lines.append("")

    # Candidate-only wins / baseline-only wins
    cand_wins = []
    base_wins = []
    for task, b, c, delta in task_rows:
        if delta > 0.01:
            cand_wins.append((task, delta, b, c))
        elif delta < -0.01:
            base_wins.append((task, delta, b, c))

    lines += ["## Notable differences", ""]
    if cand_wins:
        lines.append("Candidate better:")
        for task, delta, b, c in sorted(cand_wins, key=lambda x: -x[1])[:10]:
            lines.append(
                f"- **{task}** ({delta:+.3f}): pass {pct(b['pass_rate'])} → {pct(c['pass_rate'])}, "
                f"tokens {b['avg_tokens']:,} → {c['avg_tokens']:,}, "
                f"tool calls {b['avg_tool_calls']:.1f} → {c['avg_tool_calls']:.1f}"
            )
        lines.append("")
    if base_wins:
        lines.append("Baseline better:")
        for task, delta, b, c in sorted(base_wins, key=lambda x: x[1])[:10]:
            lines.append(
                f"- **{task}** ({delta:+.3f}): pass {pct(b['pass_rate'])} → {pct(c['pass_rate'])}, "
                f"tokens {b['avg_tokens']:,} → {c['avg_tokens']:,}, "
                f"tool calls {b['avg_tool_calls']:.1f} → {c['avg_tool_calls']:.1f}"
            )
        lines.append("")

    # Per-run composite spread for the main aggregate.
    lines += ["## Repeatability", ""]
    for variant in ("baseline", "candidate"):
        vals = [r["scores"]["composite"] for r in results if r["variant"] == variant]
        if vals:
            lines.append(
                f"- {variant}: mean {statistics.mean(vals):.3f}, "
                f"stdev {statistics.pstdev(vals):.3f}, min {min(vals):.3f}, max {max(vals):.3f}"
            )
    lines.append("")

    report = root / "REPORT.md"
    report.write_text("\n".join(lines) + "\n")
    print("\n".join(lines))
    print(f"\nWrote {report}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
