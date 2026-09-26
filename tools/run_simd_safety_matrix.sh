#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PARITY_RE='^(TestFinalRangeVerification|TestDecoderParityLibopusMatrix|TestEncoderCompliancePacketsFixtureCoverage|TestEncoderVariantsFixtureCoverage|TestEncoderVariantsFixtureSignalHash)$'
ROOT_KERNEL_RE='^(TestKernelTreeContainsNoAssembly|TestConvertFloat32ToInt16Unit)$'
CELT_KERNEL_RE='^(TestAMD64DispatchMatchesGeneric|TestPVQDispatchMatchesGeneric|TestCELTKernelsMatchReferenceEdges|TestArm64HotHelpersMatchReference|TestStereoLayoutArm64MatchesGenericExact|TestIMDCTRotateDispatchMatchesReference|TestIMDCTPostRotateF32FromKissMatchesReference|TestKfBfly5N1MatchesReference|TestKfBfly3InnerCOrderMatchesGeneric|TestHaar1SpecializedMatchesGeneric|TestHaar1StrideFastPathsMatchGenericExact|TestScaleFloat64IntoMatchesGeneric|TestSumOfSquaresF64toF32Arm64MatchesLibopusNEONOrder|TestComputeBandRMSUsesArm64LibopusInnerProdOrder|TestDecodePulsesInto32MatchesIntPath)$'
SILK_KERNEL_RE='^(TestSilkKernelsMatchReference|TestSilkPitchXcorrAVX2KernelMatchesReference|TestSynthesizeLPCOrder16CoreMatchesScalar|TestShortTermPrediction16Asm|TestShortTermPrediction10Asm|TestShortTermPrediction16EdgeCases|TestInnerProductF32|TestInnerProductF32Edge|TestInnerProductF32Lengths|TestInnerProductFLPRandom|TestInnerProductFLPAVX2MatchesReference|TestInnerProductFLPArm64MatchesReference|TestEnergyF32|TestEnergyF32Edge|TestEnergyF32Lengths)$'
DNN_ASM_RE='^(TestReciprocalEstimate32FiniteAndBounded)$'
ASM_FUZZTIME="${GOPUS_ASM_FUZZTIME:-5000x}"

cd "${ROOT_DIR}"

run_lane() {
  local arch_label="$1"
  shift

  echo "== kernel safety lane: ${arch_label} =="
  "$@"
}

host_arch="$(GOWORK=off go env GOARCH)"

case "${host_arch}" in
  amd64)
    for goamd64 in v1 v3; do
      run_lane "amd64 ${goamd64} source contract" \
        env GOWORK=off GOAMD64="${goamd64}" go test . -run "${ROOT_KERNEL_RE}" -count=1
      run_lane "amd64 ${goamd64} celt kernel parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/celt -run "${CELT_KERNEL_RE}" -count=1
      run_lane "amd64 ${goamd64} silk kernel parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/silk -run "${SILK_KERNEL_RE}" -count=1
      run_lane "amd64 ${goamd64} dnnmath kernel parity" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/dnnmath -run "${DNN_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} nosimd kernel references" \
        env GOWORK=off GOAMD64="${goamd64}" go test -tags=nosimd . ./internal/celt ./internal/silk ./internal/dnnmath -run "${ROOT_KERNEL_RE}|${CELT_KERNEL_RE}|${SILK_KERNEL_RE}|${DNN_ASM_RE}" -count=1
      run_lane "amd64 ${goamd64} celt kernel fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/celt -run '^$' -fuzz FuzzCELTKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} silk kernel fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/silk -run '^$' -fuzz FuzzSilkKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} dnnmath kernel fuzz smoke" \
        env GOWORK=off GOAMD64="${goamd64}" go test ./internal/dnnmath -run '^$' -fuzz FuzzReciprocalEstimate32FiniteAndBounded -fuzztime "${ASM_FUZZTIME}" -count=1
      run_lane "amd64 ${goamd64} parity" \
        env GOWORK=off GOAMD64="${goamd64}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
          go test ./testvectors -run "${PARITY_RE}" -count=1
      run_lane "amd64 ${goamd64} nosimd parity" \
        env GOWORK=off GOAMD64="${goamd64}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
          go test -tags=nosimd ./testvectors -run "${PARITY_RE}" -count=1
    done
    ;;
  arm64)
    run_lane "arm64 assembly vet" \
      env GOWORK=off go vet . ./internal/celt ./internal/silk ./internal/dnnmath
    run_lane "linux/arm64 assembly vet" \
      env GOWORK=off GOOS=linux GOARCH=arm64 go vet . ./internal/celt ./internal/silk ./internal/dnnmath
    run_lane "arm64 opt-in tone LPC assembly vet" \
      env GOWORK=off go vet -tags=gopus_neon_tone_lpc_corr ./internal/celt
    run_lane "linux/arm64 opt-in tone LPC assembly vet" \
      env GOWORK=off GOOS=linux GOARCH=arm64 go vet -tags=gopus_neon_tone_lpc_corr ./internal/celt
    run_lane "arm64 source contract" \
      env GOWORK=off go test . -run "${ROOT_KERNEL_RE}" -count=1
    run_lane "arm64 celt kernel parity" \
      env GOWORK=off go test ./internal/celt -run "${CELT_KERNEL_RE}" -count=1
    run_lane "arm64 silk kernel parity" \
      env GOWORK=off go test ./internal/silk -run "${SILK_KERNEL_RE}" -count=1
    run_lane "arm64 dnnmath kernel parity" \
      env GOWORK=off go test ./internal/dnnmath -run "${DNN_ASM_RE}" -count=1
    run_lane "arm64 nosimd kernel references" \
      env GOWORK=off go test -tags=nosimd . ./internal/celt ./internal/silk ./internal/dnnmath -run "${ROOT_KERNEL_RE}|${CELT_KERNEL_RE}|${SILK_KERNEL_RE}|${DNN_ASM_RE}" -count=1
    run_lane "arm64 celt kernel fuzz smoke" \
      env GOWORK=off go test ./internal/celt -run '^$' -fuzz FuzzCELTKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 silk kernel fuzz smoke" \
      env GOWORK=off go test ./internal/silk -run '^$' -fuzz FuzzSilkKernelsMatchReference -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 dnnmath kernel fuzz smoke" \
      env GOWORK=off go test ./internal/dnnmath -run '^$' -fuzz FuzzReciprocalEstimate32FiniteAndBounded -fuzztime "${ASM_FUZZTIME}" -count=1
    run_lane "arm64 parity" \
      env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
        go test ./testvectors -run "${PARITY_RE}" -count=1
    run_lane "arm64 nosimd parity" \
      env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
        go test -tags=nosimd ./testvectors -run "${PARITY_RE}" -count=1
    ;;
  *)
    echo "kernel safety matrix: unsupported host arch ${host_arch}; nothing to run"
    ;;
esac
