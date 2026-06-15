#!/usr/bin/env bash
# Compare a kernel's default build (hand-written asm on arm64, scalar fallback on
# amd64/purego) against the archsimd path (Go tip + GOEXPERIMENT=simd) with
# benchstat. The dispatch picks exactly one implementation per build, so the
# comparison is necessarily across two binaries on the same host; the
# always-compiled scalar "...Ref" benchmark is the shared anchor that confirms
# the two runs are measuring the same machine state.
#
# Usage: scripts/bench-simd-kernel.sh [package] [bench-regex] [count]
#   scripts/bench-simd-kernel.sh ./internal/celt/ BenchmarkScaleInto
set -euo pipefail

pkg="${1:-./internal/celt/}"
bench="${2:-BenchmarkScaleInto}"
count="${3:-8}"

gotip="$(go env GOPATH)/bin/gotip"
if [[ ! -x "$gotip" ]]; then
	echo "gotip not found at $gotip; run: go install golang.org/dl/gotip@latest && gotip download" >&2
	exit 1
fi

benchstat="$(command -v benchstat || true)"
[[ -z "$benchstat" && -x "$(go env GOPATH)/bin/benchstat" ]] && benchstat="$(go env GOPATH)/bin/benchstat"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo ">> baseline (default toolchain: arm64 NEON asm, amd64/purego scalar)"
go test "$pkg" -run '^$' -bench "$bench" -benchtime=200ms -count="$count" | tee "$tmp/baseline.txt"

echo ">> archsimd (gotip + GOEXPERIMENT=simd)"
GOEXPERIMENT=simd "$gotip" test "$pkg" -run '^$' -bench "$bench" -benchtime=200ms -count="$count" | tee "$tmp/archsimd.txt"

if [[ -n "$benchstat" ]]; then
	echo ">> benchstat baseline vs archsimd"
	"$benchstat" "$tmp/baseline.txt" "$tmp/archsimd.txt"
else
	echo "benchstat not installed (go install golang.org/x/perf/cmd/benchstat@latest); raw files:"
	echo "  $tmp/baseline.txt  $tmp/archsimd.txt"
	trap - EXIT
fi
