#!/usr/bin/env python3
"""Tests for native-host guards and PGO training provenance."""

import json
import pathlib
import tempfile
import unittest
from unittest import mock

import refresh_pgo


class RefreshPGOTests(unittest.TestCase):
    def test_rejects_cross_compilation_and_arm_hosts(self):
        for machine, host, target in (("arm64", "amd64", "amd64"), ("x86_64", "arm64", "amd64"), ("x86_64", "amd64", "arm64")):
            with self.subTest(machine=machine, host=host, target=target), self.assertRaises(RuntimeError):
                refresh_pgo.require_native_amd64(machine, {"GOHOSTARCH": host, "GOARCH": target})
        refresh_pgo.require_native_amd64("x86_64", {"GOHOSTARCH": "amd64", "GOARCH": "amd64"})

    def test_equal_training_and_merged_profile_provenance(self):
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory)
            commands = []

            def capture(command, env):
                if command[:2] == ["go", "env"]:
                    return json.dumps({"GOARCH": "amd64", "GOHOSTARCH": "amd64", "GOAMD64": "v3"})
                if command[:2] == ["git", "status"]:
                    return ""
                return "source-or-host-evidence"

            def run(command, **kwargs):
                commands.append((command, kwargs["env"]))
                if command[0] == "make":
                    path = next(arg.removeprefix("PGO_FILE=") for arg in command if arg.startswith("PGO_FILE="))
                    pathlib.Path(path).write_bytes(kwargs["env"]["GOEXPERIMENT"].encode())
                else:
                    self.assertEqual(command[:4], ["go", "tool", "pprof", "-proto"])
                    pathlib.Path(command[4].removeprefix("-output=")).write_bytes(b"merged")

            with mock.patch.object(refresh_pgo.platform, "machine", return_value="x86_64"), mock.patch.object(refresh_pgo.platform, "platform", return_value="Linux-amd64"), mock.patch.object(refresh_pgo, "capture", side_effect=capture), mock.patch.object(refresh_pgo, "training_source_hash", return_value="training-source-hash"), mock.patch.object(refresh_pgo, "project_command", side_effect=lambda command: command), mock.patch.object(refresh_pgo.subprocess, "run", side_effect=run):
                refresh_pgo.refresh(output, "20s", 1)
            self.assertEqual(len(commands), 3)
            for (command, env), mode in zip(commands[:2], ("nosimd", "simd")):
                self.assertIn("PGO_GENERATE_FLAG=-pgo=off", command)
                self.assertIn("PGO_BENCHTIME=20s", command)
                self.assertIn("PGO_COUNT=1", command)
                self.assertEqual(env["GOEXPERIMENT"], mode)
                self.assertEqual(env["GOAMD64"], "v3")
                self.assertEqual(env["GOMAXPROCS"], "1")
            metadata = json.loads((output / "provenance.json").read_text())
            self.assertEqual(metadata["profile_sha256"], refresh_pgo.file_hash(output / "default.pgo"))
            self.assertEqual([item["goexperiment"] for item in metadata["training"]], ["nosimd", "simd"])
            self.assertEqual(metadata["source_sha"], "source-or-host-evidence")
            self.assertEqual(metadata["training_pgo"], "off")
            self.assertEqual(metadata["training_source_sha256"], "training-source-hash")


if __name__ == "__main__":
    unittest.main()
