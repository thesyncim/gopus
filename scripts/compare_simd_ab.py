#!/usr/bin/env python3
"""Gate native Go SIMD against old assembly and the same SIMD libopus build."""

import argparse
import json
import pathlib
import re
import sys


CBR_ROW = re.compile(r"^\s+\S+\.go:\d+:\s+(\S+)\s+(\d+)\s+(\d+)\s+(OK|FAIL|RESIDUAL|SKIP|~(?: \([^)]*\))?)\s*$")
CBR_TOTAL = re.compile(r"pass=(\d+) residual=(\d+) fail=(\d+) skip=(\d+)")
CBR_STRICT_TOTAL = re.compile(
    r"strict paired CBR summary: variant=(\S+) cases=(\d+) exact_cases=(\d+) "
    r"packets=(\d+) packet_diffs=(\d+) range_diffs=(\d+)"
)
CBR_SEVERITY = {"OK": 0, "RESIDUAL": 1, "FAIL": 2, "SKIP": 3}
DECODE_DETAIL = re.compile(r"(?:PCM diverges|diverging packet=)")
BENCH = re.compile(r"^Benchmark\S+\s+\d+\s+\S+ ns/op\s+(\d+) B/op\s+(\d+) allocs/op$")
PRECISION_GO = re.compile(r"RealContent gopus Q=([-\d.]+)")
PRECISION_C = re.compile(r"RealContent libopus Q=([-\d.]+)")
# Hybrid-FB-20ms-stereo-96k: testvectors floor -0.05 plus measurement tolerance 0.15.
PRECISION_MIN_GAP = -0.20
SAMPLE_DIFFERENCE = re.compile(r"(\d+)/(\d+) samples differ, maxAbs=([\d.eE+-]+)")
REMOVED_ASSEMBLY_TEST = re.compile(
    r"^(?:TestAssemblyValidationContract|TestCELTLegacyFloat64AssemblyRequiresOptInTag|"
    r"FuzzCELTAssemblyWrappersMatchReference(?:/seed#\d+)?|"
    r"TestCELTAssemblyWrappersMatchReferenceEdges|"
    r"FuzzSilkAssemblyKernelsMatchReference(?:/seed#\d+)?|"
    r"TestSilkAssemblyKernelsMatchReference)$"
)
REPLACEMENT_TESTS = {
    ("github.com/thesyncim/gopus", "TestKernelTreeContainsNoAssembly"),
    ("github.com/thesyncim/gopus/internal/celt", "FuzzCELTKernelsMatchReference"),
    ("github.com/thesyncim/gopus/internal/celt", "TestCELTKernelsMatchReferenceEdges"),
    ("github.com/thesyncim/gopus/internal/silk", "FuzzSilkKernelsMatchReference"),
    ("github.com/thesyncim/gopus/internal/silk", "TestSilkKernelsMatchReference"),
}


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
            rows[name] = (int(frames), int(differences), "RESIDUAL" if status.startswith("~") else status)
    if not rows:
        raise ValueError("no CBR summary rows")
    total = CBR_TOTAL.search(log)
    if not total or sum(int(value) for value in total.groups()) != len(rows):
        raise ValueError("CBR summary count does not match parsed rows")
    return rows


def strict_cbr_summary(log: str):
    matches = list(CBR_STRICT_TOTAL.finditer(log))
    if len(matches) != 1:
        raise ValueError(f"expected one strict paired CBR summary, found {len(matches)}")
    variant, cases, exact_cases, packets, packet_diffs, range_diffs = matches[0].groups()
    return {
        "variant": variant,
        "cases": int(cases),
        "exact_cases": int(exact_cases),
        "packets": int(packets),
        "packet_diffs": int(packet_diffs),
        "range_diffs": int(range_diffs),
    }


def first_strict_cbr_mismatch(log: str):
    marker = "exact paired CBR mismatch:"
    for line in log.splitlines():
        if marker in line:
            return line.split(marker, 1)[1].strip()
    return ""


def strict_cbr_errors(exit_code: int, log: str, expected_cases: int = 19):
    try:
        summary = strict_cbr_summary(log)
    except ValueError as exc:
        return [f"strict CBR summary: {exc}"]
    errors = []
    if summary["cases"] != expected_cases or summary["exact_cases"] != expected_cases:
        errors.append(
            f"strict CBR covered {summary['exact_cases']}/{summary['cases']} exact cases; want {expected_cases}/{expected_cases}"
        )
    if exit_code or summary["packet_diffs"] or summary["range_diffs"]:
        mismatch = first_strict_cbr_mismatch(log)
        detail = f"; first mismatch: {mismatch}" if mismatch else ""
        errors.append(
            f"strict CBR failed: exit={exit_code} variant={summary['variant']} "
            f"cases={summary['cases']} exact={summary['exact_cases']} packets={summary['packet_diffs']} "
            f"ranges={summary['range_diffs']}{detail}"
        )
    return errors


def decode_details(log: str):
    return [line.split(": ", 2)[-1].strip() for line in log.splitlines() if DECODE_DETAIL.search(line)]


def precision_gap(log: str):
    go_q, c_q = PRECISION_GO.search(log), PRECISION_C.search(log)
    if not go_q or not c_q:
        raise ValueError("missing mode-matched Go or libopus Q")
    return float(go_q.group(1)) - float(c_q.group(1))


def full_parity(log: str):
    tests = {}
    failed_packages = set()
    sample_differences = []
    for line in log.splitlines():
        event = json.loads(line)
        action = event.get("Action")
        package = event.get("Package")
        test = event.get("Test")
        if action in {"pass", "fail", "skip"} and test:
            key = (package, test)
            if key in tests:
                raise ValueError(f"duplicate full-parity result: {key}")
            tests[key] = action
        elif action == "fail" and package:
            failed_packages.add(package)
        if action == "output" and test:
            for count, total, maximum in SAMPLE_DIFFERENCE.findall(event.get("Output", "")):
                sample_differences.append((int(count), int(total), float(maximum)))
    if not tests:
        raise ValueError("full-parity JSON has no test results")
    return tests, failed_packages, sample_differences


def compare_full_parity(root: pathlib.Path):
    base_code, base_log = read_phase(root, "baseline", "default-full-parity")
    simd_code, simd_log = read_phase(root, "candidate", "simd-full-parity")
    if base_code not in {0, 1} or simd_code not in {0, 1}:
        return [f"full parity did not finish normally: old asm={base_code}, Go SIMD={simd_code}"], None
    base_tests, base_packages, base_samples = full_parity(base_log)
    simd_tests, simd_packages, simd_samples = full_parity(simd_log)
    errors = []
    missing = {key for key in base_tests.keys() - simd_tests.keys() if not REMOVED_ASSEMBLY_TEST.fullmatch(key[1])}
    if missing:
        errors.append(f"Go SIMD omits {len(missing)} baseline tests: {sorted(missing)[:5]}")
    for key in sorted(REPLACEMENT_TESTS):
        if simd_tests.get(key) != "pass":
            errors.append(f"replacement oracle does not pass: {key}")
    new_skips = {key for key in base_tests.keys() & simd_tests.keys()
                 if base_tests[key] != "skip" and simd_tests[key] == "skip"}
    if new_skips:
        errors.append(f"Go SIMD skips {len(new_skips)} baseline tests: {sorted(new_skips)[:5]}")
    new_failures = {key for key, status in simd_tests.items()
                    if status == "fail" and base_tests.get(key) != "fail"}
    if new_failures:
        errors.append(f"Go SIMD adds {len(new_failures)} failing tests: {sorted(new_failures)[:5]}")
    new_failed_packages = simd_packages - base_packages
    if new_failed_packages:
        errors.append(f"Go SIMD adds failing packages: {sorted(new_failed_packages)}")
    if simd_code and not base_code:
        errors.append("Go SIMD full parity fails while old assembly passes")
    base_sample_count = sum(count for count, _, _ in base_samples)
    simd_sample_count = sum(count for count, _, _ in simd_samples)
    if simd_sample_count > base_sample_count:
        errors.append(f"decode sample differences increase: old asm={base_sample_count}, Go SIMD={simd_sample_count}")
    base_abs_bound = sum(count * maximum for count, _, maximum in base_samples)
    simd_abs_bound = sum(count * maximum for count, _, maximum in simd_samples)
    if simd_abs_bound > base_abs_bound:
        errors.append(f"decode absolute-error upper bound increases: old asm={base_abs_bound}, Go SIMD={simd_abs_bound}")
    summary = (len(base_tests), len(simd_tests),
               sum(status == "fail" for status in base_tests.values()),
               sum(status == "fail" for status in simd_tests.values()),
               base_sample_count, simd_sample_count)
    return errors, summary


def compare(root: pathlib.Path):
    errors = []
    for side, phase in [
        ("baseline", "ensure-libopus"),
        ("baseline", "ensure-libopus-scalar"),
        ("candidate", "ensure-libopus"),
        ("candidate", "ensure-libopus-scalar"),
        ("baseline", "platform-fixtures"),
        ("candidate", "platform-fixtures"),
        ("baseline", "default-selected-kernel-files"),
        ("baseline", "purego-selected-kernel-files"),
        ("candidate", "simd-selected-kernel-files"),
        ("candidate", "simd-xcorr-runtime-identity"),
        ("candidate", "simd-xcorr-one-pass-oracle"),
        ("candidate", "simd-pvq-dispatch"),
        ("baseline", "default-precision-guard"),
        ("baseline", "purego-precision-guard"),
        ("candidate", "simd-precision-guard"),
    ]:
        code, _ = read_phase(root, side, phase)
        if code:
            errors.append(f"{side} {phase} exited {code}")

    base_code, base_cbr_log = read_phase(root, "baseline", "default-cbr-parity")
    base_cbr = cbr_rows(base_cbr_log)
    if base_code or len(base_cbr) != 19:
        errors.append(f"baseline CBR summary failed or covered {len(base_cbr)}/19 cases")
    scalar_code, scalar_cbr_log = read_phase(root, "baseline", "purego-cbr-parity")
    scalar_cbr = cbr_rows(scalar_cbr_log)
    if scalar_code or len(scalar_cbr) != 19:
        errors.append(f"baseline purego CBR summary failed or covered {len(scalar_cbr)}/19 cases")

    for mode in ("default", "nosimd", "simd"):
        code, log = read_phase(root, "candidate", f"{mode}-cbr-parity")
        errors.extend(
            f"candidate {mode} {error}"
            for error in strict_cbr_errors(code, log)
        )

    for mode in ("default", "nosimd", "simd"):
        code, log = read_phase(root, "candidate", f"{mode}-precision-guard")
        if code:
            errors.append(f"candidate {mode} precision guard exited {code}")
        try:
            gap = precision_gap(log)
        except ValueError as exc:
            errors.append(f"candidate {mode} precision guard: {exc}")
        else:
            if gap < PRECISION_MIN_GAP:
                errors.append(f"candidate {mode} quality gap exceeds the existing -0.05 floor and 0.15 tolerance")

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

    full_errors, full_summary = compare_full_parity(root)
    errors.extend(full_errors)

    return errors, len(base_cbr), full_summary


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("artifact_dir", type=pathlib.Path)
    args = parser.parse_args()
    try:
        errors, cases, full_summary = compare(args.artifact_dir)
    except (OSError, ValueError) as exc:
        errors, cases, full_summary = [str(exc)], 0, None
    for error in errors:
        print(f"SIMD A/B regression: {error}", file=sys.stderr)
    if errors:
        return 1
    print(f"Native SIMD A/B passes: {cases} CBR cases, focused decode, precision, dispatch, zero-allocation kernels")
    if full_summary:
        old_tests, go_tests, old_fails, go_fails, old_samples, go_samples = full_summary
        print(f"Full parity: {old_tests} old-asm tests / {go_tests} Go SIMD tests; failures {old_fails} → {go_fails}; differing samples {old_samples} → {go_samples}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
