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
    def test_full_matrix_runs_with_empty_tags_under_system_bash(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            bin_dir = root / "bin"
            tmp_dir = root / "tmp"
            bin_dir.mkdir()
            tmp_dir.mkdir()
            log_path = root / "calls.jsonl"

            interpreter = "/bin/bash"
            self.assertTrue(Path(interpreter).is_file(), "system bash must be installed")
            python = shutil.which("python3")
            self.assertIsNotNone(python, "python3 must be installed")

            for name in ("deadcode", "staticcheck"):
                tool = bin_dir / name
                tool.write_text(
                    "#!" + python + "\n"
                    "import json, os, sys\n"
                    "with open(os.environ['DEADCODE_MATRIX_TEST_LOG'], 'a') as log:\n"
                    "    log.write(json.dumps({'tool': os.path.basename(sys.argv[0]), "
                    "'args': sys.argv[1:], 'arch': os.environ.get('GOARCH')}) + '\\n')\n"
                    + ("print('[]')\n" if name == "deadcode" else "")
                )
                tool.chmod(0o755)

            # The script only checks that Go exists; the analyzer stubs handle
            # every actual invocation and keep this regression independent of
            # installed Go analyzer versions.
            go = bin_dir / "go"
            go.write_text("#!/bin/sh\nexit 0\n")
            go.chmod(0o755)

            env = os.environ.copy()
            env.update({
                "PATH": str(bin_dir) + os.pathsep + env.get("PATH", ""),
                "TMPDIR": str(tmp_dir),
                "DEADCODE_MATRIX_TEST_LOG": str(log_path),
            })
            result = subprocess.run(
                [interpreter, str(SCRIPT)],
                cwd=ROOT,
                env=env,
                text=True,
                capture_output=True,
                check=False,
            )

            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("deadcode-matrix: 41 configurations", result.stderr)
            self.assertIn("# valid configs (41):", result.stdout)
            progress = [line for line in result.stderr.splitlines() if line.strip().startswith("[")]
            self.assertEqual(len(progress), 41, result.stderr)
            self.assertIn("[1/41] default (GOARCH=arm64 tags='')", result.stderr)
            self.assertIn("[41/41] neon_tone (GOARCH=arm64 tags='gopus_neon_tone_lpc_corr')", result.stderr)

            calls = [json.loads(line) for line in log_path.read_text().splitlines()]
            deadcode_calls = [call for call in calls if call["tool"] == "deadcode"]
            staticcheck_calls = [call for call in calls if call["tool"] == "staticcheck"]
            self.assertEqual(len(deadcode_calls), 41)
            self.assertEqual(len(staticcheck_calls), 41)
            self.assertEqual(deadcode_calls[0]["arch"], "arm64")
            self.assertEqual(deadcode_calls[0]["args"], ["-test", "-json", "-tags", "", "./..."])
            self.assertEqual(staticcheck_calls[0]["arch"], "arm64")
            self.assertEqual(staticcheck_calls[0]["args"], ["-f", "json", "-tags", "", "./..."])
            self.assertTrue(any("nosimd" in call["args"] for call in deadcode_calls))


if __name__ == "__main__":
    unittest.main()
