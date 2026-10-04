#!/usr/bin/env python3
"""Run one deterministic shard of the runnable Go test inventory.

The sharding boundary is a top-level Test, Fuzz, or Example name. Tests with
the same name in different packages stay in the same shard so the single Go
`-run` expression selects exactly one disjoint slice across all packages.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import os
import pathlib
import shlex
import subprocess
import sys


TEST_PREFIXES = ("Test", "Fuzz", "Example")


def shard_for_name(name: str, total: int) -> int:
    digest = hashlib.sha256(name.encode("utf-8")).digest()
    return int.from_bytes(digest[:8], "big") % total


def parse_shard(value: str) -> tuple[int, int]:
    try:
        index_text, total_text = value.split("/", 1)
        index, total = int(index_text), int(total_text)
    except (ValueError, TypeError) as exc:
        raise ValueError("shard must be INDEX/TOTAL, with INDEX starting at 0") from exc
    if total < 1 or index < 0 or index >= total:
        raise ValueError("shard must satisfy 0 <= INDEX < TOTAL")
    return index, total


def parse_go_env(value: str) -> dict[str, str]:
    env: dict[str, str] = {}
    for assignment in shlex.split(value):
        key, separator, item = assignment.partition("=")
        if not separator or not key:
            raise ValueError(f"invalid GO_WORK_ENV assignment: {assignment!r}")
        env[key] = item
    return env


def test_selection_args(args: list[str]) -> list[str]:
    result = []
    has_count = False
    i = 0
    while i < len(args):
        arg = args[i]
        option, equals, value = arg.partition("=")
        if option in {"-run", "-bench", "-fuzz", "-list", "-args", "-exec"}:
            raise ValueError(
                f"GOPUS_TEST_SHARD does not accept filtered test runs ({option}); "
                "unset GOPUS_TEST_SHARD for focused test commands"
            )
        if option == "-count":
            has_count = True
            if equals:
                count = value
                result.append(arg)
            elif i + 1 < len(args):
                i += 1
                count = args[i]
                result.extend((arg, count))
            else:
                raise ValueError("-count requires a value")
            if count != "1":
                raise ValueError("GOPUS_TEST_SHARD requires -count=1 so each test has one terminal result")
            i += 1
            continue
        if arg == "-json":
            i += 1
            continue
        result.append(arg)
        i += 1
    if not has_count:
        result.append("-count=1")
    return result


def discover_tests(
    go_command: list[str], env: dict[str, str], test_args: list[str], packages: list[str],
    root: pathlib.Path,
) -> list[tuple[str, str]]:
    command = go_command + ["test"] + test_args + ["-json", "-list=."] + packages
    completed = subprocess.run(command, cwd=root, env=env, text=True,
                               capture_output=True, check=False)
    if completed.returncode:
        sys.stderr.write(completed.stdout)
        sys.stderr.write(completed.stderr)
        raise RuntimeError(f"Go test inventory discovery failed with exit={completed.returncode}")

    inventory: set[tuple[str, str]] = set()
    for line_number, line in enumerate(completed.stdout.splitlines(), 1):
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            raise RuntimeError(f"invalid go test -list JSON at output line {line_number}: {exc}") from exc
        if event.get("Action") != "output":
            continue
        package = event.get("Package")
        if not package:
            continue
        for output_line in event.get("Output", "").splitlines():
            name = output_line.strip()
            if name.startswith(TEST_PREFIXES):
                inventory.add((package, name))
    if not inventory:
        raise RuntimeError("Go test inventory discovery found no Test, Fuzz, or Example functions")
    return sorted(inventory)


def runnable_packages(go_command: list[str], env: dict[str, str], root: pathlib.Path) -> list[str]:
    command = go_command + ["list", "./..."]
    completed = subprocess.run(command, cwd=root, env=env, text=True,
                               capture_output=True, check=False)
    if completed.returncode:
        sys.stderr.write(completed.stdout)
        sys.stderr.write(completed.stderr)
        raise RuntimeError(f"Go package discovery failed with exit={completed.returncode}")
    packages = []
    for package in completed.stdout.splitlines():
        package = package.strip()
        if not package:
            continue
        if package == "github.com/thesyncim/gopus/tmp_check" or package.startswith(
            "github.com/thesyncim/gopus/tmp_check/"
        ):
            continue
        packages.append(package)
    if not packages:
        raise RuntimeError("Go package discovery found no runnable packages")
    return packages


def write_manifest(path: pathlib.Path, index: int, total: int,
                   inventory: list[tuple[str, str]]) -> None:
    manifest = {
        "schema": 1,
        "shard_index": index,
        "shard_total": total,
        "tests": [
            {"package": package, "name": name, "shard": shard_for_name(name, total)}
            for package, name in sorted(inventory)
        ],
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_suffix(path.suffix + ".tmp")
    temporary.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
    temporary.replace(path)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--go", default="go", help="Go executable")
    parser.add_argument("--go-work-env", default="", help="space-separated KEY=VALUE assignments")
    parser.add_argument("--root", type=pathlib.Path, default=pathlib.Path.cwd())
    parser.add_argument("--shard", required=True, help="zero-based INDEX/TOTAL")
    parser.add_argument("--report", type=pathlib.Path)
    parser.add_argument("--test-arg", action="append", default=[])
    parser.add_argument("--package", action="append", default=[])
    args = parser.parse_args()

    try:
        index, total = parse_shard(args.shard)
        go_command = shlex.split(args.go)
        if not go_command:
            raise ValueError("Go executable is empty")
        env = os.environ.copy()
        env.update(parse_go_env(args.go_work_env))
        test_args = test_selection_args(args.test_arg)
        packages = args.package or runnable_packages(go_command, env, args.root)
        inventory = discover_tests(go_command, env, test_args, packages, args.root)
    except (ValueError, RuntimeError) as exc:
        print(f"go test sharding: {exc}", file=sys.stderr)
        return 2

    selected_names = sorted({
        name for _, name in inventory if shard_for_name(name, total) == index
    })
    if not selected_names:
        print(f"go test sharding: shard {index}/{total} has no top-level tests", file=sys.stderr)
        return 2

    report_path = args.report or pathlib.Path(
        f"test-shard-{index}-of-{total}.inventory.json"
    )
    write_manifest(report_path, index, total, inventory)
    pattern = "^(" + "|".join(selected_names) + ")$"
    command = go_command + ["test"] + test_args + ["-json", "-run=" + pattern] + packages

    # The sharding controls are runner metadata and must not become test inputs.
    env.pop("GOPUS_TEST_SHARD", None)
    env.pop("GOPUS_TEST_SHARD_REPORT", None)
    result = subprocess.run(command, cwd=args.root, env=env, check=False)
    return result.returncode


if __name__ == "__main__":
    sys.exit(main())
