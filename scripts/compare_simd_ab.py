#!/usr/bin/env python3
"""Gate lane-matched Go implementations against pinned libopus builds."""

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
E2E_BENCHMARKS = frozenset({
    "BenchmarkDecoderDecode_CELT",
    "BenchmarkDecoderDecode_Hybrid",
    "BenchmarkDecoderDecode_SILK",
    "BenchmarkEncoderEncode_CallerBuffer",
    "BenchmarkEncoderEncode_VoIP",
    "BenchmarkEncoderEncode_LowDelay",
})
E2E_BENCH_NAME = re.compile(
    r"^(Benchmark(?:DecoderDecode_(?:CELT|Hybrid|SILK)|"
    r"EncoderEncode_(?:CallerBuffer|VoIP|LowDelay)))(?:-\d+)?\s"
)
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


def audited_test_replacements():
    # Exact names only: a renamed parent does not silently excuse absent
    # children. Each replacement's reviewed leaf set must also pass.
    path = pathlib.Path(__file__).with_name("simd_ab_test_replacements.json")
    replacements = {}
    for group in json.loads(path.read_text()):
        targets = {(group["baseline_package"], item) if isinstance(item, str)
                   else (item["package"], item["test"])
                   for item in group["required_candidate_tests"]}
        if not targets:
            raise ValueError("empty audited test replacement")
        for test in group["baseline_tests"]:
            key = (group["baseline_package"], test)
            if key in replacements:
                raise ValueError(f"duplicate audited test replacement: {key}")
            replacements[key] = targets
    return replacements


def audited_test_retirements():
    # These exact internal API contracts no longer exist. Their removal is
    # reported separately from passing replacement coverage and codec parity.
    path = pathlib.Path(__file__).with_name("simd_ab_retired_test_contracts.json")
    retired = set()
    for group in json.loads(path.read_text()):
        if not group["retired_contract"] or not group["source_review"]:
            raise ValueError("undocumented retired test contract")
        for test in group["baseline_tests"]:
            key = (group["baseline_package"], test)
            if key in retired:
                raise ValueError(f"duplicate retired test contract: {key}")
            retired.add(key)
    return retired


def missing_baseline_tests(base_tests, candidate_tests):
    replacements = audited_test_replacements()
    retired = audited_test_retirements()
    missing = set()
    for key in base_tests.keys() - candidate_tests.keys():
        if key in retired or REMOVED_ASSEMBLY_TEST.fullmatch(key[1]):
            continue
        targets = replacements.get(key)
        if targets and all(candidate_tests.get(target) == "pass" for target in targets):
            continue
        missing.add(key)
    return missing


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


def baseline_cbr_errors(exit_code: int, log: str, expected_cases: int = 19):
    """Require complete baseline evidence; its codec residuals remain reported."""
    rows = cbr_rows(log)
    errors = []
    if exit_code not in {0, 1} or len(rows) != expected_cases:
        errors.append(f"baseline CBR did not complete: exit={exit_code} cases={len(rows)}/{expected_cases}")
    if any(status == "SKIP" for _, _, status in rows.values()):
        errors.append("baseline CBR contains skipped cases")
    return errors


def candidate_decode_errors(exit_code: int, log: str):
    # A passing candidate must not reproduce a baseline decode error. The
    # selected libopus reference, rather than old Go output, defines parity.
    if exit_code or decode_details(log):
        return [f"candidate focused decode differs from matched libopus: exit={exit_code}"]
    if "--- PASS: TestDecodeDifferentialEncodeThenDecode/" not in log:
        return ["candidate focused decode has no passing configurations"]
    if "--- SKIP:" in log:
        return ["candidate focused decode contains skipped configurations"]
    return []


def precision_gap(log: str):
    go_q, c_q = PRECISION_GO.search(log), PRECISION_C.search(log)
    if not go_q or not c_q:
        raise ValueError("missing mode-matched Go or libopus Q")
    return float(go_q.group(1)) - float(c_q.group(1))


def e2e_benchmark_errors(exit_code: int, log: str):
    lines = [line for line in log.splitlines() if line.startswith("Benchmark")]
    errors = []
    if exit_code or not lines:
        errors.append(f"E2E benchmark command exited {exit_code} or produced no rows")
    names = set()
    for line in lines:
        match = BENCH.match(line)
        name = E2E_BENCH_NAME.match(line)
        if not match or not name:
            errors.append(f"malformed or unexpected E2E benchmark row: {line}")
            continue
        names.add(name.group(1))
        if match.groups() != ("0", "0"):
            errors.append(f"E2E benchmark allocates: {line}")
    missing = E2E_BENCHMARKS - names
    if missing:
        errors.append(f"E2E benchmark rows missing: {sorted(missing)}")
    return errors


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
    base_code, base_log = read_phase(root, "baseline", "simd-full-parity")
    candidate_code, candidate_log = read_phase(root, "candidate", "simd-full-parity")
    if base_code not in {0, 1} or candidate_code not in {0, 1}:
        return [f"full SIMD parity did not finish normally: base={base_code}, candidate={candidate_code}"], None
    base_tests, base_packages, base_samples = full_parity(base_log)
    candidate_tests, candidate_packages, candidate_samples = full_parity(candidate_log)
    errors = []
    missing = missing_baseline_tests(base_tests, candidate_tests)
    if missing:
        errors.append(f"candidate Go SIMD omits {len(missing)} base tests: {sorted(missing)[:5]}")
    for key in sorted(REPLACEMENT_TESTS):
        if candidate_tests.get(key) != "pass":
            errors.append(f"replacement oracle does not pass: {key}")
    new_skips = {key for key in base_tests.keys() & candidate_tests.keys()
                 if base_tests[key] != "skip" and candidate_tests[key] == "skip"}
    if new_skips:
        errors.append(f"candidate Go SIMD skips {len(new_skips)} base tests: {sorted(new_skips)[:5]}")
    new_failures = {key for key, status in candidate_tests.items()
                    if status == "fail" and base_tests.get(key) != "fail"}
    if new_failures:
        errors.append(f"candidate Go SIMD adds {len(new_failures)} failing tests: {sorted(new_failures)[:5]}")
    new_failed_packages = candidate_packages - base_packages
    if new_failed_packages:
        errors.append(f"candidate Go SIMD adds failing packages: {sorted(new_failed_packages)}")
    if candidate_code and not base_code:
        errors.append("candidate Go SIMD full parity fails while base Go SIMD passes")
    base_sample_count = sum(count for count, _, _ in base_samples)
    candidate_sample_count = sum(count for count, _, _ in candidate_samples)
    if candidate_sample_count > base_sample_count:
        errors.append(f"decode sample differences increase: base Go SIMD={base_sample_count}, candidate Go SIMD={candidate_sample_count}")
    base_abs_bound = sum(count * maximum for count, _, maximum in base_samples)
    candidate_abs_bound = sum(count * maximum for count, _, maximum in candidate_samples)
    if candidate_abs_bound > base_abs_bound:
        errors.append(f"decode absolute-error upper bound increases: base Go SIMD={base_abs_bound}, candidate Go SIMD={candidate_abs_bound}")
    summary = (len(base_tests), len(candidate_tests),
               sum(status == "fail" for status in base_tests.values()),
               sum(status == "fail" for status in candidate_tests.values()),
               base_sample_count, candidate_sample_count,
               sorted((base_tests.keys() - candidate_tests.keys()) & audited_test_retirements()))
    return errors, summary


def compare(root: pathlib.Path):
    errors = []
    for side, phase in [
        ("baseline", "ensure-libopus"),
        ("baseline", "ensure-libopus-scalar"),
        ("baseline", "ensure-libopus-simd"),
        ("candidate", "ensure-libopus"),
        ("candidate", "ensure-libopus-scalar"),
        ("candidate", "ensure-libopus-simd"),
        ("baseline", "platform-fixtures"),
        ("candidate", "platform-fixtures"),
        ("baseline", "save-simd-opusdec-fixture"),
        ("candidate", "save-simd-opusdec-fixture"),
        ("baseline", "restore-simd-opusdec-fixture"),
        ("candidate", "restore-simd-opusdec-fixture"),
        ("baseline", "default-selected-kernel-files"),
        ("baseline", "purego-selected-kernel-files"),
        ("baseline", "simd-selected-kernel-files"),
        ("candidate", "simd-selected-kernel-files"),
        ("baseline", "simd-xcorr-runtime-identity"),
        ("candidate", "simd-xcorr-runtime-identity"),
        ("candidate", "simd-xcorr-one-pass-oracle"),
        ("candidate", "simd-pvq-dispatch"),
        ("baseline", "default-precision-guard"),
        ("baseline", "purego-precision-guard"),
        ("baseline", "simd-precision-guard"),
        ("candidate", "simd-precision-guard"),
    ]:
        code, _ = read_phase(root, side, phase)
        if code:
            errors.append(f"{side} {phase} exited {code}")

    base_code, base_cbr_log = read_phase(root, "baseline", "default-cbr-parity")
    base_cbr = cbr_rows(base_cbr_log)
    errors.extend(baseline_cbr_errors(base_code, base_cbr_log))
    scalar_code, scalar_cbr_log = read_phase(root, "baseline", "purego-cbr-parity")
    errors.extend(f"purego {error}" for error in baseline_cbr_errors(scalar_code, scalar_cbr_log))
    base_simd_code, base_simd_cbr_log = read_phase(root, "baseline", "simd-cbr-parity")
    errors.extend(f"baseline simd {error}" for error in baseline_cbr_errors(base_simd_code, base_simd_cbr_log))

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

    for mode in ("default", "simd"):
        base_code, _ = read_phase(root, "baseline", f"{mode}-decode-differential")
        candidate_code, candidate_decode = read_phase(root, "candidate", f"{mode}-decode-differential")
        if base_code not in {0, 1}:
            errors.append(f"baseline {mode} focused decode did not finish normally: exit={base_code}")
        errors.extend(
            f"candidate {mode} {error}"
            for error in candidate_decode_errors(candidate_code, candidate_decode)
        )

    for side, mode in [
        ("baseline", "default"), ("candidate", "default"),
        ("baseline", "simd"), ("candidate", "simd"),
    ]:
        code, log = read_phase(root, side, f"{mode}-kernel-benchmarks")
        lines = [line for line in log.splitlines() if line.startswith("Benchmark")]
        if code or not lines:
            errors.append(f"{side} {mode} direct kernel benchmarks missing or failed")
        for line in lines:
            match = BENCH.match(line)
            if not match or match.groups() != ("0", "0"):
                errors.append(f"{side} {mode} allocation or malformed benchmark: {line}")

    for side, modes in [("baseline", ("default", "simd")),
                        ("candidate", ("default", "nosimd", "simd"))]:
        for mode in modes:
            code, log = read_phase(root, side, f"{mode}-e2e-benchmarks")
            errors.extend(
                f"{side} {mode} {error}"
                for error in e2e_benchmark_errors(code, log)
            )

    for side in ("baseline", "candidate"):
        fixture = root / f"{side}-simd-committed-opusdec-fixture.json"
        if not fixture.is_file() or fixture.stat().st_size == 0:
            errors.append(f"{side} committed SIMD opusdec fixture snapshot is missing or empty")

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
        print(f"Lane-matched A/B regression: {error}", file=sys.stderr)
    if full_summary:
        retired = full_summary[6]
        print(f"Audited retired internal contracts: {len(retired)}; not replacement passes: {retired}")
    if errors:
        return 1
    print(f"Native lane-matched A/B passes: {cases} baseline CBR cases, focused decode, precision, dispatch, zero-allocation kernels")
    if full_summary:
        base_tests, candidate_tests, base_fails, candidate_fails, base_samples, candidate_samples, _ = full_summary
        print(f"Full SIMD parity: {base_tests} base Go SIMD tests / {candidate_tests} candidate Go SIMD tests; failures {base_fails} → {candidate_fails}; differing samples {base_samples} → {candidate_samples}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
