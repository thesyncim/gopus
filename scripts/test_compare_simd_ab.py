"""Focused regression checks for the native same-ISA parity comparator."""

import json
import pathlib
import tempfile
import unittest

from compare_simd_ab import (
    E2E_BENCHMARKS,
    REPLACEMENT_TESTS,
    audited_test_replacements,
    audited_test_retirements,
    baseline_cbr_errors,
    candidate_decode_errors,
    cbr_rows,
    compare_full_parity,
    e2e_benchmark_errors,
    missing_baseline_tests,
    precision_gap,
    strict_cbr_errors,
    strict_cbr_summary,
)


def event(action, test=None, output=None, package="github.com/thesyncim/gopus"):
    value = {"Action": action, "Package": package}
    if test is not None:
        value["Test"] = test
    if output is not None:
        value["Output"] = output
    return json.dumps(value)


class FullParityComparisonTest(unittest.TestCase):
    def test_scalar_cbr_residual_is_counted(self):
        log = "    encoder_cbr_byte_parity_test.go:637: CELT-FB-5ms-mono-64k 200 5 ~ (pure-Go CELT float residual)\npass=0 residual=1 fail=0 skip=0\n"
        self.assertEqual(cbr_rows(log), {"CELT-FB-5ms-mono-64k": (200, 5, "RESIDUAL")})

    def test_parses_live_legacy_19_case_summary(self):
        log = """\
    encoder_cbr_byte_parity_test.go:1023: SILK-NB-10ms-mono-16k                   100       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-NB-20ms-mono-16k                    50       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-MB-20ms-mono-24k                    50       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-WB-10ms-mono-32k                   100       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-WB-20ms-mono-32k                    50       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-WB-40ms-mono-32k                    25       0     OK
    encoder_cbr_byte_parity_test.go:1023: SILK-WB-20ms-stereo-48k                  50       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-2p5ms-mono-64k                  400       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-2p5ms-stereo-128k               400       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-5ms-mono-64k                    200       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-5ms-stereo-128k                 200       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-10ms-mono-64k                   100       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-20ms-mono-64k                    50       0     OK
    encoder_cbr_byte_parity_test.go:1023: CELT-FB-20ms-stereo-128k                 50       0     OK
    encoder_cbr_byte_parity_test.go:1023: Hybrid-SWB-10ms-mono-48k                100       0     OK
    encoder_cbr_byte_parity_test.go:1023: Hybrid-SWB-20ms-mono-48k                 50       0     OK
    encoder_cbr_byte_parity_test.go:1023: Hybrid-FB-10ms-mono-64k                 100       0     OK
    encoder_cbr_byte_parity_test.go:1023: Hybrid-FB-20ms-mono-64k                  50       0     OK
    encoder_cbr_byte_parity_test.go:1023: Hybrid-FB-20ms-stereo-96k                50       0     OK
    encoder_cbr_byte_parity_test.go:1031: pass=19 fail=0 skip=0  arch=linux/amd64
"""
        rows = cbr_rows(log)
        self.assertEqual(len(rows), 19)
        self.assertEqual(baseline_cbr_errors(0, log), [])

    def test_rejects_cbr_summary_category_mismatch(self):
        log = "    encoder_cbr_byte_parity_test.go:1: case1 50 0 OK\npass=0 fail=1 skip=0 arch=linux/amd64\n"
        with self.assertRaisesRegex(ValueError, "row categories"):
            cbr_rows(log)

    def test_legacy_cbr_summary_cannot_hide_residual_rows(self):
        log = "    encoder_cbr_byte_parity_test.go:1: case1 50 2 ~ (pure-Go CELT float residual)\npass=1 fail=0 skip=0 arch=linux/amd64\n"
        with self.assertRaisesRegex(ValueError, "row categories"):
            cbr_rows(log)

    def test_precision_gap_uses_both_mode_matched_q_values(self):
        log = "RealContent gopus Q=31.56\nRealContent libopus Q=31.56\n"
        self.assertEqual(precision_gap(log), 0)
        with self.assertRaises(ValueError):
            precision_gap("RealContent gopus Q=31.56\n")

    def compare(self, base_status="fail", candidate_status="fail", candidate_skip=False,
                candidate_sample_count=2):
        with tempfile.TemporaryDirectory() as name:
            root = pathlib.Path(name)
            base = [event("pass", "TestHealthy"),
                    event("output", "TestResidual", "2/100 samples differ, maxAbs=0.001\n"),
                    event(base_status, "TestResidual"), event("fail")]
            candidate = [event("skip" if candidate_skip else "pass", "TestHealthy"),
                         event("output", "TestResidual", f"{candidate_sample_count}/100 samples differ, maxAbs=0.001\n"),
                         event(candidate_status, "TestResidual")]
            for package, test in REPLACEMENT_TESTS:
                candidate.append(event("pass", test, package=package))
            candidate.append(event("fail"))
            for side, phase, lines in [
                ("baseline", "simd-full-parity", base),
                ("candidate", "simd-full-parity", candidate),
            ]:
                stem = root / f"{side}-{phase}"
                stem.with_suffix(".log").write_text("\n".join(lines) + "\n")
                stem.with_suffix(".exit").write_text("1\n")
            return compare_full_parity(root)[0]

    def test_existing_residual_is_accepted(self):
        self.assertEqual(self.compare(), [])

    def test_new_failure_is_rejected(self):
        self.assertTrue(any("adds" in error for error in self.compare(base_status="pass")))

    def test_new_skip_is_rejected(self):
        self.assertTrue(any("skips" in error for error in self.compare(candidate_skip=True)))

    def test_worse_decode_metric_is_rejected(self):
        self.assertTrue(any("sample differences increase" in error
                            for error in self.compare(candidate_sample_count=3)))

    def test_default_scalar_artifact_cannot_stand_in_for_base_simd(self):
        with tempfile.TemporaryDirectory() as name:
            root = pathlib.Path(name)
            for side, phase in [
                ("baseline", "default-full-parity"),
                ("candidate", "simd-full-parity"),
            ]:
                stem = root / f"{side}-{phase}"
                stem.with_suffix(".log").write_text(event("pass", "TestModeIdentity") + "\n")
                stem.with_suffix(".exit").write_text("0\n")
            with self.assertRaises(FileNotFoundError):
                compare_full_parity(root)


class StrictCBRSummaryTest(unittest.TestCase):
    def test_parses_exact_packet_and_range_counts(self):
        log = (
            "strict paired CBR summary: variant=simd cases=19 exact_cases=19 "
            "packets=2175 packet_diffs=0 range_diffs=0\n"
        )
        self.assertEqual(strict_cbr_summary(log), {
            "variant": "simd",
            "cases": 19,
            "exact_cases": 19,
            "packets": 2175,
            "packet_diffs": 0,
            "range_diffs": 0,
        })

    def test_rejects_missing_or_duplicate_summary(self):
        with self.assertRaises(ValueError):
            strict_cbr_summary("PASS\n")
        summary = "strict paired CBR summary: variant=scalar cases=19 exact_cases=19 packets=1 packet_diffs=0 range_diffs=0\n"
        with self.assertRaises(ValueError):
            strict_cbr_summary(summary + summary)

    def test_packet_or_range_diffs_and_nonzero_exit_are_hard_errors(self):
        packet_log = (
            "strict paired CBR summary: variant=simd cases=19 exact_cases=8 "
            "packets=2175 packet_diffs=104 range_diffs=37\n"
            "testvectors/encoder_cbr_byte_parity_test.go:1: exact paired CBR mismatch: packets=7/400 ranges=5/400 first_packet=frame:191 byte:8\n"
        )
        errors = strict_cbr_errors(1, packet_log)
        self.assertEqual(len(errors), 2)
        self.assertIn("104", errors[1])
        self.assertIn("frame:191 byte:8", errors[1])

        range_log = "strict paired CBR summary: variant=scalar cases=19 exact_cases=19 packets=2175 packet_diffs=0 range_diffs=1\n"
        self.assertTrue(any("ranges=1" in error for error in strict_cbr_errors(0, range_log)))

    def test_nonzero_exit_fails_even_with_zero_summary_diffs(self):
        log = "strict paired CBR summary: variant=scalar cases=19 exact_cases=19 packets=2175 packet_diffs=0 range_diffs=0\n"
        self.assertTrue(any("exit=1" in error for error in strict_cbr_errors(1, log)))


class E2EBenchmarkGateTest(unittest.TestCase):
    def rows(self, allocs="0 allocs/op"):
        return "\n".join(
            f"{name}-1 100 10 ns/op 0 B/op {allocs}"
            for name in sorted(E2E_BENCHMARKS)
        )

    def test_requires_all_six_zero_allocation_rows(self):
        self.assertEqual(e2e_benchmark_errors(0, self.rows()), [])

    def test_rejects_missing_or_allocating_rows(self):
        lines = self.rows().splitlines()
        self.assertTrue(e2e_benchmark_errors(0, "\n".join(lines[:-1])))
        allocating = self.rows().replace("0 allocs/op", "1 allocs/op", 1)
        self.assertTrue(e2e_benchmark_errors(0, allocating))
        self.assertTrue(e2e_benchmark_errors(1, self.rows()))


class IndependentOracleGateTest(unittest.TestCase):
    def test_audited_replacements_require_every_recorded_leaf(self):
        for old, targets in audited_test_replacements().items():
            base = {old: "pass"}
            candidate = dict.fromkeys(targets, "pass")
            self.assertEqual(missing_baseline_tests(base, candidate), set())
            for target in targets:
                for status in ("skip", "fail", None):
                    changed = dict(candidate)
                    if status is None:
                        del changed[target]
                    else:
                        changed[target] = status
                    self.assertEqual(missing_baseline_tests(base, changed), {old})

    def test_retired_internal_contracts_are_exact_names_only(self):
        retired = audited_test_retirements()
        self.assertEqual(len(retired), 15)
        self.assertFalse(retired & audited_test_replacements().keys())
        for key in retired:
            self.assertEqual(missing_baseline_tests({key: "pass"}, {}), set())
            child = (key[0], key[1] + "/unreviewed_case")
            self.assertEqual(missing_baseline_tests({child: "pass"}, {}), {child})
            other_package = ("github.com/thesyncim/gopus", key[1])
            self.assertEqual(missing_baseline_tests({other_package: "pass"}, {}), {other_package})

    def test_unreviewed_missing_test_is_rejected(self):
        old = ("github.com/thesyncim/gopus", "TestUnreviewedRemovedCase")
        self.assertEqual(missing_baseline_tests({old: "pass"}, {}), {old})

    def test_complete_failing_baseline_is_evidence(self):
        log = "    baseline.go:1: case1 50 2 FAIL\npass=0 residual=0 fail=1 skip=0\n"
        self.assertEqual(baseline_cbr_errors(1, log, expected_cases=1), [])
        self.assertTrue(baseline_cbr_errors(2, log, expected_cases=1))
        self.assertTrue(baseline_cbr_errors(1, log, expected_cases=2))

    def test_baseline_skip_is_not_complete_evidence(self):
        log = "    baseline.go:1: case1 0 0 SKIP\npass=0 residual=0 fail=0 skip=1\n"
        self.assertTrue(baseline_cbr_errors(0, log, expected_cases=1))

    def test_candidate_must_pass_without_decode_residuals(self):
        passed = "--- PASS: TestDecodeDifferentialEncodeThenDecode/case (0.01s)\n"
        self.assertEqual(candidate_decode_errors(0, passed), [])
        self.assertTrue(candidate_decode_errors(1, passed))
        self.assertTrue(candidate_decode_errors(0, passed + "PCM diverges\n"))
        self.assertTrue(candidate_decode_errors(0, passed + "--- SKIP: another_case\n"))
        self.assertTrue(candidate_decode_errors(0, "testing: warning: no tests to run\nPASS\n"))


if __name__ == "__main__":
    unittest.main()
