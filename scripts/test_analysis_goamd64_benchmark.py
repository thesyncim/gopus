"""Reject incomplete or allocating incremental analyzer timing evidence."""

import unittest

from benchmark_analysis_parity import parse_timings
from benchmark_goamd64 import RunFailure


class AnalyzerTimingTests(unittest.TestCase):
    def test_valid_complete_zero_allocation_rows(self):
        self.assertEqual(parse_timings(
            "BenchmarkOne 100 123.5 ns/op 0 B/op 0 allocs/op\n"
            "BenchmarkTwo-1 200 456 ns/op 0 B/op 0 allocs/op\n",
            ("BenchmarkOne", "BenchmarkTwo")),
            {"BenchmarkOne": 123.5, "BenchmarkTwo": 456})

    def test_invalid_evidence_fails(self):
        valid = "BenchmarkOne-1 100 123.5 ns/op 0 B/op 0 allocs/op\n"
        for text in ("", valid + valid, valid.replace("-1 ", "-2 "),
                     valid.replace("100 ", "0 "), valid.replace("123.5", "nan"),
                     valid.replace("0 B/op", "8 B/op"), valid.replace("0 allocs/op", "1 allocs/op"),
                     valid.replace("BenchmarkOne", "BenchmarkOther"),
                     "BenchmarkOne-1 100 123.5 ns/op\n"):
            with self.subTest(text=text), self.assertRaises(RunFailure):
                parse_timings(text, ("BenchmarkOne",))


if __name__ == "__main__":
    unittest.main()
