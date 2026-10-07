"""Contract tests for the Go test package-discovery wrapper."""

from __future__ import annotations

import json
import os
import pathlib
import shutil
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parent.parent
RUNNER = ROOT / "tools" / "run_go_test_runnable.sh"
FAKE_GO = r'''#!/usr/bin/env python3
import json
import os
import sys

args = sys.argv[1:]
with open(os.environ["FAKE_GO_LOG"], "a", encoding="utf-8") as log:
    log.write(json.dumps(args) + "\n")

if not args:
    sys.exit(91)

if args[0] == "list":
    if os.environ.get("FAKE_GO_LIST_MODE") == "fail":
        print("github.com/thesyncim/gopus")
        print("fake go list failed", file=sys.stderr)
        sys.exit(23)
    if os.environ.get("FAKE_GO_LIST_MODE") == "empty":
        sys.exit(0)
    patterns = [item for item in args[1:] if item == "./..." or item.startswith("./")]
    if "./..." in patterns:
        packages = [
            "github.com/thesyncim/gopus",
            "github.com/thesyncim/gopus/testvectors",
            "github.com/thesyncim/gopus/tmp_check",
        ]
    elif "./testvectors" in patterns:
        packages = ["github.com/thesyncim/gopus/testvectors"]
    else:
        packages = [item for item in patterns]
    print("\n".join(packages))
    sys.exit(0)

if args[0] == "test":
    if "-list=." in args:
        packages = [item for item in args[1:] if item.startswith("github.com/thesyncim/gopus")]
        for package in packages:
            print(json.dumps({"Action": "output", "Package": package, "Output": "TestAlpha\n"}))
        sys.exit(0)
    test_exit = int(os.environ.get("FAKE_GO_TEST_EXIT", "0"))
    if test_exit:
        print("fake go test failed", file=sys.stderr)
    sys.exit(test_exit)

sys.exit(92)
'''


def bash_commands() -> list[str]:
    path_bash = shutil.which("bash")
    if not path_bash:
        raise AssertionError("bash was not found on PATH")
    return list(dict.fromkeys(["/bin/bash", path_bash]))


class RunGoTestRunnableTest(unittest.TestCase):
    def run_wrapper(
        self, shell: str, args: list[str], env_updates: dict[str, str] | None = None,
    ) -> tuple[subprocess.CompletedProcess[str], list[list[str]]]:
        with tempfile.TemporaryDirectory() as temporary:
            temp = pathlib.Path(temporary)
            fake_go = temp / "fake-go"
            fake_go.write_text(FAKE_GO, encoding="utf-8")
            fake_go.chmod(0o755)
            log_path = temp / "calls.jsonl"
            env = os.environ.copy()
            env.update({"GO": str(fake_go), "GO_WORK_ENV": "GOWORK=off", "FAKE_GO_LOG": str(log_path)})
            if env_updates:
                env.update(env_updates)
            if env.get("GOPUS_TEST_SHARD"):
                env["GOPUS_TEST_SHARD_REPORT"] = str(temp / "shard-inventory.json")
            result = subprocess.run(
                [shell, str(RUNNER), *args], cwd=ROOT, env=env,
                text=True, capture_output=True, check=False,
            )
            calls = [json.loads(line) for line in log_path.read_text().splitlines()] if log_path.exists() else []
            return result, calls

    def test_default_discovery_filters_tmp_check_and_runs_resolved_packages(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, [])
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[0], ["list", "./..."])
                self.assertEqual(calls[1], [
                    "test",
                    "github.com/thesyncim/gopus",
                    "github.com/thesyncim/gopus/testvectors",
                ])

    def test_build_selection_options_reach_go_list_with_explicit_package_patterns(self):
        args = [
            "-race", "-tags", "gopus_dred", "-overlay=/tmp/overlay.json",
            "-modfile", "/tmp/go.mod", "-compiler=gccgo", "--", "./testvectors",
        ]
        expected_list = [
            "list", "-race", "-tags", "gopus_dred", "-overlay=/tmp/overlay.json",
            "-modfile", "/tmp/go.mod", "-compiler=gccgo", "./testvectors",
        ]
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[0], expected_list)
                self.assertEqual(calls[1][-1], "github.com/thesyncim/gopus/testvectors")

    def test_common_flag_values_are_not_mistaken_for_package_selection_flags(self):
        args = ["-coverprofile", "-tags=gopus_dred", "-run", "-race", "--", "./testvectors"]
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[0], ["list", "./testvectors"])
                self.assertEqual(calls[1], [
                    "test", "-coverprofile", "-tags=gopus_dred", "-run", "-race",
                    "github.com/thesyncim/gopus/testvectors",
                ])

    def test_coverprofile_path_is_not_discovered_as_a_package(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, ["-coverprofile", "./coverage.out"])
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[0], ["list", "./..."])
                self.assertEqual(calls[1][1:3], ["-coverprofile", "./coverage.out"])
                self.assertTrue(calls[1][-1].endswith("/testvectors"))

    def test_binary_args_follow_resolved_packages(self):
        args = ["-short", "--", "./testvectors", "-args", "-test.v"]
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, args)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[1], [
                    "test", "-short", "github.com/thesyncim/gopus/testvectors", "-args", "-test.v",
                ])

    def test_go_list_failure_propagates_and_never_runs_go_test(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, [], {"FAKE_GO_LIST_MODE": "fail"})
                self.assertEqual(result.returncode, 23)
                self.assertIn("fake go list failed", result.stderr)
                self.assertEqual(len(calls), 1)

    def test_empty_discovery_fails_before_running_go_test(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, [], {"FAKE_GO_LIST_MODE": "empty"})
                self.assertEqual(result.returncode, 1)
                self.assertIn("no runnable Go packages", result.stderr)
                self.assertEqual(len(calls), 1)

    def test_go_test_failure_status_propagates(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(shell, [], {"FAKE_GO_TEST_EXIT": "17"})
                self.assertEqual(result.returncode, 17)
                self.assertEqual(len(calls), 2)

    def test_sharded_discovery_uses_build_tags_filters_tmp_check_and_stops_on_failure(self):
        for shell in bash_commands():
            with self.subTest(shell=shell):
                result, calls = self.run_wrapper(
                    shell, ["-tags", "gopus_dred"], {"GOPUS_TEST_SHARD": "0/1"},
                )
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(calls[0], ["list", "-tags", "gopus_dred", "./..."])
                self.assertFalse(any("tmp_check" in item for call in calls[1:] for item in call))

                failed, failed_calls = self.run_wrapper(
                    shell, ["-tags", "gopus_dred"],
                    {"GOPUS_TEST_SHARD": "0/1", "FAKE_GO_LIST_MODE": "fail"},
                )
                self.assertEqual(failed.returncode, 2)
                self.assertIn("exit=23", failed.stderr)
                self.assertEqual(len(failed_calls), 1)


if __name__ == "__main__":
    unittest.main()
