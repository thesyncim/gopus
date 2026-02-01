#!/bin/sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="$ROOT/internal/celt/kf_bfly4_arm64.s"
TMP="$(mktemp -d)"

clang -O3 -c -target arm64-apple-darwin -o "$TMP/kf_bfly4_m1.o" "$ROOT/internal/celt/asmgen/kf_bfly4_m1.c"
clang -O3 -fno-vectorize -fno-slp-vectorize -c -target arm64-apple-darwin \
  -o "$TMP/kf_bfly4_mx.o" "$ROOT/internal/celt/asmgen/kf_bfly4_mx.c"

go run "$ROOT/internal/celt/asmgen/gen_kf_bfly4.go" \
  "$TMP/kf_bfly4_m1.o" \
  "$TMP/kf_bfly4_mx.o" \
  "$OUT"

rm -rf "$TMP"
