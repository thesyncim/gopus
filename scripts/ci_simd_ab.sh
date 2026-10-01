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
timing_file="$artifact_root/phase-timings.tsv"
printf 'phase\telapsed_s\texit\n' > "$timing_file"
{
  printf 'online_cpus=%s\n' "$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || printf unknown)"
  printf 'allowed_cpus=%s\n' "$(nproc 2>/dev/null || printf unknown)"
  printf 'GOAMD64=%s\n' "$(go env GOAMD64 2>/dev/null || printf unknown)"
  printf 'GOEXPERIMENT=%s\n' "${GOEXPERIMENT:-unset}"
  printf 'GOMAXPROCS=%s\n' "${GOMAXPROCS:-unset}"
  if [[ -r /sys/fs/cgroup/cpu.max ]]; then
    printf 'cgroup_cpu_max=%s\n' "$(cat /sys/fs/cgroup/cpu.max)"
  fi
  if [[ -r /sys/fs/cgroup/memory.max ]]; then
    printf 'cgroup_memory_max=%s\n' "$(cat /sys/fs/cgroup/memory.max)"
  fi
  awk -F ': *' '/^MemTotal:/ { print "host_memory_kb=" $2; exit }' /proc/meminfo 2>/dev/null || true
} > "$artifact_root/resources.txt"

printf 'runner_os=%s\nrunner_arch=%s\ngo=%s\ncc=%s\n' \
  "$(uname -s)" \
  "$(uname -m)" \
  "$(go version)" \
  "$(cc --version | sed -n '1p')" > "$artifact_root/environment.txt"
printf 'goamd64=%s\n' "$(go env GOAMD64)" >> "$artifact_root/environment.txt"
if [[ -r /proc/cpuinfo ]]; then
  awk -F ': ' '/^model name[[:space:]]*:/ { print "cpu_model=" $2; exit }' \
    /proc/cpuinfo >> "$artifact_root/environment.txt"
fi

run_phase() {
  local side="$1" root="$2" phase="$3"
  shift 3
  local log="$artifact_root/$side-$phase.log"
  local status="$artifact_root/$side-$phase.exit"
  local start_s elapsed_s

  start_s=$SECONDS
  echo "==> $side: $phase"
  (cd "$root" && "$@") >"$log" 2>&1
  local rc=$?
  elapsed_s=$((SECONDS - start_s))
  printf '%s\n' "$rc" > "$status"
  printf '%s\t%s\t%s\n' "$side-$phase" "$elapsed_s" "$rc" >> "$timing_file"
  echo "$side $phase exit=$rc"
  return 0
}

install_amd64_kernel_benchmarks() {
  local root="$1"
  case "$(uname -m)" in
    x86_64|amd64) ;;
    *) return 0 ;;
  esac

  mkdir -p "$root/internal/celt"
  cp "$candidate_root/scripts/benchmarks/kernel_port_amd64_candidate.go.tmpl" \
    "$root/internal/celt/kernel_port_bench_ci_amd64_test.go"
  cp "$candidate_root/scripts/benchmarks/kernel_port_amd64_candidate_simd.go.tmpl" \
    "$root/internal/celt/kernel_port_bench_ci_amd64_simd_test.go"
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
      env_args=(env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd)
      ;;
    *)
      echo "unknown mode: $mode" >&2
      return 2
      ;;
  esac

  # Match each Go lane to the same libopus C lane on both checkouts.
  if [[ "$mode" == default || "$mode" == nosimd || "$mode" == purego ]]; then
    ref_env_args=(GOPUS_LIBOPUS_REF_SCALAR=1)
  fi

  run_phase "$side" "$root" "$mode-selected-kernel-files" \
    "${env_args[@]}" go list "${cbr_tags[@]}" \
      -f '{{.ImportPath}}: Go={{join .GoFiles " "}} Asm={{join .SFiles " "}}' \
      ./internal/celt ./internal/silk

  if [[ "$mode" == default || "$mode" == simd ]]; then
    run_phase "$side" "$root" "$mode-xcorr-runtime-identity" \
      "${env_args[@]}" \
      go test ./internal/celt ./internal/silk \
        -run '^TestXcorrKernelRuntimeIdentity$' -count=1 -v
  fi

  if [[ "$side" == candidate && "$mode" == simd ]]; then
    run_phase "$side" "$root" "$mode-xcorr-one-pass-oracle" \
      "${env_args[@]}" \
      go test ./internal/celt ./internal/silk \
        -run '^Test(XcorrKernelAVX8OnePass|SilkXcorrKernelAVX8OnePass)' \
        -count=1 -v
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

  if [[ "$side" == candidate ]]; then
    run_phase "$side" "$root" "$mode-cbr-parity" \
      "${env_args[@]}" "${ref_env_args[@]}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      go test "${cbr_tags[@]}" ./testvectors \
        -run '^TestEncoderCBRPairedOracleExact$' \
        -count=1 -timeout=25m -v
  else
    run_phase "$side" "$root" "$mode-cbr-parity" \
      "${env_args[@]}" "${ref_env_args[@]}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      go test "${cbr_tags[@]}" ./testvectors \
        -run '^TestEncoderCBRByteParitySummary$' \
        -count=1 -timeout=25m -v
  fi

  if [[ "$mode" == default || "$mode" == simd ]]; then
    run_phase "$side" "$root" "$mode-decode-differential" \
      "${env_args[@]}" "${ref_env_args[@]}" GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
      go test . -run '^TestDecodeDifferentialEncodeThenDecode/hybrid_swb_ch2_10ms_48000bps_vbr0_fectrue_dtxfalse$' \
        -count=1 -timeout=10m -v
  fi

  run_phase "$side" "$root" "$mode-precision-guard" \
    "${env_args[@]}" "${ref_env_args[@]}" GOPUS_REQUIRE_PLATFORM_FIXTURES=1 \
    GOPUS_TEST_TIER=exhaustive GOPUS_STRICT_LIBOPUS_REF=1 \
    go test "${oracle_tags[@]}" ./testvectors \
      -run '^TestEncoderCompliancePrecisionGuard$/^Hybrid-FB-20ms-stereo-96k$' \
      -count=1 -timeout=20m -v

  # Capture matched default-scalar and Go SIMD kernel rows on both checkouts.
  if [[ "$mode" == default || "$mode" == simd ]]; then
    run_phase "$side" "$root" "$mode-kernel-benchmarks" \
      "${env_args[@]}" \
      go test "${cbr_tags[@]}" -p=1 ./internal/celt ./internal/silk \
        -run '^$' \
        -bench '^(BenchmarkInnerProd8FMA32|BenchmarkXcorrF32|BenchmarkInnerProductFLP|BenchmarkCeltPitchXcorrFloat|BenchmarkXcorrKernelFloat|BenchmarkXcorrKernelAVX8|BenchmarkPortAMD64)' \
        -benchtime=300ms -benchmem -count=5 -timeout=20m
  fi

  # Capture both scalar modes and Go SIMD for public encode/decode work.
  if [[ "$mode" == default || "$mode" == simd || ( "$side" == candidate && "$mode" == nosimd ) ]]; then
    run_phase "$side" "$root" "$mode-e2e-benchmarks" \
      "${env_args[@]}" \
      go test "${cbr_tags[@]}" . -run '^$' \
        -bench '^Benchmark(DecoderDecode_(CELT|Hybrid|SILK)|EncoderEncode_(CallerBuffer|VoIP|LowDelay))$' \
        -benchtime=300ms -count=3 -cpu=1 -benchmem -timeout=10m
  fi
}

run_side() {
  local side="$1" root="$2"
  local simd_opusdec_fixture="$artifact_root/$side-simd-committed-opusdec-fixture.json"
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

  run_phase "$side" "$root" ensure-libopus-simd make ensure-libopus-simd
  if [[ "$(cat "$artifact_root/$side-ensure-libopus-simd.exit")" != 0 ]]; then
    return 0
  fi

  install_amd64_kernel_benchmarks "$root"

  run_phase "$side" "$root" save-simd-opusdec-fixture \
    cp "$root/internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json" \
      "$simd_opusdec_fixture"
  if [[ "$(cat "$artifact_root/$side-save-simd-opusdec-fixture.exit")" != 0 ]]; then
    return 0
  fi

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
    run_phase "$side" "$root" restore-simd-opusdec-fixture \
      cp "$simd_opusdec_fixture" "$root/internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json"
    if [[ "$(cat "$artifact_root/$side-restore-simd-opusdec-fixture.exit")" != 0 ]]; then
      return 0
    fi
    run_mode "$side" "$root" simd
  else
    run_mode "$side" "$root" default
    run_mode "$side" "$root" nosimd
    run_phase "$side" "$root" restore-simd-opusdec-fixture \
      cp "$simd_opusdec_fixture" "$root/internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json"
    if [[ "$(cat "$artifact_root/$side-restore-simd-opusdec-fixture.exit")" != 0 ]]; then
      return 0
    fi
    run_mode "$side" "$root" simd
  fi
}

run_side baseline "$baseline_root"
run_side candidate "$candidate_root"

# Compare the same Go SIMD lane against the same pinned libopus SIMD reference.
run_phase baseline "$baseline_root" simd-full-parity \
  env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
  bash ./tools/run_go_test_runnable.sh -json -count=1 -timeout=25m
run_phase candidate "$candidate_root" simd-full-parity \
  env -u GOPUS_LIBOPUS_REF_SCALAR GOEXPERIMENT=simd GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
  bash ./tools/run_go_test_runnable.sh -json -count=1 -timeout=25m

# On a packet-hash change, capture the exact native opusdec fixture generated
# from the candidate bitstream for review. The comparison above still reads the
# committed fixture and fails on missing hashes; generation cannot green it.
run_phase candidate "$candidate_root" simd-crossval-fixture-refresh \
  env GOEXPERIMENT=simd GOPUS_REQUIRE_PLATFORM_FIXTURES=1 \
  GOPUS_UPDATE_OPUSDEC_CROSSVAL_FIXTURE=1 GOPUS_TEST_TIER=parity \
  go test ./internal/celt -run '^TestOpusdecCrossvalFixtureCoverage$' -count=1 -v
if [[ -f "$candidate_root/internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json" ]]; then
  cp "$candidate_root/internal/celt/testdata/opusdec_crossval_fixture_linux_amd64.json" \
    "$artifact_root/opusdec_crossval_fixture_linux_amd64.json"
fi

summary_file="${GITHUB_STEP_SUMMARY:-}"
if [[ -n "$summary_file" ]]; then
  {
    echo '## Native Linux AMD64 mode-matched A/B'
    echo
    echo 'Both checkouts use this runner, Go 1.27.1, and pinned libopus 1.6.1. Default, nosimd, and purego Go builds use scalar C; GOEXPERIMENT=simd builds use native SIMD C. Full parity and benchmark comparisons use the same Go lane on both checkouts.'
    echo
    cat "$artifact_root/environment.txt"
    echo
    echo 'Full parity exit codes: base Go SIMD / candidate Go SIMD'
    echo
    printf '%s / %s\n' \
      "$(cat "$artifact_root/baseline-simd-full-parity.exit")" \
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
          if [[ "$side" == baseline ]]; then
            sed -n '/CBR Byte Parity Summary/,/pass=.*arch=/p' "$artifact_root/$side-$mode-cbr-parity.log" | tail -n 22
          else
            grep -E 'strict paired CBR summary:|exact paired CBR mismatch:' "$artifact_root/$side-$mode-cbr-parity.log" || true
          fi
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
    echo '### Native AMD64 end-to-end benchmarks'
    echo
    for side in baseline candidate; do
      modes=(default simd)
      for mode in "${modes[@]}"; do
        echo "#### $side / $mode"
        echo
        if [[ -f "$artifact_root/$side-$mode-e2e-benchmarks.log" ]]; then
          grep -E '^Benchmark|^PASS|^FAIL' "$artifact_root/$side-$mode-e2e-benchmarks.log" || true
        else
          echo 'End-to-end benchmarks did not run.'
        fi
        echo
      done
    done
  } >> "$summary_file"
fi

python3 "$candidate_root/scripts/compare_simd_ab.py" "$artifact_root"
