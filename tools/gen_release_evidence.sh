#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-${ROOT_DIR}/reports/release}"
STAMP="$(date -u +%Y%m%d-%H%M%SZ)"
OUT_FILE="${OUT_DIR}/release-evidence-${STAMP}.md"

mkdir -p "${OUT_DIR}"

run_cmd() {
  local title="$1"
  shift

  {
    echo "## ${title}"
    echo
    echo '```bash'
    printf '%q ' "$@"
    echo
    echo '```'
    echo
    echo '```text'
  } >> "${OUT_FILE}"

  set +e
  "$@" >> "${OUT_FILE}" 2>&1
  local status=$?
  set -e

  echo '```' >> "${OUT_FILE}"
  echo >> "${OUT_FILE}"

  if [[ ${status} -ne 0 ]]; then
    echo "error: command failed for section '${title}' (status=${status})" >&2
    return "${status}"
  fi
}

{
  echo "# Release Evidence"
  echo
  echo "- Generated (UTC): $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
  echo "- Host: $(uname -s) $(uname -m)"
  echo "- Go: $(go version)"
  echo
} > "${OUT_FILE}"

cd "${ROOT_DIR}"

run_cmd "Production Gate" make verify-production
run_cmd "Exhaustive Gate" make verify-production-exhaustive
run_cmd "Hot-Path Benchmarks" go test -run '^$' -bench '^Benchmark(DecoderDecode_CELT|EncoderEncode|DecoderDecodeInt16|EncoderEncodeInt16)$' -benchmem -count=1 .

echo "wrote ${OUT_FILE}"
