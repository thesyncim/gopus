#!/usr/bin/env python3
"""Validate GOAMD64 target benchmark evidence and publish complete matrices only."""

from __future__ import annotations

import argparse
import csv
import json
import math
import pathlib
import re
import statistics
import sys
from typing import Any


TARGETS = ("v1", "v2", "v3")
MODES = ("nosimd", "simd")
ARMS = ("oldasm", "nosimd", "simd")
E2E_NAMES = (
    "BenchmarkDecoderDecode_CELT", "BenchmarkDecoderDecode_Hybrid", "BenchmarkDecoderDecode_SILK",
    "BenchmarkEncoderEncode_CallerBuffer", "BenchmarkEncoderEncode_VoIP", "BenchmarkEncoderEncode_LowDelay",
)
ENCODE_CASES = (
    "CELT-FB-20ms-stereo-128k", "CELT-FB-5ms-mono-64k", "SILK-WB-20ms-mono-32k",
    "Hybrid-FB-20ms-mono-64k", "Hybrid-FB-20ms-stereo-96k",
)
DECODE_PATHS = ("Float32", "Int16")
TSV_FIELDS = {
    "implementation", "path", "vector", "benchtime", "count", "iterations", "elapsed_ns",
    "bytes_per_op", "packets_per_op", "samples_per_op", "ns_per_sample", "ns_per_packet",
    "x_realtime", "allocs_per_op",
}
CBR_RE = re.compile(
    r"strict paired CBR summary: variant=(\S+) cases=(\d+) exact_cases=(\d+) "
    r"packets=(\d+) packet_diffs=(\d+) range_diffs=(\d+)"
)
BENCH_RE = re.compile(
    r"^(Benchmark[A-Za-z0-9_]+)(?:-(\d+))?\s+(\d+)\s+([0-9.eE+-]+)\s+ns/op\s+"
    r"(\d+)\s+B/op\s+(\d+)\s+allocs/op\s*$"
)
GO_TOOLCHAIN_RE = re.compile(r"\bgo[0-9]+\.[0-9]+(?:\.[0-9]+)?(?:beta[0-9]+|rc[0-9]+)?(?:-[^\s]+)?\b")


def extract_go_toolchain(text: str) -> str | None:
    """Return the Go version token from a go version or go version -m first line."""
    first_line = text.splitlines()[0] if text.splitlines() else ""
    if first_line.startswith("go version "):
        candidates = first_line.split()[2:]
    else:
        _path, separator, version = first_line.rpartition(": ")
        if not separator:
            return None
        candidates = [version]
    for candidate in candidates:
        if GO_TOOLCHAIN_RE.fullmatch(candidate):
            return candidate
    return None


def validate_binary_build_info(info: str, target: str, mode: str,
                               expected_toolchain: str | None) -> list[str]:
    errors: list[str] = []
    actual_toolchain = extract_go_toolchain(info)
    if actual_toolchain is None:
        errors.append("build info first line does not identify a Go toolchain")
    elif expected_toolchain is None:
        errors.append("expected Go toolchain is missing from run metadata")
    elif actual_toolchain != expected_toolchain:
        errors.append(f"binary Go toolchain={actual_toolchain}, want {expected_toolchain}")
    if target not in TARGETS or f"GOAMD64={target}" not in info:
        errors.append(f"build info does not confirm GOAMD64={target}")
    if "GOEXPERIMENT=simd" not in info:
        errors.append("build info does not confirm GOEXPERIMENT=simd")
    if mode == "nosimd" and "-tags=nosimd" not in info:
        errors.append("build info does not confirm nosimd tag")
    if mode != "nosimd" and "-tags=nosimd" in info:
        errors.append("build info unexpectedly has nosimd tag")
    return errors


def exact_suite_specs(mode: str) -> list[dict[str, Any]]:
    """Reviewed, bounded public parity checks for one candidate build mode."""
    if mode not in MODES:
        raise ValueError(f"unsupported mode: {mode}")
    encode_selector = (
        r"^TestEncodeDifferentialFuzz$/^(celt_fb_ch[12]_(5|20)ms_(64000|128000)bps|"
        r"silk_wb_ch[12]_20ms_24000bps|hybrid_fb_ch[12]_20ms_64000bps|"
        r"auto_(voice|music)_ch[12]_20ms_(32000|48000)bps)_vbr[012]_fecfalse_dtxfalse$"
    )
    decode_selector = (
        r"^TestDecodeDifferentialEncodeThenDecode$/^(celt_fb_ch[12]_(5|20)ms_96000bps|"
        r"silk_wb_ch[12]_20ms_32000bps|hybrid_fb_ch[12]_20ms_96000bps)_vbr[012]_fecfalse_dtxfalse$"
    )
    alloc_names = (
        "EncodeFloat32", "EncodeInt16", "EncodeRestrictedSilkLowComplexity", "EncodeHybridComplexityZero",
        "DecodeFloat32", "DecodeInt16", "DecodeInt24", "DecodeStereo", "DecodeSILKMono",
        "DecodeHybridMono", "DecodeSILKAndHybridStereo", "DecodeSilenceTransitions",
    )
    alloc_leaves = {
        "TestHotPathAllocs" + name: (2 if name in {
            "EncodeHybridComplexityZero", "DecodeSILKAndHybridStereo", "DecodeSilenceTransitions"
        } else 1)
        for name in alloc_names
    }
    specs = [
        {"name": "root-encode-differential", "package": ".", "selector": encode_selector,
         "expected_leaves": {"TestEncodeDifferentialFuzz": 60}},
        {"name": "root-decode-differential", "package": ".", "selector": decode_selector,
         "expected_leaves": {"TestDecodeDifferentialEncodeThenDecode": 24}},
        {"name": "root-hotpath-allocation-guards", "package": ".",
         "selector": r"^TestHotPathAllocs(" + "|".join(alloc_names) + r")$",
         "expected_leaves": alloc_leaves},
        {"name": "testvectors-cbr-packet-range-oracle", "package": "./testvectors",
         "selector": r"^TestEncoderCBRPairedOracleExact$",
         "expected_leaves": {"TestEncoderCBRPairedOracleExact": 19}, "strict_cbr": True},
    ]
    if mode == "simd":
        specs.append({
            "name": "celt-native-avx2-fma-dispatch-witness",
            "package": "./internal/celt",
            "selector": r"^TestPitchXCorrPairedLibopusSIMDRawBits$/^length10_correlations10$/^production$",
            "expected_leaves": {"TestPitchXCorrPairedLibopusSIMDRawBits": 1},
        })
        specs.append({
            "name": "celt-pvq-avx-dispatch-witness",
            "package": "./internal/celt",
            "selector": r"^TestPVQSearchSIMDDispatchUsesAVX$",
            "expected_leaves": {"TestPVQSearchSIMDDispatchUsesAVX": 1},
        })
    return specs


def validate_go_test_json(text: str, expected_leaves: dict[str, int], exit_code: int) -> list[str]:
    errors: list[str] = []
    if exit_code != 0:
        errors.append(f"go test exit={exit_code}")
    events = []
    try:
        events = [json.loads(line) for line in text.splitlines() if line.strip()]
    except json.JSONDecodeError as exc:
        return errors + [f"invalid go test JSON: {exc}"]
    runs: set[str] = set()
    results: dict[str, str] = {}
    package_failures = 0
    for event in events:
        action, name = event.get("Action"), event.get("Test")
        if action == "run" and name:
            if name in runs:
                errors.append(f"duplicate test run event: {name}")
            runs.add(name)
        elif action in {"pass", "fail", "skip"} and name:
            if name in results:
                errors.append(f"duplicate test result event: {name}")
            results[name] = action
        elif action in {"fail", "skip"} and not name:
            package_failures += 1
    if package_failures:
        errors.append(f"go test has {package_failures} package-level failure/skip events")
    if not runs:
        errors.append("go test JSON contains no selected tests")
    roots = {name for name in runs if "/" not in name}
    if roots != set(expected_leaves):
        errors.append(f"selected roots differ: missing={sorted(set(expected_leaves)-roots)} extra={sorted(roots-set(expected_leaves))}")
    for root, count in expected_leaves.items():
        if root not in runs:
            errors.append(f"selected root did not run: {root}")
            continue
        descendants = {name for name in runs if name.startswith(root + "/")}
        leaves = {name for name in descendants if not any(other.startswith(name + "/") for other in descendants)}
        if not descendants:
            leaves = {root}
        if len(leaves) != count:
            errors.append(f"{root} ran {len(leaves)} leaf tests, want {count}")
        for leaf in leaves:
            if results.get(leaf) != "pass":
                errors.append(f"leaf did not pass: {leaf} status={results.get(leaf, 'missing')}")
    for name, status in results.items():
        if status != "pass":
            errors.append(f"test did not pass: {name} status={status}")
    for name in runs - results.keys():
        errors.append(f"test has no result event: {name}")
    return errors


def validate_cbr_summary(text: str, expected_variant: str) -> list[str]:
    matches = list(CBR_RE.finditer(text))
    if len(matches) != 1:
        return [f"expected one strict CBR summary, found {len(matches)}"]
    variant, cases, exact, packets, packet_diffs, range_diffs = matches[0].groups()
    got = tuple(map(int, (cases, exact, packets, packet_diffs, range_diffs)))
    if variant != expected_variant:
        return [f"strict CBR variant={variant!r}, want {expected_variant!r}"]
    if got != (19, 19, 2175, 0, 0):
        return [f"strict CBR summary={got}, want cases/exact/packets/packet-diffs/range-diffs=(19,19,2175,0,0)"]
    return []


def parse_e2e(text: str, exit_code: int) -> tuple[dict[str, float], list[str]]:
    errors: list[str] = []
    if exit_code != 0:
        errors.append(f"benchmark exit={exit_code}")
    rows: dict[str, float] = {}
    for line in text.splitlines():
        if not line.startswith("Benchmark"):
            continue
        match = BENCH_RE.fullmatch(line)
        if not match:
            errors.append(f"malformed benchmark row: {line}")
            continue
        name, _cpu, _iterations, ns_raw, bytes_raw, allocs_raw = match.groups()
        if name not in E2E_NAMES or name in rows:
            errors.append(f"unexpected/duplicate benchmark row: {name}")
            continue
        value = float(ns_raw)
        if not math.isfinite(value) or value <= 0:
            errors.append(f"invalid ns/op for {name}: {ns_raw}")
        if int(bytes_raw) != 0 or int(allocs_raw) != 0:
            errors.append(f"{name} allocates: {bytes_raw} B/op, {allocs_raw} allocs/op")
        rows[name] = value
    if set(rows) != set(E2E_NAMES):
        errors.append(f"benchmark rows missing: {sorted(set(E2E_NAMES)-set(rows))}")
    return rows, errors


def _tsv_rows(text: str, label: str) -> tuple[list[dict[str, str]], list[str]]:
    reader = csv.DictReader(text.splitlines(), delimiter="\t")
    if not reader.fieldnames or not TSV_FIELDS.issubset(reader.fieldnames):
        return [], [f"{label} TSV header is incomplete"]
    rows = list(reader)
    errors = [f"{label} TSV has malformed rows"] if any(None in row for row in rows) else []
    return rows, errors


def validate_tool_tsv(encoder_text: str, decode_text: str, encoder_exit: int,
                      decode_exit: int) -> tuple[dict[tuple[str, str], dict[str, float]], list[str]]:
    errors: list[str] = []
    if encoder_exit != 0:
        errors.append(f"encoderbenchcmp exit={encoder_exit}")
    if decode_exit != 0:
        errors.append(f"testvectorbenchcmp exit={decode_exit}")
    enc_rows, row_errors = _tsv_rows(encoder_text, "encoderbenchcmp")
    errors.extend(row_errors)
    dec_rows, row_errors = _tsv_rows(decode_text, "testvectorbenchcmp")
    errors.extend(row_errors)
    parsed: dict[tuple[str, str, str], dict[str, str]] = {}
    for family, rows, allowed in (
        ("encode", enc_rows, {("Float32", case) for case in ENCODE_CASES}),
        ("decode", dec_rows, {(path, "all") for path in DECODE_PATHS}),
    ):
        for row in rows:
            impl, path, vector = row.get("implementation", ""), row.get("path", ""), row.get("vector", "")
            key = (path, vector)
            label = f"{family}/{impl}/{path}/{vector}"
            if key not in allowed or impl not in {"gopus", "libopus"}:
                errors.append(f"unexpected TSV row: {label}")
                continue
            workload = vector if family == "encode" else path
            full_key = (family, impl, workload)
            if full_key in parsed:
                errors.append(f"duplicate TSV row: {full_key}")
                continue
            parsed[full_key] = row
            if row.get("benchtime") != "250ms" or row.get("count") != "3":
                errors.append(f"{label} did not use 3 × 250ms")
            valid_timing = True
            try:
                ns_sample, ns_packet = float(row["ns_per_sample"]), float(row["ns_per_packet"])
                if not math.isfinite(ns_sample) or ns_sample <= 0 or not math.isfinite(ns_packet) or ns_packet <= 0:
                    raise ValueError
                if min(int(row[field]) for field in ("iterations", "elapsed_ns", "samples_per_op")) <= 0:
                    raise ValueError
            except (KeyError, ValueError):
                errors.append(f"{label} has invalid or non-positive timing/count fields")
                valid_timing = False
            want_allocs = "0" if impl == "gopus" else "-"
            if row.get("allocs_per_op") != want_allocs:
                errors.append(f"{label} allocs_per_op={row.get('allocs_per_op')!r}, want {want_allocs!r}")
            if not valid_timing:
                row["ns_per_sample"] = "0"
    values: dict[tuple[str, str], dict[str, float]] = {}
    for family, workloads in (("encode", ENCODE_CASES), ("decode", DECODE_PATHS)):
        for workload in workloads:
            pair = {}
            for impl in ("gopus", "libopus"):
                row = parsed.get((family, impl, workload))
                if row is None:
                    errors.append(f"missing paired row: {family}/{impl}/{workload}")
                else:
                    pair[impl] = float(row["ns_per_sample"])
            if len(pair) == 2:
                values[(family, workload)] = pair
    if len(enc_rows) != 10 or len(dec_rows) != 4:
        errors.append(f"TSV row counts are encoder={len(enc_rows)}/10 decode={len(dec_rows)}/4")
    return values, errors


def _read(root: pathlib.Path, relative: str) -> str:
    return (root / relative).read_text(errors="replace")


def _input_hashes(text: str, target: str) -> tuple[dict[str, str], list[str]]:
    errors: list[str] = []
    manifest_lines = [line for line in text.splitlines() if line.startswith("encoderbenchcmp-workload-manifest\t")]
    workload_lines = [line for line in text.splitlines() if line.startswith("encoderbenchcmp-workload\t")]
    if len(manifest_lines) != 1:
        errors.append(f"expected one encoder workload manifest, found {len(manifest_lines)}")
    elif not manifest_lines[0].startswith(
        f"encoderbenchcmp-workload-manifest\tgoos=linux\tgoarch=amd64\tgoamd64={target}\ttarget={target}"
    ):
        errors.append(f"encoder workload manifest does not identify linux/amd64 {target}")
    hashes: dict[str, str] = {}
    for line in workload_lines:
        fields = {}
        for part in line.split("\t")[1:]:
            if "=" in part:
                key, value = part.split("=", 1)
                fields[key] = value
        name, digest = fields.get("name", ""), fields.get("sha256_float32le", "")
        if name in hashes:
            errors.append(f"duplicate encoder workload hash: {name}")
        if not re.fullmatch(r"[0-9a-f]{64}", digest):
            errors.append(f"invalid encoder workload SHA-256: {name}")
        hashes[name] = digest
    if set(hashes) != set(ENCODE_CASES):
        errors.append(f"encoder workload hashes incomplete: missing={sorted(set(ENCODE_CASES)-set(hashes))}")
    return hashes, errors


def validate_manifest(manifest: dict[str, Any], artifact_root: pathlib.Path) -> dict[str, Any]:
    errors = list(manifest.get("errors", []))
    if manifest.get("status") != "complete":
        errors.append(f"run status is {manifest.get('status', 'missing')}, expected complete")
        return {"schema_version": 1, "valid": False, "errors": errors,
                "metadata": manifest.get("metadata", {}), "references": manifest.get("references", {}),
                "binaries": manifest.get("binaries", {}), "go_benchmarks": [], "matched_c_benchmarks": []}

    # Validate references recorded by the runner, including the exact target flags.
    references = manifest.get("references", {})
    for target in TARGETS:
        for variant in ("scalar", "simd"):
            ref = references.get(target, {}).get(variant, {})
            fields = ref.get("stamp_fields", {})
            target_arch = "x86-64" if target == "v1" else f"x86-64-{target}"
            cflags = "-O3 -DNDEBUG" + (" -fno-tree-vectorize -fno-tree-slp-vectorize" if variant == "scalar" else "")
            cflags += f" -march={target_arch} -mtune=generic"
            configure = "--enable-static --disable-shared"
            configure += " --disable-asm --disable-rtcd --disable-intrinsics" if variant == "scalar" else " --enable-rtcd --enable-intrinsics"
            if not ref.get("archive_sha256") or not ref.get("config_sha256") or not ref.get("stamp_sha256"):
                errors.append(f"missing {target}/{variant} archive/config/stamp hash")
            for path_key in ("archive_path", "config_path", "stamp_path"):
                try:
                    if not (artifact_root / ref[path_key]).is_file():
                        errors.append(f"missing {target}/{variant} archived {path_key}")
                except KeyError:
                    errors.append(f"missing {target}/{variant} {path_key}")
            if (fields.get("version") != "1.6.1" or fields.get("amd64_target") != target
                    or fields.get("CFLAGS") != cflags or fields.get("configure") != configure
                    or any(fields.get(feature) != "0" for feature in ("qext", "fixed", "custom"))):
                errors.append(f"{target}/{variant} libopus stamp does not match requested target/flags")

    canonical = {(mode, spec["name"]): spec for mode in MODES for spec in exact_suite_specs(mode)}
    exact = {}
    for item in manifest.get("exactness", []):
        key = (item.get("target"), item.get("mode"), item.get("name"))
        if key in exact:
            errors.append(f"duplicate exactness record: {key}")
        exact[key] = item
    expected = {(target, mode, spec["name"]) for target in TARGETS for mode in MODES for spec in exact_suite_specs(mode)}
    if set(exact) != expected:
        errors.append(f"exactness records incomplete: missing={sorted(expected-set(exact))}")
    for (target, mode, name), item in exact.items():
        spec = canonical.get((mode, name))
        if spec is None:
            errors.append(f"unknown exactness suite: {mode}/{name}")
            continue
        for field in ("package", "selector", "expected_leaves"):
            if item.get(field) != spec.get(field):
                errors.append(f"{target}/{mode}/{name} {field} differs from the reviewed expectation")
        if bool(item.get("strict_cbr")) != bool(spec.get("strict_cbr")):
            errors.append(f"{target}/{mode}/{name} CBR policy differs from the reviewed expectation")
        try:
            text = _read(artifact_root, item["stdout"])
        except (KeyError, OSError) as exc:
            errors.append(f"missing {target}/{mode}/{name} JSONL: {exc}")
            continue
        errors.extend(f"{target}/{mode}/{name}: {err}" for err in validate_go_test_json(text, spec["expected_leaves"], item.get("exit", -1)))
        if spec.get("strict_cbr"):
            outputs = []
            for line in text.splitlines():
                try:
                    event = json.loads(line)
                except json.JSONDecodeError:
                    continue
                outputs.append(event.get("Output", ""))
            c_variant = "scalar" if mode == "nosimd" else "simd"
            errors.extend(f"{target}/{mode}/{name}: {err}" for err in validate_cbr_summary("".join(outputs), c_variant))

    expected_binary_keys = {f"{target}/{arm}/root" for target in TARGETS for arm in ARMS} | {
        f"{target}/{mode}/{tool}" for target in TARGETS for mode in MODES
        for tool in ("encoderbenchcmp", "testvectorbenchcmp")
    }
    binaries: dict[str, dict[str, Any]] = {}
    raw_binaries = manifest.get("binaries", {})
    binary_items = ([{"key": key, **value} for key, value in raw_binaries.items()]
                    if isinstance(raw_binaries, dict) else raw_binaries)
    for item in binary_items:
        key = item.get("key")
        if key in binaries:
            errors.append(f"duplicate Go binary build record: {key}")
        binaries[key] = item
    if set(binaries) != expected_binary_keys:
        errors.append(f"Go binary records incomplete: missing={sorted(expected_binary_keys-set(binaries))}")
    metadata = manifest.get("metadata", {})
    expected_toolchain = metadata.get("go_toolchain") or extract_go_toolchain(metadata.get("go_version", ""))
    if expected_toolchain is None:
        errors.append("run metadata does not identify the pinned Go toolchain")
    for key, item in binaries.items():
        target, mode = item.get("target"), item.get("mode")
        try:
            info = _read(artifact_root, item["version_m"])
            binary_path = artifact_root / item["path"]
            if not binary_path.is_file():
                raise OSError("binary file missing")
        except (KeyError, OSError) as exc:
            errors.append(f"missing binary/build info for {key}: {exc}")
            continue
        errors.extend(f"{key} {error}" for error in
                      validate_binary_build_info(info, target, mode, expected_toolchain))

    startup_records = {}
    for item in manifest.get("startup_checks", []):
        key = (item.get("target"), item.get("arm"))
        if key in startup_records:
            errors.append(f"duplicate target-binary startup record: {key}")
        startup_records[key] = item.get("exit")
    startups = startup_records
    expected_startups = {(target, arm) for target in TARGETS for arm in ARMS}
    if set(startups) != expected_startups:
        errors.append("target-binary startup checks are incomplete")
    for key, code in startups.items():
        if code != 0:
            errors.append(f"target-binary startup failed: {key} exit={code}")

    e2e_records = manifest.get("e2e_samples", [])
    e2e_ids = [(row.get("target"), row.get("arm"), row.get("sample")) for row in e2e_records]
    expected_e2e = {(target, arm, sample) for target in TARGETS for arm in ARMS for sample in (1, 2, 3, 4)}
    if len(e2e_ids) != len(set(e2e_ids)) or set(e2e_ids) != expected_e2e:
        errors.append("E2E benchmark rounds must contain each target/arm/round exactly once")
    forward = [f"{target}/{arm}" for target in TARGETS for arm in ARMS]
    expected_orders = {
        "1-forward": forward,
        "2-reverse": list(reversed(forward)),
        "3-rotated-forward": forward[3:] + forward[:3],
        "4-rotated-reverse": list(reversed(forward[3:] + forward[:3])),
    }
    if manifest.get("metadata", {}).get("round_orders") != expected_orders:
        errors.append("recorded E2E round orders differ from the required rotation")
    for sample, label in enumerate(expected_orders, 1):
        rows = sorted((row for row in e2e_records if row.get("sample") == sample),
                      key=lambda row: row.get("sequence", -1))
        actual_order = [f"{row.get('target')}/{row.get('arm')}" for row in rows]
        if actual_order != expected_orders[label]:
            errors.append(f"E2E round {sample} order differs from {label}")
        for offset, row in enumerate(rows, 1):
            if row.get("sequence") != (sample - 1) * len(ARMS) * len(TARGETS) + offset:
                errors.append(f"E2E round {sample} has invalid sequence index")
    e2e: dict[tuple[str, str, str], list[float]] = {}
    for record in e2e_records:
        key = (record.get("target"), record.get("arm"), record.get("sample"))
        try:
            text = _read(artifact_root, record["stdout"])
        except (KeyError, OSError) as exc:
            errors.append(f"missing E2E output {key}: {exc}")
            continue
        rows, row_errors = parse_e2e(text, record.get("exit", -1))
        errors.extend(f"{key}: {err}" for err in row_errors)
        for name, value in rows.items():
            e2e.setdefault((key[0], key[1], name), []).append(value)
    for target in TARGETS:
        for arm in ARMS:
            for name in E2E_NAMES:
                if len(e2e.get((target, arm, name), [])) != 4:
                    errors.append(f"E2E cell {target}/{arm}/{name} does not have four samples")

    c_reports = {}
    for item in manifest.get("c_benchmarks", []):
        key = (item.get("target"), item.get("mode"))
        if key in c_reports:
            errors.append(f"duplicate paired C benchmark report: {key}")
        c_reports[key] = item
    expected_c_reports = {(target, mode) for target in TARGETS for mode in MODES}
    if set(c_reports) != expected_c_reports:
        errors.append("paired C benchmark reports are incomplete")
    c_values = {}
    for key, item in c_reports.items():
        try:
            enc_text = _read(artifact_root, item["encoder_tsv"])
            dec_text = _read(artifact_root, item["decode_tsv"])
        except (KeyError, OSError) as exc:
            errors.append(f"missing paired C TSV {key}: {exc}")
            continue
        values, cell_errors = validate_tool_tsv(enc_text, dec_text, item.get("encoder_exit", -1), item.get("decode_exit", -1))
        errors.extend(f"{key}: {err}" for err in cell_errors)
        c_values[key] = values
        try:
            hash_text = _read(artifact_root, item["encoder_stderr"])
        except (KeyError, OSError) as exc:
            errors.append(f"missing encoder workload hash metadata {key}: {exc}")
            continue
        hashes, hash_errors = _input_hashes(hash_text, key[0])
        errors.extend(f"{key}: {err}" for err in hash_errors)
        item["input_hashes"] = hashes

    for workload in ENCODE_CASES:
        hashes = {
            item.get("input_hashes", {}).get(workload)
            for item in c_reports.values()
        }
        if len(hashes) != 1 or None in hashes:
            errors.append(f"encoder PCM input hash differs across target/mode cells for {workload}")

    summary: dict[str, Any] = {
        "schema_version": 1, "valid": not errors, "errors": errors,
        "metadata": manifest.get("metadata", {}), "go_benchmarks": [], "matched_c_benchmarks": [],
        "references": manifest.get("references", {}), "binaries": manifest.get("binaries", {}),
    }
    if errors:
        return summary
    for target in TARGETS:
        for name in E2E_NAMES:
            summary["go_benchmarks"].append({
                "target": target, "workload": name.removeprefix("Benchmark"), "unit": "ns/op",
                **{arm: statistics.median(e2e[(target, arm, name)]) for arm in ARMS},
            })
        for family, workloads in (("encode", ENCODE_CASES), ("decode", DECODE_PATHS)):
            for workload in workloads:
                scalar = c_values[(target, "nosimd")][(family, workload)]
                simd = c_values[(target, "simd")][(family, workload)]
                summary["matched_c_benchmarks"].append({
                    "target": target, "family": family, "workload": workload, "unit": "ns/sample",
                    "c_scalar": scalar["libopus"], "go_scalar": scalar["gopus"],
                    "c_simd": simd["libopus"], "go_simd": simd["gopus"],
                })
    return summary


def render_markdown(summary: dict[str, Any]) -> str:
    lines = ["# GOAMD64 v1/v2/v3 benchmark matrix", ""]
    if not summary["valid"]:
        lines.extend(["**Invalid or incomplete evidence. No timing matrices are published.**", "", "## Validation errors", ""])
        lines.extend(f"- {error}" for error in summary["errors"])
        return "\n".join(lines) + "\n"
    lines.extend([
        "Values are medians. GOAMD64 is the compiler target level and does not identify the active SIMD ISA.",
        "E2E values are ns/op from four rotated rounds; all Go allocation counts are zero.",
        "Decode differential leaves compare fresh-state packets across three output formats; they do not measure streaming transitions.",
        "", "## End-to-end Go workloads", "",
        "| Target | Workload | Old ASM (ns/op) | Candidate nosimd (ns/op) | Candidate SIMD (ns/op) |",
        "| --- | --- | ---: | ---: | ---: |",
    ])
    for row in summary["go_benchmarks"]:
        lines.append(f"| {row['target']} | {row['workload']} | {row['oldasm']:.2f} | {row['nosimd']:.2f} | {row['simd']:.2f} |")
    lines.extend([
        "", "## Matched libopus workloads", "",
        "Encoder and RFC decode values are ns/sample; Go allocations are zero. Libopus allocations are not measured.",
        "", "| Target | Workload | C scalar | Go scalar | C SIMD | Go SIMD |",
        "| --- | --- | ---: | ---: | ---: | ---: |",
    ])
    for row in summary["matched_c_benchmarks"]:
        lines.append(f"| {row['target']} | {row['workload']} | {row['c_scalar']:.2f} | {row['go_scalar']:.2f} | {row['c_simd']:.2f} | {row['go_simd']:.2f} |")
    metadata = summary.get("metadata", {})
    lines.extend(["", "## Run metadata", "", f"- Go: `{metadata.get('go_version', 'unknown')}`",
                  f"- Runner: `{metadata.get('runner', 'unknown')}`",
                  f"- Baseline commit: `{metadata.get('baseline_commit', 'unknown')}`",
                  f"- Candidate commit: `{metadata.get('candidate_commit', 'unknown')}`",
        "- Per-binary Go build info and C archive/config/stamp hashes are recorded in `manifest.json` and target artifacts.", ""])
    return "\n".join(lines)


def write_summary(root: pathlib.Path, manifest: dict[str, Any]) -> dict[str, Any]:
    summary = validate_manifest(manifest, root)
    (root / "summary.json").write_text(json.dumps(summary, indent=2, sort_keys=True) + "\n")
    (root / "summary.md").write_text(render_markdown(summary))
    return summary


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact_dir", type=pathlib.Path)
    args = parser.parse_args()
    try:
        data = json.loads((args.artifact_dir / "manifest.json").read_text())
    except (OSError, json.JSONDecodeError) as exc:
        print(f"cannot read benchmark manifest: {exc}", file=sys.stderr)
        sys.exit(2)
    result = write_summary(args.artifact_dir, data)
    print(f"GOAMD64 benchmark evidence valid={result['valid']} errors={len(result['errors'])}")
    for error in result["errors"]:
        print(f"- {error}", file=sys.stderr)
    sys.exit(0 if result["valid"] else 1)
