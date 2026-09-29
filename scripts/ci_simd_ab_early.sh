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
# These exact kernel and public-output checks share the existing four feature batches.
exact_audit_selector='^Test(PublicEncodeAPIErrorFinalRangeAndRecoveryMatchesLibopus|DecodeMalformedFramingPrecedesSmallOutput(96k)?|Native96kEncoderFrameSizeBoundariesMatchLibopus|MultistreamEncodeInt2496kLongFramesMatchesSelectedLibopus|MultistreamForceChannelsPartialFailureMatchesLibopus|MultistreamEncoderSetApplicationAfterLowSpaceMatchesLibopus|(Encoder|Decoder)CTLSequenceFuzz|PublicDNNReferenceIdentityUsesBuilderStampContract|DNNHelperIncludesPinnedSourceRootAfterBuildConfig|SinF32MatchesSamePlatformCLibm|DecoderPitchAfterSILKRateResetMatchesSelectedLibopus|SILKCNGRateChangeRetainsExcitationMatchesLibopus|SILKCNGRateChangeWarmZeroAllocs|AlgUnquantQEXTRefinedEnergy(PublicBoundary|Tail)MatchesSelectedLibopus|DecodeWithFECRobustnessMalformed|DecodeWithFECRateSwitchRecoveryAndLossMatchesSelectedLibopus|DecodeWithFECMonoToStereoTransitionMatchesSelectedLibopus|DecodeWithFECStereoToMonoTransitionMatchesSelectedLibopus|DecodeWithFECMonoToStereoLongFrameMatchesSelectedLibopus|RootNative96kModeBudgetSequenceMatchesSelectedLibopus|MultistreamNativeHD96kBudgetSequenceMatchesSelectedLibopus|CoarseEnergyVariableBudgetAllocs|CELTCoarseEnergyVariableBudgetAllocs|DecodeWithFECSILKPLCResetsOnRateChange.*|CELTChannelRecoveryMatchesSelectedLibopus|MultistreamCELTChannelRecoveryMatchesSelectedLibopus|QEXTAfterEmptyRepeatMatchesSelectedLibopus|MultistreamNativeHD96kEncodeMatchesSelectedLibopus|QEXTMultistreamDecoderNative96kMatchesLibopus|MultistreamLongPLCBurstMatchesSelectedLibopus|MultistreamMalformedHybridTransitionMatchesSelectedLibopus|HybridQEXTPayloadMatchesSelectedLibopus|HybridQEXTDecodeIntoWarmZeroAllocs|FixedHybridQEXTPayloadMatchesSelectedLibopus|FixedHybridQEXTPayloadWarmZeroAllocs|QEXTNonFullbandHeaderMatchesSelectedLibopus|QEXTDiscardedBandsMatchSelectedLibopus|QEXTDiscardedBandSynthesisStagesMatchSelectedLibopus|IntegerFormatSoftClipLifecycleMatchesSelectedLibopus|HybridMalformedMainLengthMatchesSelectedLibopus|ProjectionRobustOracleErrorClassification|ProjectionDecodeRobustnessMalformed|HybridStereoFloatSameArchParity|MultistreamPerStreamModeTransitionMatchesLibopus|Libopus_MSRecovery_.*|DecoderHybridToCELT(10|20)msTransitionParity|MultistreamDecodeFixedPointParity|MSRobustOracleErrorClassification|DecodeMultistreamMalformedSILKRedundancyParity|DecodeMultistreamRobustnessMalformed|FixedSILKMultiframeRedundancyMatchesSelectedLibopus|FixedSILKRedundancyDecodeWarmZeroAllocs|MultistreamSILKRedundancyFinalRangeMatchesSelectedLibopus|MultistreamHybridRedundancyFinalRangeMatchesSelectedLibopus|ProjectionDecodePCMAndFinalRangeMatchesSelectedLibopus|MultistreamSoftClipLifecycleMatchesSelectedLibopus|MultistreamConstructorsValidateSampleRate|ProjectionDecoderValidatesChannelsBeforeAllocation|ProjectionRectangularDecodeMatchesSelectedLibopus|AlgUnquantQEXTN2MatchesSelectedLibopus|CELTDecoderAPIRate(ToFloat32|PLC)MatchesLibopus|MultistreamDecodeFloat32MatchesLibopus|MultistreamDecodeRequestedPLCDurationMatchesLibopus|MultistreamDecodeOverlongAndEmptyPLCMatchesLibopus|MultistreamDecodeInt16HighGainMatchesLibopus|MSDecoderCTL_Gain(Broadcast|AudioMatchesLibopus|AudioMatchesLibopusSILK)|DecodeFECNoPacketLossChannelRoutingMatchesLibopus|EncodeDecodeLongStreamSoak|EncodeDiffSILKCBRFloorFinding|EncoderCELTSameArchByteExact|QEXTCubic(Decode|Encode)MatchesLibopus|QEXTCubicReductionBoundaryGrid|QEXTMonoMultiFrameSynthesisStagesMatchSelectedLibopus|DecoderQEXT.*|EncoderAutoModeCrossProductParity|SurroundInt16PacketRangeMatchesLibopus|DecodeWithFECHybridToSILK(MatchesLibopus|WarmZeroAllocs)|ThetaRDODistortionMatchesLibopusFloatPath|DecodeFrameWithPacketStereoToFloat32MatchesDecodeFrame|StereoMergeVsLibopus|PitchDownsample(Sig|FloatInput)MatchesLibopus|RemoveDoublingMatchesLibopus|Haar1(MatchesLibopus|NormMatchesLibopus|SpecializedMatchesGeneric|StrideFastPathsMatchGenericExact)|QuantPartitionZeroPulseMatchesLibopus|CELTPLCSeedSynthesisStagesMatchLibopusC|RenormalizeVectorMatchesLibopusFloatPath|Alg(Quant|Unquant)MatchesLibopusFloatPath|StereoIthetaMatchesLibopusFloatPath|OPPVQSearchMatchesLibopusFloatPath)$'
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

capture_dnn_primitive() {
  local helper
  helper="$(python3 - "$artifact_root/candidate-simd-osce-exact-pcm.jsonl" <<'PYDNN'
import json
import sys
with open(sys.argv[1]) as source:
    for line in source:
        event = json.loads(line)
        output = event.get("Output", "")
        marker = "native DNN primitive helper="
        if marker in output:
            print(output.split(marker, 1)[1].strip())
            break
PYDNN
)"
  case "$helper" in
    "$candidate_root"/tmp_check/build-opus-*/gopus_libopus_dnn_kernel_*|*/gopus_libopus_test_helpers/gopus_libopus_dnn_kernel_*) ;;
    *) echo 'missing native DNN primitive helper path' >&2; return 1 ;;
  esac
  if [[ ! -x "$helper" ]]; then
    echo "native DNN primitive helper unavailable: $helper" >&2
    return 1
  fi
  cp "$helper" "$artifact_root/dnn-primitive-helper"
  sha256sum "$helper" > "$artifact_root/dnn-primitive-helper.sha256"
  objdump -d "$helper" > "$artifact_root/dnn-primitive-disassembly.txt"
}

run_phase libopus-reference-build \
  make -C "$candidate_root" ensure-libopus ensure-libopus-scalar ensure-libopus-simd
run_phase capture-paired-libopus-reference-artifacts capture_reference_artifacts

for mode in simd nosimd; do
  if [[ "$mode" == simd ]]; then
    build_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd)
    run_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOPUS_STRICT_LIBOPUS_REF=1 GOPUS_TEST_TIER=parity GOPUS_REQUIRE_NATIVE_AVX2_FMA=1 GOEXPERIMENT=simd)
    build_args=()
    oracle_build_args=(-tags gopus_libopus_oracle)
  else
    build_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd)
    run_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOPUS_STRICT_LIBOPUS_REF=1 GOPUS_TEST_TIER=parity GOEXPERIMENT=simd)
    build_args=(-tags nosimd)
    oracle_build_args=(-tags nosimd,gopus_libopus_oracle)
  fi

  feature_scalar_tag=""
  if [[ "$mode" == nosimd ]]; then feature_scalar_tag=",nosimd"; fi

  # Run active OSCE witnesses before the broader matrices, covering every
  # supported QEXT/DRED combination in this existing job.
  for osce_features in gopus_osce gopus_osce,gopus_qext gopus_dred,gopus_osce gopus_dred,gopus_osce,gopus_qext; do
    osce_phase="osce-exact-pcm"
    osce_extra_selector=""
    if [[ "$osce_features" == gopus_osce,gopus_qext ]]; then osce_phase="osce-qext-exact-pcm"; fi
    if [[ "$osce_features" == gopus_dred,gopus_osce ]]; then osce_phase="dred-osce-exact-pcm"; fi
    if [[ "$osce_features" == gopus_dred,gopus_osce,gopus_qext ]]; then
      osce_phase="dred-osce-qext-exact-pcm"
      osce_extra_selector="|DecoderExplicitDREDWarmup48kStateMatchesLibopus|DecoderDREDTripleReferenceArchiveMatchesGoFeatures"
    fi
    run_json_phase "candidate-$mode-$osce_phase" \
      run_in_checkout "$candidate_root" \
      "${run_env[@]}" GOPUS_TRACE_OSCE_LACE=1 go test -json -tags "${osce_features}${feature_scalar_tag}" \
      . ./internal/osce/... ./multistream ./internal/libopustest ./internal/celt ./internal/dnnmath ./internal/silk \
      -run "^Test(OSCEAutomaticLossRecoveryMatchesSelectedLibopus|DecoderOSCELACECrossFadeTransition|DecoderOSCELACEHybridTransitionMatchesSelectedLibopus|StreamOSCECELTMarkPreservesSILKState|OSCEModelReloadPreservesActiveStateMatchesLibopus|OSCEFECMissingPrefixThenLBRRUsesResetChannelState|OSCEFECTinyLBRRUsesMainModelLossPath|OSCEFECFallbackPreservesClassicalLossHistory|DecoderOSCELACEGetterReportsEffectiveComplexityGate|StreamOSCELACEComplexityEnablePrecedence|MultistreamOSCEComplexityLifecycleMatchesSelectedLibopus|MultistreamOSCEExplicitLACEOverridePrecedence|DREDHistory.*|DecoderDecodePLCAppliesNeuralConcealmentWhenReady|DecoderDecodeNilConsumesMultistreamDREDNeural.*|MSDREDDormancy_.*|MSPerStreamDREDQueue.*|DecodeDREDCarrierHistoryCodecStateMatchesLibopus|DecoderDREDFECPLCUpdatesMatchLibopusHistory|DecoderNoSidecarDeepPLC.*|MultistreamMainModelNeuralPLC.*|DecoderStateSnapshotPreservesIntegerLowBits|OSCE(EndToEndSampleParity|LACEForward|NoLACEForward|BWE(RawSignalNet|ForwardPass|CrossFade))|BWE(FeatureFFTMatchesLibopus|VariableSequenceStateMatchesSelectedLibopus|ProcessDoesNotAllocateAfterWarmup)|FNetConv1UsesSelectedDNNLinearKernel|LACEAndNoLACEFeatureStateMatchesLibopusRawBits|X86SGEMVScalarRemainderMatchesSelectedLibopus|MultistreamDecoderOSCE|StreamOSCE|MultistreamReferenceFeaturePairing|CurrentPublicAPIHelperConfig|PublicDNNReferenceIdentityUsesBuilderStampContract|DNNHelperIncludesPinnedSourceRootAfterBuildConfig|DNNFeatureBuild|DNNCompilerTarget|DNNVectorActivationsMatchSelectedLibopusOracle|DNNVectorActivationSweepMatchesSelectedLibopusOracle|CGEMV8x4MatchesSelectedLibopus|ComputeLinearInt8MatchesSelectedLibopus|AntiCollapseVsLibopus${osce_extra_selector})" \
      -count=1 -timeout=10m
  done

  for package in internal/celt internal/silk; do
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
    run_phase build-candidate-simd-encoder \
      run_in_checkout "$candidate_root" \
      "${build_env[@]}" go test -c -pgo=auto -o "$artifact_root/candidate-simd-encoder.test" ./internal/encoder
    run_phase build-candidate-simd-dnnmath \
      run_in_checkout "$candidate_root" \
      "${build_env[@]}" go test -c -pgo=auto -o "$artifact_root/candidate-simd-dnnmath.test" ./internal/dnnmath
    # Native AVX2 hosts cannot detect an illegal AVX2 instruction on the AVX
    # fallback path. CPU emulation exercises those paths without a C helper,
    # whose subprocess would otherwise run against the host CPU features.
    run_phase candidate-cpu-emulator-version qemu-x86_64 --version
    for cpu in Penryn SandyBridge; do
      run_phase "candidate-simd-$cpu-analysis" \
        env GOPUS_TEST_TIER=fast GOMAXPROCS=2 \
        qemu-x86_64 -cpu "$cpu" "$artifact_root/candidate-simd-encoder.test" \
        -test.run '^TestAnalysis(Bins(CPUFallback|MatchesScalar|ZeroAllocs)|Atan2MatchesBranchyForm)$' \
        -test.count=1 -test.timeout=2m -test.v
      run_phase "candidate-simd-$cpu-dnn-dispatch" \
        env GOPUS_TEST_TIER=fast GOMAXPROCS=2 \
        qemu-x86_64 -cpu "$cpu" "$artifact_root/candidate-simd-dnnmath.test" \
        -test.run '^TestDNNX86CPUFallback$' -test.count=1 -test.timeout=2m -test.v
      run_phase "candidate-simd-$cpu-silk-dispatch" \
        env GOPUS_TEST_TIER=fast GOMAXPROCS=2 \
        qemu-x86_64 -cpu "$cpu" "$artifact_root/candidate-simd-silk.test" \
        -test.run '^TestSILKAMD64SIMDRequiresAVX2$' \
        -test.count=1 -test.timeout=2m -test.v
      run_phase "candidate-simd-$cpu-celt-dispatch" \
        env GOPUS_TEST_TIER=fast GOMAXPROCS=2 \
        qemu-x86_64 -cpu "$cpu" "$artifact_root/candidate-simd-celt.test" \
        -test.run '^TestCELT(CPUFeatureFallbackMath|AVXSafeFloatAbsNeg|AVX2XCorrFallbackMath|RawMaxMinInitialNaNMatchesSequential)$' \
        -test.count=1 -test.timeout=2m -test.v
      run_phase "candidate-simd-$cpu-public-smoke" \
        env GOPUS_TEST_TIER=fast GOMAXPROCS=2 \
        qemu-x86_64 -cpu "$cpu" "$root_test_binary" \
        -test.run '^$' -test.bench '^(BenchmarkDecoderDecode_(CELT|Hybrid|SILK)|BenchmarkEncoderEncode_(CallerBuffer|VoIP|LowDelay))$' \
        -test.benchtime=3x -test.count=1 -test.timeout=2m -test.benchmem
    done
    run_phase candidate-simd-xcorr-silk-oracles \
      "${run_env[@]}" "$artifact_root/candidate-simd-celt.test" \
      -test.run '^TestPitchXCorrPairedLibopusSIMDRawBits$' -test.count=1 -test.timeout=10m -test.v
    run_phase candidate-simd-xcorr-primitive-artifacts capture_xcorr_primitive
    run_phase candidate-simd-silk-paired-xcorr \
      "${run_env[@]}" "$artifact_root/candidate-simd-silk.test" \
      -test.run '^Test(SilkPitchXCorrPairedLibopusSIMDRawBits|SilkPitchXcorrAVX2TinyFirstLaneEdgeValues|SilkPitchXcorrNativeZeroAlloc)$' \
      -test.count=1 -test.timeout=10m -test.v
  fi

  # Exact union of the original selectors, sharing package/helper setup.
  run_json_phase "candidate-$mode-standard-correctness-and-cbr-batch" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${oracle_build_args[@]}" \
    ./internal/celt . ./multistream ./internal/encoder ./testvectors ./internal/dnnmath ./internal/dred/rdovae ./internal/libopustest ./internal/opusmath \
    -run '(^(TestApplyDeemphasis.*MatchesLibopus|TestDeemphasisMatchesLibopus|TestDeemphasisSilenceTransitionsAndDownsampleStateMatchLibopus|TestCELTPLCStagesMatchLibopusC|TestCELTPLCFIRMatchesLibopus|TestCELTPLCIIRMatchesLibopus|TestCombFilterConstantBodyHistorySeamMatchesLibopus|TestCombFilterRampedHistorySeamMatchesLibopus|TestCombFilterConstSSEOrderZeroAllocs|TestPitchSearchNearTieMatchesSelectedLibopus|TestExpRotationMatchesLibopusFloatPath|TestPatchTransientHistoryStrideMatchesLibopus|TestPVQProjectionRoundingMatchesLibopus|TestOpPVQSearchFloatHighKNearTieResidual|TestCELTStereoIthetaQ30MatchesLibopus|TestHaar1Transform)$)|(^(TestCELTSilenceDecodeMatchesLibopusFloatBits|TestCELTReceivedSilenceHistoryMatchesLibopus|TestHotPathAllocsDecodeSilenceTransitions|TestHotPathAllocsMultistreamDecode|TestMultistreamCallerBuffer.*|TestMultistreamModeTransitionsWarmZeroAllocs)$)|(^(TestPaddedStreamMatchesLibopusRepacketizer|TestMultistream(CBRDTXMatchesLibopus|EncodeBudgetMatchesLibopus|EncodeTooSmallPreservesState|SelfDelimitedBudgetFramingWarmZeroAllocs)|TestSurroundTransientHistoryStrideMatchesLibopus|TestSurroundPVQProjectionRoundingMatchesLibopus|TestSurroundLowSpaceFinalRangeMatchesLibopus|TestSurroundLowSpaceThenRealFrameMatchesLibopus|TestLowSpacePacketPreservesInputHighPassState|TestProjectionDemixing(Float|Int16)MatchesLibopusMatrixOracle|TestProjectionAnalysisMatchesLibopus|TestInitialStereoToMonoMatchesLibopus|TestProjectionInitialMonoDecisionMatchesLibopus|TestStereoFadeMatchesLibopus|TestStereoWidthComputation|TestHPCutoffMatchesLibopus|TestClampRedundancyBytesAfterSilkMatchesLibopusFormula)$)|(^(TestSub48NativeEncodeParity|TestEncodeStatefulDTXRunFuzz|TestStereoFadeTransitionPacketMatchesLibopus|TestLowDelayApplicationControlsMatchLibopus)$)|(^TestDecodeWithFEC(MultiFrameSILK|SideReset)MatchesLibopus$)|(^TestLowDelayCrossModeParity$)|(^TestDecodeDifferentialEncodeThenDecode$)|(^(TestHybridToSILKFadeRequiresDecodedHistoryMatchesLibopus|TestTransitionPLCStageGainMatchesLibopus|TestCELTTransitionPLCStageHasInnerAndOuterGainChecks|TestCELTTransitionFadeReplaysMatchedLibopus|TestFloatPLCUsesCELTAfterRedundancySequence|TestTransitionFullSequenceMatchesLibopus|TestTransitionPreviousCELTPLCStageMatchesLibopus|TestSILKToCELTTransitionPLCMatchesLibopus|TestSILKPLCDurationChangesMatchLibopus|TestMultistreamSurroundDecodeDifferentialFuzz|TestMultistreamDiscreteDecodeDifferentialFuzz|TestProjectionDecodeDifferentialFuzz|TestMultistreamGopusEncodedDecodeDifferentialFuzz|TestProjectionDecodeIntoPrefilledBuffer|TestCELTActualRotationPacketsMatchLibopus)$)|(^Test(DNNVectorActivationsMatchSelectedLibopusOracle|RDOVAECGEMV8x4MatchesSelectedLibopusOracle|RDOVAESGEMVMatchesSelectedLibopusOracle|RDOVAESparseFloatLinearMatchesSelectedLibopusOracle|RDOVAEIntegerLinearBiasMatchesSelectedLibopusOracle|RDOVAEIntegerInputQuantizerMatchesSelectedLibopusOracle)$)|(^TestEncoderCBRPairedOracleExact$)|(^Test(CurrentPublicAPIHelperConfig|PublicDNNReferenceIdentityUsesBuilderStampContract|DNNHelperIncludesPinnedSourceRootAfterBuildConfig|DNNFeatureBuild|DNNCompilerTarget))|(^TestFloat32ToInt(16.*MatchesLibopus.*|24MatchesLibopus)$)|(^TestProjectionDecodeHigherOrderAndGainMatchesLibopus$)|(^TestProjection(ShortMixingMatchesLibopus|EncodeInt16.*|Int16PacketRangeMatchesLibopus|Int16FullRangeMatchesLibopus)$)|(^Test(SurroundEncodeMatchesLibopusByteExact|SurroundEncodeUnconstrainedVBRMatchesLibopus|SurroundEncodeComplexityMatchesLibopus|ProjectionDecodeMatchesLibopus|ProjectionDecodePerStreamModeClassification)$)|(^Test(PLCModeAfterCELTRedundancyAcrossFormatsMatchesLibopus|DTXByteExactParity_.*|DecodeWithFECNoLBRR.*|DecodeWithFECOverlongNoLBRRRequestMatchesLibopus)$)'"|$exact_audit_selector" \
    -count=1 -timeout=25m

  run_json_phase "candidate-$mode-root-stateful-mono-transition" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^TestEncodeStatefulTransitionFuzz$/^xfr_auto_ch1_(40|60)ms_(24000|32000|48000|64000)bps_vbr[012]_cx5_fecfalse_dtx(true|false)$' \
    -count=1 -timeout=10m

  run_phase "candidate-$mode-lpc-ltp-oracles" \
    "${run_env[@]}" "$artifact_root/candidate-$mode-silk.test" \
    -test.run '^Test(SILKCorrelationMatrixVectorMatchesLibopusOracle|SILKAutocorrelationF32MatchesLibopusOracle|SILKBurgModifiedFLPMatchesLibopusOracle|SILKLPCAnalysisFilterFLPMatchesLibopusOracle|SILKInnerProductFLPMatchesLibopusOracle|SILKFindLPCFLPMatchesLibopusOracle|SILKFindLTPFLPMatchesLibopusOracle)$' \
    -test.count=1 -test.timeout=10m -test.v

  # Feature helpers select the same scalar/SIMD reference as the Go build.
  run_json_phase "candidate-$mode-custom-mode-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes${feature_scalar_tag}" \
    ./internal/celt/custom -count=1 -timeout=10m

  run_json_phase "candidate-$mode-custom-qext-mode-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes,gopus_qext${feature_scalar_tag}" \
    ./internal/celt/custom -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-custom-mode-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes,gopus_fixed_point${feature_scalar_tag}" \
    ./internal/celt/custom \
    -run '^(TestCustomSignalling(DefaultAndRawMatchLibopus|FiniteBitrateMatchesLibopus|ConstructorDefaultsMatchLibopus|ChannelTransitionsMatchLibopus|HeaderLMEndBandAndPaddingMatchLibopus|RejectedHeaderPreservesDecoderState|EndBandCommitsBeforePacketErrors|EndBandSurvivesRawToggleAndReset)|TestCustomSignalled(DecodeFloatWarmZeroAllocs|VBRInputAPIsWarmZeroAllocs)|TestOracleParity(StandardModes|NonStandardModes|NonStandardStereo)|TestOracleWideBand(Mode|Stateful)Parity|TestFixedCustom(Standard.*|Scaled.*|Wide.*|DynamicModesSupported|FloatToRes.*|EncodeRejects.*))$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-fixed-custom-qext-mode-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes,gopus_fixed_point,gopus_qext${feature_scalar_tag}" \
    ./internal/celt/custom ./internal/fixedpoint ./internal/celt \
    -run '^(TestCustomSignalling(DefaultAndRawMatchLibopus|FiniteBitrateMatchesLibopus|ConstructorDefaultsMatchLibopus|ChannelTransitionsMatchLibopus|HeaderLMEndBandAndPaddingMatchLibopus|RejectedHeaderPreservesDecoderState|EndBandCommitsBeforePacketErrors|EndBandSurvivesRawToggleAndReset)|TestCustomSignalled(QEXTPaddingMatchesLibopus|DecodeFloatWarmZeroAllocs|VBRInputAPIsWarmZeroAllocs)|TestOracleParityStandardModes|TestQEXTModePresenceMatchesLibopus|TestFixedCustom(Standard.*|QEXT(EncoderParity|EncoderZeroAlloc|DynamicStatefulParity|DynamicZeroAlloc|2048SafeSequence|EncoderBudgetAndLMParity)|FloatToRes.*|EncodeRejects.*)|TestQEXTCustomMDCTMatchesSelectedLibopus)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-qext-custom-mode-presence" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_custom_modes,gopus_qext${feature_scalar_tag}" \
    ./internal/celt -run '^TestQEXTModePresenceMatchesLibopus$' -count=1 -timeout=10m

  run_json_phase "candidate-$mode-qext-stateful-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_qext${feature_scalar_tag}" . ./internal/celt ./internal/encoder ./internal/libopustest ./multistream ./testvectors \
    -run '(^Test(QEXT(Stateful|ActiveStereoDecode|ReceivedSilence|Decode96kOracle|96kAutoChannelTransitionsMatchLibopus|Native96PLCPitchPeriodMatchesSelectedReference|FirstLossPublicPLCMatchesSelectedReference)|Native96k(Decode|IntegerDecode|MixedInteger)|HD96kPublicFinalRangeMatchesLibopus))|(^Test(SelectedSIMDPLCRawAutocorrMatchesArchive|QEXTBandEnergyMatchesSelectedLibopus|AlgQuantQEXTRefinementMatchesLibopusAtFloatRoundingBoundary|AlgQuantQEXTSearchMatchesLibopusAtPVQCorrectionBoundary)$)|(^Test(HD96kAutoCELTRawTraceMatchesSelectedLibopus|HD96kNativeEncodeMainPayloadParity|HD96kFramingByteParityMatchesLibopus|HD96kFramingExtensionRoundtrip|NativeHD96kFloatCELTInputMatchesSelectedLibopus|PublicQEXTNative96(ShortCELT|Hybrid10ms)MatchesSelectedReference|PublicQEXTHybridNative96TransitionsMatchSelectedFloatReference)$)|(^Test(ConcealPeriodicPLCMatchesLibopus|PeriodicPLCSynthesisStagesMatchLibopusBits|PeriodicPLCEnergyMatchesLibopusVectorRemainders|CELTPVQBandsGridMatchesLibopus|AntiCollapseVsLibopus)$)|(^Test(CurrentPublicAPIHelperConfig|DecodeDifferential(EncodeThenDecode|Malformed)|CELTMonoRecoveryAfterLongGap|CELTReceivedSilenceHistory|EncodeDifferential(LongFrames|LowComplexity)|EncodeStateful(Transition|DTX)|Sub48NativeEncode|LowDelayApplicationControls|MultistreamCallerBuffer|Public(Explicit)?MultistreamInt16|DecodeGainModeTransition|LibopusCELTTraceMatchesReferenceDecodeWindow))|(^Test(QEXTExtensionBandsContentMatchesLibopusOracle|HD96kMDCTMatchesLibopusQEXT|CELTStereoIthetaQ30MatchesLibopus|Haar1Transform|QEXTSynthesisStagesMatchSelectedLibopus)$)|(^Test(SurroundEncodeMatchesLibopusByteExact|SurroundEncodeUnconstrainedVBRMatchesLibopus|SurroundEncodeComplexityMatchesLibopus|ProjectionDecodeMatchesLibopus|ProjectionDecodePerStreamModeClassification)$)'"|$exact_audit_selector" -count=1 -timeout=10m

  # Exact union of the original selectors, sharing package/helper setup.
  run_json_phase "candidate-$mode-fixed-point-correctness-batch" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_libopus_oracle${feature_scalar_tag}" \
    . ./internal/silk ./internal/encoder ./internal/celt ./internal/libopustest ./multistream ./testvectors ./internal/fixedpoint ./internal/opusmath \
    -run '(^Test(CELTAllocTrimAnalysisParity|CELTAllocTrimAnalysisQCONST32RoundingBoundary|TransientAnalysisMatchesLibopusFixed|PatchTransientDecisionMatchesLibopusFixed|PrefilterAnalysisMatchesLibopusFixed)$)|(^TestEncoderCBRPairedOracleExact$)|(^TestSILKFixedRes24API48kCBRSequenceMatchesLibopus$)|(^TestFixedOuterCBRInputPreprocessMatchesLibopus$)|(^Test(DecodeDifferentialFixedPoint|DecoderFixedPoint|DecodeWithFEC|HotPathAllocsDecode|DecodeMalformedVBRPreservesSelectedLibopusState|DecodeMalformedRawCSequenceWitnessPreservesState))|(^Test(Public.*SILK|PacketEncoderEncodeZeroAlloc))|(^(TestPublicFixedShortEncodeMatchesLibopus|TestPublicFixedVoIPShortCELTMatchesLibopus|TestPublicFixedShortExpertFrameDurationMatchesLibopus|TestPublicFixedLongCELTPacketsMatchLibopus|TestPublicFixedInputAPIsShareQ8History|TestPublicFixedVoIPInputAPIsShareQ8History|TestPublicFixedStereoWidthFadeMatchesLibopus|TestPublicFixedSILKHybridInputAPIsMatchLibopus|TestPublicFixedSILKHybridRatesDurationsAndDownmixMatchLibopus|TestPublicFixedSILKHybridModeTransitionsMatchLibopus|TestPublicFixedAutoSILKHybridSequencesMatchLibopus|TestPublicFixedAutoCELTSequenceMatchesLibopus|TestPublicCELTEncodeFixedByteExact|TestPublicCELTEncodeFixedRateByteExact|TestOpusEncodeFixedCELTByteExact|TestOpusEncodeFixedCELTFloatInputSingleFrameByteExact|TestOpusEncodeFixedSILKHybridMatchedFloatInputByteExact|TestFixedPointTonalityAnalysisStagesMatchLibopus|TestEncodeDifferentialFuzzFixedPoint|TestFixedCBRRawTailMatchesLibopus|TestFixedStereoPrefilterThresholdMatchesLibopus|TestFixedOuterOpusEncodeRawInt16MatchesLibopus|TestFixedOuterOpusEncodeRecordsPreserveCalls|TestFixedHPCutoffResMatchesLibopus|TestFixedVoIPHPCutoffResetAndLowSpaceState|TestVoIPHPCutoffResetMatchesFreshEncoder|TestLowSpacePacketPreservesInputHighPassState|TestFixedDCRejectQ8MatchesLibopus|TestAllocationNonpositiveBudgetMatchesFixedLibopus|TestFixedCELTEnergyMaskFloatBoundaryConversion|TestPublicFixedCELTEnergyMaskAndLFEControlsMatchOracle|TestPublicFixedCELTEnergyMaskResetLifetimeMatchesOracle|TestPublicFixedCELTQ24MaskMatchesOracle|TestPublicFixedLFEMatchesOracle|TestPublicFixedShortFrameSILKRequestFallsBackToCELTOracle)$)|(^(TestCELTHybridEncodeWithECSeededOracle|TestCELTResetClearsEnergyMaskOracle|TestAmp2Log2Oracle)$)|(^Test(FixedCELTOracleHelpersUsePairedReference|MultistreamReferenceFeaturePairing)$)|(^(TestFixedPointPhaseInversionControlMatchesLibopus|TestDecoderFixedPointRedundancyCodedOutputChannelParity|TestFixedMultistreamDecodeFloat32MatchesSelectedLibopus|TestFixedMultistreamDecodeFloat32PLCRecoveryMatchesSelectedLibopus|TestMultistreamDecodeFixedPointGainMatchesLibopus|TestMultistreamFixedCELT.*|TestFixedPublicMultistreamHybridDecodeToInt16MatchesSelectedLibopus|TestProjectionDecodeInt24MatchesLibopus|TestProjectionFixed(PLCSequenceMatchesLibopus|DecodeBufferPreflightPreservesState|DecodeIntoPLCZeroAllocs))$)|(^TestFloat32ToInt(16.*MatchesLibopus.*|24MatchesLibopus)$)|(^TestProjectionDecodeHigherOrderAndGainMatchesLibopus$)|(^TestProjection(ShortMixingMatchesLibopus|EncodeInt16.*|Int16PacketRangeMatchesLibopus|Int16FullRangeMatchesLibopus)$)|(^Test(FixedStereoWidthAndModeThresholdMatchesLibopus|ProjectionEncodeDifferentialFuzz|FixedMultistreamHybridLowRateMatchesSelectedLibopus|FixedHybridToCELTTransitionMatchesLibopus|FixedPLCUsesCELTAfterRedundancySequence|MultistreamFixedHybridPLCRecoveryMatchesLibopus|FixedHybridMultiframeInt24MatchesLibopusAndZeroAllocs|FixedProjectionHybridRedundancyMatchesLibopusInt24|MultistreamModeTransitionsWarmZeroAllocs|DecoderQEXT.*|MultistreamSurroundDecodeDifferentialFuzz|MultistreamDiscreteDecodeDifferentialFuzz|ProjectionDecodeDifferentialFuzz|MultistreamGopusEncodedDecodeDifferentialFuzz|ProjectionDecodePerStreamModeClassification)$)|(^Test(SurroundEncodeMatchesLibopusByteExact|SurroundEncodeUnconstrainedVBRMatchesLibopus|SurroundEncodeComplexityMatchesLibopus|ProjectionDecodeMatchesLibopus|ProjectionDecodePerStreamModeClassification)$)|(^Test(PLCModeAfterCELTRedundancyAcrossFormatsMatchesLibopus|DTXByteExactParity_.*|DecodeWithFECNoLBRR.*|DecodeWithFECOverlongNoLBRRRequestMatchesLibopus)$)'"|$exact_audit_selector" \
    -count=1 -timeout=25m

  run_json_phase "candidate-$mode-fixed-fec-control-transitions" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point${feature_scalar_tag}" . \
    -run '^TestEncodeStatefulTransitionFuzz$/^xfr_auto_ch1_(10ms_32000bps_vbr0_cx5|20ms_24000bps_vbr0_cx0|40ms_24000bps_vbr0_cx0)_fectrue_dtxfalse$' \
    -count=1 -timeout=10m

  # Exact union of the original selectors, sharing package/helper setup.
  run_json_phase "candidate-$mode-fixed-qext-correctness-batch" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_fixed_point,gopus_qext,gopus_libopus_oracle${feature_scalar_tag}" \
    ./internal/libopustest ./internal/fixedpoint . ./internal/encoder ./internal/silk ./internal/celt ./multistream ./testvectors ./internal/opusmath \
    -run '(^Test(CELTAllocTrimAnalysisParity|CELTAllocTrimAnalysisQCONST32RoundingBoundary|TransientAnalysisMatchesLibopusFixed|PatchTransientDecisionMatchesLibopusFixed|PrefilterAnalysisMatchesLibopusFixed|GainFadeResQEXTMatchesSelectedLibopus|GainFadeResQEXTAllocs|CELTDecoderFullFrameOracle|CELTDecoderSequenceOracle|CELTDecoderDownsampleOracle|CELTDecoderPLCOracle|CELTDecoderPLCRecoveryOracle|CELTSynthesisOracle)$)|(^TestEncoderCBRPairedOracleExact$)|(^TestSILKFixedRes24API48kCBRSequenceMatchesLibopus$)|(^TestFixedOuterCBRInputPreprocessMatchesLibopus$)|(^(TestFixedQEXTArchiveAndPublicHelperUsePairedReference|TestCELTEncodeWithECFixedQEXTReferenceArchive)$)|(^(TestQEXTStatefulPacketMatrixMatchesLibopus|TestQEXTCBRExtensionFramingByteParityMatchesLibopus|TestFixedDecodeGainClampMatchesSelectedLibopus|TestFixedQEXTHybridSWBMonoPLCRecoveryMatchesSelectedReference|TestFixedQEXTMultistreamDecodeUsesSidePayload|TestMultistreamDecodeRequestedPLCDurationMatchesLibopus|TestMultistreamDecodeOverlongAndEmptyPLCMatchesLibopus|TestMultistreamDecodeInt16HighGainMatchesLibopus|TestMultistreamCallerBufferWarmZeroAlloc|TestDecodeDifferentialFixedPointPLC|TestDecodeDifferentialFixedPointLossSampleRates|TestDecodeDifferentialMalformed|TestDecodeDifferentialCrossFrameCorrupt|TestEncodeStatefulTransitionFuzz|TestEncodeStatefulDTXRunFuzz|TestMultistreamDecodeFixedPointParity|TestMultistreamDecodeFloat32MatchesLibopus|TestMultistreamCallerBuffersMatchLibopus|TestPublicExplicitMultistreamInt16MatchesLibopus|TestQEXTMultistreamDecodeMatchesLibopusOracle|TestHybridStereoFloatSameArchParity|TestPublicFixedQEXTPacketsMatchLibopus|TestPublicFixedQEXTConstraintPersistsAcrossCBR|TestPublicFixedQEXTFrameSizeModeAndInputMatrix|TestPublicFixedQEXTWarmEncodeAllocations|TestPublicFixedQEXTHighBudgetWarmEncodeAllocations|TestPublicFixedQEXT96kDurationsMatchLibopus|TestFixedQEXTInputBypassesDCHighpassWithoutAdvancingMemory|TestCELTFixedQEXTMainPayloadMatchesLibopus|TestCELTFixedQEXTReservedMainPayloadMatchesLibopus|TestCELTFixedQEXTNative96KFrameMatchesLibopus|TestCELTFixedQEXTNative96KSidePayloadMatchesLibopus|TestCELTFixedQEXTNative96KShortFrameNoSidePayloadMatchesLibopus|TestCELTFixedQEXTNative96KStatefulResetMatchesLibopus|TestCELTFixedQEXTNative96KEncodeDoesNotAllocateAfterWarmup|TestCELTFixedQEXTExtraAllocationMatchesLibopus)$)|(^(TestFixedPointTonalityAnalysisStagesMatchLibopus|TestFixedCELTEnergyMaskFloatBoundaryConversion|TestPublicFixedCELTEnergyMaskAndLFEControlsMatchOracle|TestPublicFixedCELTEnergyMaskResetLifetimeMatchesOracle|TestPublicFixedCELTQ24MaskMatchesOracle|TestPublicFixedLFEMatchesOracle|TestPublicFixedShortFrameSILKRequestFallsBackToCELTOracle)$)|(^(TestPublicFixedQEXTCELTReceivedFramesMatchSelectedReference|TestPublicFixedQEXTCELTMainWithoutExtensionMatchesSelectedReference|TestPublicFixedQEXTSmallBufferDoesNotAdvanceCELTState|TestPublicFixedQEXTLostCELTFrameMatchesSelectedReference|TestPublicFixedQEXTLostCELTBurstMatchesSelectedReference|TestPublicFixedQEXTStructuralMalformedPacketPreservesCELTState|TestPublicFixedQEXTHybridReceivedFramesMatchSelectedReference|TestPublicFixedQEXTCELTToHybridTransitionMatchesSelectedReference|TestPublicFixedQEXTHybridToCELTTransitionMatchesSelectedReference|TestPublicFixedQEXTHybridLostFrameMatchesSelectedReference|TestPublicFixedQEXTHybridLowerRateAndDownmixMatchesSelectedReference|TestPublicFixedQEXTHybridFECMatchesSelectedReference|TestPublicFixedQEXTHybridRedundancyDirectionsMatchSelectedReference|TestPublicFixedQEXTCELTShortFramesMatchSelectedReference|TestPublicQEXTNative96ShortCELTMatchesSelectedReference|TestPublicQEXTNative96Hybrid10msMatchesSelectedReference|TestPublicFixedQEXTHybridNative96TransitionsMatchSelectedReference|TestPublicFixedQEXTHybridNative96RedundancyMatchesSelectedReference|TestPublicFixedQEXTNative96SILKOutputMatchesSelectedReference|TestPublicFixedQEXT96kAutoChannelTransitionsMatchLibopus|TestPublicFixedQEXT96kAutoChannelTransitionStateMatchesCELT|TestPublicFixedQEXT48kAutoChannelTransitionsMatchLibopus|TestPublicFixedQEXT48kAutoChannelTransitionStateMatchesCELT|TestNative96kDecodeMatchesQEXTOracle(Mono|Stereo)|TestQEXTDecode96kOracleProducesNative96k|TestNative96kDecodeCrossFramePostfilterParity|TestNative96kIntegerDecodeFormatsMatchQEXTOracle|TestNative96kIntegerDecodeSmallBufferPreservesState|TestNative96kIntegerDecodeGainMatchesQEXTOracle|TestNative96kMixedIntegerFormatsMatchQEXTOracle|TestAlgQuantQEXTMatchesFixedLibopus|TestAlgUnquantQEXTMatchesSelectedLibopus|TestQuantAllBandsDecodeOracle|TestQuantAllBandsDecodeQEXTMatchesSelectedLibopus|TestQuantPartitionQEXTUsesZeroResolutionCubicLeaf)$)|(^(TestPublicFixedAutoCELTSequenceMatchesLibopus|TestPublicFixedSILKHybridModeTransitionsMatchLibopus|TestCombFilterQEXTPFMatchesLibopus|TestFixedPointSurroundEncodeMatchesLibopus|TestFixedPointSurroundAnalysisMatchesLibopus|TestFixedPointSurroundMaskRoutingMatchesAnalyzer|TestFixedPointFloatSurroundEncodeMatchesLibopus|TestFixedQEXTMonoSurroundCELTResetMatchesLibopus|TestMultistreamEncodeDecodeAllocGuard|TestProjectionDemixing(Float|Int16)MatchesLibopusMatrixOracle|TestMultistreamReferenceFeaturePairing)$)|(^(TestQEXTMDCTForwardMatchesFixedLibopus|TestQEXTMDCT96000ForwardMatchesFixedLibopus|TestQEXTMDCTBackwardMatchesSelectedLibopus|TestQEXTMDCTSilenceHeadroomMatchesFixedLibopus|TestQEXTKissFFTMatchesFixedLibopus|TestQEXTMDCTForwardDoesNotAllocateAfterWarmup|TestQEXTKissFFTDoesNotAllocate|TestAmp2Log2Oracle|TestCELTExp2DBFixedQEXTMatchesSelectedLibopus|TestDenormaliseBandsOracle|TestAntiCollapseMatchesLibopusFixed|TestSmoothFadeResQEXTMatchesSelectedLibopus)$)|(^(TestFixedPointPhaseInversionControlMatchesLibopus|TestDecoderFixedPointRedundancyCodedOutputChannelParity|TestFixedMultistreamDecodeFloat32MatchesSelectedLibopus|TestFixedMultistreamDecodeFloat32PLCRecoveryMatchesSelectedLibopus|TestMultistreamDecodeFixedPointGainMatchesLibopus|TestMultistreamFixedCELT.*|TestFixedPublicMultistreamHybridDecodeToInt16MatchesSelectedLibopus|TestProjectionDecodeInt24MatchesLibopus|TestProjectionFixed(PLCSequenceMatchesLibopus|DecodeBufferPreflightPreservesState|DecodeIntoPLCZeroAllocs))$)|(^TestFloat32ToInt(16.*MatchesLibopus.*|24MatchesLibopus)$)|(^TestProjectionDecodeHigherOrderAndGainMatchesLibopus$)|(^TestProjection(ShortMixingMatchesLibopus|EncodeInt16.*|Int16PacketRangeMatchesLibopus|Int16FullRangeMatchesLibopus)$)|(^Test(FixedStereoWidthAndModeThresholdMatchesLibopus|ProjectionEncodeDifferentialFuzz|FixedMultistreamHybridLowRateMatchesSelectedLibopus|FixedHybridToCELTTransitionMatchesLibopus|FixedPLCUsesCELTAfterRedundancySequence|MultistreamFixedHybridPLCRecoveryMatchesLibopus|FixedHybridMultiframeInt24MatchesLibopusAndZeroAllocs|FixedProjectionHybridRedundancyMatchesLibopusInt24|MultistreamModeTransitionsWarmZeroAllocs|DecoderQEXT.*|MultistreamSurroundDecodeDifferentialFuzz|MultistreamDiscreteDecodeDifferentialFuzz|ProjectionDecodeDifferentialFuzz|MultistreamGopusEncodedDecodeDifferentialFuzz|ProjectionDecodePerStreamModeClassification)$)|(^Test(QEXTExtensionBandsContentMatchesLibopusOracle|HD96kMDCTMatchesLibopusQEXT|CELTStereoIthetaQ30MatchesLibopus|Haar1Transform|QEXTSynthesisStagesMatchSelectedLibopus)$)|(^Test(SurroundEncodeMatchesLibopusByteExact|SurroundEncodeUnconstrainedVBRMatchesLibopus|SurroundEncodeComplexityMatchesLibopus|ProjectionDecodeMatchesLibopus|ProjectionDecodePerStreamModeClassification)$)|(^Test(PLCModeAfterCELTRedundancyAcrossFormatsMatchesLibopus|DTXByteExactParity_.*|DecodeWithFECNoLBRR.*|DecodeWithFECOverlongNoLBRRRequestMatchesLibopus)$)'"|$exact_audit_selector" \
    -count=1 -timeout=25m

  run_json_phase "candidate-$mode-neural-analysis-parity" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_osce${feature_scalar_tag}" \
    ./internal/lpcnetplc -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-initial-latents" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_dred${feature_scalar_tag}" \
    ./internal/encoder -run '^TestEncoderDRED(InitialLatentsTraceMatchesLibopus|LongCELTLatentsUseLibopusSubframeCadence|SetDNNBlobPreservesActiveRuntime)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-dred-stateful-concealment" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" GOPUS_DRED_AUDIO_QUALITY=1 go test -json \
    -tags "gopus_dred${feature_scalar_tag}" . ./internal/celt ./internal/lpcnetplc ./internal/silk ./multistream \
    -run '^Test(DREDHistory.*|DecoderDecodePLCAppliesNeuralConcealmentWhenReady|DecoderDecodeNilConsumesMultistreamDREDNeural.*|MSDREDDormancy_.*|MSPerStreamDREDQueue.*|DecodeDREDCarrierHistoryCodecStateMatchesLibopus|DecoderDREDFECPLCUpdatesMatchLibopusHistory|DecoderNoSidecarDeepPLC.*|MultistreamMainModelNeuralPLC.*|DecoderStateSnapshotPreservesIntegerLowBits|DREDLowDelayReferenceOffsetAgainstLibopus|DREDLowDelayFullSequenceEncoderMatchesLibopus|DREDLongLossPCMMatchesLibopusRawBits|DREDLongSequenceAllDecodedPCMMatchesLibopusRawBits|DecoderCELTNeuralPLCAPIRatesMatchesLibopusRawBits|DREDBurgSelectedCFirstLossRawBits|DREDPredictorSelectedCFirstLossRawBits|ExplicitDRED.*Quality.*SixtyPercentLoss|AntiCollapseVsLibopus)$' \
    -count=1 -timeout=10m

  # Exact union of the original selectors, sharing package/helper setup.
  run_json_phase "candidate-$mode-dred-qext-correctness-batch" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json -tags "gopus_dred,gopus_qext${feature_scalar_tag}" \
    . ./internal/celt ./internal/encoder ./internal/libopustest ./internal/libopustooling ./multistream ./internal/silk \
    -run '(^(TestDREDHistory.*|TestDecoderDecodePLCAppliesNeuralConcealmentWhenReady|TestDecoderDecodeNilConsumesMultistreamDREDNeural.*|TestMSDREDDormancy_.*|TestMSPerStreamDREDQueue.*|TestDecodeDREDCarrierHistoryCodecStateMatchesLibopus|TestDecoderDREDFECPLCUpdatesMatchLibopusHistory|TestDecoderNoSidecarDeepPLC.*|TestMultistreamMainModelNeuralPLC.*|TestDecoderStateSnapshotPreservesIntegerLowBits|TestDREDLongLossPCMMatchesLibopusRawBits|TestDREDLongSequenceAllDecodedPCMMatchesLibopusRawBits|TestMultistreamReferenceFeaturePairing|TestCombinedDREDQEXTBuildOptionalExtensionContract|TestCombinedDREDQEXTBuildPublicAPIContract|TestDREDQEXTFloatSurroundMasksMatchSelectedLibopus|TestMaybeBuildSingleFrameDREDPacketCarriesQEXTAndDRED|TestEncodeCELTDREDQEXTPacketCarriesBothExtensions|TestMaybeBuildLongCELTDREDQEXTPacketCarriesBothExtensions|TestResolveLibopusDREDQEXTReferenceMatchesGoISA|TestHelperRefDirSelectsDREDQEXTTree|TestCHelperReferenceSelectionRejectsConflictingVariants|TestDREDQEXTReferenceVariantPairsScalarAndSIMD|TestValidateDREDQEXTReferenceBuildRequiresCombinedFlagsAndPairedISA|TestExistingReferenceBuildsRejectDREDAndDeepPLCFeatures|TestDREDQEXTBuildEnvironmentClearsConflictingFeatureFlags|TestSILKPLCDurationChangesMatchLibopus|TestSelectedSIMDPLCRawAutocorrMatchesArchive|TestQEXTFirstLossPublicPLCMatchesSelectedReference|TestSelfDelimitedGrowingExtensionsZeroAllocAndByteExact|TestConcealPeriodicPLCMatchesLibopus|TestPeriodicPLCSynthesisStagesMatchLibopusBits|TestPeriodicPLCEnergyMatchesLibopusVectorRemainders|TestCELTPVQBandsGridMatchesLibopus|TestAntiCollapseVsLibopus)$)|(^TestDREDQEXTSurroundAndProjection(EncodeMatchesLibopus|WarmedCycleZeroAllocs)$)' \
    -count=1 -timeout=25m

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

  cat > "$artifact_root/paired-libopus-e2e-info.txt" <<'EOF'
Paired end-to-end codec timings use tools/encoderbenchcmp and tools/testvectorbenchcmp.
Scalar rows compile Go with -tags nosimd and select the stamped libopus scalar archive.
SIMD rows compile Go with GOEXPERIMENT=simd and select the stamped libopus SIMD archive.
Encoder cases are reported individually for CELT, SILK, and Hybrid; decode aggregates all
official RFC 8251 vectors for float32 and int16 output paths. Each report uses three runs
with a 250 ms minimum per run; Go uses the same -pgo=auto policy as the adjacent E2E
benchmarks. The reports enforce and print Go allocs_per_op=0; C allocations are not
measured. The paired archives, config.h files, and build stamps are in libopus-scalar/ and
libopus-simd/.
EOF

  run_phase candidate-paired-libopus-testvectors \
    run_in_checkout "$candidate_root" make ensure-testvectors
  vector_status="$(cat "$artifact_root/candidate-paired-libopus-testvectors.exit")"
  for mode in nosimd simd; do
    if [[ "$mode" == simd ]]; then
      mode_env=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1)
      mode_args=()
    else
      mode_env=(env -u GOEXPERIMENT GOPUS_LIBOPUS_REF_SCALAR=1 GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1)
      mode_args=(-tags nosimd)
    fi

    run_phase "candidate-$mode-libopus-encoder-e2e" \
      run_in_checkout "$candidate_root" \
      "${mode_env[@]}" go run -pgo=auto "${mode_args[@]}" ./tools/encoderbenchcmp \
      -cases=per-case -benchtime=250ms -count=3 -format=tsv -max-gopus-allocs-per-op=0
    if [[ "$vector_status" == 0 ]]; then
      run_phase "candidate-$mode-libopus-decode-e2e" \
        run_in_checkout "$candidate_root" \
        "${mode_env[@]}" go run -pgo=auto "${mode_args[@]}" ./tools/testvectorbenchcmp \
        -cases=aggregate -paths=all -benchtime=250ms -count=3 -format=tsv -max-gopus-allocs-per-op=0
    else
      printf 'official test-vector preparation failed with exit=%s; see candidate-paired-libopus-testvectors.log\n' \
        "$vector_status" > "$artifact_root/candidate-$mode-libopus-decode-e2e.log"
      printf '%s\n' "$vector_status" > "$artifact_root/candidate-$mode-libopus-decode-e2e.exit"
    fi
  done
else
  overall_status=1
  printf 'baseline, SIMD, or nosimd E2E binaries unavailable after an earlier build failure\n' > "$artifact_root/profile-bench-skipped.txt"
fi

run_phase candidate-simd-dnn-primitive-artifacts capture_dnn_primitive

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
