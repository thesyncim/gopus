#!/usr/bin/env python3
"""Run the manual native GOAMD64 v1/v2/v3 performance matrix."""

from __future__ import annotations

import hashlib
import json
import os
import pathlib
import platform
import shutil
import subprocess
import sys
from typing import Any

from validate_goamd64_targets import (
    ARMS, E2E_NAMES, MODES, TARGETS, exact_suite_specs, extract_go_toolchain, parse_e2e,
    validate_binary_build_info, validate_cbr_summary, validate_go_test_json, write_summary,
)


PROJECT_ENV = os.environ.get("CODEX_PROJECT_ENV", "/Users/thesyncim/.codex/bin/project-env")
LIBOPUS_VERSION = "1.6.1"
FEATURE_ENV = (
    "LIBOPUS_ENABLE_QEXT", "LIBOPUS_ENABLE_QEXT_SCALAR", "LIBOPUS_ENABLE_QEXT_SIMD",
    "LIBOPUS_ENABLE_DRED_QEXT_SCALAR", "LIBOPUS_ENABLE_DRED_QEXT_SIMD",
    "LIBOPUS_ENABLE_FIXED_SCALAR", "LIBOPUS_ENABLE_FIXED_SIMD",
    "LIBOPUS_ENABLE_FIXED_QEXT_SCALAR", "LIBOPUS_ENABLE_FIXED_QEXT_SIMD",
    "LIBOPUS_ENABLE_CUSTOM", "LIBOPUS_ENABLE_CUSTOM_QEXT_SCALAR", "LIBOPUS_ENABLE_CUSTOM_QEXT_SIMD",
    "LIBOPUS_ENABLE_CUSTOM_FIXED_SCALAR", "LIBOPUS_ENABLE_CUSTOM_FIXED_SIMD",
    "LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SCALAR", "LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SIMD",
    "LIBOPUS_ENABLE_CUSTOM_SCALAR",
)
ENCODER_TOOL_ARGS = (
    "-cases=per-case", "-benchtime=250ms", "-count=3", "-format=tsv",
    "-max-gopus-allocs-per-op=0", "--workload-hashes",
)
DECODE_TOOL_ARGS = (
    "-cases=aggregate", "-paths=all", "-benchtime=250ms", "-count=3", "-format=tsv",
    "-max-gopus-allocs-per-op=0",
)


class RunFailure(RuntimeError):
    pass


def sha256(path: pathlib.Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as source:
        for chunk in iter(lambda: source.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def parse_stamp(path: pathlib.Path) -> dict[str, str]:
    fields: dict[str, str] = {}
    for line in path.read_text().splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            fields[key] = value
    return fields


def rel(root: pathlib.Path, path: pathlib.Path) -> str:
    return str(path.relative_to(root))


def atomic_json(path: pathlib.Path, value: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + ".tmp")
    temp.write_text(json.dumps(value, indent=2, sort_keys=True) + "\n")
    temp.replace(path)


class Runner:
    def __init__(self, baseline: pathlib.Path, candidate: pathlib.Path, artifact: pathlib.Path):
        self.baseline = baseline.resolve()
        self.candidate = candidate.resolve()
        self.artifact = artifact.resolve()
        self.manifest_path = self.artifact / "manifest.json"
        self.manifest: dict[str, Any] = {
            "schema_version": 1,
            "status": "running",
            "errors": [],
            "metadata": {},
            "references": {},
            "binaries": {},
            "phase_records": [],
            "exactness": [],
            "startup_checks": [],
            "e2e_samples": [],
            "c_benchmarks": [],
        }
        self.artifact.mkdir(parents=True, exist_ok=True)
        if self.manifest_path.exists():
            raise RunFailure(f"output directory already contains {self.manifest_path}; choose a fresh artifact directory")

    def save(self) -> None:
        atomic_json(self.manifest_path, self.manifest)

    def fail(self, message: str) -> None:
        self.manifest["errors"].append(message)
        self.manifest["status"] = "failed"
        self.save()

    def go_command(self, cwd: pathlib.Path, args: list[str]) -> list[str]:
        if pathlib.Path(PROJECT_ENV).is_file() and os.access(PROJECT_ENV, os.X_OK):
            return [PROJECT_ENV, "go", *args]
        return ["go", *args]

    def run(
        self,
        name: str,
        cwd: pathlib.Path,
        command: list[str],
        env: dict[str, str],
        stdout_rel: str,
        stderr_rel: str | None = None,
    ) -> tuple[int, pathlib.Path, pathlib.Path]:
        stdout_path = self.artifact / stdout_rel
        stderr_path = self.artifact / (stderr_rel or (stdout_rel + ".stderr"))
        stdout_path.parent.mkdir(parents=True, exist_ok=True)
        stderr_path.parent.mkdir(parents=True, exist_ok=True)
        try:
            with stdout_path.open("wb") as stdout, stderr_path.open("wb") as stderr:
                completed = subprocess.run(command, cwd=cwd, env=env, stdout=stdout, stderr=stderr, check=False)
            code = completed.returncode
        except OSError as exc:
            stdout_path.write_text("")
            stderr_path.write_text(str(exc) + "\n")
            code = 127
        self.manifest["phase_records"].append({
            "name": name, "cwd": str(cwd), "argv": command, "exit": code,
            "stdout": rel(self.artifact, stdout_path), "stderr": rel(self.artifact, stderr_path),
        })
        self.save()
        return code, stdout_path, stderr_path

    def go_env(self, cwd: pathlib.Path, target: str | None = None) -> dict[str, str]:
        env = os.environ.copy()
        env["CODEX_AGENT_ID"] = "goamd64-benchmark"
        env["GOTOOLCHAIN"] = "local"
        env["GOFLAGS"] = ""
        env.pop("GOOS", None)
        env.pop("GOARCH", None)
        if target:
            env["GOAMD64"] = target
            env["GOEXPERIMENT"] = "simd"
        else:
            env.pop("GOAMD64", None)
        return env

    def mode_env(self, target: str, mode: str, *, oracle: bool = False) -> dict[str, str]:
        env = self.go_env(self.candidate, target)
        env["GOPUS_LIBOPUS_AMD64_TARGET"] = target
        if mode == "nosimd":
            env["GOPUS_LIBOPUS_REF_SCALAR"] = "1"
        else:
            env.pop("GOPUS_LIBOPUS_REF_SCALAR", None)
        if oracle:
            env["GOPUS_TEST_TIER"] = "parity"
            env["GOPUS_STRICT_LIBOPUS_REF"] = "1"
        else:
            env.pop("GOPUS_TEST_TIER", None)
            env.pop("GOPUS_STRICT_LIBOPUS_REF", None)
        if mode == "simd":
            env["GOPUS_REQUIRE_NATIVE_AVX2_FMA"] = "1"
            env["GOPUS_REQUIRE_PVQ_SIMD"] = "1"
        else:
            env.pop("GOPUS_REQUIRE_NATIVE_AVX2_FMA", None)
            env.pop("GOPUS_REQUIRE_PVQ_SIMD", None)
        return env

    def checked_run(self, name: str, cwd: pathlib.Path, cmd: list[str], env: dict[str, str], out: str) -> pathlib.Path:
        code, stdout, stderr = self.run(name, cwd, cmd, env, out)
        if code != 0:
            detail = stderr.read_text(errors="replace")[-2000:]
            raise RunFailure(f"{name} failed with exit={code}: {detail.strip()}")
        return stdout

    def preflight(self) -> None:
        system = platform.system()
        machine = platform.machine().lower()
        if system != "Linux" or machine not in {"x86_64", "amd64"}:
            raise RunFailure(f"GOAMD64 timing matrix requires a native Linux/amd64 runner; found {system}/{machine}")
        for root in (self.baseline, self.candidate):
            if not (root / "go.mod").is_file():
                raise RunFailure(f"checkout does not contain go.mod: {root}")
        cc_target = subprocess.run(["cc", "-dumpmachine"], text=True, capture_output=True, check=False)
        if cc_target.returncode or not cc_target.stdout.strip().startswith(("x86_64-", "amd64-")):
            raise RunFailure(f"GOAMD64 timing matrix requires an amd64 C compiler target; got {cc_target.stdout.strip()!r}")
        go_version = self.checked_run("go-version", self.candidate,
                                      self.go_command(self.candidate, ["version"]),
                                      self.go_env(self.candidate), "environment/go-version.txt").read_text().strip()
        go_toolchain = extract_go_toolchain(go_version)
        if go_toolchain is None:
            raise RunFailure(f"cannot determine local Go toolchain from `go version`: {go_version!r}")
        cache = self.checked_run("go-cache-path", self.candidate,
                                 self.go_command(self.candidate, ["env", "GOCACHE"]),
                                 self.go_env(self.candidate), "environment/gocache.txt").read_text().strip()
        if ".codex/worktrees" in cache:
            raise RunFailure(f"invalid Go build cache path under .codex/worktrees: {cache}")
        cpu_model, cpu_flags = "", ""
        cpuinfo = pathlib.Path("/proc/cpuinfo")
        if cpuinfo.is_file():
            for line in cpuinfo.read_text(errors="replace").splitlines():
                if line.startswith("model name") and not cpu_model:
                    cpu_model = line.split(":", 1)[1].strip()
                if line.startswith("flags") and not cpu_flags:
                    cpu_flags = line.split(":", 1)[1].strip()
        cc_version = subprocess.run(["cc", "--version"], text=True, capture_output=True, check=False).stdout.splitlines()
        self.manifest["metadata"] = {
            "runner": f"{system}/{machine}", "cpu_model": cpu_model, "cpu_flags": cpu_flags,
            "cc_target": cc_target.stdout.strip(), "cc_version": cc_version[0] if cc_version else "unknown",
            "go_version": go_version, "go_toolchain": go_toolchain, "go_cache": cache,
            "baseline_commit": self.git_commit(self.baseline), "candidate_commit": self.git_commit(self.candidate),
            "pgo_profiles": {"baseline": self.pgo_profiles(self.baseline), "candidate": self.pgo_profiles(self.candidate)},
            "ambient_go_settings_cleared": {key: os.environ.get(key, "") for key in ("GOFLAGS", "GOOS", "GOARCH", "GOAMD64")},
            "round_orders": self.round_orders(),
            "timing_note": "Go binaries and C archives are prepared before timing; each paired-C tool compiles its helper before its own measurements.",
        }
        self.save()

    @staticmethod
    def git_commit(root: pathlib.Path) -> str:
        result = subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"], text=True, capture_output=True, check=False)
        if result.returncode:
            raise RunFailure(f"cannot resolve commit in {root}: {result.stderr.strip()}")
        return result.stdout.strip()

    @staticmethod
    def pgo_profiles(root: pathlib.Path) -> list[dict[str, Any]]:
        output = []
        for path in sorted(root.glob("*.pgo")):
            if path.is_file():
                output.append({"path": path.name, "sha256": sha256(path), "bytes": path.stat().st_size})
        return output

    @staticmethod
    def round_orders() -> dict[str, list[str]]:
        forward = [f"{target}/{arm}" for target in TARGETS for arm in ARMS]
        reverse = list(reversed(forward))
        rotated = forward[3:] + forward[:3]
        rotated_reverse = list(reversed(rotated))
        return {"1-forward": forward, "2-reverse": reverse,
                "3-rotated-forward": rotated, "4-rotated-reverse": rotated_reverse}

    def build_env(self, root: pathlib.Path, target: str, mode: str | None = None) -> dict[str, str]:
        env = self.go_env(root, target)
        if mode is not None:
            env["GOPUS_LIBOPUS_AMD64_TARGET"] = target
        else:
            env.pop("GOPUS_LIBOPUS_AMD64_TARGET", None)
        if mode == "nosimd":
            env["GOPUS_LIBOPUS_REF_SCALAR"] = "1"
        else:
            env.pop("GOPUS_LIBOPUS_REF_SCALAR", None)
        return env

    def build_reference(self, target: str, variant: str) -> None:
        env = os.environ.copy()
        for key in FEATURE_ENV:
            env[key] = "0"
        env["LIBOPUS_ENABLE_SCALAR"] = "0"
        env["LIBOPUS_ENABLE_SIMD"] = "0"
        env["LIBOPUS_VERSION"] = LIBOPUS_VERSION
        env["GOPUS_LIBOPUS_AMD64_TARGET"] = target
        env["LIBOPUS_ENABLE_SCALAR" if variant == "scalar" else "LIBOPUS_ENABLE_SIMD"] = "1"
        for key in ("LIBOPUS_CFLAGS", "LIBOPUS_CPPFLAGS", "CPPFLAGS", "LDFLAGS"):
            env.pop(key, None)
        self.checked_run(f"libopus-{target}-{variant}-build", self.candidate,
                         ["bash", "tools/ensure_libopus.sh"], env,
                         f"{target}/references/{variant}/builder.stdout")
        source = self.candidate / f"tmp_check/opus-{LIBOPUS_VERSION}-amd64-{target}-{variant}"
        build_dir = self.artifact / target / "references" / variant
        build_dir.mkdir(parents=True, exist_ok=True)
        artifacts = {
            "archive": source / ".libs/libopus.a", "config": source / "config.h",
            "stamp": source / ".gopus-libopus-build",
        }
        for path in artifacts.values():
            if not path.is_file() or path.stat().st_size == 0:
                raise RunFailure(f"missing built libopus reference artifact: {path}")
        fields = parse_stamp(artifacts["stamp"])
        march = "x86-64" if target == "v1" else f"x86-64-{target}"
        expected_cflags = "-O3 -DNDEBUG"
        if variant == "scalar":
            expected_cflags += " -fno-tree-vectorize -fno-tree-slp-vectorize"
        expected_cflags += f" -march={march} -mtune=generic"
        expected_configure = "--enable-static --disable-shared"
        expected_configure += " --disable-asm --disable-rtcd --disable-intrinsics" if variant == "scalar" else " --enable-rtcd --enable-intrinsics"
        if (fields.get("version") != LIBOPUS_VERSION or fields.get("amd64_target") != target
                or fields.get("CFLAGS") != expected_cflags or fields.get("configure") != expected_configure
                or any(fields.get(feature) != "0" for feature in ("qext", "fixed", "custom"))):
            raise RunFailure(f"wrong {target}/{variant} libopus stamp: target={fields.get('amd64_target')} CFLAGS={fields.get('CFLAGS')!r}")
        copied = {}
        for name, path in artifacts.items():
            destination = build_dir / ("libopus.a" if name == "archive" else "config.h" if name == "config" else ".gopus-libopus-build")
            shutil.copy2(path, destination)
            copied[name] = {"path": rel(self.artifact, destination), "sha256": sha256(destination), "bytes": destination.stat().st_size}
        self.manifest["references"].setdefault(target, {})[variant] = {
            "archive_path": copied["archive"]["path"], "config_path": copied["config"]["path"],
            "stamp_path": copied["stamp"]["path"], "archive_sha256": copied["archive"]["sha256"],
            "config_sha256": copied["config"]["sha256"], "stamp_sha256": copied["stamp"]["sha256"],
            "stamp_fields": fields,
        }
        self.save()

    def build_binaries(self) -> None:
        for target in TARGETS:
            for arm in ARMS:
                root = self.baseline if arm == "oldasm" else self.candidate
                mode = None if arm == "oldasm" else arm
                env = self.build_env(root, target, mode)
                tags = ["-tags", "nosimd"] if mode == "nosimd" else []
                binary = self.artifact / target / "binaries" / f"{arm}-root.test"
                cmd = self.go_command(root, ["test", "-c", "-pgo=auto", *tags, "-o", str(binary), "."])
                self.checked_run(f"build-{target}-{arm}-root", root, cmd, env,
                                 f"{target}/build/{arm}-root-build.log")
                self.record_binary(f"{target}/{arm}/root", root, target, mode or "oldasm", binary)
            for mode in MODES:
                env = self.build_env(self.candidate, target, mode)
                tags = ["-tags", "nosimd"] if mode == "nosimd" else []
                for tool in ("encoderbenchcmp", "testvectorbenchcmp"):
                    binary = self.artifact / target / "binaries" / f"{mode}-{tool}"
                    cmd = self.go_command(self.candidate, ["build", "-pgo=auto", *tags, "-o", str(binary), f"./tools/{tool}"])
                    self.checked_run(f"build-{target}-{mode}-{tool}", self.candidate, cmd, env,
                                     f"{target}/build/{mode}-{tool}-build.log")
                    self.record_binary(f"{target}/{mode}/{tool}", self.candidate, target, mode, binary)

    def record_binary(self, key: str, cwd: pathlib.Path, target: str, mode: str, binary: pathlib.Path) -> None:
        env = self.go_env(cwd, target)
        info_path = self.artifact / "build-info" / f"{key.replace('/', '-')}.txt"
        info = self.checked_run(f"version-m-{key}", cwd,
                                self.go_command(cwd, ["version", "-m", str(binary)]), env,
                                rel(self.artifact, info_path))
        text = info.read_text(errors="replace")
        errors = validate_binary_build_info(
            text, target, mode, self.manifest["metadata"].get("go_toolchain"))
        if errors:
            raise RunFailure(f"go version -m for {key} is invalid: {'; '.join(errors)}")
        self.manifest["binaries"][key] = {
            "path": rel(self.artifact, binary), "version_m": rel(self.artifact, info_path),
            "target": target, "mode": mode, "source_commit": self.git_commit(cwd),
        }
        self.save()

    def check_startup(self) -> None:
        for target in TARGETS:
            for arm in ARMS:
                binary = self.artifact / target / "binaries" / f"{arm}-root.test"
                env = self.build_env(self.baseline if arm == "oldasm" else self.candidate,
                                     target, None if arm == "oldasm" else arm)
                code, stdout, stderr = self.run(f"startup-{target}-{arm}",
                                                self.baseline if arm == "oldasm" else self.candidate,
                                                [str(binary), "-test.run", "^$"], env,
                                                f"{target}/startup/{arm}.stdout")
                self.manifest["startup_checks"].append({
                    "target": target, "arm": arm, "exit": code,
                    "stdout": rel(self.artifact, stdout), "stderr": rel(self.artifact, stderr),
                })
                if code != 0:
                    raise RunFailure(f"{target}/{arm} test binary cannot start on this native runner; see {stderr}")
        self.save()

    def run_exactness(self) -> bool:
        gate_failed = False
        for target in TARGETS:
            for mode in MODES:
                env = self.mode_env(target, mode, oracle=True)
                tags = ["-tags", "nosimd"] if mode == "nosimd" else []
                for spec in exact_suite_specs(mode):
                    out = f"{target}/exact/{mode}/{spec['name']}.jsonl"
                    cmd = self.go_command(self.candidate, [
                        "test", "-json", "-count=1", "-timeout=10m", *tags,
                        "-run", spec["selector"], spec["package"],
                    ])
                    code, stdout, stderr = self.run(f"exact-{target}-{mode}-{spec['name']}", self.candidate,
                                                    cmd, env, out)
                    entry = {
                        "target": target, "mode": mode, "name": spec["name"],
                        "package": spec["package"], "selector": spec["selector"],
                        "expected_leaves": spec["expected_leaves"], "strict_cbr": spec.get("strict_cbr", False),
                        "exit": code, "stdout": rel(self.artifact, stdout), "stderr": rel(self.artifact, stderr),
                    }
                    self.manifest["exactness"].append(entry)
                    errors = validate_go_test_json(stdout.read_text(errors="replace"), spec["expected_leaves"], code)
                    if spec.get("strict_cbr"):
                        output = ""
                        for line in stdout.read_text(errors="replace").splitlines():
                            try:
                                output += json.loads(line).get("Output", "")
                            except json.JSONDecodeError:
                                continue
                        c_variant = "scalar" if mode == "nosimd" else "simd"
                        errors.extend(validate_cbr_summary(output, c_variant))
                    if errors:
                        gate_failed = True
                        self.manifest["errors"].extend(f"{target}/{mode}/{spec['name']}: {error}" for error in errors)
                    self.save()
        return not gate_failed

    def vectors(self) -> None:
        self.checked_run("ensure-rfc-vectors", self.candidate, ["make", "ensure-testvectors"],
                         os.environ.copy(), "environment/ensure-testvectors.log")

    def run_e2e_rounds(self) -> None:
        base_order = self.manifest["metadata"]["round_orders"]
        sequence = 0
        for sample, label in enumerate(("1-forward", "2-reverse", "3-rotated-forward", "4-rotated-reverse"), 1):
            for arm_id in base_order[label]:
                sequence += 1
                target, arm = arm_id.split("/", 1)
                root = self.baseline if arm == "oldasm" else self.candidate
                mode = None if arm == "oldasm" else arm
                env = self.build_env(root, target, mode)
                binary = self.artifact / target / "binaries" / f"{arm}-root.test"
                pattern = r"^Benchmark(DecoderDecode_(CELT|Hybrid|SILK)|EncoderEncode_(CallerBuffer|VoIP|LowDelay))$"
                out = f"{target}/timings/e2e-round-{sample}-{arm}.log"
                code, stdout, stderr = self.run(f"e2e-{target}-{arm}-round-{sample}", root,
                                                [str(binary), "-test.run", "^$", "-test.bench", pattern,
                                                 "-test.benchtime=500ms", "-test.count=1", "-test.cpu=1", "-test.benchmem"],
                                                env, out)
                self.manifest["e2e_samples"].append({
                    "target": target, "arm": arm, "sample": sample, "order": label, "exit": code,
                    "sequence": sequence, "stdout": rel(self.artifact, stdout), "stderr": rel(self.artifact, stderr),
                })
                rows, errors = parse_e2e(stdout.read_text(errors="replace"), code)
                if errors:
                    self.manifest["errors"].extend(f"{target}/{arm}/round{sample}: {error}" for error in errors)
                self.save()

    def run_c_benchmarks(self) -> None:
        for target in TARGETS:
            for mode in MODES:
                env = self.mode_env(target, mode, oracle=True)
                directory = f"{target}/timings/paired-c-{mode}"
                encoder = self.artifact / target / "binaries" / f"{mode}-encoderbenchcmp"
                decoder = self.artifact / target / "binaries" / f"{mode}-testvectorbenchcmp"
                enc_code, enc_out, enc_err = self.run(
                    f"encoderbenchcmp-{target}-{mode}", self.candidate,
                    [str(encoder), *ENCODER_TOOL_ARGS], env, f"{directory}/encoder.tsv", f"{directory}/encoder.stderr",
                )
                dec_code, dec_out, dec_err = self.run(
                    f"testvectorbenchcmp-{target}-{mode}", self.candidate,
                    [str(decoder), *DECODE_TOOL_ARGS], env, f"{directory}/decode.tsv", f"{directory}/decode.stderr",
                )
                self.manifest["c_benchmarks"].append({
                    "target": target, "mode": mode,
                    "encoder_tsv": rel(self.artifact, enc_out), "encoder_stderr": rel(self.artifact, enc_err),
                    "encoder_exit": enc_code, "decode_tsv": rel(self.artifact, dec_out),
                    "decode_stderr": rel(self.artifact, dec_err), "decode_exit": dec_code,
                })
                self.save()

    def run_all(self) -> int:
        self.save()
        try:
            self.preflight()
            for target in TARGETS:
                for variant in ("scalar", "simd"):
                    self.build_reference(target, variant)
            self.build_binaries()
            self.check_startup()
            if not self.run_exactness():
                self.manifest["status"] = "failed"
                self.save()
                return self.finish()
            self.vectors()
            self.run_e2e_rounds()
            self.run_c_benchmarks()
            self.manifest["status"] = "complete" if not self.manifest["errors"] else "failed"
            self.save()
        except RunFailure as exc:
            self.fail(str(exc))
        except Exception as exc:
            self.fail(f"unexpected runner error: {type(exc).__name__}: {exc}")
        return self.finish()

    def finish(self) -> int:
        summary = write_summary(self.artifact, self.manifest)
        print(f"GOAMD64 target matrix valid={summary['valid']} artifact_dir={self.artifact}")
        for error in summary["errors"]:
            print(f"error: {error}", file=sys.stderr)
        return 0 if summary["valid"] else 1


def main(args: list[str]) -> int:
    if len(args) != 3:
        print(f"usage: {pathlib.Path(sys.argv[0]).name} <baseline-checkout> <candidate-checkout> <artifact-dir>", file=sys.stderr)
        return 2
    try:
        runner = Runner(pathlib.Path(args[0]), pathlib.Path(args[1]), pathlib.Path(args[2]))
        return runner.run_all()
    except RunFailure as exc:
        print(f"GOAMD64 matrix: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main(sys.argv[1:]))
