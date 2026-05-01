#!/usr/bin/env python3
"""Score benchmark results and generate summary table."""

import json
import sys
import os
from pathlib import Path
from statistics import median


def load_result(path):
    with open(path) as f:
        d = json.load(f)
    u = d["usage"]
    total_input = (
        u.get("input_tokens", 0)
        + u.get("cache_creation_input_tokens", 0)
        + u.get("cache_read_input_tokens", 0)
    )
    return {
        "result": d.get("result", ""),
        "input_tokens": total_input,
        "output_tokens": u.get("output_tokens", 0),
        "total_tokens": total_input + u.get("output_tokens", 0),
        "cost_usd": d.get("total_cost_usd", 0),
        "duration_ms": d.get("duration_ms", 0),
        "num_turns": d.get("num_turns", 0),
    }


def main(results_dir):
    results_dir = Path(results_dir)
    tasks = ["1_impact", "2_flow", "3_reverse", "4_trap", "5_path"]
    modes = ["mcp", "baseline"]

    # Collect all results
    data = {}
    for task in tasks:
        data[task] = {}
        for mode in modes:
            runs = []
            for run_num in range(1, 4):
                path = results_dir / f"{task}_{mode}_run{run_num}.json"
                if path.exists():
                    runs.append(load_result(path))
            data[task][mode] = runs

    # Print raw metrics table
    print("\n" + "=" * 90)
    print("BENCHMARK RESULTS — RAW METRICS (median of 3 runs)")
    print("=" * 90)
    print(
        f"{'Task':<16} {'Mode':<10} {'Tokens':>10} {'Output':>8} "
        f"{'Cost':>8} {'Duration':>10} {'Turns':>6}"
    )
    print("-" * 90)

    totals = {m: {"tokens": [], "cost": [], "duration": []} for m in modes}

    for task in tasks:
        for mode in modes:
            runs = data[task][mode]
            if not runs:
                print(f"{task:<16} {mode:<10} {'(no data)':>10}")
                continue

            med_tokens = median([r["total_tokens"] for r in runs])
            med_output = median([r["output_tokens"] for r in runs])
            med_cost = median([r["cost_usd"] for r in runs])
            med_dur = median([r["duration_ms"] for r in runs])
            med_turns = median([r["num_turns"] for r in runs])

            totals[mode]["tokens"].append(med_tokens)
            totals[mode]["cost"].append(med_cost)
            totals[mode]["duration"].append(med_dur)

            print(
                f"{task:<16} {mode:<10} {med_tokens:>10.0f} {med_output:>8.0f} "
                f"${med_cost:>7.4f} {med_dur/1000:>9.1f}s {med_turns:>6.0f}"
            )
        print()

    # Aggregates
    print("=" * 90)
    print("AGGREGATE")
    print("=" * 90)

    for mode in modes:
        if totals[mode]["tokens"]:
            t = sum(totals[mode]["tokens"])
            c = sum(totals[mode]["cost"])
            d = sum(totals[mode]["duration"])
            print(
                f"{mode:<10} total_tokens={t:.0f}  "
                f"total_cost=${c:.4f}  total_duration={d/1000:.1f}s"
            )

    bl_tokens = sum(totals["baseline"]["tokens"]) if totals["baseline"]["tokens"] else 0
    mcp_tokens = sum(totals["mcp"]["tokens"]) if totals["mcp"]["tokens"] else 0

    if bl_tokens > 0:
        saving = (bl_tokens - mcp_tokens) / bl_tokens * 100
        print(f"\nToken saving: {saving:+.1f}%")

    bl_cost = sum(totals["baseline"]["cost"]) if totals["baseline"]["cost"] else 0
    mcp_cost = sum(totals["mcp"]["cost"]) if totals["mcp"]["cost"] else 0
    if bl_cost > 0:
        cost_saving = (bl_cost - mcp_cost) / bl_cost * 100
        print(f"Cost saving:  {cost_saving:+.1f}%")

    # Save results text for each run (for manual scoring)
    print("\n" + "=" * 90)
    print("ANSWERS (for manual correctness scoring)")
    print("=" * 90)

    for task in tasks:
        for mode in modes:
            runs = data[task][mode]
            for i, run in enumerate(runs):
                outpath = results_dir / f"{task}_{mode}_run{i+1}_answer.txt"
                with open(outpath, "w") as f:
                    f.write(run["result"])
                print(f"  Saved: {outpath.name}")

    print(
        "\n>>> Review *_answer.txt files and fill in correctness scores "
        "in BENCHMARK.md summary table."
    )
    print(
        ">>> Score each answer against ground truth using the checklist "
        "in BENCHMARK.md."
    )


if __name__ == "__main__":
    if len(sys.argv) < 2:
        print(f"Usage: {sys.argv[0]} <results_dir>")
        sys.exit(1)
    main(sys.argv[1])
