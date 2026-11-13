#!/usr/bin/env python3
"""Generate deterministic workspaces for the PicoClaw vs Hermes benchmark."""
from __future__ import annotations

import csv
import json
import random
from pathlib import Path

ROOT = Path(__file__).resolve().parent
TASKS = ROOT / "tasks"


def write_task(task_id: str, title: str, prompt: str, timeout: int = 480, tags=None):
    task = {
        "id": task_id,
        "title": title,
        "prompt": prompt,
        "timeout_sec": timeout,
        "tags": tags or [],
    }
    (TASKS / task_id).mkdir(parents=True, exist_ok=True)
    (TASKS / task_id / "task.json").write_text(json.dumps(task, indent=2) + "\n")
    (TASKS / task_id / "workspace").mkdir(parents=True, exist_ok=True)


def write(rel: str, content: str):
    path = TASKS / rel
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)


# --------------------------------------------------------------------------
# 1. log triage
# --------------------------------------------------------------------------
write_task(
    "log-triage",
    "Incident log triage",
    "Analyze app.log in the current directory. Count how many times each distinct ERROR message occurs "
    "(normalize timestamps and request ids so identical failures group together). Write report.md with: "
    "(1) a Markdown table of ERROR types and counts sorted from most to least frequent, (2) the single most "
    "frequent failure, and (3) a short root-cause analysis (3-5 sentences) with one concrete remediation. "
    "Do not modify app.log and do not write any other files.",
    tags=["ops", "analysis", "files"],
)
random.seed(7)
lines = []
errors = [
    ("db timeout: connection pool exhausted", 37),
    ("redis unavailable: dial tcp 10.0.0.5:6379: connect: connection refused", 22),
    ("panic recovered: assignment to entry in nil map", 9),
    ("payment gateway returned HTTP 502", 5),
]
sec = 0
for i in range(446):
    sec += 3
    ts = f"2026-11-01T{8 + sec // 3600:02d}:{(sec // 60) % 60:02d}:{sec % 60:02d}Z"
    if i % 9 == 0:
        lines.append(f"{ts} WARN req={i:04d} slow query took {random.randint(20, 90) / 10:.1f}s")
    else:
        lines.append(f"{ts} INFO req={i:04d} method=GET path=/api/orders/{random.randint(1, 900)} status=200 duration={random.randint(3, 90)}ms")
for msg, count in errors:
    for _ in range(count):
        sec += 1
        ts = f"2026-11-01T{8 + sec // 3600:02d}:{(sec // 60) % 60:02d}:{sec % 60:02d}Z"
        lines.append(f"{ts} ERROR req={random.randint(1000, 9999)} {msg}")
random.shuffle(lines)
write("log-triage/workspace/app.log", "\n".join(lines) + "\n")

# --------------------------------------------------------------------------
# 2. bug fix (Python, stdlib unittest)
# --------------------------------------------------------------------------
write_task(
    "bug-fix",
    "Fix a failing pricing test",
    "The Python test suite in this directory fails. Find the bug in pricing.py, fix it, and make "
    "`python3 -m unittest -v` pass. Do not modify the tests. Explain the root cause in your final answer.",
    tags=["coding", "debugging", "tests"],
)
write(
    "bug-fix/workspace/pricing.py",
    '''"""Order pricing helpers."""


def apply_discount(price_cents: int, percent: int) -> int:
    """Return the price after a percentage discount, rounded to the nearest cent."""
    if not 0 <= percent <= 100:
        raise ValueError("percent must be between 0 and 100")
    discount = price_cents * percent // 100
    return price_cents - discount


def order_total(prices_cents: list[int], percent: int) -> int:
    """Apply the same discount to every line and return the order total."""
    return sum(apply_discount(p, percent) for p in prices_cents)
''',
)
write(
    "bug-fix/workspace/test_pricing.py",
    '''import unittest

from pricing import apply_discount, order_total


class ApplyDiscountTests(unittest.TestCase):
    def test_rounds_half_up_to_nearest_cent(self):
        # 15% of 1999 = 299.85 -> 300; 1999 - 300 = 1699
        self.assertEqual(apply_discount(1999, 15), 1699)

    def test_rounds_up_above_half_cent(self):
        # 10% of 1995 = 199.5 -> 200; 1995 - 200 = 1795
        self.assertEqual(apply_discount(1995, 10), 1795)

    def test_exact_values_are_unchanged(self):
        self.assertEqual(apply_discount(2000, 25), 1500)
        self.assertEqual(apply_discount(1000, 0), 1000)
        self.assertEqual(apply_discount(1000, 100), 0)

    def test_rejects_out_of_range_percent(self):
        with self.assertRaises(ValueError):
            apply_discount(1000, -1)
        with self.assertRaises(ValueError):
            apply_discount(1000, 101)


class OrderTotalTests(unittest.TestCase):
    def test_applies_discount_per_line(self):
        # 1999 -> 1699, 1995 -> 1696 (299.25 rounds to 299), 2000 -> 1700
        self.assertEqual(order_total([1999, 1995, 2000], 15), 1699 + 1696 + 1700)


if __name__ == "__main__":
    unittest.main()
''',
)

# --------------------------------------------------------------------------
# 3. feature add (CLI todo with priorities)
# --------------------------------------------------------------------------
write_task(
    "feature-add",
    "Add priorities to a todo CLI",
    "Read README.md and todo.py in this directory. Implement the priority feature described in the "
    "README: `add` accepts `--priority {high,medium,low}` (default medium), and `list` accepts an "
    "optional `--priority` filter. Keep the existing JSON file format working for todos written before "
    "this change (missing priority means medium). Run the CLI yourself to check it, and leave the "
    "working directory clean apart from todos.json if you create one.",
    tags=["coding", "feature", "cli"],
)
write(
    "feature-add/workspace/README.md",
    """# todo CLI

A tiny JSON-backed todo manager.

## Commands

- `python3 todo.py add "text"` — add a todo (default priority: `medium`).
- `python3 todo.py list` — list all todos as `[ ]` or `[x]` plus their text.
- `python3 todo.py done <id>` — mark a todo done.

## Priority feature (to implement)

- `add` gains `--priority {high,medium,low}`, default `medium`.
- `list` gains an optional `--priority {high,medium,low}` filter.
- Todos created before this feature have no `priority` field and must be treated as `medium`.
- Invalid priority values must exit non-zero with a clear message.
- Keep the stored JSON shape: a list of objects with `id`, `text`, `done`, and now `priority`.
""",
)
write(
    "feature-add/workspace/todo.py",
    '''#!/usr/bin/env python3
"""Tiny JSON-backed todo CLI."""
import argparse
import json
import sys
from pathlib import Path

STORE = Path("todos.json")


def load():
    if not STORE.exists():
        return []
    return json.loads(STORE.read_text() or "[]")


def save(todos):
    STORE.write_text(json.dumps(todos, indent=2) + "\\n")


def cmd_add(args):
    todos = load()
    next_id = max((t["id"] for t in todos), default=0) + 1
    todos.append({"id": next_id, "text": args.text, "done": False})
    save(todos)
    print(f"added #{next_id}")


def cmd_list(args):
    for todo in load():
        mark = "x" if todo.get("done") else " "
        print(f"[{mark}] #{todo['id']} {todo['text']}")


def cmd_done(args):
    todos = load()
    for todo in todos:
        if todo["id"] == args.id:
            todo["done"] = True
            save(todos)
            print(f"done #{args.id}")
            return
    print(f"no todo with id {args.id}", file=sys.stderr)
    raise SystemExit(1)


def main(argv=None):
    parser = argparse.ArgumentParser()
    sub = parser.add_subparsers(dest="command", required=True)
    add = sub.add_parser("add")
    add.add_argument("text")
    add.set_defaults(func=cmd_add)
    listing = sub.add_parser("list")
    listing.set_defaults(func=cmd_list)
    done = sub.add_parser("done")
    done.add_argument("id", type=int)
    done.set_defaults(func=cmd_done)
    args = parser.parse_args(argv)
    args.func(args)


if __name__ == "__main__":
    main()
''',
)
write(
    "feature-add/workspace/todos.json",
    json.dumps(
        [
            {"id": 1, "text": "legacy item without priority", "done": False},
            {"id": 2, "text": "already finished", "done": True},
        ],
        indent=2,
    )
    + "\n",
)

# --------------------------------------------------------------------------
# 4. data transformation
# --------------------------------------------------------------------------
write_task(
    "data-transform",
    "Aggregate a sales CSV",
    "Write aggregate.py in this directory. It must read sales.csv and write summary.json with exactly "
    "this shape: {\"total_revenue\": <float rounded to 2 decimals>, \"by_region\": {<region>: <float "
    "rounded to 2 decimals>, ...}, \"top_product\": {\"name\": <string>, \"units\": <int>}}. Revenue "
    "for a row is units * unit_price. top_product is the product with the most units sold across all "
    "rows (ties break alphabetically). Run the script and leave both aggregate.py and summary.json.",
    tags=["data", "coding", "shell"],
)
random.seed(11)
regions = ["North", "South", "East", "West"]
products = ["widget", "gadget", "sprocket", "cog", "spring"]
rows = []
for i in range(140):
    rows.append(
        {
            "date": f"2026-{random.randint(1, 10):02d}-{random.randint(1, 28):02d}",
            "region": random.choice(regions),
            "product": random.choice(products),
            "units": random.randint(1, 25),
            "unit_price": f"{random.randint(100, 900) / 100:.2f}",
        }
    )
with open(TASKS / "data-transform/workspace/sales.csv", "w", newline="") as fh:
    writer = csv.DictWriter(fh, fieldnames=["date", "region", "product", "units", "unit_price"])
    writer.writeheader()
    writer.writerows(rows)

# --------------------------------------------------------------------------
# 5. repository Q&A
# --------------------------------------------------------------------------
write_task(
    "repo-qa",
    "Answer questions about a small service",
    "Read the inventory/ package and answer these questions in answers.md, numbered 1-4, with file and "
    "function references: (1) Which function decrements stock when an item is reserved? (2) What "
    "exception is raised when stock is insufficient, and where is it defined? (3) Is reserve_item "
    "atomic with respect to concurrent callers? Explain why or why not. (4) Which function computes the "
    "reorder point and what formula does it use? Do not modify any source file.",
    tags=["analysis", "code-reading", "docs"],
)
write(
    "repo-qa/workspace/inventory/__init__.py",
    '"""Tiny inventory service used as a reading exercise."""\n',
)
write(
    "repo-qa/workspace/inventory/models.py",
    '''"""Domain models and pure calculations."""
from dataclasses import dataclass


class InsufficientStock(Exception):
    """Raised when a reservation asks for more units than are available."""


@dataclass
class Item:
    sku: str
    name: str
    stock: int
    daily_sales: float
    lead_time_days: int

    def reorder_point(self) -> float:
        """Stock level that triggers a replenishment order.

        Safety stock is one standard deviation of daily sales (approximated by
        20% of the mean) times the square root of the lead time.
        """
        safety = 0.2 * self.daily_sales * (self.lead_time_days ** 0.5)
        return self.daily_sales * self.lead_time_days + safety
''',
)
write(
    "repo-qa/workspace/inventory/storage.py",
    '''"""In-memory storage. Not thread safe."""
from .models import Item


class ItemStore:
    def __init__(self):
        self._items = {}

    def put(self, item: Item) -> None:
        self._items[item.sku] = item

    def get(self, sku: str) -> Item:
        return self._items[sku]

    def all(self):
        return list(self._items.values())
''',
)
write(
    "repo-qa/workspace/inventory/service.py",
    '''"""Reservation workflow."""
from .models import InsufficientStock
from .storage import ItemStore


class InventoryService:
    def __init__(self, store: ItemStore):
        self.store = store

    def reserve_item(self, sku: str, units: int) -> int:
        """Reserve units and return the remaining stock.

        The read and the write are two separate operations on the store, so
        two concurrent callers can both observe the same stock level.
        """
        item = self.store.get(sku)
        if item.stock < units:
            raise InsufficientStock(f"{sku}: requested {units}, available {item.stock}")
        item.stock -= units
        return item.stock

    def needs_reorder(self, sku: str) -> bool:
        item = self.store.get(sku)
        return item.stock <= item.reorder_point()
''',
)
write(
    "repo-qa/workspace/README.md",
    """# inventory

Small in-memory inventory service.

- `inventory/models.py`: `Item`, `InsufficientStock`, reorder-point calculation.
- `inventory/storage.py`: `ItemStore`, an in-memory dict wrapper.
- `inventory/service.py`: `InventoryService`, the reservation workflow.
""",
)

# --------------------------------------------------------------------------
# 6. refactor
# --------------------------------------------------------------------------
write_task(
    "refactor",
    "Rename a helper across a package",
    "Rename the function `calc_avg` to `mean` across the metrics package and its tests, including "
    "imports and call sites. Keep behavior identical and make `python3 -m unittest -v` pass. Do not "
    "leave a compatibility alias unless a test requires it.",
    tags=["coding", "refactor", "tests"],
)
write("refactor/workspace/metrics/__init__.py", "from .stats import calc_avg\n\n__all__ = [\"calc_avg\"]\n")
write(
    "refactor/workspace/metrics/stats.py",
    '''"""Small statistics helpers."""


def calc_avg(values):
    """Arithmetic mean of a non-empty sequence."""
    if not values:
        raise ValueError("calc_avg requires at least one value")
    return sum(values) / len(values)


def median(values):
    ordered = sorted(values)
    n = len(ordered)
    if n == 0:
        raise ValueError("median requires at least one value")
    mid = n // 2
    if n % 2:
        return ordered[mid]
    return (ordered[mid - 1] + ordered[mid]) / 2
''',
)
write(
    "refactor/workspace/metrics/report.py",
    '''from .stats import calc_avg


def summarize(values):
    return {"count": len(values), "avg": calc_avg(values)}
''',
)
write(
    "refactor/workspace/test_metrics.py",
    '''import unittest

from metrics import calc_avg
from metrics.report import summarize
from metrics.stats import calc_avg as direct_avg, median


class StatsTests(unittest.TestCase):
    def test_calc_avg(self):
        self.assertEqual(calc_avg([2, 4, 6]), 4)
        self.assertEqual(direct_avg([1]), 1)

    def test_calc_avg_rejects_empty(self):
        with self.assertRaises(ValueError):
            calc_avg([])

    def test_median(self):
        self.assertEqual(median([3, 1, 2]), 2)
        self.assertEqual(median([1, 2, 3, 4]), 2.5)

    def test_summarize(self):
        self.assertEqual(summarize([2, 4, 6]), {"count": 3, "avg": 4})


if __name__ == "__main__":
    unittest.main()
''',
)

# --------------------------------------------------------------------------
# 7. web research
# --------------------------------------------------------------------------
write_task(
    "web-research",
    "Web research brief",
    "Use web search and/or web fetch to find the latest stable Go release (the version advertised on "
    "go.dev). Write go-release.md containing the version, its release date, and the source URL. Keep it "
    "under 150 words. Do not guess: if the network is unavailable, say so explicitly in the file.",
    tags=["research", "web"],
    timeout=300,
)

print(f"generated tasks under {TASKS}")
