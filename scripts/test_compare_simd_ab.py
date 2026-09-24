"""Focused regression checks for the native same-ISA parity comparator."""

import json
import pathlib
import tempfile
import unittest

from compare_simd_ab import REPLACEMENT_TESTS, compare_full_parity


def event(action, test=None, output=None, package="github.com/thesyncim/gopus"):
    value = {"Action": action, "Package": package}
    if test is not None:
        value["Test"] = test
    if output is not None:
        value["Output"] = output
    return json.dumps(value)


class FullParityComparisonTest(unittest.TestCase):
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


if __name__ == "__main__":
    unittest.main()
