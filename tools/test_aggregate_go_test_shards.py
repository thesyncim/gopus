"""Regression tests for exact test-shard aggregation."""

import json
import pathlib
import tempfile
import unittest

from aggregate_go_test_shards import merge_shards
from run_go_test_sharded import shard_for_name, write_manifest


class AggregateGoTestShardsTest(unittest.TestCase):
    def make_shards(self, root, total=3):
        prefix = "candidate-simd-full-parity"
        inventory = [
            ("example.org/project", "TestAlpha"),
            ("example.org/project", "TestBeta"),
            ("example.org/project/other", "TestAlpha"),
            ("example.org/project/other", "ExampleRoundTrip"),
            ("example.org/project/fuzz", "FuzzPacket"),
        ]
        logs = []
        for index in range(total):
            stem = f"{prefix}-shard-{index}-of-{total}"
            write_manifest(root / f"{stem}.inventory.json", index, total, inventory)
            events = []
            for package, name in inventory:
                if shard_for_name(name, total) != index:
                    continue
                events.append({"Action": "run", "Package": package, "Test": name})
                events.append({"Action": "pass", "Package": package, "Test": name + "/case"})
                events.append({"Action": "pass", "Package": package, "Test": name})
            log_path = root / f"{stem}.jsonl"
            log_path.write_text("".join(json.dumps(event) + "\n" for event in events))
            (root / f"{stem}.exit").write_text("1\n" if index == 1 else "0\n")
            logs.append(log_path)
        return prefix, inventory, logs

    def test_merges_complete_inventory_and_preserves_subtest_events(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prefix, inventory, logs = self.make_shards(root)
            output_log = root / "combined.log"
            output_exit = root / "combined.exit"

            count, failed_shards = merge_shards(root, prefix, 3, output_log, output_exit)

            self.assertEqual(count, len(inventory))
            self.assertEqual(failed_shards, 1)
            self.assertEqual(output_exit.read_text(), "1\n")
            combined = [json.loads(line) for line in output_log.read_text().splitlines()]
            self.assertEqual(sum(event.get("Test", "").endswith("/case") for event in combined), len(inventory))
            self.assertEqual(len(logs), 3)

    def test_rejects_missing_top_level_result(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prefix, inventory, _ = self.make_shards(root)
            missing_name = "TestAlpha"
            shard = shard_for_name(missing_name, 3)
            stem = f"{prefix}-shard-{shard}-of-3"
            log_path = root / f"{stem}.jsonl"
            events = [json.loads(line) for line in log_path.read_text().splitlines()]
            log_path.write_text("".join(
                json.dumps(event) + "\n"
                for event in events
                if not (event.get("Test") == missing_name and event.get("Action") == "pass")
            ))
            with self.assertRaisesRegex(ValueError, "omitted"):
                merge_shards(root, prefix, 3, root / "combined.log", root / "combined.exit")

    def test_rejects_a_child_result_masquerading_as_parent_completion(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prefix, inventory, _ = self.make_shards(root)
            name = "TestAlpha"
            shard = shard_for_name(name, 3)
            stem = f"{prefix}-shard-{shard}-of-3"
            log_path = root / f"{stem}.jsonl"
            events = [json.loads(line) for line in log_path.read_text().splitlines()]
            events = [event for event in events if event.get("Test") != name]
            events.append({"Action": "pass", "Package": "example.org/project", "Test": name + "/case"})
            log_path.write_text("".join(json.dumps(event) + "\n" for event in events))
            with self.assertRaisesRegex(ValueError, "omitted"):
                merge_shards(root, prefix, 3, root / "combined.log", root / "combined.exit")

    def test_rejects_duplicate_or_wrong_shard_terminal_result(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prefix, inventory, _ = self.make_shards(root)
            name = "TestAlpha"
            shard = shard_for_name(name, 3)
            stem = f"{prefix}-shard-{shard}-of-3"
            log_path = root / f"{stem}.jsonl"
            events = [json.loads(line) for line in log_path.read_text().splitlines()]
            event = next(
                event for event in events
                if event.get("Test") == name and event.get("Action") == "pass"
            )
            events.append(event)
            log_path.write_text("".join(json.dumps(item) + "\n" for item in events))
            with self.assertRaisesRegex(ValueError, "duplicate terminal"):
                merge_shards(root, prefix, 3, root / "combined.log", root / "combined.exit")

    def test_rejects_missing_shard_artifacts(self):
        with tempfile.TemporaryDirectory() as directory:
            root = pathlib.Path(directory)
            prefix, _, _ = self.make_shards(root)
            (root / f"{prefix}-shard-1-of-3.exit").unlink()
            with self.assertRaises(FileNotFoundError):
                merge_shards(root, prefix, 3, root / "combined.log", root / "combined.exit")


if __name__ == "__main__":
    unittest.main()
