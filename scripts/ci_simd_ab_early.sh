#!/usr/bin/env bash
set -u

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <baseline-checkout> <candidate-checkout> <artifact-dir>" >&2
  exit 2
fi

baseline_root="$(cd "$1" && pwd)"
candidate_root="$(cd "$2" && pwd)"
artifact_root="$3"
mkdir -p "$artifact_root"
artifact_root="$(cd "$artifact_root" && pwd)"
overall_status=0
cc_target="$(cc -dumpmachine 2>/dev/null)"

printf 'runner_os=%s\nrunner_arch=%s\ngo=%s\ngoamd64=%s\ncc=%s\ncc_target=%s\n' \
  "$(uname -s)" \
  "$(uname -m)" \
  "$(go version)" \
  "$(go env GOAMD64)" \
  "$(cc --version | sed -n '1p')" \
  "$cc_target" > "$artifact_root/environment.txt"
if [[ -r /proc/cpuinfo ]]; then
  awk -F ': ' '
    /^model name[[:space:]]*:/ && model == "" { model=$2 }
    /^flags[[:space:]]*:/ && flags == "" { flags=$2 }
    END { if (model != "") print "cpu_model=" model; if (flags != "") print "cpu_flags=" flags }
  ' \
    /proc/cpuinfo >> "$artifact_root/environment.txt"
fi
printf 'baseline_commit=%s\ncandidate_commit=%s\n' \
  "$(git -C "$baseline_root" rev-parse HEAD)" \
  "$(git -C "$candidate_root" rev-parse HEAD)" >> "$artifact_root/environment.txt"

run_phase() {
  local phase="$1"
  shift
  local log="$artifact_root/$phase.log"
  local status="$artifact_root/$phase.exit"
  echo "==> $phase"
  "$@" >"$log" 2>&1
  local rc=$?
  printf '%s\n' "$rc" > "$status"
  if [[ $rc -ne 0 ]]; then
    overall_status=1
    echo "$phase failed with exit=$rc"
  else
    echo "$phase passed"
  fi
  return 0
}

run_json_phase() {
  local phase="$1"
  shift
  local log="$artifact_root/$phase.jsonl"
  local status="$artifact_root/$phase.exit"
  echo "==> $phase"
  "$@" >"$log" 2>&1
  local rc=$?
  printf '%s\n' "$rc" > "$status"
  if [[ $rc -ne 0 ]]; then
    overall_status=1
    echo "$phase failed with exit=$rc"
  else
    echo "$phase passed"
  fi
  return 0
}

run_in_checkout() {
  local root="$1"
  shift
  (cd "$root" && "$@")
}

capture_reference_artifacts() {
  local name src dst
  for name in scalar simd; do
    src="$candidate_root/tmp_check/opus-1.6.1-$name"
    dst="$artifact_root/libopus-$name"
    mkdir -p "$dst"
    for file in .libs/libopus.a config.h .gopus-libopus-build; do
      if [[ ! -s "$src/$file" ]]; then
        echo "missing paired libopus artifact: $src/$file" >&2
        return 1
      fi
      mkdir -p "$dst/$(dirname "$file")"
      cp "$src/$file" "$dst/$file"
    done
    ar t "$src/.libs/libopus.a" > "$dst/archive-members.txt"
    nm -Ao "$src/.libs/libopus.a" > "$dst/archive-symbols.txt"
    objdump -d "$src/.libs/libopus.a" > "$dst/archive-disassembly.txt"
  done
}

capture_xcorr_primitive() {
  local helper
  helper="$(sed -n 's/.*native xcorr primitive helper=//p' "$artifact_root/candidate-simd-xcorr-silk-oracles.log" | head -n 1)"
  case "$helper" in
    */gopus_libopus_test_helpers/gopus_libopus_pitch_xcorr_primitives_*) ;;
    *) echo 'missing native xcorr primitive helper path' >&2; return 1 ;;
  esac
  if [[ ! -x "$helper" ]]; then
    echo "native xcorr primitive helper unavailable: $helper" >&2
    return 1
  fi
  cp "$helper" "$artifact_root/xcorr-primitive-helper"
  sha256sum "$helper" > "$artifact_root/xcorr-primitive-helper.sha256"
  objdump -d "$helper" > "$artifact_root/xcorr-primitive-disassembly.txt"
}

run_phase libopus-reference-build \
  make -C "$candidate_root" ensure-libopus ensure-libopus-scalar ensure-libopus-simd
run_phase capture-paired-libopus-reference-artifacts capture_reference_artifacts

for mode in simd nosimd; do
  if [[ "$mode" == simd ]]; then
    build_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd)
    run_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOPUS_STRICT_LIBOPUS_REF=1 GOPUS_TEST_TIER=parity GOPUS_REQUIRE_NATIVE_AVX2_FMA=1 GOEXPERIMENT=simd)
    build_args=()
  else
    build_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd)
    run_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOPUS_STRICT_LIBOPUS_REF=1 GOPUS_TEST_TIER=parity GOEXPERIMENT=simd)
    build_args=(-tags nosimd)
  fi

  for package in internal/celt internal/silk testvectors; do
    package_name="${package##*/}"
    output="$artifact_root/candidate-$mode-$package_name.test"
    run_phase "build-candidate-$mode-$package_name" \
      run_in_checkout "$candidate_root" \
      "${build_env[@]}" go test "${build_args[@]}" -c -pgo=auto -o "$output" "./$package"
  done

  root_test_binary="$artifact_root/candidate-$mode-root.test"
  run_phase "build-candidate-$mode-root" \
    run_in_checkout "$candidate_root" \
    "${build_env[@]}" go test "${build_args[@]}" -c -pgo=auto -o "$root_test_binary" .

  if [[ "$mode" == simd ]]; then
    run_phase candidate-simd-xcorr-silk-oracles \
      "${run_env[@]}" "$artifact_root/candidate-simd-celt.test" \
      -test.run '^TestPitchXCorrPairedLibopusSIMDRawBits$' -test.count=1 -test.timeout=10m -test.v
    run_phase candidate-simd-xcorr-primitive-artifacts capture_xcorr_primitive
    run_phase candidate-simd-silk-paired-xcorr \
      "${run_env[@]}" "$artifact_root/candidate-simd-silk.test" \
      -test.run '^Test(SilkPitchXCorrPairedLibopusSIMDRawBits|SilkPitchXcorrAVX2TinyFirstLaneEdgeValues|SilkPitchXcorrNativeZeroAlloc)$' \
      -test.count=1 -test.timeout=10m -test.v
  fi

  run_json_phase "candidate-$mode-celt-deemphasis-state-plc" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./internal/celt \
    -run '^(TestApplyDeemphasis.*MatchesLibopus|TestDeemphasisMatchesLibopus|TestDeemphasisSilenceTransitionsAndDownsampleStateMatchLibopus|TestCELTPLCStagesMatchLibopusC|TestCELTPLCFIRMatchesLibopus|TestCELTPLCIIRMatchesLibopus|TestCombFilterConstantBodyHistorySeamMatchesLibopus|TestCombFilterRampedHistorySeamMatchesLibopus|TestCombFilterConstSSEOrderZeroAllocs|TestPitchSearchNearTieMatchesSelectedLibopus|TestExpRotationMatchesLibopusFloatPath|TestPatchTransientHistoryStrideMatchesLibopus|TestPVQProjectionRoundingMatchesLibopus|TestOpPVQSearchFloatHighKNearTieResidual)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-silence-allocation" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^(TestCELTSilenceDecodeMatchesLibopusFloatBits|TestCELTReceivedSilenceHistoryMatchesLibopus|TestHotPathAllocsDecodeSilenceTransitions|TestHotPathAllocsMultistreamDecode|TestMultistreamCallerBuffer.*)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-multistream-encode-budget" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./multistream ./internal/encoder \
    -run '^(TestPaddedStreamMatchesLibopusRepacketizer|TestMultistream(CBRDTXMatchesLibopus|EncodeBudgetMatchesLibopus|EncodeTooSmallPreservesState|SelfDelimitedBudgetFramingWarmZeroAllocs)|TestSurroundTransientHistoryStrideMatchesLibopus|TestSurroundPVQProjectionRoundingMatchesLibopus|TestSurroundLowSpaceFinalRangeMatchesLibopus|TestSurroundLowSpaceThenRealFrameMatchesLibopus|TestLowSpacePacketPreservesInputHighPassState|TestProjectionAnalysisMatchesLibopus|TestInitialStereoToMonoMatchesLibopus|TestProjectionInitialMonoDecisionMatchesLibopus|TestStereoFadeMatchesLibopus|TestStereoWidthComputation|TestHPCutoffMatchesLibopus|TestClampRedundancyBytesAfterSilkMatchesLibopusFormula)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-native-rate-dtx" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^(TestSub48NativeEncodeParity|TestEncodeStatefulDTXRunFuzz|TestStereoFadeTransitionPacketMatchesLibopus|TestLowDelayApplicationControlsMatchLibopus)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-multiframe-fec" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^TestDecodeWithFEC(MultiFrameSILK|SideReset)MatchesLibopus$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-lowdelay-exact" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./testvectors \
    -run '^TestLowDelayCrossModeParity$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-valid-decode-exact" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^TestDecodeDifferentialEncodeThenDecode$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-stateful-mono-transition" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^TestEncodeStatefulTransitionFuzz$/^xfr_auto_ch1_(40|60)ms_(24000|32000|48000|64000)bps_vbr[012]_cx5_fecfalse_dtx(true|false)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-multistream-history-strict-decode" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./multistream \
    -run '^(TestHybridToSILKFadeRequiresDecodedHistoryMatchesLibopus|TestTransitionPLCStageGainMatchesLibopus|TestCELTTransitionPLCStageHasInnerAndOuterGainChecks|TestCELTTransitionFadeReplaysMatchedLibopus|TestTransitionFullSequenceMatchesLibopus|TestTransitionPreviousCELTPLCStageMatchesLibopus|TestSILKToCELTTransitionPLCMatchesLibopus|TestSILKPLCDurationChangesMatchLibopus|TestMultistreamSurroundDecodeDifferentialFuzz|TestMultistreamDiscreteDecodeDifferentialFuzz|TestProjectionDecodeDifferentialFuzz|TestMultistreamGopusEncodedDecodeDifferentialFuzz|TestProjectionDecodeIntoPrefilledBuffer|TestCELTActualRotationPacketsMatchLibopus)$' \
    -count=1 -timeout=25m

  run_phase "candidate-$mode-lpc-ltp-oracles" \
    "${run_env[@]}" "$artifact_root/candidate-$mode-silk.test" \
    -test.run '^Test(SILKCorrelationMatrixVectorMatchesLibopusOracle|SILKAutocorrelationF32MatchesLibopusOracle|SILKBurgModifiedFLPMatchesLibopusOracle|SILKLPCAnalysisFilterFLPMatchesLibopusOracle|SILKInnerProductFLPMatchesLibopusOracle|SILKFindLPCFLPMatchesLibopusOracle|SILKFindLTPFLPMatchesLibopusOracle)$' \
    -test.count=1 -test.timeout=10m -test.v

  # Feature helpers select the same scalar/SIMD reference as the Go build.
  feature_scalar_tag=""
  if [[ "$mode" == nosimd ]]; then feature_scalar_tag=",nosimd"; fi

  run_json_phase "candidate-$mode-custom-mode-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes${feature_scalar_tag}" \
    ./internal/celt/custom -count=1 -timeout=10m

  run_json_phase "candidate-$mode-qext-stateful-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_qext${feature_scalar_tag}" . \
    -run '^Test(QEXT(Stateful|ActiveStereoDecode|ReceivedSilence|Decode96kOracle)|Native96k(Decode|IntegerDecode|MixedInteger)|HD96kPublicFinalRangeMatchesLibopus)' -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-stateful-decode" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point${feature_scalar_tag}" . \
    -run '^Test(DecodeDifferentialFixedPoint|DecoderFixedPoint|DecodeWithFEC|HotPathAllocsDecode|DecodeMalformedVBRPreservesSelectedLibopusState|DecodeMalformedRawCSequenceWitnessPreservesState)' -count=1 -timeout=15m

  run_json_phase "candidate-$mode-fixed-silk-api" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point${feature_scalar_tag}" \
    ./internal/silk -run '^Test(Public.*SILK|PacketEncoderEncodeZeroAlloc)' -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-encode" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point${feature_scalar_tag}" \
    . ./internal/encoder ./internal/celt ./testvectors \
    -run '^(TestPublicFixedShortEncodeMatchesLibopus|TestPublicFixedVoIPShortCELTMatchesLibopus|TestPublicFixedShortExpertFrameDurationMatchesLibopus|TestPublicFixedLongCELTPacketsMatchLibopus|TestPublicFixedInputAPIsShareQ8History|TestPublicFixedVoIPInputAPIsShareQ8History|TestPublicFixedStereoWidthFadeMatchesLibopus|TestPublicFixedSILKHybridInputAPIsMatchLibopus|TestPublicFixedSILKHybridRatesDurationsAndDownmixMatchLibopus|TestPublicFixedSILKHybridModeTransitionsMatchLibopus|TestPublicFixedAutoSILKHybridSequencesMatchLibopus|TestPublicCELTEncodeFixedByteExact|TestPublicCELTEncodeFixedRateByteExact|TestOpusEncodeFixedCELTByteExact|TestOpusEncodeFixedCELTFloatInputSingleFrameByteExact|TestOpusEncodeFixedSILKHybridMatchedFloatInputByteExact|TestFixedPointTonalityAnalysisStagesMatchLibopus|TestEncodeDifferentialFuzzFixedPoint|TestFixedCBRRawTailMatchesLibopus|TestFixedStereoPrefilterThresholdMatchesLibopus|TestFixedOuterOpusEncodeRawInt16MatchesLibopus|TestFixedOuterOpusEncodeRecordsPreserveCalls|TestFixedHPCutoffResMatchesLibopus|TestFixedVoIPHPCutoffResetAndLowSpaceState|TestVoIPHPCutoffResetMatchesFreshEncoder|TestLowSpacePacketPreservesInputHighPassState|TestFixedDCRejectQ8MatchesLibopus|TestAllocationNonpositiveBudgetMatchesFixedLibopus|TestFixedCELTEnergyMaskFloatBoundaryConversion|TestPublicFixedCELTEnergyMaskAndLFEControlsMatchOracle|TestPublicFixedCELTEnergyMaskResetLifetimeMatchesOracle|TestPublicFixedCELTQ24MaskMatchesOracle|TestPublicFixedLFEMatchesOracle|TestPublicFixedShortFrameSILKRequestFallsBackToCELTOracle)$' \
    -count=1 -timeout=25m

  run_json_phase "candidate-$mode-fixed-celt-transition-oracles" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point${feature_scalar_tag}" \
    ./internal/fixedpoint \
    -run '^(TestCELTHybridEncodeWithECSeededOracle|TestCELTResetClearsEnergyMaskOracle|TestAmp2Log2Oracle)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-qext-paired-reference" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    ./internal/libopustest ./internal/fixedpoint \
    -run '^(TestFixedQEXTArchiveAndPublicHelperUsePairedReference|TestCELTEncodeWithECFixedQEXTReferenceArchive)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-qext-encode" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    . ./internal/encoder ./internal/fixedpoint \
    -run '^(TestPublicFixedQEXTPacketsMatchLibopus|TestPublicFixedQEXTConstraintPersistsAcrossCBR|TestPublicFixedQEXTFrameSizeModeAndInputMatrix|TestPublicFixedQEXTWarmEncodeAllocations|TestPublicFixedQEXTHighBudgetWarmEncodeAllocations|TestPublicFixedQEXT96kDurationsMatchLibopus|TestFixedQEXTInputBypassesDCHighpassWithoutAdvancingMemory|TestCELTFixedQEXTMainPayloadMatchesLibopus|TestCELTFixedQEXTReservedMainPayloadMatchesLibopus|TestCELTFixedQEXTNative96KFrameMatchesLibopus|TestCELTFixedQEXTNative96KSidePayloadMatchesLibopus|TestCELTFixedQEXTNative96KShortFrameNoSidePayloadMatchesLibopus|TestCELTFixedQEXTNative96KStatefulResetMatchesLibopus|TestCELTFixedQEXTNative96KEncodeDoesNotAllocateAfterWarmup|TestCELTFixedQEXTExtraAllocationMatchesLibopus)$' \
    -count=1 -timeout=25m

  run_json_phase "candidate-$mode-fixed-qext-analysis" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    ./internal/encoder \
    -run '^(TestFixedPointTonalityAnalysisStagesMatchLibopus|TestFixedCELTEnergyMaskFloatBoundaryConversion|TestPublicFixedCELTEnergyMaskAndLFEControlsMatchOracle|TestPublicFixedCELTEnergyMaskResetLifetimeMatchesOracle|TestPublicFixedCELTQ24MaskMatchesOracle|TestPublicFixedLFEMatchesOracle|TestPublicFixedShortFrameSILKRequestFallsBackToCELTOracle)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-qext-received" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    . ./internal/fixedpoint \
    -run '^(TestPublicFixedQEXTCELTReceivedFramesMatchSelectedReference|TestPublicFixedQEXTCELTMainWithoutExtensionMatchesSelectedReference|TestPublicFixedQEXTSmallBufferDoesNotAdvanceCELTState|TestPublicFixedQEXTLostCELTFrameMatchesSelectedReference|TestPublicFixedQEXTLostCELTBurstMatchesSelectedReference|TestPublicFixedQEXTStructuralMalformedPacketPreservesCELTState|TestPublicFixedQEXTHybridReceivedFramesMatchSelectedReference|TestPublicFixedQEXTCELTToHybridTransitionMatchesSelectedReference|TestPublicFixedQEXTHybridToCELTTransitionMatchesSelectedReference|TestPublicFixedQEXTHybridLostFrameMatchesSelectedReference|TestPublicFixedQEXTHybridLowerRateAndDownmixMatchesSelectedReference|TestPublicFixedQEXTHybridFECMatchesSelectedReference|TestPublicFixedQEXTHybridRedundancyDirectionsMatchSelectedReference|TestNative96kDecodeMatchesQEXTOracle(Mono|Stereo)|TestQEXTDecode96kOracleProducesNative96k|TestNative96kDecodeCrossFramePostfilterParity|TestNative96kIntegerDecodeFormatsMatchQEXTOracle|TestNative96kIntegerDecodeSmallBufferPreservesState|TestNative96kIntegerDecodeGainMatchesQEXTOracle|TestNative96kMixedIntegerFormatsMatchQEXTOracle|TestAlgQuantQEXTMatchesFixedLibopus|TestAlgUnquantQEXTMatchesSelectedLibopus|TestQuantAllBandsDecodeOracle|TestQuantAllBandsDecodeQEXTMatchesSelectedLibopus|TestQuantPartitionQEXTUsesZeroResolutionCubicLeaf)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-surround" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    ./internal/encoder ./internal/fixedpoint ./multistream \
    -run '^(TestPublicFixedAutoCELTSequenceMatchesLibopus|TestCombFilterQEXTPFMatchesLibopus|TestFixedPointSurroundEncodeMatchesLibopus|TestFixedPointSurroundAnalysisMatchesLibopus|TestFixedPointSurroundMaskRoutingMatchesAnalyzer|TestFixedPointFloatSurroundEncodeMatchesLibopus|TestFixedQEXTMonoSurroundCELTResetMatchesLibopus|TestMultistreamEncodeDecodeAllocGuard)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-qext-transform" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    ./internal/fixedpoint \
    -run '^(TestQEXTMDCTForwardMatchesFixedLibopus|TestQEXTMDCT96000ForwardMatchesFixedLibopus|TestQEXTMDCTBackwardMatchesSelectedLibopus|TestQEXTMDCTSilenceHeadroomMatchesFixedLibopus|TestQEXTKissFFTMatchesFixedLibopus|TestQEXTMDCTForwardDoesNotAllocateAfterWarmup|TestQEXTKissFFTDoesNotAllocate|TestAmp2Log2Oracle|TestCELTExp2DBFixedQEXTMatchesSelectedLibopus|TestDenormaliseBandsOracle|TestAntiCollapseMatchesLibopusFixed)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-neural-kernel-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" \
    ./internal/dnnmath ./internal/dred/rdovae \
    -run '^Test(DNNVectorActivationsMatchSelectedLibopusOracle|RDOVAECGEMV8x4MatchesSelectedLibopusOracle|RDOVAESGEMVMatchesSelectedLibopusOracle|RDOVAESparseFloatLinearMatchesSelectedLibopusOracle|RDOVAEIntegerLinearBiasMatchesSelectedLibopusOracle|RDOVAEIntegerInputQuantizerMatchesSelectedLibopusOracle)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-neural-analysis-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_osce${feature_scalar_tag}" \
    ./internal/lpcnetplc -count=1 -timeout=10m

  run_json_phase "candidate-$mode-osce-exact-pcm" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_osce${feature_scalar_tag}" \
    . ./internal/osce/... ./multistream \
    -run '^Test(OSCE(EndToEndSampleParity|BWE(RawSignalNet|ForwardPass|CrossFade))|LACEAndNoLACEFeatureStateMatchesLibopusRawBits|MultistreamDecoderOSCE|StreamOSCE)' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-initial-latents" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_dred${feature_scalar_tag}" \
    ./internal/encoder -run '^TestEncoderDREDInitialLatentsTraceMatchesLibopus$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-stateful-concealment" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" GOPUS_DRED_AUDIO_QUALITY=1 go test -json \
    -tags "gopus_dred${feature_scalar_tag}" . ./internal/lpcnetplc \
    -run '^Test(DREDLowDelayReferenceOffsetAgainstLibopus|DREDLowDelayFullSequenceEncoderMatchesLibopus|DREDLongLossPCMMatchesLibopusRawBits|DREDLongSequenceAllDecodedPCMMatchesLibopusRawBits|DecoderCELTNeuralPLCAPIRatesMatchesLibopusRawBits|DREDBurgSelectedCFirstLossRawBits|DREDPredictorSelectedCFirstLossRawBits|ExplicitDRED.*Quality.*SixtyPercentLoss)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-qext-reference-packet-contract" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_dred,gopus_qext${feature_scalar_tag}" \
    . ./internal/encoder ./internal/libopustest ./internal/libopustooling ./multistream \
    -run '^(TestCombinedDREDQEXTBuildOptionalExtensionContract|TestCombinedDREDQEXTBuildPublicAPIContract|TestDREDQEXTFloatSurroundMasksMatchSelectedLibopus|TestMaybeBuildSingleFrameDREDPacketCarriesQEXTAndDRED|TestEncodeCELTDREDQEXTPacketCarriesBothExtensions|TestMaybeBuildLongCELTDREDQEXTPacketCarriesBothExtensions|TestResolveLibopusDREDQEXTReferenceMatchesGoISA|TestHelperRefDirSelectsDREDQEXTTree|TestCHelperReferenceSelectionRejectsConflictingVariants|TestDREDQEXTReferenceVariantPairsScalarAndSIMD|TestValidateDREDQEXTReferenceBuildRequiresCombinedFlagsAndPairedISA|TestExistingReferenceBuildsRejectDREDAndDeepPLCFeatures|TestDREDQEXTBuildEnvironmentClearsConflictingFeatureFlags)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-qext-multistream-parity-allocation" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_dred,gopus_qext${feature_scalar_tag}" \
    ./multistream \
    -run '^TestDREDQEXTSurroundAndProjectionEncodeMatchesLibopus$' \
    -count=1 -timeout=25m

  run_phase "candidate-$mode-strict-cbr" \
    "${run_env[@]}" "$artifact_root/candidate-$mode-testvectors.test" \
    -test.run '^TestEncoderCBRPairedOracleExact$' -test.count=1 -test.timeout=25m -test.v
done

run_phase build-baseline-test-binary \
  run_in_checkout "$baseline_root" env -u GOEXPERIMENT -u GOPUS_LIBOPUS_REF_SCALAR \
  go test -c -pgo=auto -o "$artifact_root/baseline-default-root.test" .

run_profile() {
  local side="$1" binary="$2" profile="$3" workload="$4" benchmark="$5" checkout
  if [[ "$side" == baseline ]]; then checkout="$baseline_root"; else checkout="$candidate_root"; fi
  run_phase "$side-$workload-cpu-profile" \
    run_in_checkout "$checkout" env "$binary" \
      -test.run '^$' \
      -test.bench "$benchmark" \
      -test.benchtime=3s -test.count=1 -test.cpu=1 -test.benchmem \
      -test.cpuprofile="$profile"
  if [[ ! -s "$profile" ]]; then
    printf 'missing CPU profile: %s\n' "$profile" >> "$artifact_root/$side-$workload-cpu-profile.log"
    overall_status=1
  fi
}

if [[ -x "$artifact_root/baseline-default-root.test" && -x "$artifact_root/candidate-simd-root.test" && -x "$artifact_root/candidate-nosimd-root.test" ]]; then
  run_profile baseline "$artifact_root/baseline-default-root.test" "$artifact_root/baseline-callerbuffer.cpu" callerbuffer '^BenchmarkEncoderEncode_CallerBuffer$'
  run_profile candidate-simd "$artifact_root/candidate-simd-root.test" "$artifact_root/candidate-simd-callerbuffer.cpu" callerbuffer '^BenchmarkEncoderEncode_CallerBuffer$'
  run_profile baseline "$artifact_root/baseline-default-root.test" "$artifact_root/baseline-hybrid-decode.cpu" hybrid-decode '^BenchmarkDecoderDecode_Hybrid$'
  run_profile candidate-simd "$artifact_root/candidate-simd-root.test" "$artifact_root/candidate-simd-hybrid-decode.cpu" hybrid-decode '^BenchmarkDecoderDecode_Hybrid$'

  for sample in 1 2 3 4; do
    if (( sample % 2 == 1 )); then sides=(baseline candidate-simd); else sides=(candidate-simd baseline); fi
    for side in "${sides[@]}"; do
      if [[ "$side" == baseline ]]; then
        binary="$artifact_root/baseline-default-root.test"
        checkout="$baseline_root"
      else
        binary="$artifact_root/candidate-simd-root.test"
        checkout="$candidate_root"
      fi
      run_phase "interleaved-callerbuffer-$side-$sample" \
        run_in_checkout "$checkout" env "$binary" \
          -test.run '^$' \
          -test.bench '^BenchmarkEncoderEncode_CallerBuffer$' \
          -test.benchtime=500ms -test.count=1 -test.cpu=1 -test.benchmem
      if ! grep -Eq '[[:space:]]0 B/op[[:space:]]+0 allocs/op[[:space:]]*$' "$artifact_root/interleaved-callerbuffer-$side-$sample.log"; then
        printf 'expected a zero-allocation caller-buffer benchmark row\n' >> "$artifact_root/interleaved-callerbuffer-$side-$sample.log"
        printf '1\n' > "$artifact_root/interleaved-callerbuffer-$side-$sample.exit"
        overall_status=1
      fi
    done
  done

  e2e_bench_pattern='^Benchmark(DecoderDecode_(CELT|Hybrid|SILK)|EncoderEncode_(CallerBuffer|VoIP|LowDelay))$'
  e2e_row_pattern='^Benchmark(DecoderDecode_(CELT|Hybrid|SILK)|EncoderEncode_(CallerBuffer|VoIP|LowDelay))(-[0-9]+)?[[:space:]]'
  e2e_benchmarks=(
    BenchmarkDecoderDecode_CELT
    BenchmarkDecoderDecode_Hybrid
    BenchmarkDecoderDecode_SILK
    BenchmarkEncoderEncode_CallerBuffer
    BenchmarkEncoderEncode_VoIP
    BenchmarkEncoderEncode_LowDelay
  )
  for sample in 1 2 3 4; do
    case $((sample % 3)) in
      1) sides=(baseline candidate-simd candidate-nosimd) ;;
      2) sides=(candidate-simd candidate-nosimd baseline) ;;
      0) sides=(candidate-nosimd baseline candidate-simd) ;;
    esac
    for side in "${sides[@]}"; do
      case "$side" in
        baseline)
          binary="$artifact_root/baseline-default-root.test"
          checkout="$baseline_root"
          ;;
        candidate-simd)
          binary="$artifact_root/candidate-simd-root.test"
          checkout="$candidate_root"
          ;;
        candidate-nosimd)
          binary="$artifact_root/candidate-nosimd-root.test"
          checkout="$candidate_root"
          ;;
      esac
      phase="interleaved-e2e-$side-$sample"
      run_phase "$phase" \
        run_in_checkout "$checkout" env "$binary" \
          -test.run '^$' \
          -test.bench "$e2e_bench_pattern" \
          -test.benchtime=500ms -test.count=1 -test.cpu=1 -test.benchmem
      rows="$(grep -E "$e2e_row_pattern" "$artifact_root/$phase.log" || true)"
      row_count="$(printf '%s\n' "$rows" | wc -l | tr -d '[:space:]')"
      names_valid=1
      for benchmark in "${e2e_benchmarks[@]}"; do
        count="$(grep -Ec "^${benchmark}(-[0-9]+)?[[:space:]]" "$artifact_root/$phase.log" || true)"
        if [[ "$count" -ne 1 ]]; then names_valid=0; fi
      done
      if [[ "$row_count" -ne 6 || "$names_valid" -ne 1 ]] || printf '%s\n' "$rows" | grep -Ev '[[:space:]]0 B/op[[:space:]]+0 allocs/op[[:space:]]*$' >/dev/null; then
        printf 'expected six zero-allocation E2E benchmark rows; found %s\n' "$row_count" >> "$artifact_root/$phase.log"
        printf '1\n' > "$artifact_root/$phase.exit"
        overall_status=1
      fi
    done
  done
else
  overall_status=1
  printf 'baseline, SIMD, or nosimd E2E binaries unavailable after an earlier build failure\n' > "$artifact_root/profile-bench-skipped.txt"
fi

printf '%s\n' "$overall_status" > "$artifact_root/early.exit"
if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
  {
    echo '### Early native AMD64 paired-reference evidence'
    echo
    cat "$artifact_root/environment.txt"
    echo
    echo '| Phase | Exit |'
    echo '| --- | ---: |'
    for status in "$artifact_root"/*.exit; do
      [[ "$(basename "$status")" == early.exit ]] && continue
      printf '| %s | %s |\n' "$(basename "$status" .exit)" "$(cat "$status")"
    done
    echo
    echo "early gate exit=$overall_status"
  } >> "$GITHUB_STEP_SUMMARY"
fi

exit "$overall_status"
