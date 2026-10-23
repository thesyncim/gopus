"""Keep allocation gates independent of CPU profiler overhead."""

import json
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory
from unittest.mock import patch

from benchmark_goamd64 import Runner, RunFailure


class ShortFrameProfileTests(unittest.TestCase):
    def run_profiles(self, *, setup_bytes=0, profile_bytes=33, omit_profile_row=False):
        with TemporaryDirectory() as temporary:
            root = Path(temporary)
            runner = Runner(root, root, root / "artifact")
            runner.manifest["metadata"]["go_toolchain"] = "go1.27.1"
            phases = []

            def checked_run(name, cwd, command, env, output):
                phases.append(name)
                path = runner.artifact / output
                path.parent.mkdir(parents=True, exist_ok=True)
                text = "synthetic diagnostic output\n"
                if name.startswith("build-"):
                    Path(command[command.index("-o") + 1]).write_bytes(b"synthetic test binary")
                elif name.startswith("version-"):
                    text = ("profile.test: go1.27.1-X:simd\n"
                            "\tbuild\tGOAMD64=v3\n\tbuild\tGOEXPERIMENT=simd\n")
                elif name.startswith(("setup-", "profile-")):
                    benchmark = command[command.index("-test.bench") + 1].replace("^", "").replace("$", "")
                    profiled = name.startswith("profile-")
                    allocated_bytes = profile_bytes if profiled else setup_bytes
                    text = f"{benchmark} 100 1000 ns/op {allocated_bytes} B/op 0 allocs/op\n"
                    if profiled:
                        profile = next(arg.split("=", 1)[1] for arg in command if arg.startswith("-test.cpuprofile="))
                        Path(profile).write_bytes(b"synthetic CPU profile")
                        if omit_profile_row:
                            text = "PASS\n"
                path.write_text(text)
                return path

            with patch.object(runner, "checked_run", side_effect=checked_run), \
                    patch.object(runner, "git_commit", return_value="candidate-commit"):
                runner.run_short_frame_profiles()
            provenance = json.loads((runner.artifact / "v3/profiles/provenance.json").read_text())
            return phases, provenance

    def test_profiled_bytes_are_diagnostic_after_zero_allocation_setup(self):
        phases, provenance = self.run_profiles(profile_bytes=33)
        self.assertEqual(sum(name.startswith("setup-") for name in phases), 2)
        self.assertEqual(sum(name.startswith("profile-") for name in phases), 2)
        self.assertIn("profiler overhead", provenance["allocation_note"])

    def test_unprofiled_allocation_gate_remains_strict(self):
        with self.assertRaisesRegex(RunFailure, "unprofiled setup allocates"):
            self.run_profiles(setup_bytes=8)

    def test_profile_must_execute_the_named_benchmark(self):
        with self.assertRaisesRegex(RunFailure, "profile requires one benchmark row"):
            self.run_profiles(omit_profile_row=True)


if __name__ == "__main__":
    unittest.main()
