"""Focused tests for the deterministic Go test sharder."""

import json
import pathlib
import shlex
import subprocess
import sys
import tempfile
import unittest

from aggregate_go_test_shards import merge_shards
from run_go_test_sharded import test_selection_args


FAKE_GO = r'''import json
import re
import sys

args = sys.argv[1:]
packages = [item for item in args if item.startswith("example.org/")]
names = {
    "example.org/project": ["TestAlpha", "TestBeta", "FuzzPacket"],
    "example.org/project/other": ["TestAlpha", "ExampleRoundTrip"],
}

def emit(action, package, test=None, output=None):
    event = {"Action": action, "Package": package}
    if test is not None:
        event["Test"] = test
    if output is not None:
        event["Output"] = output
    print(json.dumps(event))

if "-list=." in args:
    for package in packages:
        emit("start", package)
        for name in names[package]:
            emit("output", package, output=name + "\n")
        emit("pass", package)
else:
    pattern = next(item.split("=", 1)[1] for item in args if item.startswith("-run="))
    selected = re.compile(pattern)
    for package in packages:
        emit("start", package)
        for name in names[package]:
            if selected.fullmatch(name):
                emit("run", package, test=name)
                emit("pass", package, test=name + "/case")
                emit("pass", package, test=name)
        emit("pass", package)
'''


class RunGoTestShardedTest(unittest.TestCase):
    def test_forces_live_test_execution_without_overriding_explicit_count(self):
        self.assertEqual(test_selection_args(["-timeout=25m"]), ["-timeout=25m", "-count=1"])
        self.assertEqual(test_selection_args(["-count=1", "-timeout=25m"]),
                         ["-count=1", "-timeout=25m"])
        with self.assertRaisesRegex(ValueError, "requires -count=1"):
            test_selection_args(["-count=2"])

    def test_shards_partition_top_level_names_and_keep_duplicate_names_together(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            fake_go = root / "fake_go.py"
            fake_go.write_text(FAKE_GO)
            prefix = "candidate-simd-full-parity"
            for index in range(2):
                stem = f"{prefix}-shard-{index}-of-2"
                report = root / f"{stem}.inventory.json"
                command = [
                    sys.executable,
                    str(pathlib.Path(__file__).with_name("run_go_test_sharded.py").resolve()),
                    "--go=" + shlex.join([sys.executable, str(fake_go)]),
                    "--shard=" + str(index) + "/2",
                    "--root",
                    directory,
                    "--report",
                    str(report),
                    "--test-arg=-json",
                    "--test-arg=-count=1",
                    "--test-arg=-timeout=25m",
                    "--package=example.org/project",
                    "--package=example.org/project/other",
                ]
                result = subprocess.run(command, text=True, capture_output=True, check=False)
                self.assertEqual(result.returncode, 0, result.stderr)
                (root / f"{stem}.jsonl").write_text(result.stdout)
                (root / f"{stem}.exit").write_text("0\n")

            output_log = root / "combined.log"
            output_exit = root / "combined.exit"
            count, failing_shards = merge_shards(root, prefix, 2, output_log, output_exit)

            self.assertEqual(count, 5)
            self.assertEqual(failing_shards, 0)
            events = [json.loads(line) for line in output_log.read_text().splitlines()]
            completed = {
                (event["Package"], event["Test"])
                for event in events
                if event.get("Action") == "pass" and event.get("Test")
                and "/" not in event["Test"]
            }
            self.assertEqual(completed, {
                ("example.org/project", "TestAlpha"),
                ("example.org/project", "TestBeta"),
                ("example.org/project", "FuzzPacket"),
                ("example.org/project/other", "TestAlpha"),
                ("example.org/project/other", "ExampleRoundTrip"),
            })

    def test_rejects_filtered_runs_instead_of_changing_their_selection(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            fake_go = root / "fake_go.py"
            fake_go.write_text(FAKE_GO)
            command = [
                sys.executable,
                str(pathlib.Path(__file__).with_name("run_go_test_sharded.py").resolve()),
                "--go=" + shlex.join([sys.executable, str(fake_go)]),
                "--shard=0/1",
                "--root",
                directory,
                "--test-arg=-run=^TestAlpha$",
                "--package=example.org/project",
            ]
            result = subprocess.run(command, text=True, capture_output=True, check=False)
            self.assertEqual(result.returncode, 2)
            self.assertIn("unset GOPUS_TEST_SHARD", result.stderr)


if __name__ == "__main__":
    unittest.main()
