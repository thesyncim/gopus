#!/usr/bin/env python3
"""Guard runtime libopus scalar-width parity debt.

The codec currently has known legacy float64/complex128 debt. This guard makes
that debt explicit and ratchets it down: current findings must match the
allowlist exactly, new findings fail, and removed findings require refreshing
the baseline so cleanup is recorded.
"""

from __future__ import annotations

import argparse
import hashlib
import os
import re
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


DEFAULT_ALLOWLIST = Path("tools/type_parity_allowlist.tsv")

RUNTIME_ROOTS = (
    Path("."),
    Path("celt"),
    Path("encoder"),
    Path("hybrid"),
    Path("multistream"),
    Path("plc"),
    Path("silk"),
    Path("rangecoding"),
    Path("internal/osce"),
    Path("internal/lpcnetplc"),
)

ROOT_GO_FILES = {
    "application_common.go",
    "controls_common.go",
    "decoder.go",
    "decoder_decode.go",
    "decoder_dred_48k.go",
    "decoder_dred_explicit_extra.go",
    "decoder_dred_helpers.go",
    "decoder_dred_helpers_default.go",
    "decoder_fec.go",
    "decoder_misc.go",
    "decoder_opus_frame.go",
    "decoder_osce_bwe_apply.go",
    "decoder_osce_bwe_apply_default.go",
    "decoder_osce_bwe_crossfade.go",
    "decoder_osce_lace_apply.go",
    "decoder_osce_lace_apply_default.go",
    "decoder_osce_lace_crossfade.go",
    "decoder_plc_helpers.go",
    "decoder_qext_hook.go",
    "decoder_qext_hook_default.go",
    "decoder_qext_payloads_default.go",
    "decoder_qext_payloads_qext.go",
    "dred_decoder_extra.go",
    "dred_payload.go",
    "encode_common.go",
    "encoder.go",
    "encoder_encode.go",
    "multistream.go",
    "multistream_decode.go",
    "multistream_encode.go",
    "optional_extensions.go",
    "packet.go",
    "packet_extension_helpers.go",
    "packet_extensions.go",
    "packet_multistream_helpers.go",
    "packet_parse.go",
    "packet_repacketizer.go",
    "pcm.go",
    "pcm_convert_arm64.go",
    "pcm_convert_default.go",
    "softclip.go",
    "stream.go",
}

FINDING_RE = re.compile(
    r"\bfloat64\b|\bcomplex128\b|\bKissFFT64State\b|\bensureFloat64Slice\b|\bensureComplexSlice\b"
)


@dataclass(frozen=True)
class FindingKey:
    path: str
    digest: str


@dataclass
class Finding:
    key: FindingKey
    count: int
    lines: list[int]
    sample: str


@dataclass
class Allowed:
    count: int
    reason: str
    sample: str


def repo_files() -> list[Path]:
    proc = subprocess.run(
        ["git", "ls-files", "*.go"],
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    )
    files: list[Path] = []
    for raw in proc.stdout.splitlines():
        path = Path(raw)
        if path.name.endswith("_test.go"):
            continue
        if path.parts and path.parts[0] in {"examples", "testvectors", "tools"}:
            continue
        if is_runtime_path(path):
            files.append(path)
    return sorted(files)


def is_runtime_path(path: Path) -> bool:
    if len(path.parts) == 1:
        return path.name in ROOT_GO_FILES
    return any(path == root or root in path.parents for root in RUNTIME_ROOTS if root != Path("."))


def normalized_line(line: str) -> str:
    return " ".join(line.strip().split())


def digest_line(line: str) -> str:
    return hashlib.sha256(normalized_line(line).encode("utf-8")).hexdigest()


def escape_field(value: str) -> str:
    return value.replace("\\", "\\\\").replace("\t", "\\t").replace("\n", "\\n")


def unescape_field(value: str) -> str:
    return value.replace("\\n", "\n").replace("\\t", "\t").replace("\\\\", "\\")


def scan() -> dict[FindingKey, Finding]:
    findings: dict[FindingKey, Finding] = {}
    for path in repo_files():
        try:
            lines = path.read_text(encoding="utf-8").splitlines()
        except UnicodeDecodeError:
            lines = path.read_text(encoding="latin-1").splitlines()
        for idx, line in enumerate(lines, start=1):
            if not FINDING_RE.search(line):
                continue
            key = FindingKey(path.as_posix(), digest_line(line))
            finding = findings.get(key)
            if finding is None:
                findings[key] = Finding(key=key, count=1, lines=[idx], sample=normalized_line(line))
            else:
                finding.count += 1
                finding.lines.append(idx)
    return findings


def read_allowlist(path: Path) -> dict[FindingKey, Allowed]:
    allowed: dict[FindingKey, Allowed] = {}
    if not path.exists():
        return allowed
    for line_no, raw in enumerate(path.read_text(encoding="utf-8").splitlines(), start=1):
        if not raw or raw.startswith("#"):
            continue
        parts = raw.split("\t", 4)
        if len(parts) != 5:
            raise SystemExit(f"{path}:{line_no}: invalid allowlist row")
        path_s, count_s, digest, reason, sample = parts
        try:
            count = int(count_s)
        except ValueError as exc:
            raise SystemExit(f"{path}:{line_no}: invalid count {count_s!r}") from exc
        allowed[FindingKey(path_s, digest)] = Allowed(
            count=count,
            reason=unescape_field(reason),
            sample=unescape_field(sample),
        )
    return allowed


def write_allowlist(path: Path, findings: dict[FindingKey, Finding], reason: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    lines = [
        "# Runtime libopus type-parity legacy debt baseline.",
        "# Generated by: python3 tools/check_type_parity.py --update",
        "#",
        "# Columns: path<TAB>count<TAB>sha256(normalized line)<TAB>reason<TAB>normalized sample",
        "# Agents: do not update this file to hide new float64/complex128 debt.",
        "# Migrate runtime codec/scratch storage to libopus-width types instead.",
    ]
    for key in sorted(findings, key=lambda k: (k.path, k.digest)):
        finding = findings[key]
        lines.append(
            "\t".join(
                [
                    key.path,
                    str(finding.count),
                    key.digest,
                    escape_field(reason),
                    escape_field(finding.sample[:220]),
                ]
            )
        )
    path.write_text("\n".join(lines) + "\n", encoding="utf-8")


def report_mismatches(
    current: dict[FindingKey, Finding],
    allowed: dict[FindingKey, Allowed],
    allowlist_path: Path,
    allow_stale: bool,
) -> int:
    new_or_grown: list[tuple[FindingKey, int, int, Finding]] = []
    stale: list[tuple[FindingKey, int, int, Allowed]] = []

    for key, finding in current.items():
        baseline = allowed.get(key)
        allowed_count = baseline.count if baseline else 0
        if finding.count > allowed_count:
            new_or_grown.append((key, finding.count, allowed_count, finding))

    if not allow_stale:
        for key, baseline in allowed.items():
            current_count = current.get(key).count if key in current else 0
            if current_count < baseline.count:
                stale.append((key, current_count, baseline.count, baseline))

    if not new_or_grown and not stale:
        total = sum(f.count for f in current.values())
        print(f"type parity guard passed: {total} legacy finding(s) match {allowlist_path}")
        return 0

    print("type parity guard failed", file=sys.stderr)
    if new_or_grown:
        print("\nNew or increased runtime float64/complex128 debt:", file=sys.stderr)
        for key, current_count, allowed_count, finding in sorted(new_or_grown, key=lambda row: row[0].path):
            locs = ",".join(str(line) for line in finding.lines[:5])
            more = "" if len(finding.lines) <= 5 else ",..."
            print(
                f"  {key.path}:{locs}{more}: count {current_count} > allowed {allowed_count}: {finding.sample}",
                file=sys.stderr,
            )
    if stale:
        print("\nStale baseline entries after cleanup:", file=sys.stderr)
        for key, current_count, allowed_count, baseline in sorted(stale, key=lambda row: row[0].path):
            print(
                f"  {key.path}: count {current_count} < baseline {allowed_count}: {baseline.sample}",
                file=sys.stderr,
            )
    print(
        "\nFix by migrating runtime codec and scratch storage to libopus-width types. "
        "Only refresh the baseline when cleanup removes legacy debt, or when a remaining "
        "float64 is tied to an explicit C double helper.",
        file=sys.stderr,
    )
    return 1


def main(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--allowlist", type=Path, default=DEFAULT_ALLOWLIST)
    parser.add_argument("--update", action="store_true", help="rewrite the allowlist from the current tree")
    parser.add_argument(
        "--reason",
        default="legacy-current",
        help="reason written when --update is used",
    )
    parser.add_argument(
        "--allow-stale",
        action="store_true",
        default=os.environ.get("GOPUS_TYPE_PARITY_ALLOW_STALE") == "1",
        help="do not fail when allowlist entries have already been removed",
    )
    args = parser.parse_args(argv)

    findings = scan()
    if args.update:
        write_allowlist(args.allowlist, findings, args.reason)
        print(f"wrote {args.allowlist} with {sum(f.count for f in findings.values())} finding(s)")
        return 0

    allowed = read_allowlist(args.allowlist)
    return report_mismatches(findings, allowed, args.allowlist, args.allow_stale)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
