#!/usr/bin/env python3
"""Validate and merge complete, disjoint `run_go_test_sharded.py` outputs."""

from __future__ import annotations

import argparse
import json
import pathlib
import sys

from run_go_test_sharded import shard_for_name


def read_manifest(path: pathlib.Path, index: int, total: int):
    manifest = json.loads(path.read_text())
    if manifest.get("schema") != 1:
        raise ValueError(f"unsupported shard manifest schema in {path}")
    if manifest.get("shard_index") != index or manifest.get("shard_total") != total:
        raise ValueError(f"wrong shard identity in {path}")
    tests = manifest.get("tests")
    if not isinstance(tests, list) or not tests:
        raise ValueError(f"empty or malformed test inventory in {path}")
    inventory = []
    for item in tests:
        if not isinstance(item, dict):
            raise ValueError(f"malformed test inventory row in {path}")
        package, name, shard = item.get("package"), item.get("name"), item.get("shard")
        if not isinstance(package, str) or not isinstance(name, str):
            raise ValueError(f"malformed test inventory identity in {path}")
        expected_shard = shard_for_name(name, total)
        if shard != expected_shard:
            raise ValueError(f"wrong shard assignment for {package} {name} in {path}")
        inventory.append((package, name, shard))
    if inventory != sorted(set(inventory)):
        raise ValueError(f"duplicate or unsorted test inventory in {path}")
    return inventory


def terminal_roots(log_path: pathlib.Path):
    roots: dict[tuple[str, str], int] = {}
    lines = log_path.read_text().splitlines()
    for line_number, line in enumerate(lines, 1):
        if not line:
            continue
        try:
            event = json.loads(line)
        except json.JSONDecodeError as exc:
            raise ValueError(f"invalid JSON in {log_path}:{line_number}: {exc}") from exc
        action = event.get("Action")
        package = event.get("Package")
        test = event.get("Test")
        if action not in {"pass", "fail", "skip"} or not test or not package:
            continue
        root = test.split("/", 1)[0]
        if test == root and root.startswith(("Test", "Fuzz", "Example")):
            key = package, root
            roots[key] = roots.get(key, 0) + 1
    return roots, lines


def merge_shards(shard_dir: pathlib.Path, prefix: str, total: int,
                 output_log: pathlib.Path, output_exit: pathlib.Path):
    if total < 1:
        raise ValueError("shard count must be positive")
    manifests = []
    shard_logs = []
    shard_exits = []
    for index in range(total):
        stem = f"{prefix}-shard-{index}-of-{total}"
        manifest_path = shard_dir / f"{stem}.inventory.json"
        log_path = shard_dir / f"{stem}.jsonl"
        exit_path = shard_dir / f"{stem}.exit"
        for required in (manifest_path, log_path, exit_path):
            matches = list(shard_dir.rglob(required.name))
            if len(matches) != 1:
                raise FileNotFoundError(
                    f"expected exactly one {required.name} below {shard_dir}; found {len(matches)}"
                )
            if required == manifest_path:
                manifest_path = matches[0]
            elif required == log_path:
                log_path = matches[0]
            else:
                exit_path = matches[0]
        manifest = read_manifest(manifest_path, index, total)
        if manifests and manifest != manifests[0]:
            raise ValueError(f"test inventory differs between full-parity shard {index} and shard 0")
        manifests.append(manifest)
        try:
            code = int(exit_path.read_text().strip())
        except ValueError as exc:
            raise ValueError(f"invalid test exit code in {exit_path}") from exc
        if code not in {0, 1}:
            raise ValueError(f"full-parity shard {index}/{total} did not finish normally: exit={code}")
        roots, lines = terminal_roots(log_path)
        expected = {(package, name) for package, name, shard in manifest if shard == index}
        actual = {key for key, count in roots.items() if count == 1}
        duplicates = {key for key, count in roots.items() if count != 1}
        if duplicates:
            raise ValueError(f"duplicate terminal test results in shard {index}: {sorted(duplicates)[:5]}")
        missing = expected - actual
        unexpected = actual - expected
        if missing:
            raise ValueError(f"shard {index}/{total} omitted {len(missing)} assigned tests: {sorted(missing)[:5]}")
        if unexpected:
            raise ValueError(f"shard {index}/{total} ran {len(unexpected)} tests assigned elsewhere: {sorted(unexpected)[:5]}")
        shard_logs.append(lines)
        shard_exits.append(code)

    inventory = manifests[0]
    output_log.parent.mkdir(parents=True, exist_ok=True)
    output_log.write_text("\n".join(line for shard in shard_logs for line in shard if line) + "\n")
    output_exit.write_text(f"{max(shard_exits)}\n")
    return len(inventory), sum(shard_exits)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--shard-dir", type=pathlib.Path, required=True)
    parser.add_argument("--prefix", required=True)
    parser.add_argument("--shards", type=int, required=True)
    parser.add_argument("--output-log", type=pathlib.Path, required=True)
    parser.add_argument("--output-exit", type=pathlib.Path, required=True)
    args = parser.parse_args()
    try:
        count, failed_shards = merge_shards(
            args.shard_dir, args.prefix, args.shards, args.output_log, args.output_exit
        )
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        print(f"Go test shard aggregation failed: {exc}", file=sys.stderr)
        return 2
    print(f"merged {count} top-level tests from {args.shards} shards; failing shard exits={failed_shards}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
