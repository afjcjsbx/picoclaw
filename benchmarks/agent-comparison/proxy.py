#!/usr/bin/env python3
"""OpenAI-compatible logging proxy used by the agent benchmark harness.

Forwards chat-completions requests to OpenCode Go and records, per request:
  - the full request body (messages, tools, sampling params)
  - the assembled response (content, tool calls, finish reason)
  - token usage reported upstream
  - latency and HTTP status

One proxy process is started per benchmark run so logs never mix between
agents or tasks. Standard library only.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
import threading
import time
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

UPSTREAM = os.environ.get(
    "BENCH_UPSTREAM",
    "https://opencode.ai/inference/go/openai/v1/chat/completions",
)

_lock = threading.Lock()
_seq = 0


def _write_record(path: str, record: dict) -> None:
    with _lock:
        with open(path, "a", encoding="utf-8") as fh:
            fh.write(json.dumps(record, ensure_ascii=False) + "\n")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    log_path = ""
    api_key = ""
    session = "bench-proxy"

    def log_message(self, fmt, *args):  # silence default stderr logging
        pass

    def _json(self, code: int, payload: dict) -> None:
        body = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _read_body(self) -> bytes:
        if self.headers.get("Transfer-Encoding", "").lower().strip() == "chunked":
            chunks = []
            while True:
                line = self.rfile.readline(65536).strip()
                if not line:
                    continue
                size = int(line.split(b";", 1)[0], 16)
                if size == 0:
                    # consume trailers
                    while True:
                        trailer = self.rfile.readline(65536)
                        if trailer in (b"\r\n", b"\n", b""):
                            break
                    break
                chunks.append(self.rfile.read(size))
                self.rfile.read(2)  # CRLF
            return b"".join(chunks)
        length = int(self.headers.get("Content-Length", "0"))
        return self.rfile.read(length)

    def do_GET(self):
        if self.path.rstrip("/") in ("/health", "/__health"):
            self._json(200, {"ok": True, "upstream": UPSTREAM})
            return
        self._json(404, {"error": "not found"})

    def do_POST(self):
        global _seq
        path = self.path.split("?")[0].rstrip("/")
        if not path.endswith("/chat/completions"):
            self._json(404, {"error": f"unsupported path {self.path}"})
            return
        raw = self._read_body()
        try:
            payload = json.loads(raw)
        except Exception as exc:  # noqa: BLE001
            self._json(400, {"error": f"bad json: {exc}"})
            return

        stream = bool(payload.get("stream"))
        if stream:
            opts = payload.setdefault("stream_options", {})
            if isinstance(opts, dict):
                opts.setdefault("include_usage", True)
        forwarded = json.dumps(payload).encode()

        headers = {
            "Content-Type": "application/json",
            "Authorization": f"Bearer {self.api_key}",
        }
        headers["x-opencode-session"] = self.headers.get("x-opencode-session") or self.session
        for name in ("user-agent", "accept"):
            value = self.headers.get(name)
            if value:
                headers[name] = value
        if stream:
            headers["Accept"] = "text/event-stream"

        req = urllib.request.Request(UPSTREAM, data=forwarded, headers=headers, method="POST")
        started = time.time()
        with _lock:
            _seq += 1
            seq = _seq
        try:
            resp = urllib.request.urlopen(req, timeout=900)
            status = resp.status
        except urllib.error.HTTPError as exc:
            body = exc.read()
            record = {
                "seq": seq,
                "started_at": started,
                "duration_ms": round((time.time() - started) * 1000, 1),
                "status": exc.code,
                "stream": stream,
                "request": payload,
                "error": body.decode("utf-8", "replace")[:20000],
            }
            _write_record(self.log_path, record)
            self.send_response(exc.code)
            self.send_header("Content-Type", exc.headers.get("Content-Type", "application/json"))
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        except Exception as exc:  # noqa: BLE001
            _write_record(
                self.log_path,
                {
                    "seq": seq,
                    "started_at": started,
                    "duration_ms": round((time.time() - started) * 1000, 1),
                    "status": 502,
                    "stream": stream,
                    "request": payload,
                    "error": f"{type(exc).__name__}: {exc}",
                },
            )
            self._json(502, {"error": f"proxy upstream failure: {exc}"})
            return

        if not stream:
            body = resp.read()
            try:
                data = json.loads(body)
            except Exception:  # noqa: BLE001
                data = {"_raw": body.decode("utf-8", "replace")}
            _write_record(
                self.log_path,
                {
                    "seq": seq,
                    "started_at": started,
                    "duration_ms": round((time.time() - started) * 1000, 1),
                    "status": status,
                    "stream": False,
                    "request": payload,
                    "response": data,
                },
            )
            self.send_response(status)
            self.send_header("Content-Type", resp.headers.get("Content-Type", "application/json"))
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return

        # Streaming passthrough: relay bytes immediately, assemble for logging.
        self.send_response(status)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()

        content_parts: list[str] = []
        reasoning_parts: list[str] = []
        tool_calls: dict[int, dict] = {}
        finish_reason = None
        usage = None
        model = None
        buffer = ""
        try:
            while True:
                chunk = resp.read(8192)
                if not chunk:
                    break
                self.wfile.write(f"{len(chunk):X}\r\n".encode())
                self.wfile.write(chunk)
                self.wfile.write(b"\r\n")
                self.wfile.flush()
                buffer += chunk.decode("utf-8", "replace")
                while "\n" in buffer:
                    line, buffer = buffer.split("\n", 1)
                    line = line.strip()
                    if not line.startswith("data:"):
                        continue
                    data_str = line[5:].strip()
                    if data_str == "[DONE]":
                        continue
                    try:
                        event = json.loads(data_str)
                    except Exception:  # noqa: BLE001
                        continue
                    model = event.get("model") or model
                    if event.get("usage"):
                        usage = event["usage"]
                    for choice in event.get("choices") or []:
                        delta = choice.get("delta") or {}
                        if delta.get("content"):
                            content_parts.append(delta["content"])
                        if delta.get("reasoning_content"):
                            reasoning_parts.append(delta["reasoning_content"])
                        for tc in delta.get("tool_calls") or []:
                            idx = tc.get("index", 0)
                            slot = tool_calls.setdefault(
                                idx,
                                {"id": None, "type": "function", "function": {"name": "", "arguments": ""}},
                            )
                            if tc.get("id"):
                                slot["id"] = tc["id"]
                            fn = tc.get("function") or {}
                            if fn.get("name"):
                                slot["function"]["name"] += fn["name"]
                            if fn.get("arguments"):
                                slot["function"]["arguments"] += fn["arguments"]
                        if choice.get("finish_reason"):
                            finish_reason = choice["finish_reason"]
            self.wfile.write(b"0\r\n\r\n")
            self.wfile.flush()
        finally:
            try:
                resp.close()
            except Exception:  # noqa: BLE001
                pass

        _write_record(
            self.log_path,
            {
                "seq": seq,
                "started_at": started,
                "duration_ms": round((time.time() - started) * 1000, 1),
                "status": status,
                "stream": True,
                "request": payload,
                "response": {
                    "model": model,
                    "content": "".join(content_parts),
                    "reasoning_content": "".join(reasoning_parts),
                    "tool_calls": [tool_calls[i] for i in sorted(tool_calls)],
                    "finish_reason": finish_reason,
                    "usage": usage,
                },
            },
        )


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=18999)
    ap.add_argument("--log", required=True)
    ap.add_argument("--api-key", default=os.environ.get("OPENCODE_GO_API_KEY", ""))
    ap.add_argument("--session", default="bench-proxy")
    args = ap.parse_args()
    os.makedirs(os.path.dirname(os.path.abspath(args.log)), exist_ok=True)
    Handler.log_path = args.log
    Handler.api_key = args.api_key
    Handler.session = args.session
    server = ThreadingHTTPServer(("127.0.0.1", args.port), Handler)
    server.daemon_threads = True
    print(json.dumps({"ready": True, "port": args.port, "upstream": UPSTREAM}), flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
