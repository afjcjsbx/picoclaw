#!/usr/bin/env python3
"""Deterministic verifier for benchmark tasks.

Usage: python3 verify.py <task-id> <workspace>
Prints a JSON result with per-check outcomes and a 0..1 score.
"""
from __future__ import annotations

import csv
import hashlib
import json
import re
import subprocess
import sys
from pathlib import Path

TASK_ROOT = Path(__file__).resolve().parent


def sha(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def original(task: str, rel: str) -> Path:
    return TASK_ROOT / "tasks" / task / "workspace" / rel


def run(cmd, cwd, timeout=120):
    try:
        proc = subprocess.run(
            cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout
        )
        return proc.returncode, proc.stdout, proc.stderr
    except subprocess.TimeoutExpired:
        return 124, "", "timeout"


def check(results, name, ok, detail=""):
    results.append({"name": name, "ok": bool(ok), "detail": detail})
    return ok


def verify_log_triage(ws: Path):
    r = []
    report = ws / "report.md"
    check(r, "report.md exists", report.exists())
    if report.exists():
        text = report.read_text(errors="replace")
        low = text.lower()
        rows = [l for l in text.splitlines() if l.count("|") >= 2]
        by_keyword = []
        for keyword, count in [
            ("connection pool", 37),
            ("redis", 22),
            ("nil map", 9),
            ("payment", 5),
        ]:
            row = next((l for l in rows if keyword in l.lower()), "")
            by_keyword.append((keyword, count, row, str(count) in row))
            check(r, f"{keyword} count {count}", str(count) in row, row.strip()[:200])
        check(r, "mentions all error types", all(k in low for k, _, _, _ in by_keyword))
        check(r, "markdown table present", len(rows) >= 5, f"{len(rows)} rows")
        check(r, "root cause section", any(k in low for k in ["root cause", "causa", "because", "likely"]))
        check(r, "actionable remediation", any(k in low for k in ["fix", "increase", "pool", "retry", "remediat", "action"]))
    check(r, "app.log unchanged", sha(ws / "app.log") == sha(original("log-triage", "app.log")))
    extras = sorted(
        p.name
        for p in ws.iterdir()
        if p.name not in {"app.log", "report.md"}
        and p.name not in {".artifacts", "memory", "sessions", "state", "logs"}
    )
    check(r, "no extra files", not extras, ", ".join(extras))
    return r


def verify_bug_fix(ws: Path):
    r = []
    check(r, "tests unchanged", sha(ws / "test_pricing.py") == sha(original("bug-fix", "test_pricing.py")))
    code, out, err = run([sys.executable, "-m", "unittest", "-v"], ws)
    check(r, "unittest passes", code == 0, (out + err)[-1500:])
    probe = (
        "import pricing;"
        "assert pricing.apply_discount(1999,15)==1699;"
        "assert pricing.apply_discount(1995,10)==1795;"
        "assert pricing.apply_discount(2000,25)==1500;"
        "assert pricing.order_total([1999,1995,2000],15)==5095;"
        "print('ok')"
    )
    code, out, err = run([sys.executable, "-c", probe], ws)
    check(r, "rounding behavior", code == 0, (out + err)[-800:])
    return r


def verify_feature_add(ws: Path):
    r = []
    todo = ws / "todo.py"
    check(r, "todo.py exists", todo.exists())
    code, out, err = run([sys.executable, "todo.py", "list"], ws)
    check(r, "legacy todos still list", code == 0 and "legacy item" in out, out + err)

    code, out, err = run([sys.executable, "todo.py", "add", "urgent task", "--priority", "high"], ws)
    check(r, "add --priority high exits 0", code == 0, out + err)
    code, out, err = run([sys.executable, "todo.py", "add", "bogus priority", "--priority", "urgent"], ws)
    check(r, "invalid priority rejected", code != 0, out + err)

    try:
        todos = json.loads((ws / "todos.json").read_text())
        by_text = {t["text"]: t for t in todos}
        check(r, "high priority persisted", by_text.get("urgent task", {}).get("priority") == "high")
        check(r, "legacy todo has no forced priority", "priority" not in by_text.get("legacy item without priority", {}))
    except Exception as exc:  # noqa: BLE001
        check(r, "todos.json valid", False, str(exc))

    code, out, err = run([sys.executable, "todo.py", "list", "--priority", "high"], ws)
    check(r, "filter high includes urgent", code == 0 and "urgent task" in out, out + err)
    check(r, "filter high excludes legacy", "legacy item" not in out, out)
    code, out, err = run([sys.executable, "todo.py", "list", "--priority", "medium"], ws)
    check(r, "legacy treated as medium", code == 0 and "legacy item" in out, out + err)
    code, out, err = run([sys.executable, "todo.py", "add", "default task"], ws)
    try:
        todos = json.loads((ws / "todos.json").read_text())
        by_text = {t["text"]: t for t in todos}
        check(r, "default priority medium", by_text.get("default task", {}).get("priority") == "medium")
    except Exception as exc:  # noqa: BLE001
        check(r, "default priority persisted", False, str(exc))
    return r


def expected_summary():
    with open(TASK_ROOT / "tasks/data-transform/workspace/sales.csv", newline="") as fh:
        rows = list(csv.DictReader(fh))
    by_region: dict[str, float] = {}
    by_product: dict[str, int] = {}
    total = 0.0
    for row in rows:
        revenue = int(row["units"]) * float(row["unit_price"])
        total += revenue
        by_region[row["region"]] = by_region.get(row["region"], 0.0) + revenue
        by_product[row["product"]] = by_product.get(row["product"], 0) + int(row["units"])
    top_units = max(by_product.values())
    top = sorted(p for p, u in by_product.items() if u == top_units)[0]
    return {
        "total_revenue": round(total, 2),
        "by_region": {k: round(v, 2) for k, v in sorted(by_region.items())},
        "top_product": {"name": top, "units": top_units},
    }


def verify_data_transform(ws: Path):
    r = []
    check(r, "aggregate.py exists", (ws / "aggregate.py").exists())
    check(r, "summary.json exists", (ws / "summary.json").exists())
    if (ws / "summary.json").exists():
        try:
            got = json.loads((ws / "summary.json").read_text())
            want = expected_summary()
            check(r, "total_revenue", abs(float(got.get("total_revenue", -1)) - want["total_revenue"]) <= 0.01,
                  f"got {got.get('total_revenue')} want {want['total_revenue']}")
            check(r, "by_region", got.get("by_region") == want["by_region"],
                  f"got {got.get('by_region')} want {want['by_region']}")
            check(r, "top_product", got.get("top_product") == want["top_product"],
                  f"got {got.get('top_product')} want {want['top_product']}")
        except Exception as exc:  # noqa: BLE001
            check(r, "summary.json valid", False, str(exc))
    # Re-running the script must be deterministic.
    code, out, err = run([sys.executable, "aggregate.py"], ws)
    check(r, "aggregate.py reruns", code == 0, (out + err)[-800:])
    return r


def verify_repo_qa(ws: Path):
    r = []
    answers = ws / "answers.md"
    check(r, "answers.md exists", answers.exists())
    if answers.exists():
        low = answers.read_text(errors="replace").lower()
        for label, needles in [
            ("Q1 reserve_item", ["reserve_item"]),
            ("Q1 service.py", ["service.py"]),
            ("Q2 InsufficientStock", ["insufficientstock"]),
            ("Q2 models.py", ["models.py"]),
            ("Q3 not atomic", ["not atomic", "non-atomic", "race", "not thread", "no lock", "concurrent"]),
            ("Q4 reorder_point", ["reorder_point"]),
            ("Q4 formula", ["daily_sales", "0.2", "lead_time"]),
        ]:
            check(r, label, any(n in low for n in needles))
        numbers = re.findall(r"^\s*(?:1|2|3|4)[.)]", answers.read_text(), re.M)
        check(r, "numbered 1-4", len(set(numbers)) >= 4, str(numbers))
    for rel in ["inventory/models.py", "inventory/service.py", "inventory/storage.py"]:
        check(r, f"{rel} unchanged", sha(ws / rel) == sha(original("repo-qa", rel)))
    return r


def verify_refactor(ws: Path):
    r = []
    code, out, err = run(
        ["grep", "-RIn", "--include=*.py", "calc_avg", "."], ws
    )
    check(r, "no calc_avg references", code != 0, out[:1000])
    code, out, err = run([sys.executable, "-m", "unittest", "-v"], ws)
    check(r, "unittest passes", code == 0, (out + err)[-1500:])
    probe = (
        "from metrics import mean;"
        "from metrics.stats import mean as m2;"
        "assert mean([2,4,6])==4 and m2([1])==1;"
        "from metrics.report import summarize;"
        "assert summarize([2,4,6])=={'count':3,'avg':4};"
        "print('ok')"
    )
    code, out, err = run([sys.executable, "-c", probe], ws)
    check(r, "mean exported and works", code == 0, (out + err)[-800:])
    return r


def verify_web_research(ws: Path):
    r = []
    doc = ws / "go-release.md"
    check(r, "go-release.md exists", doc.exists())
    if doc.exists():
        text = doc.read_text(errors="replace")
        low = text.lower()
        check(r, "mentions go1.x", bool(re.search(r"go\s*1\.\d+", low)), text[:300])
        check(r, "has the current release go1.27.3", bool(re.search(r"go\s*1\.27\.3", low)), text[:300])
        check(r, "has release date 2026-10-19", "2026-10-19" in low or "october 19, 2026" in low, text[:300])
        check(r, "source url", "go.dev" in low, text[:300])
        check(r, "date", bool(re.search(r"(20\d\d[-/]\d\d[-/]\d\d)|(january|february|march|april|may|june|july|august|september|october|november|december)", low)))
        check(r, "short", len(text.split()) < 300, f"{len(text.split())} words")
    return r


VERIFIERS = {
    "log-triage": verify_log_triage,
    "bug-fix": verify_bug_fix,
    "feature-add": verify_feature_add,
    "data-transform": verify_data_transform,
    "repo-qa": verify_repo_qa,
    "refactor": verify_refactor,
    "web-research": verify_web_research,
}


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: verify.py <task-id> <workspace>", file=sys.stderr)
        return 2
    task, ws = sys.argv[1], Path(sys.argv[2])
    verifier = VERIFIERS.get(task)
    if not verifier:
        print(json.dumps({"error": f"unknown task {task}"}))
        return 2
    results = verifier(ws)
    passed = sum(1 for c in results if c["ok"])
    score = passed / len(results) if results else 0.0
    print(json.dumps({"task": task, "pass": passed == len(results), "score": round(score, 4),
                      "checks": results}, indent=2))
    return 0


if __name__ == "__main__":
    sys.exit(main())
