#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PARITY_RE='^(TestSILKParamTraceAgainstLibopus|TestEncoderComplianceSummary|TestDecoderParityLibopusMatrix)$'

cd "${ROOT_DIR}"

run_lane() {
  local arch_label="$1"
  shift

  echo "== assembly safety lane: ${arch_label} =="
  "$@"
}

host_arch="$(GOWORK=off go env GOARCH)"

case "${host_arch}" in
  amd64)
    for goamd64 in v1 v3; do
      run_lane "amd64 ${goamd64}" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./celt -run '^(TestAMD64DispatchMatchesGeneric|TestPVQDispatchMatchesGeneric)$' -count=1
      run_lane "amd64 ${goamd64} parity" \
        env GOWORK=off GOAMD64="${goamd64}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
          go test ./testvectors -run "${PARITY_RE}" -count=1
    done
    ;;
  arm64)
    run_lane "arm64 helpers" \
      env GOWORK=off go test ./celt -run '^(TestArm64HotHelpersMatchReference|TestStereoLayoutArm64MatchesGenericExact)$' -count=1
    run_lane "arm64 parity" \
      env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
        go test ./testvectors -run "${PARITY_RE}" -count=1
    ;;
  *)
    echo "assembly safety matrix: unsupported host arch ${host_arch}; nothing to run"
    ;;
esac
