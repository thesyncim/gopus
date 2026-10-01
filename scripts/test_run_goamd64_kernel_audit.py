"""Regression tests for the bounded native GOAMD64 audit runner."""

from __future__ import annotations

import contextlib
import io
import json
import os
import pathlib
import subprocess
import tempfile
import unittest
from unittest import mock

import run_goamd64_kernel_audit as audit
from validate_goamd64_targets import exact_suite_specs


REQUIRED_ENV = {
    "GOAMD64": "v3",
    "GOPUS_LIBOPUS_AMD64_TARGET": "v3",
    "GOPUS_TEST_TIER": "parity",
    "GOPUS_STRICT_LIBOPUS_REF": "1",
}


def successful_test_json(expected_leaves: dict[str, int]) -> str:
    events = []
    for root, count in expected_leaves.items():
        events.append({"Action": "run", "Test": root})
        for index in range(count):
            leaf = f"{root}/case{index}"
            events.extend((
                {"Action": "run", "Test": leaf},
                {"Action": "pass", "Test": leaf},
            ))
        events.append({"Action": "pass", "Test": root})
    return "".join(json.dumps(event) + "\n" for event in events)


class KernelAuditSelectorTests(unittest.TestCase):
    def test_audit_keeps_reviewed_bounded_selectors_and_counts(self):
        for mode in ("nosimd", "simd"):
            with self.subTest(mode=mode):
                expected = [
                    spec for spec in exact_suite_specs(mode)
                    if spec["name"] in audit.AUDIT_SUITES
                ]
                got = audit.audit_specs(mode)
                self.assertEqual(
                    [(spec["name"], spec["selector"], spec["expected_leaves"]) for spec in got[:-2]],
                    [(spec["name"], spec["selector"], spec["expected_leaves"]) for spec in expected],
                )
                leaf_counts = {
                    root: count
                    for spec in got
                    for root, count in spec["expected_leaves"].items()
                }
                self.assertEqual(leaf_counts["TestEncodeDifferentialFuzz"], 60)
                self.assertEqual(leaf_counts["TestDecodeDifferentialEncodeThenDecode"], 24)
                self.assertEqual(got[-2], {
                    "name": "analysis-state-200", "package": "./internal/encoder",
                    "selector": r"^TestAnalysisMatchesLibopusLive(EncoderVariants)?$",
                    "expected_leaves": {"TestAnalysisMatchesLibopusLive": 180,
                                        "TestAnalysisMatchesLibopusLiveEncoderVariants": 20},
                })

                self.assertEqual(got[-1], {
                    "name": "encoder-state-regressions", "package": "./internal/encoder",
                    "selector": r"^Test(StereoWidthComputation|SILKFinalRangeUsesLastPacketModeWithCELTSidecar)$",
                    "expected_leaves": {"TestStereoWidthComputation": 11,
                                        "TestSILKFinalRangeUsesLastPacketModeWithCELTSidecar": 1},
                })

    def test_wrong_mode_or_target_fails_before_running_go(self):
        cases = (
            ("nosimd", {**REQUIRED_ENV, "GOAMD64": "v2", "GOEXPERIMENT": "nosimd"}),
            ("nosimd", {**REQUIRED_ENV, "GOPUS_LIBOPUS_AMD64_TARGET": "v2", "GOEXPERIMENT": "nosimd"}),
            ("nosimd", {**REQUIRED_ENV, "GOEXPERIMENT": "simd"}),
            ("nosimd", {**REQUIRED_ENV, "GOPUS_TEST_TIER": "quality", "GOEXPERIMENT": "nosimd"}),
            ("simd", {**REQUIRED_ENV, "GOPUS_STRICT_LIBOPUS_REF": "0", "GOEXPERIMENT": "simd"}),
            ("simd", {**REQUIRED_ENV, "GOEXPERIMENT": "nosimd"}),
        )
        for mode, environment in cases:
            with self.subTest(mode=mode, environment=environment):
                with tempfile.TemporaryDirectory() as temporary:
                    with mock.patch.dict(os.environ, environment, clear=True):
                        with mock.patch.object(audit.subprocess, "run", side_effect=AssertionError("go must not run")):
                            with contextlib.redirect_stderr(io.StringIO()):
                                result = audit.main(["run_goamd64_kernel_audit.py", mode, str(pathlib.Path(temporary) / "audit")])
                self.assertEqual(result, 2)

    def test_arm_host_fails_before_running_go(self):
        with tempfile.TemporaryDirectory() as temporary:
            environment = {**REQUIRED_ENV, "GOEXPERIMENT": "simd"}
            with mock.patch.dict(os.environ, environment, clear=True):
                with mock.patch.object(audit, "go_command", return_value=["go"]):
                    with mock.patch.object(audit.platform, "machine", return_value="aarch64"):
                        with mock.patch.object(audit.subprocess, "run", side_effect=AssertionError("go must not run")):
                            with contextlib.redirect_stderr(io.StringIO()):
                                result = audit.main([
                                    "run_goamd64_kernel_audit.py", "simd",
                                    str(pathlib.Path(temporary) / "audit"),
                                ])
        self.assertEqual(result, 1)

    def test_go_arch_mismatch_fails_before_cache_lookup_or_tests(self):
        with tempfile.TemporaryDirectory() as temporary:
            environment = {**REQUIRED_ENV, "GOEXPERIMENT": "nosimd"}
            calls = []

            def fake_run(command, **kwargs):
                calls.append(command)
                return subprocess.CompletedProcess(command, 0, stdout="arm64\n", stderr="")

            with mock.patch.dict(os.environ, environment, clear=True):
                with mock.patch.object(audit, "go_command", return_value=["go"]):
                    with mock.patch.object(audit.platform, "machine", return_value="x86_64"):
                        with mock.patch.object(audit.subprocess, "run", side_effect=fake_run):
                            with contextlib.redirect_stderr(io.StringIO()):
                                result = audit.main([
                                    "run_goamd64_kernel_audit.py", "nosimd",
                                    str(pathlib.Path(temporary) / "audit"),
                                ])
        self.assertEqual(result, 1)
        self.assertEqual(calls, [["go", "env", "GOARCH"]])

    def test_valid_run_uses_exact_specs_and_validates_every_suite(self):
        for mode in ("nosimd", "simd"):
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                environment = {**REQUIRED_ENV, "GOEXPERIMENT": mode}
                calls = []

                def fake_run(command, **kwargs):
                    calls.append((command, kwargs.get("env", {}).copy()))
                    if command[-2:] == ["env", "GOARCH"]:
                        return subprocess.CompletedProcess(command, 0, stdout="amd64\n", stderr="")
                    if command[-2:] == ["env", "GOCACHE"]:
                        return subprocess.CompletedProcess(command, 0, stdout="/tmp/gocache\n", stderr="")
                    self.assertEqual(command[1], "test")
                    selector = command[command.index("-run") + 1]
                    spec = next(item for item in audit.audit_specs(mode) if item["selector"] == selector)
                    kwargs["stdout"].write(successful_test_json(spec["expected_leaves"]).encode())
                    return subprocess.CompletedProcess(command, 0, stdout="", stderr="")

                with mock.patch.dict(os.environ, environment, clear=True):
                    with mock.patch.object(audit, "go_command", return_value=["go"]):
                        with mock.patch.object(audit.platform, "machine", return_value="x86_64"):
                            with mock.patch.object(audit.subprocess, "run", side_effect=fake_run):
                                with contextlib.redirect_stdout(io.StringIO()):
                                    with contextlib.redirect_stderr(io.StringIO()):
                                        result = audit.main([
                                            "run_goamd64_kernel_audit.py", mode,
                                            str(pathlib.Path(temporary) / f"{mode}-audit"),
                                        ])

                self.assertEqual(result, 0)
                test_calls = [(command, env) for command, env in calls if "test" in command]
                specs = audit.audit_specs(mode)
                self.assertEqual(
                    [command[command.index("-run") + 1] for command, _env in test_calls],
                    [spec["selector"] for spec in specs],
                )
                for _command, env in test_calls:
                    self.assertEqual(env["GOAMD64"], "v3")
                    self.assertEqual(env["GOEXPERIMENT"], mode)
                    self.assertEqual(env["GOPUS_TEST_TIER"], "parity")
                    self.assertEqual(env["GOPUS_STRICT_LIBOPUS_REF"], "1")
                    self.assertEqual(env["GOFLAGS"], "")
                    self.assertNotIn("GOOS", env)
                    self.assertNotIn("GOARCH", env)
                    self.assertNotIn("GOPUS_LIBOPUS_REF_SCALAR", env)
                    self.assertEqual(env["CODEX_AGENT_ID"], "v3-matrix")


if __name__ == "__main__":
    unittest.main()
