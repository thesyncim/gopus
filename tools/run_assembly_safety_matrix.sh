#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PARITY_RE='^(TestFinalRangeVerification|TestEncoderComplianceSummary|TestDecoderParityLibopusMatrix)$'
ROOT_ASM_RE='^(TestAssemblyValidationContract|TestConvertFloat32ToInt16Unit)$'
CELT_ASM_RE='^(TestAMD64DispatchMatchesGeneric|TestPVQDispatchMatchesGeneric|TestCELTAssemblyWrappersMatchReferenceEdges|TestArm64HotHelpersMatchReference|TestStereoLayoutArm64MatchesGenericExact|TestIMDCTRotateDispatchMatchesReference|TestIMDCTPostRotateF32FromKissMatchesReference|TestKfBfly5N1MatchesReference|TestKfBfly3InnerCOrderMatchesGeneric|TestHaar1SpecializedMatchesGeneric|TestHaar1StrideFastPathsMatchGenericExact|TestScaleFloat64IntoMatchesGeneric|TestSumOfSquaresF64toF32Arm64MatchesLibopusNEONOrder|TestComputeBandRMSUsesArm64LibopusInnerProdOrder|TestDecodePulsesInto32MatchesIntPath)$'
SILK_ASM_RE='^(TestSilkAssemblyKernelsMatchReference|TestSynthesizeLPCOrder16CoreMatchesScalar|TestShortTermPrediction16Asm|TestShortTermPrediction10Asm|TestShortTermPrediction16EdgeCases|TestInnerProductF32|TestInnerProductF32Edge|TestInnerProductF32Lengths|TestInnerProductFLPRandom|TestEnergyF32|TestEnergyF32Edge|TestEnergyF32Lengths)$'
DNN_ASM_RE='^(TestReciprocalEstimate32FiniteAndBounded)$'
ASM_FUZZTIME="${GOPUS_ASM_FUZZTIME:-5s}"

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
      run_lane "amd64 ${goamd64} root assembly contract" \
        env GOWORK=off GOAMD64="${goamd64}" go test . -run "${ROOT_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} celt assembly parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./celt -run "${CELT_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} silk assembly parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./silk -run "${SILK_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} dnnmath assembly parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/dnnmath -run "${DNN_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} purego assembly references" \
        env GOWORK=off GOAMD64="${goamd64}" go test -tags=purego . ./celt ./silk ./internal/dnnmath -run "${ROOT_ASM_RE}|${CELT_ASM_RE}|${SILK_ASM_RE}|${DNN_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} assembly fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./celt -run '^$' -fuzz FuzzCELTAssemblyWrappersMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} silk assembly fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./silk -run '^$' -fuzz FuzzSilkAssemblyKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} dnnmath assembly fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/dnnmath -run '^$' -fuzz FuzzReciprocalEstimate32FiniteAndBounded -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} parity" \
        env GOWORK=off GOAMD64="${goamd64}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
          go test ./testvectors -run "${PARITY_RE}" -count=1
      run_lane "amd64 ${goamd64} purego parity" \
        env GOWORK=off GOAMD64="${goamd64}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
          go test -tags=purego ./testvectors -run "${PARITY_RE}" -count=1
    done
    ;;
  arm64)
    run_lane "arm64 root assembly contract" \
      env GOWORK=off go test . -run "${ROOT_ASM_RE}" -count=1
    run_lane "arm64 celt assembly parity" \
      env GOWORK=off go test ./celt -run "${CELT_ASM_RE}" -count=1
    run_lane "arm64 silk assembly parity" \
      env GOWORK=off go test ./silk -run "${SILK_ASM_RE}" -count=1
    run_lane "arm64 dnnmath assembly parity" \
      env GOWORK=off go test ./internal/dnnmath -run "${DNN_ASM_RE}" -count=1
    run_lane "arm64 purego assembly references" \
      env GOWORK=off go test -tags=purego . ./celt ./silk ./internal/dnnmath -run "${ROOT_ASM_RE}|${CELT_ASM_RE}|${SILK_ASM_RE}|${DNN_ASM_RE}" -count=1
    run_lane "arm64 celt assembly fuzz smoke" \
      env GOWORK=off go test ./celt -run '^$' -fuzz FuzzCELTAssemblyWrappersMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 silk assembly fuzz smoke" \
      env GOWORK=off go test ./silk -run '^$' -fuzz FuzzSilkAssemblyKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 dnnmath assembly fuzz smoke" \
      env GOWORK=off go test ./internal/dnnmath -run '^$' -fuzz FuzzReciprocalEstimate32FiniteAndBounded -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 parity" \
      env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
        go test ./testvectors -run "${PARITY_RE}" -count=1
    run_lane "arm64 purego parity" \
      env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
        go test -tags=purego ./testvectors -run "${PARITY_RE}" -count=1
    ;;
  *)
    echo "assembly safety matrix: unsupported host arch ${host_arch}; nothing to run"
    ;;
esac
