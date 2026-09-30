#!/usr/bin/env bash
# Compare the runtime footprint of two PicoClaw binaries on the same Linux host.
#
# The benchmark starts a minimal, model-free gateway repeatedly and measures the
# time until its /ready endpoint responds, peak RSS, and CPU time used during
# startup. Warm-up starts are excluded and measured starts alternate between
# revisions to balance filesystem cache effects. It is intentionally
# dependency-free except for bash, curl, Python 3, and Linux /proc, which are
# available on GitHub's Ubuntu runners.

set -euo pipefail

usage() {
  echo "Usage: $0 --base <binary> --head <binary> --output <directory> [--runs <count>]" >&2
  exit 2
}

base_binary=""
head_binary=""
output_dir=""
runs=10

while [[ $# -gt 0 ]]; do
  case "$1" in
    --base) base_binary=${2:?missing value for --base}; shift 2 ;;
    --head) head_binary=${2:?missing value for --head}; shift 2 ;;
    --output) output_dir=${2:?missing value for --output}; shift 2 ;;
    --runs) runs=${2:?missing value for --runs}; shift 2 ;;
    *) usage ;;
  esac
done

[[ -x "$base_binary" && -x "$head_binary" && -n "$output_dir" ]] || usage
[[ "$runs" =~ ^[1-9][0-9]*$ ]] || { echo "--runs must be a positive integer" >&2; exit 2; }

command -v curl >/dev/null
command -v python3 >/dev/null

mkdir -p "$output_dir"
results_file="$output_dir/results.tsv"
printf 'revision\trun\tstartup_ms\tpeak_rss_kib\tstartup_cpu_us\n' > "$results_file"

free_port() {
  python3 -c 'import socket; s = socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

read_proc_metrics() {
  local pid=$1
  local rss_kib cpu_ns
  rss_kib=$(awk '/^VmHWM:/ { print $2; exit }' "/proc/$pid/status")
  cpu_ns=$(awk '{ print $1; exit }' "/proc/$pid/schedstat")
  printf '%s\t%s' "$rss_kib" "$((cpu_ns / 1000))"
}

run_once() {
  local revision=$1 binary=$2 run=$3 record_result=$4
  local run_dir port pid started_ns now_ns elapsed_ms metrics ready=false
  run_dir=$(mktemp -d "$output_dir/${revision}-${run}.XXXXXX")
  port=$(free_port)

  cat > "$run_dir/config.json" <<EOF
{"version":1,"gateway":{"host":"127.0.0.1","port":${port},"log_level":"error"}}
EOF

  started_ns=$(date +%s%N)
  PICOCLAW_HOME="$run_dir/home" PICOCLAW_CONFIG="$run_dir/config.json" \
    "$binary" --no-color gateway --allow-empty > "$run_dir/gateway.log" 2>&1 &
  pid=$!

  for _ in $(seq 1 3000); do
    if curl --fail --silent --show-error "http://127.0.0.1:${port}/ready" >/dev/null 2>&1; then
      ready=true
      break
    fi
    if ! kill -0 "$pid" 2>/dev/null; then
      break
    fi
    sleep 0.01
  done

  if [[ "$ready" != true ]]; then
    echo "${revision} run ${run}: gateway did not become ready" >&2
    sed -n '1,160p' "$run_dir/gateway.log" >&2 || true
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
    return 1
  fi

  now_ns=$(date +%s%N)
  elapsed_ms=$(((now_ns - started_ns) / 1000000))
  metrics=$(read_proc_metrics "$pid")
  if [[ "$record_result" == true ]]; then
    printf '%s\t%s\t%s\t%s\n' "$revision" "$run" "$elapsed_ms" "$metrics" >> "$results_file"
  fi
  printf '%s run %s: startup=%sms, peak RSS=%s KiB, startup CPU=%sµs%s\n' \
    "$revision" "$run" "$elapsed_ms" "${metrics%%$'\t'*}" "${metrics##*$'\t'}" \
    "$([[ "$record_result" == true ]] && printf '' || printf ' (warm-up)')"

  kill -TERM "$pid" 2>/dev/null || true
  wait "$pid" 2>/dev/null || true
}

run_once base "$base_binary" warmup false
run_once head "$head_binary" warmup false

for run in $(seq 1 "$runs"); do
  if (( run % 2 )); then
    run_once base "$base_binary" "$run" true
    run_once head "$head_binary" "$run" true
  else
    run_once head "$head_binary" "$run" true
    run_once base "$base_binary" "$run" true
  fi
done

python3 - "$results_file" "$output_dir" "$runs" "$base_binary" "$head_binary" <<'PY'
import csv
import json
import statistics
import sys
from pathlib import Path

results_path = Path(sys.argv[1])
output_dir = Path(sys.argv[2])
runs = int(sys.argv[3])
base_binary = Path(sys.argv[4])
head_binary = Path(sys.argv[5])
metrics = ("startup_ms", "peak_rss_kib", "startup_cpu_us", "binary_size_bytes")
labels = {
    "startup_ms": "Startup to /ready (ms)",
    "peak_rss_kib": "Peak resident memory (KiB)",
    "startup_cpu_us": "CPU time during startup (ms)",
    "binary_size_bytes": "Binary size (KiB)",
}

values = {"base": {m: [] for m in metrics}, "head": {m: [] for m in metrics}}
with results_path.open(newline="") as f:
    for row in csv.DictReader(f, delimiter="\t"):
        for metric in metrics[:-1]:
            values[row["revision"]][metric].append(int(row[metric]))
values["base"]["binary_size_bytes"].append(base_binary.stat().st_size)
values["head"]["binary_size_bytes"].append(head_binary.stat().st_size)

def describe(samples):
    return {
        "median": statistics.median(samples),
        "mean": statistics.mean(samples),
        "stdev": statistics.stdev(samples) if len(samples) > 1 else 0,
    }

summary = {revision: {metric: describe(samples) for metric, samples in data.items()} for revision, data in values.items()}
(output_dir / "summary.json").write_text(json.dumps(summary, indent=2) + "\n")

def display(metric, value):
    if metric == "startup_cpu_us":
        return f"{value / 1000:.2f}"
    if metric == "binary_size_bytes":
        return f"{value / 1024:.2f}"
    return f"{value:g}"

def signed_display(metric, value):
    sign = "+" if value > 0 else "-" if value < 0 else ""
    return sign + display(metric, abs(value))

def delta(metric, base, head):
    absolute = head - base
    if base == 0:
        return signed_display(metric, absolute)
    return f"{signed_display(metric, absolute)} ({absolute / base:+.1%})"

lines = [
    "## PicoClaw runtime footprint",
    "",
    f"{runs} measured process starts per revision on this GitHub Actions runner; one warm-up start per revision is excluded.",
    "",
    "| Metric | Base median | PR median | Delta | Base mean ± σ | PR mean ± σ |",
    "| --- | ---: | ---: | ---: | ---: | ---: |",
]
for metric in metrics:
    base = summary["base"][metric]
    head = summary["head"][metric]
    lines.append(
        f"| {labels[metric]} | {display(metric, base['median'])} | {display(metric, head['median'])} | "
        f"{delta(metric, base['median'], head['median'])} | "
        f"{display(metric, base['mean'])} ± {display(metric, base['stdev'])} | "
        f"{display(metric, head['mean'])} ± {display(metric, head['stdev'])} |"
    )

regression_factor = 1.5
regressions = [
    metric for metric in metrics
    if summary["base"][metric]["median"] > 0
    and summary["head"][metric]["median"] >= regression_factor * summary["base"][metric]["median"]
]
lines.extend([
    "",
    "The gateway uses the same minimal configuration for both revisions, starts without a model, and is measured until `GET /ready` succeeds. Measured starts alternate base/PR order to balance cache effects. CPU is process execution time from Linux `schedstat`; memory is Linux peak RSS. Binary size is the compiled executable size.",
    "The CI gate fails when a PR metric is at least 1.5x its non-zero base metric (a 50% regression); sampled runtime metrics use their median.",
    "",
])
(output_dir / "summary.md").write_text("\n".join(lines))

if regressions:
    print(
        "Runtime footprint regression: " + ", ".join(labels[metric] for metric in regressions),
        file=sys.stderr,
    )
    sys.exit(1)
PY
