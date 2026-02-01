#!/bin/sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="$ROOT/internal/celt/kf_bfly35_arm64.s"
TMP="$(mktemp -d)"

clang -O3 -c -target arm64-apple-darwin -o "$TMP/kf_bfly3_m1.o" "$ROOT/internal/celt/asmgen/kf_bfly3_m1.c"
clang -O3 -c -target arm64-apple-darwin -o "$TMP/kf_bfly5_m1.o" "$ROOT/internal/celt/asmgen/kf_bfly5_m1.c"

go run "$ROOT/internal/celt/asmgen/gen_kf_bfly35.go" \
  "$TMP/kf_bfly3_m1.o" \
  "$TMP/kf_bfly5_m1.o" \
  "$OUT"

rm -rf "$TMP"
