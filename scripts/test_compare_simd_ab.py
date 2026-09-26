"""Focused regression checks for the native same-ISA parity comparator."""

import json
import pathlib
import tempfile
import unittest

from compare_simd_ab import (
    REPLACEMENT_TESTS,
    cbr_rows,
    compare_full_parity,
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
                ("baseline", "default-full-parity", base),
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


if __name__ == "__main__":
    unittest.main()
