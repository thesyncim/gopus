#!/usr/bin/env python3
"""Gate native Go SIMD against old assembly and the same SIMD libopus build."""

import argparse
import pathlib
import re
import sys


CBR_ROW = re.compile(r"^\s+\S+\.go:\d+:\s+(\S+)\s+(\d+)\s+(\d+)\s+(OK|FAIL|RESIDUAL|SKIP)\s*$")
CBR_TOTAL = re.compile(r"pass=(\d+) residual=(\d+) fail=(\d+) skip=(\d+)")
CBR_SEVERITY = {"OK": 0, "RESIDUAL": 1, "FAIL": 2, "SKIP": 3}
DECODE_DETAIL = re.compile(r"(?:PCM diverges|diverging packet=)")
BENCH = re.compile(r"^Benchmark\S+\s+\d+\s+\S+ ns/op\s+(\d+) B/op\s+(\d+) allocs/op$")


def read_phase(root: pathlib.Path, side: str, phase: str):
    stem = root / f"{side}-{phase}"
    return int(stem.with_suffix(".exit").read_text().strip()), stem.with_suffix(".log").read_text()


def cbr_rows(log: str):
    rows = {}
    for line in log.splitlines():
        match = CBR_ROW.match(line)
        if match:
            name, frames, differences, status = match.groups()
            if name in rows:
                raise ValueError(f"duplicate CBR case: {name}")
            rows[name] = (int(frames), int(differences), status)
    if not rows:
        raise ValueError("no CBR summary rows")
    total = CBR_TOTAL.search(log)
    if not total or sum(int(value) for value in total.groups()) != len(rows):
        raise ValueError("CBR summary count does not match parsed rows")
    return rows


def decode_details(log: str):
    return [line.split(": ", 2)[-1].strip() for line in log.splitlines() if DECODE_DETAIL.search(line)]


def compare(root: pathlib.Path):
    errors = []
    for side, phase in [
        ("baseline", "ensure-libopus"),
        ("candidate", "ensure-libopus"),
        ("candidate", "ensure-libopus-scalar"),
        ("baseline", "platform-fixtures"),
        ("candidate", "platform-fixtures"),
        ("baseline", "default-selected-kernel-files"),
        ("candidate", "simd-selected-kernel-files"),
        ("candidate", "simd-xcorr-runtime-identity"),
        ("candidate", "simd-pvq-dispatch"),
        ("baseline", "default-precision-guard"),
        ("candidate", "simd-precision-guard"),
    ]:
        code, _ = read_phase(root, side, phase)
        if code:
            errors.append(f"{side} {phase} exited {code}")

    _, base_cbr_log = read_phase(root, "baseline", "default-cbr-parity")
    _, simd_cbr_log = read_phase(root, "candidate", "simd-cbr-parity")
    base_cbr, simd_cbr = cbr_rows(base_cbr_log), cbr_rows(simd_cbr_log)
    if base_cbr.keys() != simd_cbr.keys():
        errors.append(f"CBR case sets differ: baseline={sorted(base_cbr)} candidate={sorted(simd_cbr)}")
    for name in sorted(base_cbr.keys() & simd_cbr.keys()):
        base_frames, base_diff, base_status = base_cbr[name]
        simd_frames, simd_diff, simd_status = simd_cbr[name]
        if simd_frames != base_frames or simd_diff > base_diff:
            errors.append(f"{name}: old asm {base_diff}/{base_frames}, Go SIMD {simd_diff}/{simd_frames}")
        if CBR_SEVERITY[simd_status] > CBR_SEVERITY[base_status]:
            errors.append(f"{name}: old asm reports {base_status}, Go SIMD reports {simd_status}")
        if simd_status == "SKIP":
            errors.append(f"{name}: Go SIMD skipped the CBR case")

    base_code, base_decode = read_phase(root, "baseline", "default-decode-differential")
    simd_code, simd_decode = read_phase(root, "candidate", "simd-decode-differential")
    base_details, simd_details = decode_details(base_decode), decode_details(simd_decode)
    if base_code != simd_code or base_details != simd_details or (simd_code and not simd_details):
        errors.append("focused decode differential differs from old assembly")

    for side, mode in [("baseline", "default"), ("candidate", "default"), ("candidate", "simd")]:
        code, log = read_phase(root, side, f"{mode}-kernel-benchmarks")
        lines = [line for line in log.splitlines() if line.startswith("Benchmark")]
        if code or not lines:
            errors.append(f"{side} {mode} direct kernel benchmarks missing or failed")
        for line in lines:
            match = BENCH.match(line)
            if not match or match.groups() != ("0", "0"):
                errors.append(f"{side} {mode} allocation or malformed benchmark: {line}")

    return errors, len(base_cbr)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact_dir", type=pathlib.Path)
    args = parser.parse_args()
    try:
        errors, cases = compare(args.artifact_dir)
    except (OSError, ValueError) as exc:
        errors, cases = [str(exc)], 0
    for error in errors:
        print(f"SIMD A/B regression: {error}", file=sys.stderr)
    if errors:
        return 1
    print(f"Native SIMD A/B passes: {cases} CBR cases, focused decode, precision, dispatch, zero-allocation kernels")
    return 0


if __name__ == "__main__":
    sys.exit(main())
