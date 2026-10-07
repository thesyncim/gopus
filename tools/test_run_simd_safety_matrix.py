"""Mocked environment tests for the native kernel safety matrix."""

from __future__ import annotations

import os
import pathlib
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
MATRIX = ROOT / "tools" / "run_simd_safety_matrix.sh"


class SimdSafetyMatrixEnvironmentTests(unittest.TestCase):
    def run_matrix(self, goarch: str, inherited_target: str | None = None) -> tuple[subprocess.CompletedProcess[str], list[list[str]]]:
        with tempfile.TemporaryDirectory() as temporary:
            temp = pathlib.Path(temporary)
            mock_bin = temp / "bin"
            mock_bin.mkdir()
            command_log = temp / "commands.tsv"

            go = mock_bin / "go"
            go.write_text(
                "#!/bin/sh\n"
                'if [ "$1" = env ] && [ "$2" = GOARCH ]; then\n'
                '  printf "%s\\n" "$MOCK_GOARCH"\n'
                "  exit 0\n"
                "fi\n"
                'echo "unexpected Go invocation: $*" >&2\n'
                "exit 97\n"
            )
            env = mock_bin / "env"
            env.write_text(
                "#!/bin/sh\n"
                'for arg do printf "%s\\t" "$arg" >> "$MATRIX_COMMAND_LOG"; done\n'
                'printf "\\n" >> "$MATRIX_COMMAND_LOG"\n'
            )
            go.chmod(0o755)
            env.chmod(0o755)

            process_env = os.environ.copy()
            process_env["PATH"] = f"{mock_bin}{os.pathsep}{process_env.get('PATH', '')}"
            process_env["MOCK_GOARCH"] = goarch
            process_env["MATRIX_COMMAND_LOG"] = str(command_log)
            process_env["GOPUS_ASM_FUZZTIME"] = "1x"
            process_env.pop("GOAMD64", None)
            if inherited_target is None:
                process_env.pop("GOPUS_LIBOPUS_AMD64_TARGET", None)
            else:
                process_env["GOPUS_LIBOPUS_AMD64_TARGET"] = inherited_target

            result = subprocess.run(
                ["bash", str(MATRIX)],
                cwd=ROOT,
                env=process_env,
                check=False,
                capture_output=True,
                text=True,
            )
            calls = [
                line.split("\t")[:-1]
                for line in command_log.read_text().splitlines()
                if line
            ]
            return result, calls

    def test_amd64_lanes_pair_each_go_target_with_matching_libopus_target(self):
        result, calls = self.run_matrix("amd64", inherited_target="v2")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(calls), 20)

        tests_by_target = {"v1": 0, "v3": 0}
        for args in calls:
            target = next(
                (arg.split("=", 1)[1] for arg in args if arg.startswith("GOPUS_LIBOPUS_AMD64_TARGET=")),
                None,
            )
            go_target = next(
                (arg.split("=", 1)[1] for arg in args if arg.startswith("GOAMD64=")),
                None,
            )
            self.assertIn(target, tests_by_target, args)
            self.assertEqual(target, go_target, args)
            if "test" in args:
                tests_by_target[target] += 1
        self.assertEqual(tests_by_target, {"v1": 10, "v3": 10})

    def test_arm64_lanes_clear_an_inherited_amd64_target(self):
        result, calls = self.run_matrix("arm64", inherited_target="v3")
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertEqual(len(calls), 14)
        go_commands = []
        for args in calls:
            self.assertIn("go", args)
            go_commands.append(args[args.index("go") + 1])
            self.assertGreaterEqual(len(args), 2, args)
            self.assertEqual(args[:2], ["-u", "GOPUS_LIBOPUS_AMD64_TARGET"], args)
            self.assertFalse(
                any(arg.startswith("GOPUS_LIBOPUS_AMD64_TARGET=") for arg in args),
                args,
            )

        self.assertEqual(go_commands.count("test"), 10)
        self.assertEqual(go_commands.count("vet"), 4)


if __name__ == "__main__":
    unittest.main()
