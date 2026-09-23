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

run_side() {
  local side="$1" root="$2"
  run_phase "$side" "$root" ensure-libopus make ensure-libopus
  if [[ "$(cat "$artifact_root/$side-ensure-libopus.exit")" != 0 ]]; then
    return 0
  fi

  run_phase "$side" "$root" platform-fixtures make fixtures-gen-platform
  if [[ "$(cat "$artifact_root/$side-platform-fixtures.exit")" != 0 ]]; then
    return 0
  fi

  run_phase "$side" "$root" cbr-parity env \
    GOEXPERIMENT=simd \
    GOPUS_TEST_TIER=parity \
    GOPUS_STRICT_LIBOPUS_REF=1 \
    go test ./testvectors \
      -run '^TestEncoderCBRByteParitySummary$' \
      -count=1 -timeout=25m -v

  run_phase "$side" "$root" precision-guard env \
    GOEXPERIMENT=simd \
    GOPUS_REQUIRE_PLATFORM_FIXTURES=1 \
    GOPUS_TEST_TIER=exhaustive \
    GOPUS_STRICT_LIBOPUS_REF=1 \
    go test -tags gopus_libopus_oracle ./testvectors \
      -run '^TestEncoderCompliancePrecisionGuard$/^Hybrid-FB-20ms-stereo-96k$' \
      -count=1 -timeout=20m -v

  # These direct benches cover amd64 SIMD kernel families with benchmark cases
  # shared by the base and candidate snapshots. Their outputs complement the
  # separate 53-symbol report; they do not stand in for that full inventory.
  run_phase "$side" "$root" kernel-benchmarks env \
    GOEXPERIMENT=simd \
    go test ./internal/celt ./internal/silk \
      -run '^$' \
      -bench '^(BenchmarkInnerProd8FMA32|BenchmarkXcorrF32|BenchmarkInnerProductFLP|BenchmarkCeltPitchXcorrFloat|BenchmarkXcorrKernelFloat)' \
      -benchmem -count=5 -timeout=20m
}

run_side baseline "$baseline_root"
run_side candidate "$candidate_root"

summary_file="${GITHUB_STEP_SUMMARY:-}"
if [[ -n "$summary_file" ]]; then
  {
    echo '## Native Linux AMD64 SIMD A/B'
    echo
    echo 'Both checkouts use this runner, Go 1.27.1, and the pinned libopus 1.6.1 build. Full command output is attached as an artifact.'
    echo
    cat "$artifact_root/environment.txt"
    echo
    echo '| Checkout | Setup | Platform fixtures | CBR matrix | Precision case | Direct CELT benchmarks |'
    echo '| --- | ---: | ---: | ---: | ---: | ---: |'
    for side in baseline candidate; do
      values=()
      for phase in ensure-libopus platform-fixtures cbr-parity precision-guard kernel-benchmarks; do
        if [[ -f "$artifact_root/$side-$phase.exit" ]]; then
          values+=("$(cat "$artifact_root/$side-$phase.exit")")
        else
          values+=(not-run)
        fi
      done
      printf '| %s | %s | %s | %s | %s | %s |\n' "$side" "${values[@]}"
    done
    echo
    echo '### CBR summaries'
    echo
    for side in baseline candidate; do
      echo "#### $side"
      echo
      if [[ -f "$artifact_root/$side-cbr-parity.log" ]]; then
        sed -n '/CBR Byte Parity Summary/,/pass=.*arch=/p' "$artifact_root/$side-cbr-parity.log" | tail -n 22
      else
        echo 'CBR test did not run.'
      fi
      echo
    done
    echo '### Precision case'
    echo
    for side in baseline candidate; do
      echo "#### $side"
      echo
      if [[ -f "$artifact_root/$side-precision-guard.log" ]]; then
        grep -E 'RealContent (gopus|libopus) Q=|precision regression|PASS|FAIL|gap guard skipped' \
          "$artifact_root/$side-precision-guard.log" || true
      else
        echo 'Precision test did not run.'
      fi
      echo
    done
    echo '### Native AMD64 kernel benchmarks'
    echo
    echo 'These cover shared direct CELT and SILK microbenchmarks. The full 53-symbol inventory remains in the kernel evidence report.'
    echo
    for side in baseline candidate; do
      echo "#### $side"
      echo
      if [[ -f "$artifact_root/$side-kernel-benchmarks.log" ]]; then
        grep -E '^Benchmark|^PASS|^FAIL' "$artifact_root/$side-kernel-benchmarks.log" || true
      else
        echo 'Kernel benchmarks did not run.'
      fi
      echo
    done
  } >> "$summary_file"
fi

exit 0
