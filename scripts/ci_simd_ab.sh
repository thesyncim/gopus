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

printf 'runner_os=%s\nrunner_arch=%s\ngo=%s\ncc=%s\n' \
  "$(uname -s)" \
  "$(uname -m)" \
  "$(go version)" \
  "$(cc --version | sed -n '1p')" > "$artifact_root/environment.txt"

run_phase() {
  local side="$1" root="$2" phase="$3"
  shift 3
  local log="$artifact_root/$side-$phase.log"
  local status="$artifact_root/$side-$phase.exit"

  echo "==> $side: $phase"
  (cd "$root" && "$@") >"$log" 2>&1
  local rc=$?
  printf '%s\n' "$rc" > "$status"
  echo "$side $phase exit=$rc"
  return 0
}

install_amd64_kernel_benchmarks() {
  local side="$1" root="$2"
  case "$(uname -m)" in
    x86_64|amd64) ;;
    *) return 0 ;;
  esac

  mkdir -p "$root/internal/celt"
  if [[ "$side" == baseline ]]; then
    cp "$candidate_root/scripts/benchmarks/kernel_port_amd64_baseline.go.tmpl" \
      "$root/internal/celt/kernel_port_bench_ci_amd64_test.go"
  else
    cp "$candidate_root/scripts/benchmarks/kernel_port_amd64_candidate.go.tmpl" \
      "$root/internal/celt/kernel_port_bench_ci_amd64_test.go"
    cp "$candidate_root/scripts/benchmarks/kernel_port_amd64_candidate_simd.go.tmpl" \
      "$root/internal/celt/kernel_port_bench_ci_amd64_simd_test.go"
  fi
}

run_mode() {
  local side="$1" root="$2" mode="$3"
  local env_args=(env)
  local ref_env_args=()
  local cbr_tags=()
  local oracle_tags=(-tags gopus_libopus_oracle)

  case "$mode" in
    default)
      env_args=(env -u GOEXPERIMENT)
      ;;
    nosimd)
      env_args=(env GOEXPERIMENT=simd)
      cbr_tags=(-tags nosimd)
      oracle_tags=(-tags nosimd,gopus_libopus_oracle)
      ;;
    purego)
      env_args=(env -u GOEXPERIMENT)
      cbr_tags=(-tags purego)
      oracle_tags=(-tags purego,gopus_libopus_oracle)
      ;;
    simd)
      env_args=(env GOEXPERIMENT=simd)
      ;;
    *)
      echo "unknown mode: $mode" >&2
      return 2
      ;;
  esac

  # The PR candidate's ordinary and nosimd builds use scalar Go kernels. Keep
  # their live libopus comparisons on generic C; the retained assembly baseline
  # and candidate SIMD build use the platform libopus SIMD path.
  if [[ ( "$side" == candidate && ( "$mode" == default || "$mode" == nosimd ) ) || ( "$side" == baseline && "$mode" == purego ) ]]; then
    ref_env_args=(GOPUS_LIBOPUS_REF_SCALAR=1)
  fi

  run_phase "$side" "$root" "$mode-selected-kernel-files" \
    "${env_args[@]}" go list \
      -f '{{.ImportPath}}: Go={{join .GoFiles " "}} Asm={{join .SFiles " "}}' \
      ./internal/celt ./internal/silk

  if [[ "$mode" == default || "$mode" == simd ]]; then
    run_phase "$side" "$root" "$mode-xcorr-runtime-identity" \
      "${env_args[@]}" \
      go test ./internal/celt ./internal/silk \
        -run '^TestXcorrKernelRuntimeIdentity$' -count=1 -v
  fi

  if [[ "$side" == candidate ]]; then
    if [[ "$mode" == simd ]]; then
      run_phase "$side" "$root" "$mode-pvq-dispatch" \
        "${env_args[@]}" GOPUS_REQUIRE_PVQ_SIMD=1 \
        go test ./internal/celt -run '^TestPVQSearchSIMDDispatchUsesAVX$' -count=1 -v
    elif [[ "$mode" == default || "$mode" == nosimd ]]; then
      run_phase "$side" "$root" "$mode-pvq-dispatch" \
        "${env_args[@]}" \
        go test "${cbr_tags[@]}" ./internal/celt -run '^TestPVQSearchScalarDispatch$' -count=1 -v
    fi
  fi

  run_phase "$side" "$root" "$mode-cbr-parity" \
    "${env_args[@]}" "${ref_env_args[@]}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
    go test "${cbr_tags[@]}" ./testvectors \
      -run '^TestEncoderCBRByteParitySummary$' \
      -count=1 -timeout=25m -v

  if [[ ( "$side" == baseline && "$mode" == default ) || ( "$side" == candidate && "$mode" == simd ) ]]; then
    run_phase "$side" "$root" "$mode-decode-differential" \
      "${env_args[@]}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      go test . -run '^TestDecodeDifferentialEncodeThenDecode/hybrid_swb_ch2_10ms_48000bps_vbr0_fectrue_dtxfalse$' \
        -count=1 -timeout=10m -v
  fi

  run_phase "$side" "$root" "$mode-precision-guard" \
    "${env_args[@]}" "${ref_env_args[@]}" GOPUS_REQUIRE_PLATFORM_FIXTURES=1 \
    GOPUS_TEST_TIER=exhaustive GOPUS_STRICT_LIBOPUS_REF=1 \
    go test "${oracle_tags[@]}" ./testvectors \
      -run '^TestEncoderCompliancePrecisionGuard$/^Hybrid-FB-20ms-stereo-96k$' \
      -count=1 -timeout=20m -v

  # These direct benches compare the retained assembly baseline, the scalar
  # Go path, and the Go SIMD path on the same native AMD64 runner.
  if [[ "$side" != baseline || "$mode" == default ]]; then
    if [[ "$mode" != nosimd ]]; then
      run_phase "$side" "$root" "$mode-kernel-benchmarks" \
        "${env_args[@]}" \
        go test "${cbr_tags[@]}" ./internal/celt ./internal/silk \
          -run '^$' \
          -bench '^(BenchmarkInnerProd8FMA32|BenchmarkXcorrF32|BenchmarkInnerProductFLP|BenchmarkCeltPitchXcorrFloat|BenchmarkXcorrKernelFloat|BenchmarkXcorrKernelAVX8|BenchmarkPortAMD64)' \
          -benchmem -count=5 -timeout=20m
    fi
  fi
}

run_side() {
  local side="$1" root="$2"
  run_phase "$side" "$root" ensure-libopus make ensure-libopus
  if [[ "$(cat "$artifact_root/$side-ensure-libopus.exit")" != 0 ]]; then
    return 0
  fi

  if [[ "$side" == candidate ]]; then
    run_phase "$side" "$root" ensure-libopus-scalar make ensure-libopus-scalar
    if [[ "$(cat "$artifact_root/$side-ensure-libopus-scalar.exit")" != 0 ]]; then
      return 0
    fi
  fi

  install_amd64_kernel_benchmarks "$side" "$root"

  run_phase "$side" "$root" platform-fixtures make fixtures-gen-platform
  if [[ "$(cat "$artifact_root/$side-platform-fixtures.exit")" != 0 ]]; then
    return 0
  fi

  if [[ "$side" == baseline ]]; then
    run_phase "$side" "$root" ensure-libopus-scalar make ensure-libopus-scalar
    if [[ "$(cat "$artifact_root/$side-ensure-libopus-scalar.exit")" != 0 ]]; then
      return 0
    fi
    run_mode "$side" "$root" default
    run_mode "$side" "$root" purego
    run_mode "$side" "$root" simd
    run_phase "$side" "$root" default-full-parity \
      env -u GOEXPERIMENT GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      bash ./tools/run_go_test_runnable.sh -json -count=1 -timeout=25m
  else
    run_mode "$side" "$root" default
    run_mode "$side" "$root" nosimd
    run_mode "$side" "$root" simd
    run_phase "$side" "$root" simd-full-parity \
      env GOEXPERIMENT=simd GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      bash ./tools/run_go_test_runnable.sh -json -count=1 -timeout=25m
  fi
}

run_side baseline "$baseline_root"
run_side candidate "$candidate_root"

summary_file="${GITHUB_STEP_SUMMARY:-}"
if [[ -n "$summary_file" ]]; then
  {
    echo '## Native Linux AMD64 mode-matched A/B'
    echo
    echo 'Both checkouts use this runner, Go 1.27.1, and pinned libopus 1.6.1. Scalar Go modes use scalar C; assembly and Go SIMD modes use native SIMD C. Modes have separate artifacts, and source lists record compile-time dispatch.'
    echo
    cat "$artifact_root/environment.txt"
    echo
    echo 'Full parity exit codes: old assembly / Go SIMD'
    echo
    printf '%s / %s\n' \
      "$(cat "$artifact_root/baseline-default-full-parity.exit")" \
      "$(cat "$artifact_root/candidate-simd-full-parity.exit")"
    echo
    echo '| Checkout | Mode | CBR matrix | Hybrid precision | PVQ dispatch | Kernel benchmarks |'
    echo '| --- | --- | ---: | ---: | ---: | ---: |'
    for side in baseline candidate; do
      if [[ "$side" == baseline ]]; then modes=(default purego simd); else modes=(default nosimd simd); fi
      for mode in "${modes[@]}"; do
        values=()
        for phase in "$mode-cbr-parity" "$mode-precision-guard" "$mode-pvq-dispatch" "$mode-kernel-benchmarks"; do
          if [[ -f "$artifact_root/$side-$phase.exit" ]]; then
            values+=("$(cat "$artifact_root/$side-$phase.exit")")
          else
            values+=(not-run)
          fi
        done
        printf '| %s | %s | %s | %s | %s | %s |\n' "$side" "$mode" "${values[@]}"
      done
    done
    echo
    echo '### Selected kernel sources'
    echo
    for side in baseline candidate; do
      if [[ "$side" == baseline ]]; then modes=(default purego simd); else modes=(default nosimd simd); fi
      for mode in "${modes[@]}"; do
        echo "#### $side / $mode"
        echo
        if [[ -f "$artifact_root/$side-$mode-selected-kernel-files.log" ]]; then
          cat "$artifact_root/$side-$mode-selected-kernel-files.log"
        else
          echo 'Source listing did not run.'
        fi
        echo
      done
    done
    echo '### CBR summaries'
    echo
    for side in baseline candidate; do
      if [[ "$side" == baseline ]]; then modes=(default purego simd); else modes=(default nosimd simd); fi
      for mode in "${modes[@]}"; do
        echo "#### $side / $mode"
        echo
        if [[ -f "$artifact_root/$side-$mode-cbr-parity.log" ]]; then
          sed -n '/CBR Byte Parity Summary/,/pass=.*arch=/p' "$artifact_root/$side-$mode-cbr-parity.log" | tail -n 22
        else
          echo 'CBR test did not run.'
        fi
        echo
      done
    done
    echo '### Hybrid-FB-20ms-stereo-96k precision guard'
    echo
    for side in baseline candidate; do
      if [[ "$side" == baseline ]]; then modes=(default purego simd); else modes=(default nosimd simd); fi
      for mode in "${modes[@]}"; do
        echo "#### $side / $mode"
        echo
        if [[ -f "$artifact_root/$side-$mode-precision-guard.log" ]]; then
          grep -E 'RealContent (gopus|libopus) Q=|precision regression|PASS|FAIL|gap guard skipped' \
            "$artifact_root/$side-$mode-precision-guard.log" || true
        else
          echo 'Precision test did not run.'
        fi
        echo
      done
    done
    echo '### Native AMD64 kernel benchmarks'
    echo
    echo 'These direct microbenchmarks complement the full 53-symbol inventory in the kernel evidence report.'
    echo
    for side in baseline candidate; do
      if [[ "$side" == baseline ]]; then modes=(default simd); else modes=(default nosimd simd); fi
      for mode in "${modes[@]}"; do
        echo "#### $side / $mode"
        echo
        if [[ -f "$artifact_root/$side-$mode-kernel-benchmarks.log" ]]; then
          grep -E '^Benchmark|^PASS|^FAIL' "$artifact_root/$side-$mode-kernel-benchmarks.log" || true
        else
          echo 'Kernel benchmarks did not run.'
        fi
        echo
      done
    done
  } >> "$summary_file"
fi

python3 "$candidate_root/scripts/compare_simd_ab.py" "$artifact_root"
