#!/usr/bin/env python3
"""Deterministic read_file chaos tasks for the bytes-vs-auto A/B benchmark."""
from __future__ import annotations

import json
import re
from pathlib import Path


def check(results: list, name: str, ok: bool, detail: str = "") -> bool:
    results.append({"name": name, "ok": bool(ok), "detail": str(detail)[:600]})
    return bool(ok)


def calls(state: dict) -> list:
    return state.get("metrics", {}).get("tool_calls_list", []) or []


def count_tool(state: dict, name: str) -> int:
    return sum(1 for c in calls(state) if c.get("name") == name)


def exec_calls(state: dict) -> list:
    return [c for c in calls(state) if c.get("name") == "exec"]


def exec_commands(state: dict) -> list[str]:
    out = []
    for c in exec_calls(state):
        cmd = (c.get("args") or {}).get("command")
        if isinstance(cmd, str):
            out.append(cmd)
    return out


def any_cmd_regex(state: dict, pattern: str) -> bool:
    return any(re.search(pattern, cmd) for cmd in exec_commands(state))


def final_lower(state: dict) -> str:
    return (state.get("final_content") or "").lower()


# ---------------------------------------------------------------------------
# fixtures
# ---------------------------------------------------------------------------


def setup_none(ws: Path) -> None:
    pass


def setup_config(ws: Path) -> None:
    (ws / "config.yaml").write_text(
        "server:\n"
        "  host: localhost\n"
        "  port: 8080\n"
        "  read_timeout: 30s\n"
        "database:\n"
        "  host: db.internal\n"
        "  port: 5432\n"
        "  pool: 10\n"
        "features:\n"
        "  tracing: true\n"
        "  metrics: true\n"
        "  debug: false\n"
    )


LOG_LINE_MARKER = "REQ-4321-CRITICAL"


def setup_log_line(ws: Path) -> None:
    lines = []
    for i in range(1, 5001):
        if i == 4321:
            lines.append(f"2026-11-01T10:00:00Z ERROR req={LOG_LINE_MARKER} message=queue drained")
        else:
            lines.append(f"2026-11-01T10:00:00Z INFO req={i:05d} status=200 duration={i % 90}ms")
    (ws / "app.log").write_text("\n".join(lines) + "\n")


WINDOW_LINES = [
    "WINDOW-START line=9000",
    "ALPHA-9001",
    "BETA-9002",
    "WINDOW-END line=9003",
]


def setup_log_window(ws: Path) -> None:
    lines = []
    for i in range(1, 12001):
        if 9000 <= i <= 9003:
            lines.append(WINDOW_LINES[i - 9000])
        else:
            lines.append(f"2026-11-01T10:00:00Z INFO seq={i:05d} payload=ok")
    (ws / "audit.log").write_text("\n".join(lines) + "\n")


MINIFIED_NEEDLE = "NEEDLE-7f3c9e"


def setup_minified(ws: Path) -> None:
    payload = {
        "items": [{"id": i, "name": f"item-{i:04d}", "enabled": i % 3 == 0} for i in range(4500)],
        "needle": MINIFIED_NEEDLE,
    }
    (ws / "bundle.min.json").write_text(json.dumps(payload, separators=(",", ":")))


BINARY_MARKER = b"PICO-BINARY-MARKER"
BINARY_SIZE = 4096


def setup_binary(ws: Path) -> None:
    (ws / "assets").mkdir(parents=True, exist_ok=True)
    data = b"\x89PNG" + b"\x00" * 4 + BINARY_MARKER
    data += b"\x00" * (BINARY_SIZE - len(data))
    (ws / "assets" / "logo.bin").write_bytes(data)


def setup_empty(ws: Path) -> None:
    (ws / "empty.txt").write_text("")


UNICODE_LINE = "Résumé final"


def setup_unicode(ws: Path) -> None:
    (ws / "notes.md").write_bytes(
        "Città di Milano\r\nLine two\r\n".encode() + UNICODE_LINE.encode() + b"\r\n"
    )


LONG_LINE_TOKEN = "TOK-8b2d1f"


def setup_long_line(ws: Path) -> None:
    lines = []
    for i in range(1, 401):
        if i == 123:
            lines.append("prefix-" + "x" * 30000 + f" TOKEN={LONG_LINE_TOKEN} " + "y" * 20000)
        else:
            lines.append(f"line {i:03d} normal payload")
    (ws / "events.txt").write_text("\n".join(lines) + "\n")


def setup_server(ws: Path) -> None:
    lines = ["package main", "", "import \"fmt\"", ""]
    for i in range(1, 13):
        lines.append(f"// filler line {i}")
    lines.append("const listenPort = 8080")
    lines.append("")
    for i in range(1, 60):
        lines.append(f"// trailing line {i}")
    lines.append("func main() { fmt.Println(listenPort) }")
    (ws / "server.go").write_text("\n".join(lines) + "\n")


MANUAL_SECTION_TITLE = "Deployment Rollback"
MANUAL_SECTION_LINE = 3001


def setup_manual(ws: Path) -> None:
    lines = []
    for i in range(1, 4001):
        if i % 500 == 1:
            section = i // 500 + 1
            title = MANUAL_SECTION_TITLE if section == 7 else f"Operational Topic {section:02d}"
            lines.append(f"## Section {section:02d}: {title}")
        else:
            lines.append(f"Step {i}: inspect service, verify health, and record the result.")
    (ws / "manual.md").write_text("\n".join(lines) + "\n")


STRUCTURED_TOKEN = "TOK-4c7a90"


def setup_structured_line(ws: Path) -> None:
    filler = '"key":"value",'
    payload = filler * 2500
    body = payload[:30000] + f" TOKEN={STRUCTURED_TOKEN} " + payload[30000:50000]
    lines = [f"line {i:03d} normal payload" for i in range(1, 401)]
    lines[122] = "prefix " + body
    (ws / "events.txt").write_text("\n".join(lines) + "\n")


BLOB_MARKER = b"MARKER-AT-90000"
BLOB_OFFSET = 90000


def setup_blob(ws: Path) -> None:
    data = bytearray(b"\x00" * 110000)
    data[BLOB_OFFSET:BLOB_OFFSET + len(BLOB_MARKER)] = BLOB_MARKER
    (ws / "blob.bin").write_bytes(bytes(data))


# ---------------------------------------------------------------------------
# verification
# ---------------------------------------------------------------------------


def verify_config(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "host localhost", "localhost" in content, content[:300])
    check(r, "port 8080", "8080" in content, content[:300])
    return r


def verify_log_line(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "exact request id", LOG_LINE_MARKER in content, content[:400])
    return r


def verify_log_window(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    for line in WINDOW_LINES:
        check(r, f"reports {line}", line in content, content[:500])
    return r


def verify_minified(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "exact needle value", MINIFIED_NEEDLE in content, content[:400])
    return r


def verify_binary(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    low = content.lower()
    check(r, "PNG hex prefix", "89504e47" in low.replace(" ", ""), content[:400])
    check(r, "1-based marker offset 9", bool(re.search(r"\b9\b", content)), content[:400])
    check(r, "file size 4096", "4096" in content, content[:400])
    return r


def verify_empty(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    low = content.lower()
    check(r, "says empty", "empty" in low or "yes" in low, content[:300])
    check(r, "size 0", bool(re.search(r"\b0\b", content)), content[:300])
    return r


def verify_unicode(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "exact line 3", UNICODE_LINE in content, content[:400])
    return r


def verify_long_line(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "exact token", LONG_LINE_TOKEN in content, content[:400])
    return r


def verify_edit(ws: Path, state: dict) -> list:
    r = []
    path = ws / "server.go"
    text = path.read_text() if path.exists() else ""
    check(r, "port changed to 9090", "listenPort = 9090" in text, text[:400])
    check(r, "old port removed", "8080" not in text, text[:400])
    content = state.get("final_content") or ""
    check(r, "reports line 17", bool(re.search(r"\b17\b", content)), content[:400])
    return r


def verify_manual(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "section title", MANUAL_SECTION_TITLE in content, content[:400])
    check(r, "section line 3001", "3001" in content, content[:400])
    return r


def verify_blob(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "marker offset 90000", "90000" in content or "90,000" in content, content[:400])
    return r


def verify_structured_line(ws: Path, state: dict) -> list:
    r = []
    content = state.get("final_content") or ""
    check(r, "exact structured token", STRUCTURED_TOKEN in content, content[:400])
    return r


# ---------------------------------------------------------------------------
# compliance
# ---------------------------------------------------------------------------


def compliance_read_file(state: dict) -> list:
    r = []
    check(r, "used read_file", count_tool(state, "read_file") >= 1,
          str([c.get("name") for c in calls(state)])[:300])
    check(r, "did not use shell read shortcuts",
          not any_cmd_regex(state, r"\b(cat|head|tail|less|sed|awk)\b"),
          str(exec_commands(state))[:300])
    return r


def compliance_read_file_no_search(state: dict) -> list:
    r = compliance_read_file(state)
    check(r, "did not use search_files", count_tool(state, "search_files") == 0,
          str([c.get("name") for c in calls(state)])[:300])
    return r


def compliance_binary(state: dict) -> list:
    r = []
    check(r, "used read_file", count_tool(state, "read_file") >= 1,
          str([c.get("name") for c in calls(state)])[:300])
    return r


def compliance_edit(state: dict) -> list:
    r = compliance_read_file(state)
    check(r, "used edit_file", count_tool(state, "edit_file") >= 1,
          str([c.get("name") for c in calls(state)])[:300])
    check(r, "did not use sed/awk", not any_cmd_regex(state, r"\b(sed|awk)\b"),
          str(exec_commands(state))[:300])
    return r


# ---------------------------------------------------------------------------
# task table
# ---------------------------------------------------------------------------

TASKS = [
    {
        "id": "config-lookup",
        "title": "Normal YAML lookup",
        "prompt": "Read `config.yaml` with read_file and report the server host and port exactly as "
                  "`host=<host> port=<port>`. Do not modify any file.",
        "setup": setup_config,
        "verify": verify_config,
        "compliance": compliance_read_file,
        "budget": {"max_llm_calls": 4, "max_tool_calls": 3, "max_wall": 60},
    },
    {
        "id": "log-line-4321",
        "title": "Find a specific line in a 5k-line log",
        "prompt": "Read `app.log` with read_file. Line 4321 contains an ERROR entry. Report the exact "
                  "req= value from that line. Use read_file, not exec or search_files.",
        "setup": setup_log_line,
        "verify": verify_log_line,
        "compliance": compliance_read_file_no_search,
        "budget": {"max_llm_calls": 6, "max_tool_calls": 5, "max_wall": 90},
    },
    {
        "id": "log-window-9000",
        "title": "Read a four-line window in a 12k-line log",
        "prompt": "Read `audit.log` with read_file. Report the four lines from line 9000 through line 9003, "
                  "each on its own line. Use read_file, not exec or search_files.",
        "setup": setup_log_window,
        "verify": verify_log_window,
        "compliance": compliance_read_file_no_search,
        "budget": {"max_llm_calls": 6, "max_tool_calls": 5, "max_wall": 90},
    },
    {
        "id": "minified-json",
        "title": "Needle at the end of a one-line 100KB JSON",
        "prompt": "Read `bundle.min.json` with read_file and report the exact value of the `needle` field. "
                  "Use read_file, not exec or search_files.",
        "setup": setup_minified,
        "verify": verify_minified,
        "compliance": compliance_read_file_no_search,
        "budget": {"max_llm_calls": 8, "max_tool_calls": 7, "max_wall": 120},
    },
    {
        "id": "binary-header",
        "title": "Binary file header and marker offset",
        "prompt": "Read `assets/logo.bin` with read_file as raw bytes. Report the first 4 bytes in lowercase "
                  "hex and the 1-based byte offset of the ASCII text PICO-BINARY-MARKER, plus the file size.",
        "setup": setup_binary,
        "verify": verify_binary,
        "compliance": compliance_binary,
        "budget": {"max_llm_calls": 5, "max_tool_calls": 4, "max_wall": 75},
    },
    {
        "id": "empty-file",
        "title": "Empty file",
        "prompt": "Read `empty.txt` with read_file and tell me whether it is empty (answer yes or no) and "
                  "its size in bytes.",
        "setup": setup_empty,
        "verify": verify_empty,
        "compliance": compliance_read_file,
        "budget": {"max_llm_calls": 4, "max_tool_calls": 3, "max_wall": 60},
    },
    {
        "id": "unicode-crlf",
        "title": "Unicode text with CRLF",
        "prompt": "Read `notes.md` with read_file and report the exact text of line 3.",
        "setup": setup_unicode,
        "verify": verify_unicode,
        "compliance": compliance_read_file,
        "budget": {"max_llm_calls": 4, "max_tool_calls": 3, "max_wall": 60},
    },
    {
        "id": "long-line-token",
        "title": "Token inside a 50KB line",
        "prompt": "Read `events.txt` with read_file and report the exact value after `TOKEN=` on line 123. "
                  "Use read_file, not exec or search_files.",
        "setup": setup_long_line,
        "verify": verify_long_line,
        "compliance": compliance_read_file_no_search,
        "budget": {"max_llm_calls": 8, "max_tool_calls": 7, "max_wall": 120},
    },
    {
        "id": "read-then-edit",
        "title": "Read and edit a Go constant",
        "prompt": "Read `server.go` with read_file, then change the constant `listenPort` from 8080 to 9090 "
                  "using edit_file. Report the line number where the constant is defined. Do not change anything else.",
        "setup": setup_server,
        "verify": verify_edit,
        "compliance": compliance_edit,
        "budget": {"max_llm_calls": 6, "max_tool_calls": 5, "max_wall": 90},
    },
    {
        "id": "manual-section",
        "title": "Locate a section and line in a 4k-line manual",
        "prompt": "Read `manual.md` with read_file. What is the exact title of Section 07 and at which line "
                  "does it start? Answer as `Section 07: <title> line <n>`.",
        "setup": setup_manual,
        "verify": verify_manual,
        "compliance": compliance_read_file,
        "budget": {"max_llm_calls": 10, "max_tool_calls": 9, "max_wall": 150},
    },
    {
        "id": "long-structured-line-token",
        "title": "Token inside a 50KB structured line",
        "prompt": "Read `events.txt` with read_file and report the exact value after `TOKEN=` on line 123. "
                  "Use read_file, not exec or search_files.",
        "setup": setup_structured_line,
        "verify": verify_structured_line,
        "compliance": compliance_read_file_no_search,
        "budget": {"max_llm_calls": 8, "max_tool_calls": 7, "max_wall": 120},
    },
    {
        "id": "blob-offset",
        "title": "Marker at byte 90000 of a 110KB binary",
        "prompt": "Read `blob.bin` with read_file as raw bytes and report the 0-based byte offset of the "
                  "ASCII text MARKER-AT-90000.",
        "setup": setup_blob,
        "verify": verify_blob,
        "compliance": compliance_binary,
        "budget": {"max_llm_calls": 6, "max_tool_calls": 5, "max_wall": 90},
    },
]

TASK_INDEX = {t["id"]: t for t in TASKS}
