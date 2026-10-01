#!/usr/bin/env python3
"""Run and validate the bounded GOAMD64 v3 public and analyzer parity audit."""

from __future__ import annotations

import os
import pathlib
import platform
import subprocess
import sys

from validate_goamd64_targets import exact_suite_specs, validate_go_test_json


PROJECT_ENV = os.environ.get("CODEX_PROJECT_ENV", "/Users/thesyncim/.codex/bin/project-env")
AUDIT_SUITES = {
    "root-encode-differential",
    "root-decode-differential",
    "root-hotpath-allocation-guards",
}


def go_command() -> list[str]:
    if pathlib.Path(PROJECT_ENV).is_file() and os.access(PROJECT_ENV, os.X_OK):
        return [PROJECT_ENV, "go"]
    return ["go"]


def audit_specs(mode: str) -> list[dict[str, object]]:
    selected = []
    for spec in exact_suite_specs(mode):
        if spec["name"] not in AUDIT_SUITES:
            continue
        if spec["package"] != ".":
            raise ValueError(f"{spec['name']} does not target the root package")
        selected.append(spec)
    if {spec["name"] for spec in selected} != AUDIT_SUITES:
        raise ValueError(f"no root parity suites selected for mode {mode!r}")
    selected.append({
        "name": "analysis-state-200", "package": "./internal/encoder",
        "selector": r"^TestAnalysisMatchesLibopusLive(EncoderVariants)?$",
        "expected_leaves": {
            "TestAnalysisMatchesLibopusLive": 180,
            "TestAnalysisMatchesLibopusLiveEncoderVariants": 20,
        },
    })
    return selected


def main(argv: list[str]) -> int:
    if len(argv) != 3 or argv[1] not in {"nosimd", "simd"}:
        print("usage: run_goamd64_kernel_audit.py <nosimd|simd> <artifact-prefix>", file=sys.stderr)
        return 2

    mode = argv[1]
    artifact_prefix = pathlib.Path(argv[2])
    stderr_path = artifact_prefix.with_suffix(".stderr")
    cache_path = artifact_prefix.with_suffix(".gocache")
    arch_path = artifact_prefix.with_suffix(".goarch")
    artifact_prefix.parent.mkdir(parents=True, exist_ok=True)

    env = os.environ.copy()
    required_env = {
        "GOAMD64": "v3",
        "GOEXPERIMENT": mode,
        "GOPUS_LIBOPUS_AMD64_TARGET": "v3",
        "GOPUS_TEST_TIER": "parity",
        "GOPUS_STRICT_LIBOPUS_REF": "1",
    }
    mismatches = [
        f"{name}={env.get(name)!r}, want {value!r}"
        for name, value in required_env.items()
        if env.get(name) != value
    ]
    if mismatches:
        print("invalid GOAMD64 audit environment: " + "; ".join(mismatches), file=sys.stderr)
        return 2
    env.pop("GOPUS_LIBOPUS_REF_SCALAR", None)
    env["GOFLAGS"] = ""
    env.pop("GOOS", None)
    env.pop("GOARCH", None)
    env["CODEX_AGENT_ID"] = "v3-matrix"
    command = go_command()

    machine = platform.machine().lower()
    if machine not in {"x86_64", "amd64"}:
        print(f"GOAMD64 v3 audit requires native amd64; host machine is {machine!r}", file=sys.stderr)
        return 1
    arch_result = subprocess.run(
        [*command, "env", "GOARCH"], cwd=pathlib.Path.cwd(), env=env,
        capture_output=True, text=True, check=False,
    )
    arch_path.write_text(arch_result.stdout)
    if arch_result.returncode != 0:
        stderr_path.write_text(arch_result.stderr)
        print(f"go env GOARCH failed with exit={arch_result.returncode}", file=sys.stderr)
        return 1
    if arch_result.stdout.strip() != "amd64":
        print(f"GOAMD64 v3 audit requires GOARCH=amd64; got {arch_result.stdout.strip()!r}", file=sys.stderr)
        return 1

    cache_result = subprocess.run(
        [*command, "env", "GOCACHE"], cwd=pathlib.Path.cwd(), env=env,
        capture_output=True, text=True, check=False,
    )
    cache_path.write_text(cache_result.stdout)
    if cache_result.returncode != 0:
        stderr_path.write_text(cache_result.stderr)
        print(f"go env GOCACHE failed with exit={cache_result.returncode}", file=sys.stderr)
        return 1
    cache = cache_result.stdout.strip()
    if ".codex/worktrees" in cache:
        stderr_path.write_text(f"invalid Go build cache path under .codex/worktrees: {cache}\n")
        print(f"invalid Go build cache path under .codex/worktrees: {cache}", file=sys.stderr)
        return 1

    try:
        specs = audit_specs(mode)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2

    errors: list[str] = []
    for spec in specs:
        suffix = {
            "root-encode-differential": "encode-60",
            "root-decode-differential": "decode-24",
            "root-hotpath-allocation-guards": "allocations",
            "analysis-state-200": "analysis-200",
        }[spec["name"]]
        suite_output = artifact_prefix.with_name(f"{mode}-{suffix}.jsonl")
        suite_stderr = suite_output.with_suffix(suite_output.suffix + ".stderr")
        test_command = [
            *command, "test", "-json", "-count=1", "-timeout=15m",
            "-run", spec["selector"], spec["package"],
        ]
        with suite_output.open("wb") as stdout, suite_stderr.open("wb") as stderr:
            completed = subprocess.run(
                test_command, cwd=pathlib.Path.cwd(), env=env,
                stdout=stdout, stderr=stderr, check=False,
            )
        suite_errors = validate_go_test_json(
            suite_output.read_text(errors="replace"),
            spec["expected_leaves"],
            completed.returncode,
        )
        if suite_errors:
            errors.extend(f"{spec['name']}: {error}" for error in suite_errors)
            print(f"GOAMD64 {mode} {spec['name']} failed: {suite_output}", file=sys.stderr)
            print(f"stderr: {suite_stderr}", file=sys.stderr)
        else:
            print(f"GOAMD64 {mode} {spec['name']} passed: {spec['expected_leaves']}")

    if errors:
        print(f"GOAMD64 {mode} root parity audit failed:", file=sys.stderr)
        for error in errors:
            print(f"- {error}", file=sys.stderr)
        return 1

    print(f"GOAMD64 {mode} root parity audit passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
