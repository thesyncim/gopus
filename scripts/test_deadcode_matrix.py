"""Regression tests for portable empty-tag handling in deadcode_matrix.sh."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parent.parent
SCRIPT = ROOT / "scripts" / "deadcode_matrix.sh"


class DeadcodeMatrixTests(unittest.TestCase):
    def create_stubs(self, root, deadcode_mode="empty", staticcheck_mode="empty"):
        bin_dir = root / "bin"
        tmp_dir = root / "tmp"
        bin_dir.mkdir()
        tmp_dir.mkdir()
        log_path = root / "calls.jsonl"
        python = shutil.which("python3")
        self.assertIsNotNone(python, "python3 must be installed")

        deadcode = bin_dir / "deadcode"
        deadcode.write_text(
            "#!" + python + "\n"
            "import json, os, sys\n"
            "args = sys.argv[1:]\n"
            "tags = args[args.index('-tags') + 1]\n"
            "first = os.environ.get('GOARCH') == 'arm64' and tags == '' and os.environ.get('GOEXPERIMENT') == ''\n"
            "with open(os.environ['DEADCODE_MATRIX_TEST_LOG'], 'a') as log:\n"
            "    log.write(json.dumps({'tool': 'deadcode', 'args': args, "
            "'arch': os.environ.get('GOARCH'), 'goamd64': os.environ.get('GOAMD64'), "
            "'experiment': os.environ.get('GOEXPERIMENT')}) + '\\n')\n"
            "mode = os.environ.get('DEADCODE_MATRIX_TEST_DC_MODE', 'empty')\n"
            "if first and mode == 'fail-first':\n"
            "    print('mock deadcode failure', file=sys.stderr); sys.exit(2)\n"
            "if first and mode == 'malformed-first':\n"
            "    print('{broken'); sys.exit(0)\n"
            "if first and mode == 'malformed-schema':\n"
            "    print(json.dumps({'packages': []})); sys.exit(0)\n"
            "if mode == 'candidate':\n"
            "    path = os.environ['DEADCODE_MATRIX_TEST_FILE']\n"
            "    print(json.dumps([{'Funcs': [{'Generated': False, 'Name': 'DeadcodeMatrix.deadFixture', "
            "'Position': {'File': path}}]}]))\n"
            "else:\n"
            "    print('null')\n"
        )
        deadcode.chmod(0o755)

        staticcheck = bin_dir / "staticcheck"
        staticcheck.write_text(
            "#!" + python + "\n"
            "import json, os, sys\n"
            "args = sys.argv[1:]\n"
            "tags = args[args.index('-tags') + 1]\n"
            "first = os.environ.get('GOARCH') == 'arm64' and tags == '' and os.environ.get('GOEXPERIMENT') == ''\n"
            "with open(os.environ['DEADCODE_MATRIX_TEST_LOG'], 'a') as log:\n"
            "    log.write(json.dumps({'tool': 'staticcheck', 'args': args, "
            "'arch': os.environ.get('GOARCH'), 'goamd64': os.environ.get('GOAMD64'), "
            "'experiment': os.environ.get('GOEXPERIMENT')}) + '\\n')\n"
            "mode = os.environ.get('DEADCODE_MATRIX_TEST_SC_MODE', 'empty')\n"
            "if first and mode == 'fail-first':\n"
            "    print('mock staticcheck failure', file=sys.stderr); sys.exit(2)\n"
            "if first and mode == 'malformed-first':\n"
            "    print('{broken'); sys.exit(0)\n"
            "if first and mode == 'exit-one-empty':\n"
            "    sys.exit(1)\n"
            "if mode == 'candidate':\n"
            "    finding = {'code': 'U1000', 'message': 'func (*DeadcodeMatrix).deadFixture is unused', "
            "'location': {'file': os.environ['DEADCODE_MATRIX_TEST_FILE']}}\n"
            "    print(json.dumps(finding)); sys.exit(1)\n"
        )
        staticcheck.chmod(0o755)

        # The script checks that Go exists; analyzer stubs handle every actual
        # invocation and keep the regressions independent of tool versions.
        go = bin_dir / "go"
        go.write_text("#!/bin/sh\nexit 0\n")
        go.chmod(0o755)
        return bin_dir, tmp_dir, log_path

    def run_matrix(self, root, *args, deadcode_mode="empty", staticcheck_mode="empty"):
        bin_dir, tmp_dir, log_path = self.create_stubs(root, deadcode_mode, staticcheck_mode)
        env = os.environ.copy()
        env.update({
            "PATH": str(bin_dir) + os.pathsep + env.get("PATH", ""),
            "TMPDIR": str(tmp_dir),
            "DEADCODE_MATRIX_TEST_LOG": str(log_path),
            "DEADCODE_MATRIX_TEST_DC_MODE": deadcode_mode,
            "DEADCODE_MATRIX_TEST_SC_MODE": staticcheck_mode,
            "DEADCODE_MATRIX_TEST_FILE": str(ROOT / "internal" / "deadcode_matrix_missing.go"),
            # Prove scalar configurations clear inherited experiment/target choices.
            "GOEXPERIMENT": "simd",
            "GOAMD64": "v3",
        })
        result = subprocess.run(
            ["/bin/bash", str(SCRIPT), *args],
            cwd=ROOT,
            env=env,
            text=True,
            capture_output=True,
            check=False,
        )
        calls = [json.loads(line) for line in log_path.read_text().splitlines()]
        return result, calls

    def test_full_matrix_covers_scalar_simd_and_amd64_v3_lanes(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, calls = self.run_matrix(Path(temporary))

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("deadcode-matrix: 116 configurations", result.stderr)
            self.assertIn("# valid configs (116):", result.stdout)
            progress = [line for line in result.stderr.splitlines() if line.strip().startswith("[")]
            self.assertEqual(len(progress), 116, result.stderr)
            self.assertIn("[1/116] default (GOARCH=arm64 GOAMD64=default lane=scalar tags='')", result.stderr)
            self.assertIn("[116/116] amd64v3_simd_libopus_bench (GOARCH=amd64 GOAMD64=v3 lane=simd tags='gopus_libopus_bench')", result.stderr)

            deadcode_calls = [call for call in calls if call["tool"] == "deadcode"]
            staticcheck_calls = [call for call in calls if call["tool"] == "staticcheck"]
            self.assertEqual(len(deadcode_calls), 116)
            self.assertEqual(len(staticcheck_calls), 116)
            self.assertEqual(deadcode_calls[0]["arch"], "arm64")
            self.assertIsNone(deadcode_calls[0]["goamd64"])
            self.assertEqual(deadcode_calls[0]["experiment"], "")
            self.assertEqual(deadcode_calls[0]["args"], ["-test", "-json", "-tags", "", "./..."])
            self.assertEqual(staticcheck_calls[0]["args"], ["-f", "json", "-checks", "U1000", "-tags", "", "./..."])
            self.assertTrue(any(call["experiment"] == "simd" for call in deadcode_calls))
            self.assertTrue(any(call["arch"] == "amd64" and call["goamd64"] == "v1" for call in deadcode_calls))
            self.assertTrue(any(call["arch"] == "amd64" and call["goamd64"] == "v3" for call in deadcode_calls))
            for call in deadcode_calls:
                args = call["args"]
                tags = args[args.index("-tags") + 1].split(",")
                if "nosimd" in tags:
                    self.assertEqual(call["experiment"], "")

    def assert_matrix_fails_closed(self, result, expected_detail):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertIn("DEADCODE MATRIX INCOMPLETE", result.stderr)
        self.assertIn(expected_detail, result.stderr)
        self.assertNotIn("genuinely-dead candidates", result.stdout)

    def test_deadcode_failure_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", deadcode_mode="fail-first")
            self.assert_matrix_fails_closed(result, "deadcode exited 2")

    def test_malformed_deadcode_json_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", deadcode_mode="malformed-first")
            self.assert_matrix_fails_closed(result, "deadcode emitted invalid JSON")

    def test_malformed_deadcode_json_schema_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", deadcode_mode="malformed-schema")
            self.assert_matrix_fails_closed(result, "deadcode emitted invalid JSON")
            self.assertIn("expected a package list", result.stderr)

    def test_staticcheck_failure_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", staticcheck_mode="fail-first")
            self.assert_matrix_fails_closed(result, "staticcheck exited 2")

    def test_malformed_staticcheck_json_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", staticcheck_mode="malformed-first")
            self.assert_matrix_fails_closed(result, "staticcheck emitted invalid JSON")

    def test_staticcheck_exit_one_without_u1000_findings_invalidates_matrix(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(Path(temporary), "--quick", staticcheck_mode="exit-one-empty")
            self.assert_matrix_fails_closed(result, "staticcheck exited 1 without U1000 findings")

    def test_staticcheck_finding_exit_one_and_grep_symbol_parse(self):
        with tempfile.TemporaryDirectory() as temporary:
            result, _ = self.run_matrix(
                Path(temporary), "--quick", "--grepcheck",
                deadcode_mode="candidate", staticcheck_mode="candidate",
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("# genuinely-dead candidates (flagged in EVERY valid config): 1", result.stdout)
            self.assertIn("internal/deadcode_matrix_missing.go:DeadcodeMatrix.deadFixture", result.stdout)
            self.assertRegex(result.stdout, r"internal/deadcode_matrix_missing\.go:DeadcodeMatrix\.deadFixture\s+refs=0")


if __name__ == "__main__":
    unittest.main()
