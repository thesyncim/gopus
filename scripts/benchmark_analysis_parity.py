#!/usr/bin/env python3
"""Measure analyzer parity changes against the same Go instruction-set lanes."""

from __future__ import annotations

import json
import pathlib
import statistics
import sys

from benchmark_goamd64 import Runner, RunFailure, atomic_json, sha256
from validate_goamd64_targets import BENCH_RE, E2E_NAMES


WORKLOADS = {
    "root": tuple(name for name in E2E_NAMES if "Encoder" in name),
    "analysis": ("BenchmarkTonalityAnalysis48kMono", "BenchmarkTonalityAnalysis48kStereo"),
}
MODES = ("nosimd", "simd")


def parse_timings(text: str, expected: tuple[str, ...]) -> dict[str, float]:
    rows: dict[str, float] = {}
    for line in text.splitlines():
        if not line.startswith("Benchmark"):
            continue
        match = BENCH_RE.fullmatch(line)
        if not match:
            raise RunFailure(f"malformed benchmark row: {line}")
        name, cpu, iterations, ns, bytes_per_op, allocs = match.groups()
        if name not in expected or name in rows:
            raise RunFailure(f"unexpected or repeated workload: {name}")
        value = float(ns)
        # Go omits the suffix when the benchmark uses one CPU.
        if cpu not in (None, "1") or int(iterations) <= 0 or not 0 < value < float("inf"):
            raise RunFailure(f"invalid CPU, iterations, or timing: {line}")
        if int(bytes_per_op) or int(allocs):
            raise RunFailure(f"steady-state allocation: {line}")
        rows[name] = value
    if set(rows) != set(expected):
        raise RunFailure(f"missing workloads: {sorted(set(expected) - set(rows))}")
    return rows


class AnalyzerRunner(Runner):
    def record_binary(self, key: str, cwd: pathlib.Path, target: str, mode: str,
                      binary: pathlib.Path) -> None:
        super().record_binary(key, cwd, target, mode, binary)
        self.manifest["binaries"][key]["sha256"] = sha256(binary)
        if key.endswith("/analysis"):
            output = f"build-info/{key.replace('/', '-')}-analysis.disassembly.txt"
            self.checked_run(f"objdump-{key}", cwd, self.go_command(cwd, [
                "tool", "objdump", "-s",
                "TonalityAnalysisState.*tonalityAnalysis|[Bb]enchmarkTonalityAnalysis|analysis(AvgMod|StdUpdate)",
                str(binary),
            ]), self.go_env(cwd, target), output)
            self.manifest["binaries"][key]["analysis_disassembly"] = output
        self.save()

    def run_all(self) -> int:
        try:
            self.preflight()
            self.manifest["measurement_kind"] = "incremental-go-analysis"
            profiles = self.manifest["metadata"]["pgo_profiles"]
            if profiles["baseline"] != profiles["candidate"]:
                raise RunFailure("baseline and candidate PGO profiles differ")
            for revision, root in (("baseline", self.baseline), ("candidate", self.candidate)):
                status = self.checked_run(f"source-status-{revision}", root,
                                          ["git", "status", "--porcelain", "--untracked-files=no"],
                                          self.go_env(root), f"environment/{revision}-source-status.txt")
                if status.read_text().strip():
                    raise RunFailure(f"{revision} has tracked source changes outside its recorded revision")
                files = self.checked_run(f"assembly-check-{revision}", root,
                                         ["git", "ls-files", "*.s"], self.go_env(root),
                                         f"environment/{revision}-assembly-files.txt")
                if files.read_text().strip():
                    raise RunFailure("incremental comparison requires Go kernel revisions; choose a Go benchmark_baseline")
            # Both revisions use identical benchmark inputs and framing.
            for name in ("benchmark_alloc_test.go", "internal/encoder/analysis_bench_test.go"):
                if sha256(self.baseline / name) != sha256(self.candidate / name):
                    raise RunFailure(f"benchmark source differs between revisions: {name}")
            self.manifest["metadata"]["benchmark_sources"] = {
                name: sha256(self.candidate / name) for name in
                ("benchmark_alloc_test.go", "internal/encoder/analysis_bench_test.go")
            }
            self.manifest["metadata"]["comparison"] = (
                "Go baseline versus Go candidate within each GOAMD64=v3 lane; "
                "nosimd uses the nosimd build tag, SIMD uses GOEXPERIMENT=simd"
            )
            self.manifest["metadata"]["timing_note"] = (
                "All eight Go binaries are compiled before four rotated/reversed timing rounds; "
                "each workload must report zero bytes and allocations per operation"
            )
            if self.manifest["metadata"]["baseline_commit"] == self.manifest["metadata"]["candidate_commit"]:
                raise RunFailure("baseline and candidate revisions are identical")
            self.manifest["metadata"]["round_orders"] = {}
            for revision, root in (("baseline", self.baseline), ("candidate", self.candidate)):
                for mode in MODES:
                    env = self.build_env(root, "v3", mode)
                    tags = ["-tags", "nosimd"] if mode == "nosimd" else []
                    for group, package in (("root", "."), ("analysis", "./internal/encoder")):
                        key = f"{revision}/{mode}/{group}"
                        binary = self.artifact / "binaries" / f"{revision}-{mode}-{group}.test"
                        binary.parent.mkdir(parents=True, exist_ok=True)
                        self.checked_run(f"build-{key}", root, self.go_command(root, [
                            "test", "-c", "-pgo=auto", *tags, "-o", str(binary), package,
                        ]), env, f"build/{revision}-{mode}-{group}.log")
                        self.record_binary(key, root, "v3", mode, binary)
            pairs = [(revision, mode) for mode in MODES for revision in ("baseline", "candidate")]
            self.manifest["analysis_samples"] = []
            for sample in range(1, 5):
                rotated = pairs if sample <= 2 else pairs[1:] + pairs[:1]
                order = rotated if sample % 2 else list(reversed(rotated))
                self.manifest["metadata"]["round_orders"][str(sample)] = order
                for revision, mode in order:
                    root = self.baseline if revision == "baseline" else self.candidate
                    env = self.build_env(root, "v3", mode)
                    for group, names in WORKLOADS.items():
                        binary = self.artifact / self.manifest["binaries"][f"{revision}/{mode}/{group}"]["path"]
                        output = f"timings/round-{sample}-{revision}-{mode}-{group}.log"
                        stdout = self.checked_run(f"timing-{sample}-{revision}-{mode}-{group}", root, [
                            str(binary), "-test.run", "^$", "-test.bench", "^(" + "|".join(names) + ")$",
                            "-test.benchtime=500ms", "-test.count=1", "-test.cpu=1", "-test.benchmem",
                        ], env, output)
                        self.manifest["analysis_samples"].append({
                            "revision": revision, "mode": mode, "group": group, "sample": sample,
                            "stdout": output, "sha256": sha256(stdout),
                            "ns_per_op": parse_timings(stdout.read_text(), names),
                        })
                        self.save()
            self.manifest["status"] = "complete"
            self.save()
            summary = {"valid": True, "metadata": self.manifest["metadata"], "rows": []}
            for mode in MODES:
                for names in WORKLOADS.values():
                    for name in names:
                        medians = {}
                        for revision in ("baseline", "candidate"):
                            values = [row["ns_per_op"][name] for row in self.manifest["analysis_samples"]
                                      if row["revision"] == revision and row["mode"] == mode
                                      and name in row["ns_per_op"]]
                            if len(values) != 4:
                                raise RunFailure(f"missing paired samples for {revision}/{mode}/{name}")
                            medians[revision] = statistics.median(values)
                        summary["rows"].append({"mode": mode, "name": name, **medians,
                                                "candidate_over_baseline": medians["candidate"] / medians["baseline"]})
            atomic_json(self.artifact / "summary.json", summary)
            print(json.dumps(summary, indent=2))
            return 0
        except Exception as exc:
            self.fail(f"{type(exc).__name__}: {exc}")
            atomic_json(self.artifact / "summary.json", {"valid": False, "errors": self.manifest["errors"]})
            print(str(exc), file=sys.stderr)
            return 1


def main(args: list[str]) -> int:
    if len(args) != 3:
        print("usage: benchmark_analysis_parity.py <Go-baseline> <candidate> <artifact-dir>", file=sys.stderr)
        return 2
    try:
        return AnalyzerRunner(*(pathlib.Path(arg) for arg in args)).run_all()
    except RunFailure as exc:
        print(str(exc), file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
