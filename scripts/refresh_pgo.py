#!/usr/bin/env python3
"""Collect a native amd64 profile and its reproducible training provenance."""

import argparse
import datetime
import hashlib
import json
import os
import pathlib
import platform
import shlex
import subprocess


ROOT = pathlib.Path(__file__).resolve().parents[1]


def project_command(command):
    wrapper = pathlib.Path(os.environ.get("CODEX_PROJECT_ENV", "/Users/thesyncim/.codex/bin/project-env"))
    return [str(wrapper), *command] if wrapper.is_file() and os.access(wrapper, os.X_OK) else command


def capture(command, env):
    return subprocess.check_output(project_command(command), cwd=ROOT, env=env, text=True).strip()


def require_native_amd64(machine, go_env):
    if machine not in ("x86_64", "amd64") or go_env["GOHOSTARCH"] != "amd64" or go_env["GOARCH"] != "amd64":
        raise RuntimeError("PGO refresh requires a native amd64 host and an amd64 Go toolchain")


def training_command(profile, benchtime, count):
    return [
        "make", "pgo-generate", f"PGO_FILE={profile}", "PGO_GENERATE_FLAG=-pgo=off",
        f"PGO_BENCHTIME={benchtime}", f"PGO_COUNT={count}",
    ]


def file_hash(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def training_source_hash(env):
    paths = capture(["git", "ls-files", "*.go", "*.s", "go.mod", "go.sum", "Makefile"], env).splitlines()
    digest = hashlib.sha256()
    for name in sorted(paths):
        digest.update(name.encode() + b"\0")
        digest.update((ROOT / name).read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def refresh(output, benchtime, count):
    env = dict(os.environ, GOMAXPROCS="1", GOAMD64="v3", GOWORK="off", GOFLAGS="", GOEXPERIMENT="nosimd")
    go_env = json.loads(capture(["go", "env", "-json", "GOOS", "GOARCH", "GOHOSTOS", "GOHOSTARCH", "GOAMD64", "GOVERSION"], env))
    require_native_amd64(platform.machine(), go_env)
    output.mkdir(parents=True, exist_ok=True)
    metadata = {
        "source_sha": capture(["git", "rev-parse", "HEAD"], env),
        "source_tree": capture(["git", "rev-parse", "HEAD^{tree}"], env),
        "source_status": capture(["git", "status", "--porcelain", "--untracked-files=no"], env),
        "generated_at_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
        "go_version": capture(["go", "version"], env),
        "training_source_sha256": training_source_hash(env),
        "go_env": go_env,
        "host": platform.platform(),
        "cpu": capture(["lscpu"], env),
        "training_pgo": "off",
        "training_benchtime": benchtime,
        "training_count": count,
        "gomaxprocs": 1,
        "training": [],
    }
    if metadata["source_status"]:
        raise RuntimeError("PGO refresh requires a clean tracked source tree")
    profiles = []
    with (output / "generation.log").open("w") as log:
        for mode in ("nosimd", "simd"):
            mode_env = dict(env, GOEXPERIMENT=mode)
            profile = output / f"{mode}.pgo"
            command = training_command(profile, benchtime, count)
            log.write(f"GOEXPERIMENT={mode} GOAMD64=v3 GOMAXPROCS=1 {shlex.join(command)}\n")
            log.flush()
            subprocess.run(project_command(command), cwd=ROOT, env=mode_env, stdout=log, stderr=subprocess.STDOUT, check=True)
            profiles.append(profile)
            metadata["training"].append({
                "goexperiment": mode, "command": command, "profile": profile.name,
                "sha256": file_hash(profile),
            })
        merged = output / "default.pgo"
        command = ["go", "tool", "pprof", "-proto", f"-output={merged}", *map(str, profiles)]
        log.write(shlex.join(command) + "\n")
        log.flush()
        subprocess.run(project_command(command), cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT, check=True)
    metadata["merge_command"] = command
    metadata["profile_sha256"] = file_hash(merged)
    (output / "provenance.json").write_text(json.dumps(metadata, indent=2) + "\n")
    (output / "profile-top.txt").write_text(capture(["go", "tool", "pprof", "-top", "-nodecount=50", str(merged)], env) + "\n")
    print(f"Profile: {merged}\nSHA256: {metadata['profile_sha256']}\nSource: {metadata['source_sha']}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=pathlib.Path)
    parser.add_argument("--benchtime", default="20s")
    parser.add_argument("--count", type=int, default=1)
    args = parser.parse_args()
    if args.count < 1:
        parser.error("--count must be positive")
    refresh(args.output.resolve(), args.benchtime, args.count)


if __name__ == "__main__":
    main()
