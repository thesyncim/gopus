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
      -test.run '^Test(SilkPitchXCorrPairedLibopusSIMDRawBits|SilkPitchXcorrNativeZeroAlloc)$' \
      -test.count=1 -test.timeout=10m -test.v
  fi

  run_json_phase "candidate-$mode-celt-deemphasis-state-plc" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./internal/celt \
    -run '^(TestApplyDeemphasis.*MatchesLibopus|TestDeemphasisSilenceTransitionsAndDownsampleStateMatchLibopus|TestCELTPLCStagesMatchLibopusC|TestCELTPLCFIRMatchesLibopus|TestCELTPLCIIRMatchesLibopus)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-silence-allocation" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^(TestCELTSilenceDecodeMatchesLibopusFloatBits|TestHotPathAllocsDecodeSilenceTransitions|TestHotPathAllocsMultistreamDecode)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-multistream-encode-budget" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./multistream \
    -run '^(TestMultistream(EncodeBudgetMatchesLibopus|EncodeTooSmallPreservesState|SelfDelimitedBudgetFramingWarmZeroAllocs)|TestProjectionAnalysisMatchesLibopus)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-root-native-rate-dtx" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" . \
    -run '^(TestSub48NativeEncodeParity|TestEncodeStatefulDTXRunFuzz)$' \
    -count=1 -timeout=10m

  run_json_phase "candidate-$mode-multistream-history-strict-decode" \
    run_in_checkout "$candidate_root" \
    "${run_env[@]}" go test -json "${build_args[@]}" ./multistream \
    -run '^(TestHybridToSILKFadeRequiresDecodedHistoryMatchesLibopus|TestTransitionPLCStageGainMatchesLibopus|TestCELTTransitionPLCStageHasInnerAndOuterGainChecks|TestCELTTransitionFadeReplaysMatchedLibopus|TestTransitionFullSequenceMatchesLibopus|TestTransitionPreviousCELTPLCStageMatchesLibopus|TestSILKToCELTTransitionPLCMatchesLibopus|TestSILKPLCDurationChangesMatchLibopus|TestMultistreamSurroundDecodeDifferentialFuzz|TestMultistreamDiscreteDecodeDifferentialFuzz|TestProjectionDecodeDifferentialFuzz|TestMultistreamGopusEncodedDecodeDifferentialFuzz)$' \
    -count=1 -timeout=25m

  run_phase "candidate-$mode-lpc-ltp-oracles" \
    "${run_env[@]}" "$artifact_root/candidate-$mode-silk.test" \
    -test.run '^Test(SILKCorrelationMatrixVectorMatchesLibopusOracle|SILKAutocorrelationF32MatchesLibopusOracle|SILKBurgModifiedFLPMatchesLibopusOracle|SILKLPCAnalysisFilterFLPMatchesLibopusOracle|SILKInnerProductFLPMatchesLibopusOracle|SILKFindLPCFLPMatchesLibopusOracle|SILKFindLTPFLPMatchesLibopusOracle)$' \
    -test.count=1 -test.timeout=10m -test.v

  run_phase "candidate-$mode-strict-cbr" \
    "${run_env[@]}" "$artifact_root/candidate-$mode-testvectors.test" \
    -test.run '^TestEncoderCBRPairedOracleExact$' -test.count=1 -test.timeout=25m -test.v
done

run_phase build-baseline-test-binary \
  run_in_checkout "$baseline_root" env -u GOEXPERIMENT -u GOPUS_LIBOPUS_REF_SCALAR \
  go test -c -pgo=auto -o "$artifact_root/baseline-default-root.test" .

run_profile() {
  local side="$1" binary="$2" profile="$3" checkout
  if [[ "$side" == baseline ]]; then checkout="$baseline_root"; else checkout="$candidate_root"; fi
  run_phase "$side-callerbuffer-cpu-profile" \
    run_in_checkout "$checkout" env "$binary" \
      -test.run '^$' \
      -test.bench '^BenchmarkEncoderEncode_CallerBuffer$' \
      -test.benchtime=3s -test.count=1 -test.cpu=1 -test.benchmem \
      -test.cpuprofile="$profile"
  if [[ ! -s "$profile" ]]; then
    printf 'missing CPU profile: %s\n' "$profile" >> "$artifact_root/$side-callerbuffer-cpu-profile.log"
    overall_status=1
  fi
}

if [[ -x "$artifact_root/baseline-default-root.test" && -x "$artifact_root/candidate-simd-root.test" ]]; then
  run_profile baseline "$artifact_root/baseline-default-root.test" "$artifact_root/baseline-callerbuffer.cpu"
  run_profile candidate-simd "$artifact_root/candidate-simd-root.test" "$artifact_root/candidate-simd-callerbuffer.cpu"

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
      if ! grep -Eq '0 B/op[[:space:]]+0 allocs/op' "$artifact_root/interleaved-callerbuffer-$side-$sample.log"; then
        printf 'expected a zero-allocation caller-buffer benchmark row\n' >> "$artifact_root/interleaved-callerbuffer-$side-$sample.log"
        printf '1\n' > "$artifact_root/interleaved-callerbuffer-$side-$sample.exit"
        overall_status=1
      fi
    done
  done
else
  overall_status=1
  printf 'profile/e2e binaries unavailable after an earlier build failure\n' > "$artifact_root/profile-bench-skipped.txt"
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
