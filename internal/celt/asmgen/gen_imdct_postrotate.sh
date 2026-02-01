#!/bin/sh
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../../.." && pwd)"
OUT="$ROOT/internal/celt/imdct_arm64.s"
TMP="$(mktemp -d)"

clang -O3 -c -target arm64-apple-darwin -o "$TMP/imdct_postrotate_f32.o" "$ROOT/internal/celt/asmgen/imdct_postrotate_f32.c"

go run "$ROOT/internal/celt/asmgen/gen_imdct_postrotate.go" \
  "$TMP/imdct_postrotate_f32.o" \
  "$OUT"

rm -rf "$TMP"
