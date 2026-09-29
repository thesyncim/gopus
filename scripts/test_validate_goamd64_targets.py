import json
import sys
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parent))

from benchmark_goamd64 import Runner  # noqa: E402
from validate_goamd64_targets import (  # noqa: E402
    E2E_NAMES,
    ENCODE_CASES,
    DECODE_PATHS,
    extract_go_toolchain,
    exact_suite_role,
    exact_suite_specs,
    parity_contract_spec,
    parse_e2e,
    validate_cbr_summary,
    validate_binary_build_info,
    validate_go_test_json,
    validate_parity_contract_json,
    validate_parity_contract_records,
    validate_parity_contract_summary,
    validate_tool_tsv,
    _input_hashes,
    write_summary,
)


def test_event(action, name=None, **extra):
    value = {"Action": action, **extra}
    if name is not None:
        value["Test"] = name
    return json.dumps(value)


def paired_test_log():
    return "\n".join([
        test_event("run", "TestFixture"),
        test_event("run", "TestFixture/case-one"),
        test_event("pass", "TestFixture/case-one"),
        test_event("run", "TestFixture/case-two"),
        test_event("pass", "TestFixture/case-two"),
        test_event("pass", "TestFixture"),
    ])


def parity_contract_test_log(case_count=19, leaf_action="pass", unresolved=0):
    root = "TestEncoderCBRPairedOracleContract"
    events = [test_event("run", root)]
    for index in range(case_count):
        leaf = f"{root}/case-{index + 1:02d}"
        events.append(test_event("run", leaf))
        events.append(test_event(leaf_action, leaf))
    events.extend([
        test_event("output", root, Output=(
            "    CBR_CONTRACT cases=19 packets=2175 decode_paths=76 "
            f"unresolved_cases={unresolved}\n"
        )),
        test_event("output", root, Output=f"--- PASS: {root} (0.25s)\nPASS\n"),
        test_event("pass" if leaf_action == "pass" else "fail", root),
    ])
    return "\n".join(events)


def parity_contract_manifest(artifact_root, broken=None):
    spec = parity_contract_spec()
    records = []
    for target in ("v1", "v2", "v3"):
        for mode in ("nosimd", "simd"):
            key = (target, mode)
            action = broken[1] if broken is not None and key == broken[0] else "pass"
            path = artifact_root / f"{target}-{mode}.jsonl"
            path.write_text(parity_contract_test_log(leaf_action=action))
            records.append({
                "target": target,
                "mode": mode,
                **spec,
                "gate": "blocking",
                "exit": 1 if action == "fail" else 0,
                "stdout": path.name,
            })
    return {"parity_contracts": records}


def tsv_row(implementation, path, vector):
    allocs = "0" if implementation == "gopus" else "-"
    return "\t".join([
        implementation, path, vector, "250ms", "3", "100", "250000000", "100", "10", "960",
        "2.5", "250.0", "1.0", allocs,
    ])


def tool_tsvs():
    header = "implementation\tpath\tvector\tbenchtime\tcount\titerations\telapsed_ns\tbytes_per_op\tpackets_per_op\tsamples_per_op\tns_per_sample\tns_per_packet\tx_realtime\tallocs_per_op"
    enc = [header]
    for workload in ENCODE_CASES:
        enc.extend([tsv_row("gopus", "Float32", workload), tsv_row("libopus", "Float32", workload)])
    dec = [header]
    for path in DECODE_PATHS:
        dec.extend([tsv_row("gopus", path, "all"), tsv_row("libopus", path, "all")])
    return "\n".join(enc) + "\n", "\n".join(dec) + "\n"


class GoTestJSONValidationTests(unittest.TestCase):
    def test_counts_actual_leaf_events(self):
        self.assertEqual(validate_go_test_json(paired_test_log(), {"TestFixture": 2}, 0), [])

    def test_rejects_missing_leaf_even_when_exit_is_zero(self):
        events = "\n".join([
            test_event("run", "TestFixture"),
            test_event("run", "TestFixture/case-one"),
            test_event("pass", "TestFixture/case-one"),
            test_event("pass", "TestFixture"),
        ])
        errors = validate_go_test_json(events, {"TestFixture": 2}, 0)
        self.assertTrue(any("ran 1 leaf tests, want 2" in error for error in errors))

    def test_rejects_skips_and_package_failure(self):
        events = "\n".join([
            test_event("run", "TestFixture"),
            test_event("run", "TestFixture/case-one"),
            test_event("skip", "TestFixture/case-one"),
            test_event("pass", "TestFixture"),
            test_event("fail"),
        ])
        errors = validate_go_test_json(events, {"TestFixture": 1}, 0)
        self.assertTrue(any("status=skip" in error for error in errors))
        self.assertTrue(any("package-level" in error for error in errors))


class ParityContractValidationTests(unittest.TestCase):
    def test_contract_uses_nineteen_case_leaves(self):
        spec = parity_contract_spec()
        self.assertEqual(spec["package"], "./testvectors")
        self.assertEqual(spec["selector"], r"^TestEncoderCBRPairedOracleContract$")
        self.assertEqual(spec["expected_leaves"], {"TestEncoderCBRPairedOracleContract": 19})
        self.assertEqual(validate_parity_contract_json(parity_contract_test_log(), 0), [])

    def test_contract_requires_every_leaf_to_pass(self):
        missing = validate_parity_contract_json(parity_contract_test_log(case_count=18), 0)
        self.assertTrue(any("ran 18 leaf tests, want 19" in error for error in missing))

        failed = validate_parity_contract_json(parity_contract_test_log(leaf_action="fail"), 1)
        self.assertTrue(any("status=fail" in error for error in failed))

        skipped = validate_parity_contract_json(parity_contract_test_log(leaf_action="skip"), 0)
        self.assertTrue(any("status=skip" in error for error in skipped))

    def test_contract_summary_requires_canonical_counts_and_no_unresolved_cases(self):
        good = "    CBR_CONTRACT cases=19 packets=2175 decode_paths=76 unresolved_cases=0\n"
        self.assertEqual(validate_parity_contract_summary(good + "--- PASS: contract\nPASS\n"), [])
        self.assertTrue(validate_parity_contract_summary(good.replace("packets=2175", "packets=2174")))
        self.assertTrue(validate_parity_contract_summary(good.replace("unresolved_cases=0", "unresolved_cases=1")))
        self.assertTrue(validate_parity_contract_summary(good + good))

    def test_contract_records_are_required_for_every_target_and_mode(self):
        with TemporaryDirectory() as temporary:
            root = Path(temporary)
            _, errors = validate_parity_contract_records({"parity_contracts": []}, root)
            self.assertTrue(any("parity contract records incomplete" in error for error in errors))

            manifest = parity_contract_manifest(root)
            results, errors = validate_parity_contract_records(manifest, root)
            self.assertEqual(errors, [])
            self.assertEqual(len(results), 6)
            self.assertTrue(all(result["status"] == "passed" for result in results))

            for action in ("fail", "skip"):
                manifest = parity_contract_manifest(root, ( ("v3", "simd"), action ))
                _, errors = validate_parity_contract_records(manifest, root)
                self.assertTrue(any("v3/simd/parity-contract" in error for error in errors))

    def test_v3_packet_range_exactness_is_diagnostic_but_other_exact_gates_remain(self):
        name = "testvectors-cbr-packet-range-oracle"
        self.assertEqual(exact_suite_role("v1", name), "blocking")
        self.assertEqual(exact_suite_role("v2", name), "blocking")
        self.assertEqual(exact_suite_role("v3", name), "diagnostic")
        self.assertEqual(exact_suite_role("v3", "root-encode-differential"), "blocking")
        self.assertEqual(exact_suite_role("v3", "root-decode-differential"), "blocking")
        self.assertEqual(exact_suite_role("v3", "root-hotpath-allocation-guards"), "blocking")
        self.assertEqual(exact_suite_role("v3", "celt-native-avx2-fma-dispatch-witness"), "blocking")

    def test_parity_contract_runs_after_exact_diagnostics_fail(self):
        with TemporaryDirectory() as temporary:
            root = Path(temporary)
            runner = Runner(root, root, root / "artifact")
            with (
                patch.object(runner, "preflight"),
                patch.object(runner, "build_reference"),
                patch.object(runner, "build_binaries"),
                patch.object(runner, "check_startup"),
                patch.object(runner, "run_exactness", return_value=False),
                patch.object(runner, "run_parity_contracts", return_value=True) as contract_run,
                patch.object(runner, "finish", return_value=1),
            ):
                self.assertEqual(runner.run_all(), 1)
            contract_run.assert_called_once_with()


class BenchmarkValidationTests(unittest.TestCase):
    def test_requires_six_zero_allocation_benchmarks(self):
        text = "\n".join(f"{name}-1 100 100.0 ns/op 0 B/op 0 allocs/op" for name in E2E_NAMES)
        rows, errors = parse_e2e(text, 0)
        self.assertEqual(set(rows), set(E2E_NAMES))
        self.assertEqual(errors, [])

    def test_rejects_missing_benchmark_and_allocation(self):
        lines = [f"{name}-1 100 100.0 ns/op 0 B/op 0 allocs/op" for name in E2E_NAMES[:-1]]
        lines.append(f"{E2E_NAMES[-1]}-1 100 100.0 ns/op 8 B/op 1 allocs/op")
        _, errors = parse_e2e("\n".join(lines), 0)
        self.assertTrue(any("allocates" in error for error in errors))

    def test_paired_tsv_requires_all_rows_and_zero_go_allocations(self):
        encoder, decode = tool_tsvs()
        rows, errors = validate_tool_tsv(encoder, decode, 0, 0)
        self.assertEqual(len(rows), 7)
        self.assertEqual(errors, [])
        bad = encoder.replace("\t0\n", "\t1\n", 1)
        _, errors = validate_tool_tsv(bad, decode, 0, 0)
        self.assertTrue(any("allocs_per_op" in error for error in errors))

    def test_bad_numeric_row_is_reported_without_crashing(self):
        encoder, decode = tool_tsvs()
        encoder = encoder.replace("\t2.5\t", "\tnot-a-number\t", 1)
        _, errors = validate_tool_tsv(encoder, decode, 0, 0)
        self.assertTrue(any("invalid or non-positive" in error for error in errors))


class ReferenceValidationTests(unittest.TestCase):
    def test_binary_build_info_must_match_the_pinned_compiler(self):
        info = "./candidate.test: go1.27.1-X:simd\n\tbuild\tGOAMD64=v3\n\tbuild\tGOEXPERIMENT=simd\n"
        self.assertEqual(extract_go_toolchain("go version go1.27.1 linux/amd64"), "go1.27.1")
        self.assertEqual(extract_go_toolchain("/tmp/go1.26.4/results/v3/test: go1.27.1"), "go1.27.1")
        self.assertEqual(extract_go_toolchain("/tmp/test: go1.27.1-X:simd"), "go1.27.1")
        self.assertIsNone(extract_go_toolchain("/tmp/go1.27.1-results/v3/test"))
        self.assertIsNone(extract_go_toolchain("/tmp/test: go1.27.1-X:unexpected"))
        self.assertEqual(validate_binary_build_info(info, "v3", "simd", "go1.27.1"), [])
        errors = validate_binary_build_info(info, "v3", "simd", "go1.25.0")
        self.assertTrue(any("binary Go toolchain=go1.27.1, want go1.25.0" in error for error in errors))
        no_simd = info.replace("GOEXPERIMENT=simd", "GOEXPERIMENT=none")
        errors = validate_binary_build_info(no_simd, "v3", "simd", "go1.27.1")
        self.assertTrue(any("does not confirm GOEXPERIMENT=simd" in error for error in errors))

    def test_cbr_requires_mode_packet_count_and_exact_ranges(self):
        good = "strict paired CBR summary: variant=simd cases=19 exact_cases=19 packets=2175 packet_diffs=0 range_diffs=0"
        self.assertEqual(validate_cbr_summary(good, "simd"), [])
        scalar = good.replace("variant=simd", "variant=scalar")
        self.assertEqual(validate_cbr_summary(scalar, "scalar"), [])
        bad_variant = good.replace("variant=simd", "variant=fixed-simd")
        self.assertTrue(validate_cbr_summary(bad_variant, "simd"))
        bad_count = good.replace("packets=2175", "packets=0")
        self.assertTrue(validate_cbr_summary(bad_count, "simd"))

    def test_workload_manifest_requires_exact_names_target_and_hashes(self):
        lines = ["encoderbenchcmp-workload-manifest\tgoos=linux\tgoarch=amd64\tgoamd64=v2\ttarget=v2"]
        for name in ENCODE_CASES:
            lines.append(f"encoderbenchcmp-workload\tname={name}\tsha256_float32le={'a' * 64}")
        hashes, errors = _input_hashes("\n".join(lines), "v2")
        self.assertEqual(set(hashes), set(ENCODE_CASES))
        self.assertEqual(errors, [])
        _, errors = _input_hashes("\n".join(lines).replace("goamd64=v2", "goamd64=v1"), "v2")
        self.assertTrue(any("does not identify" in error for error in errors))

    def test_decoded_selector_count_is_bounded_and_reviewed(self):
        self.assertEqual(len(exact_suite_specs("nosimd")), 4)
        self.assertEqual(len(exact_suite_specs("simd")), 6)
        self.assertEqual(exact_suite_specs("simd")[3]["package"], "./testvectors")
        self.assertEqual(exact_suite_specs("simd")[-2]["expected_leaves"], {"TestPitchXCorrPairedLibopusSIMDRawBits": 1})
        self.assertEqual(exact_suite_specs("simd")[-1]["expected_leaves"], {"TestPVQSearchSIMDDispatchUsesAVX": 1})

    def test_failed_manifest_never_publishes_timing_tables(self):
        with self.subTest("failed build"):
            import tempfile

            with tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                result = write_summary(root, {"status": "failed", "errors": ["compiler target failed"]})
                self.assertFalse(result["valid"])
                self.assertEqual(result["go_benchmarks"], [])
                self.assertEqual(result["matched_c_benchmarks"], [])
                self.assertIn("No timing matrices are published", (root / "summary.md").read_text())


if __name__ == "__main__":
    unittest.main()
