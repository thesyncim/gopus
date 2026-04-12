#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LOG_DIR_DEFAULT="$ROOT_DIR/reports/autoresearch"
PROGRAM_FILE="$ROOT_DIR/program.md"
BENCH_INPUT_DIR="$LOG_DIR_DEFAULT"
AUTORESEARCH_FOCUS_DEFAULT="${AUTORESEARCH_FOCUS:-mixed}"
QUALITY_TEST_TARGET_DEFAULT="${AUTORESEARCH_QUALITY_TARGET:-test-quality}"
UNIMPLEMENTED_SEED_DEFAULT="${AUTORESEARCH_UNIMPLEMENTED_SEED:-mix-arrivals-f32wav}"
BENCH_SAMPLE_DEFAULT="${AUTORESEARCH_SAMPLE:-speech}"
BENCH_ITERS_DEFAULT="${AUTORESEARCH_ITERS:-2}"
BENCH_WARMUP_DEFAULT="${AUTORESEARCH_WARMUP:-1}"
BENCH_BITRATE_DEFAULT="${AUTORESEARCH_BITRATE:-64000}"
BENCH_COMPLEXITY_DEFAULT="${AUTORESEARCH_COMPLEXITY:-10}"
PARITY_REGEX_DEFAULT="${AUTORESEARCH_PARITY_REGEX:-TestSILKParamTraceAgainstLibopus|TestEncoderComplianceSummary}"
PERFORMANCE_RESULTS_HEADER=$'commit\tparity\tbenchguard\tgopus_avg_rt\tlibopus_avg_rt\trt_ratio\tstatus\tdescription'
QUALITY_RESULTS_HEADER=$'commit\tquality\tbenchguard\tquality_mean_gap_db\tquality_min_gap_db\tscore\tstatus\tdescription'

cd "$ROOT_DIR"

usage() {
  cat <<'EOF'
Usage:
  tools/autoresearch.sh init [--focus performance|quality|unimplemented|mixed] [--results path]
  tools/autoresearch.sh preflight [--focus performance|quality|unimplemented|mixed] [--results path]
  tools/autoresearch.sh best [--focus performance|quality|unimplemented|mixed] [--results path]
  tools/autoresearch.sh eval [--focus performance|quality|unimplemented|mixed] [--results path] [--description text] [--sample speech|stereo] [--iters N] [--warmup N] [--bitrate bps] [--complexity N]
  tools/autoresearch.sh loop [--focus performance|quality|unimplemented|mixed] [--results path] [--max-iterations N] [--model MODEL] [--verbose] [--dry-run]
EOF
}

die() {
  echo "autoresearch: $*" >&2
  exit 1
}

sanitize_description() {
  tr '\t\r\n' '   ' <<<"${1:-}" | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//'
}

results_header_for_focus() {
  case "$1" in
  performance)
    printf "%s\n" "$PERFORMANCE_RESULTS_HEADER"
    ;;
  quality|unimplemented|mixed)
    printf "%s\n" "$QUALITY_RESULTS_HEADER"
    ;;
  *)
    die "invalid focus '$1' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac
}

ensure_results_header() {
  local path="$1"
  local focus="$2"
  local expected_header
  expected_header="$(results_header_for_focus "$focus")"
  if [[ ! -f "$path" || ! -s "$path" ]]; then
    printf "%s\n" "$expected_header" >"$path"
    return 0
  fi

  local header
  header="$(head -n 1 "$path" || true)"
  if [[ "$header" != "$expected_header" ]]; then
    die "unexpected results.tsv header in $path"
  fi
}

require_file() {
  local path="$1"
  [[ -f "$path" ]] || die "missing required file: $path"
}

current_branch_name() {
  git -C "$ROOT_DIR" symbolic-ref -q --short HEAD || true
}

lowercase_ascii() {
  printf "%s\n" "${1:-}" | tr '[:upper:]' '[:lower:]'
}

normalize_focus() {
  local focus="${1:-}"
  focus="$(lowercase_ascii "$focus")"
  case "$focus" in
  performance|quality|unimplemented|mixed)
    printf "%s\n" "$focus"
    ;;
  "")
    printf "%s\n" "$AUTORESEARCH_FOCUS_DEFAULT"
    ;;
  *)
    die "invalid focus '$focus' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac
}

default_results_path_for_focus() {
  case "$1" in
  performance)
    echo "$ROOT_DIR/results.tsv"
    ;;
  quality)
    echo "$ROOT_DIR/results.quality.tsv"
    ;;
  unimplemented)
    echo "$ROOT_DIR/results.unimplemented.tsv"
    ;;
  mixed)
    echo "$ROOT_DIR/results.mixed.tsv"
    ;;
  *)
    die "invalid focus '$1' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac
}

resolve_results_path() {
  local focus="$1"
  local results="$2"
  if [[ -n "$results" ]]; then
    printf "%s\n" "$results"
    return 0
  fi
  default_results_path_for_focus "$focus"
}

focus_description() {
  case "$1" in
  performance)
    echo "performance-first"
    ;;
  quality)
    echo "quality-first"
    ;;
  unimplemented)
    echo "unimplemented-first"
    ;;
  mixed)
    echo "quality-first mixed"
    ;;
  *)
    die "invalid focus '$1' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac
}

unimplemented_lane_summary() {
  echo "allowlisted seed: $UNIMPLEMENTED_SEED_DEFAULT"
}

count_allowlisted_unimplemented_items() {
  local count=0
  case "$UNIMPLEMENTED_SEED_DEFAULT" in
  mix-arrivals-f32wav)
    if rg -q 'unsupported wav format %d \(only PCM=1\)' "$ROOT_DIR/examples/mix-arrivals/speech_tracks.go" ||
      rg -q 'unsupported bits-per-sample %d \(only 16\)' "$ROOT_DIR/examples/mix-arrivals/speech_tracks.go"; then
      count=$((count + 1))
    fi
    ;;
  *)
    die "invalid unimplemented seed '$UNIMPLEMENTED_SEED_DEFAULT'"
    ;;
  esac
  printf "%s\n" "$count"
}

run_unimplemented_lane_checks() {
  local log_file="$1"
  case "$UNIMPLEMENTED_SEED_DEFAULT" in
  mix-arrivals-f32wav)
    run_logged "$log_file" env GOWORK=off go test ./examples/mix-arrivals -count=1
    ;;
  *)
    die "invalid unimplemented seed '$UNIMPLEMENTED_SEED_DEFAULT'"
    ;;
  esac
}

sample_url() {
  case "$1" in
  speech)
    echo "https://upload.wikimedia.org/wikipedia/commons/6/6a/Hussain_Ahmad_Madani%27s_Voice.ogg"
    ;;
  stereo)
    echo "https://opus-codec.org/static/examples/ehren-paper_lights-96.opus"
    ;;
  *)
    return 1
    ;;
  esac
}

sample_ext() {
  case "$1" in
  speech)
    echo "ogg"
    ;;
  stereo)
    echo "opus"
    ;;
  *)
    return 1
    ;;
  esac
}

ensure_cached_input() {
  local sample="$1"
  local url ext path
  url="$(sample_url "$sample")" || die "unknown sample '$sample' (valid: speech, stereo)"
  ext="$(sample_ext "$sample")"
  mkdir -p "$BENCH_INPUT_DIR"
  path="$BENCH_INPUT_DIR/$sample.$ext"
  if [[ ! -s "$path" ]]; then
    echo "autoresearch: caching $sample sample at $path" >&2
    curl -L --fail --silent --show-error "$url" -o "$path"
  fi
  printf "%s\n" "$path"
}

extract_quality_stats() {
  local log_file="$1"
  awk '
    /encoder_compliance_test.go:[0-9]+:/ && $NF ~ /^(GOOD|BASE|PASS)$/ {
      gap = $(NF-1) + 0
      sum += gap
      count += 1
      if (!seen || gap < min_gap) {
        min_gap = gap
        seen = 1
      }
    }
    END {
      if (seen != 1) {
        exit 1
      }
      printf "%.6f\t%.6f\n", sum / count, min_gap
    }
  ' "$log_file"
}

extract_decoder_transition_min_snr() {
  local log_file="$1"
  awk '
    /decoder_transition_parity_test.go:[0-9]+:/ && /transition frame=/ {
      match($0, /snr=[-0-9.]+ dB/)
      if (RSTART > 0) {
        snr = substr($0, RSTART + 4, RLENGTH - 7) + 0
        if (!seen || snr < min_snr) {
          min_snr = snr
          seen = 1
        }
      }
    }
    END {
      if (seen == 1) {
        printf "%.6f\n", min_snr
      } else {
        printf "0.000000\n"
      }
    }
  ' "$log_file"
}

quality_score_for_focus() {
  local focus="$1"
  local quality_mean_gap="$2"
  local quality_min_gap="$3"
  local decoder_transition_min_snr="$4"
  local backlog_count="$5"
  case "$focus" in
  quality)
    awk -v q="$quality_mean_gap" -v transition="$decoder_transition_min_snr" 'BEGIN { printf "%.6f\n", q + (transition / 1000.0) }'
    ;;
  unimplemented)
    awk -v backlog="$backlog_count" -v q="$quality_mean_gap" 'BEGIN { printf "%.6f\n", (0 - backlog) + (q / 100000.0) }'
    ;;
  mixed)
    awk -v q="$quality_mean_gap" -v minq="$quality_min_gap" -v transition="$decoder_transition_min_snr" -v backlog="$backlog_count" 'BEGIN { printf "%.6f\n", q + (minq / 1000.0) + (transition / 1000.0) - (backlog / 100.0) }'
    ;;
  *)
    die "invalid focus '$focus' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac
}

best_success_row() {
  local path="$1"
  [[ -f "$path" ]] || return 0
  awk -F'\t' '
    NR == 1 { next }
    ($2 == "PASS" && $3 == "PASS" && ($7 == "baseline" || $7 == "keep")) {
      if (seen == 0 || ($6 + 0) > best) {
        best = $6 + 0
        row = $0
        seen = 1
      }
    }
    END {
      if (seen == 1) {
        print row
      }
    }
  ' "$path"
}

print_best_summary() {
  local path="$1"
  local focus="$2"
  local row
  row="$(best_success_row "$path")"
  if [[ -z "$row" ]]; then
    echo "best: none"
    return 0
  fi

  local gate metric_a metric_b score status description commit
  IFS=$'\t' read -r commit gate _ metric_a metric_b score status description <<<"$row"
  if [[ "$focus" == "performance" ]]; then
    echo "best: commit=$commit status=$status rt_ratio=$score gopus_avg_rt=$metric_a libopus_avg_rt=$metric_b desc=$description"
  else
    echo "best: commit=$commit status=$status score=$score quality_mean_gap_db=$metric_a quality_min_gap_db=$metric_b desc=$description"
  fi
}

git_commit_label() {
  local short
  short="$(git -C "$ROOT_DIR" rev-parse --short HEAD)"
  if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules --exit-code || ! git -C "$ROOT_DIR" diff --cached --quiet --ignore-submodules --exit-code; then
    short="${short}+dirty"
  fi
  printf "%s\n" "$short"
}

run_logged() {
  local log_file="$1"
  shift
  {
    printf '$'
    for arg in "$@"; do
      printf ' %q' "$arg"
    done
    printf '\n'
  } >>"$log_file"
  "$@" >>"$log_file" 2>&1
}

latest_log_snippet() {
  local log_file="$1"
  [[ -f "$log_file" ]] || return 0
  awk '
    NF { line=$0 }
    END {
      if (line != "") {
        gsub(/[[:space:]]+/, " ", line)
        print line
      }
    }
  ' "$log_file"
}

gh_auth_ready() {
  command -v gh >/dev/null 2>&1 || return 1
  gh auth status >/dev/null 2>&1
}

branch_claim_summary() {
  local branch="$1"
  gh_auth_ready || return 0
  gh pr list --head "$branch" --state open --limit 1 --json number,isDraft,title,url --jq 'if length == 0 then "" else .[0] | "\(.number)\t\(.isDraft)\t\(.url)\t\(.title)" end'
}

print_claim_status() {
  local branch="$1"
  local summary pr_number is_draft url title

  if [[ "${AUTORESEARCH_ALLOW_LOCAL_ONLY:-0}" == "1" ]]; then
    echo "claim surface: local-only bypass enabled (AUTORESEARCH_ALLOW_LOCAL_ONLY=1)"
    return 0
  fi

  if [[ -z "$branch" ]]; then
    echo "claim surface: missing (detached HEAD has no shared branch claim)"
    return 0
  fi

  if ! gh_auth_ready; then
    echo "claim surface: unknown (gh auth unavailable; use tools/prepare_claim_pr.sh to open a draft PR claim before editable loop work)"
    return 0
  fi

  summary="$(branch_claim_summary "$branch")"
  if [[ -z "$summary" ]]; then
    echo "claim surface: missing open draft PR for $branch"
    return 0
  fi

  IFS=$'\t' read -r pr_number is_draft url title <<<"$summary"
  if [[ "$is_draft" == "true" ]]; then
    echo "claim surface: draft PR #$pr_number $url"
  else
    echo "claim surface: open PR #$pr_number $url (convert to draft for active editable work)"
  fi
}

require_shared_claim() {
  local branch="$1"
  local summary pr_number is_draft url title

  if [[ "${AUTORESEARCH_ALLOW_LOCAL_ONLY:-0}" == "1" ]]; then
    return 0
  fi

  [[ -n "$branch" ]] || die "editable loop must start from a named branch with a shared claim surface"
  gh_auth_ready || die "gh auth is required to verify the shared draft PR claim; set AUTORESEARCH_ALLOW_LOCAL_ONLY=1 only for confirmed single-researcher local runs"

  summary="$(branch_claim_summary "$branch")"
  [[ -n "$summary" ]] || die "no open draft PR claim found for $branch; create one before starting the loop with tools/prepare_claim_pr.sh"

  IFS=$'\t' read -r pr_number is_draft url title <<<"$summary"
  [[ "$is_draft" == "true" ]] || die "branch $branch has open PR #$pr_number ($url) but it is not draft; use a draft PR as the shared claim surface"
}

run_codex_with_heartbeat() {
  local log_file="$1"
  shift

  # Preserve the caller's stdin so `codex exec - <prompt_file` still receives
  # the prompt when we background the process for heartbeat logging.
  "$@" <&0 >"$log_file" 2>&1 &
  local cmd_pid=$!
  local elapsed=0

  while kill -0 "$cmd_pid" >/dev/null 2>&1; do
    sleep 10
    elapsed=$((elapsed + 10))
    if ! kill -0 "$cmd_pid" >/dev/null 2>&1; then
      break
    fi
    local snippet
    snippet="$(latest_log_snippet "$log_file")"
    if [[ -n "$snippet" ]]; then
      echo "autoresearch: codex still running (${elapsed}s); latest log: $snippet"
    else
      echo "autoresearch: codex still running (${elapsed}s); waiting for first log output"
    fi
  done

  wait "$cmd_pid"
}

extract_avg_rt() {
  local pattern="$1"
  local log_file="$2"
  local line value
  line="$(grep -E "^${pattern}: " "$log_file" | tail -n 1 || true)"
  [[ -n "$line" ]] || return 1
  value="$(sed -E 's/.*avg [^ ]+ \(([0-9.]+)x\).*/\1/' <<<"$line")"
  [[ "$value" =~ ^[0-9]+([.][0-9]+)?$ ]] || return 1
  printf "%s\n" "$value"
}

append_result_row() {
  local path="$1"
  local commit="$2"
  local parity="$3"
  local benchguard="$4"
  local gopus="$5"
  local libopus="$6"
  local ratio="$7"
  local status="$8"
  local description="$9"
  printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
    "$commit" "$parity" "$benchguard" "$gopus" "$libopus" "$ratio" "$status" "$description" >>"$path"
}

format_best_summary() {
  local row="$1"
  local focus="$2"
  if [[ -z "$row" || "$row" == "none" ]]; then
    echo "none"
    return 0
  fi

  local commit gate metric_a metric_b score status description
  IFS=$'\t' read -r commit gate _ metric_a metric_b score status description <<<"$row"
  if [[ "$focus" == "performance" ]]; then
    echo "commit=$commit status=$status rt_ratio=$score gopus_avg_rt=$metric_a libopus_avg_rt=$metric_b desc=$description"
  else
    echo "commit=$commit status=$status score=$score quality_mean_gap_db=$metric_a quality_min_gap_db=$metric_b desc=$description"
  fi
}

results_row_count() {
  local path="$1"
  [[ -f "$path" ]] || {
    echo 0
    return 0
  }
  awk 'NR > 1 { count++ } END { print count + 0 }' "$path"
}

latest_result_row() {
  local path="$1"
  [[ -f "$path" ]] || return 0
  awk 'NR > 1 { row = $0 } END { if (row != "") print row }' "$path"
}

require_clean_worktree() {
  if ! git -C "$ROOT_DIR" diff --quiet --ignore-submodules --exit-code; then
    die "tracked worktree changes detected; commit or stash them before starting the loop"
  fi
  if ! git -C "$ROOT_DIR" diff --cached --quiet --ignore-submodules --exit-code; then
    die "staged changes detected; commit or stash them before starting the loop"
  fi
}

build_loop_prompt() {
  local start_commit="$1"
  local best_summary="$2"
  local focus="$3"
  local start_branch="${4:-}"
  cat <<EOF
You are running exactly one autonomous autoresearch iteration in this repository.

Read and follow these files first:
- program.md
- AGENTS.md
- README.md

Mandatory rules for this one iteration:
1. Work on exactly one small experiment in one editable surface.
2. Refresh the draft PR claim before editing with the current blocker and next action.
3. Make the code change.
4. Commit the experiment before evaluation using a conventional commit message.
5. Run: make autoresearch-eval FOCUS=$focus DESCRIPTION='<short experiment note>'
6. Inspect the appended row in the focus-specific results ledger.
7. Update the draft PR claim with the attempt description, status, latest result row, blocker, and next action.
8. If the result status is discard or crash, reset the repository back to START_COMMIT.
9. If the result status is keep or baseline, stay on the new commit.
10. Do not edit the results ledger or reports/autoresearch/ manually.
11. Finish with exactly one summary line in this format:
   outcome=<status> commit=<short> description=<description>

Context:
- START_COMMIT=$start_commit
- START_BRANCH=$start_branch
- CURRENT_BEST=$best_summary
- FOCUS=$focus
- QUALITY_TARGET=$QUALITY_TEST_TARGET_DEFAULT
- UNIMPLEMENTED_SEED=$UNIMPLEMENTED_SEED_DEFAULT

Focus guidance:
- performance: improve the fair throughput comparison only.
- quality: improve the quality metrics from existing tests.
- unimplemented: work only on the allowlisted unimplemented seed and its test-backed surface.
- mixed: prefer quality improvements first, but it is valid to close the allowlisted unimplemented seed when the quality score stays flat.

If subagents are available, spawn two read-only scouts in parallel before choosing the edit:
- one quality/compliance scout
- one unimplemented-feature scout

Do not start a second experiment in this session.
EOF
}

cmd_init() {
  local focus="$AUTORESEARCH_FOCUS_DEFAULT"
  local results=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --focus)
      focus="$(normalize_focus "$2")"
      shift 2
      ;;
    --results)
      results="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
    esac
  done

  results="$(resolve_results_path "$focus" "$results")"
  ensure_results_header "$results" "$focus"
  mkdir -p "$LOG_DIR_DEFAULT"
  echo "initialized results ledger: $results (focus=$focus)"
}

cmd_preflight() {
  local focus="$AUTORESEARCH_FOCUS_DEFAULT"
  local results=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --focus)
      focus="$(normalize_focus "$2")"
      shift 2
      ;;
    --results)
      results="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
    esac
  done

  results="$(resolve_results_path "$focus" "$results")"
  require_file "$PROGRAM_FILE"
  require_file "$ROOT_DIR/AGENTS.md"
  require_file "$ROOT_DIR/README.md"
  require_file "$ROOT_DIR/tools/benchguard/main.go"
  require_file "$ROOT_DIR/tools/bench_guardrails.json"

  echo "== gopus autoresearch preflight =="
  echo "repo: $(basename "$ROOT_DIR")"
  echo "branch: $(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD)"
  echo "head: $(git -C "$ROOT_DIR" rev-parse --short HEAD)"
  echo "focus: $focus ($(focus_description "$focus"))"

  if [[ "$(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD)" != autoresearch/* ]]; then
    echo "branch check: WARN (recommended branch prefix is autoresearch/)"
  else
    echo "branch check: OK"
  fi

  if [[ -x "$ROOT_DIR/tmp_check/opus-1.6.1/opus_demo" ]]; then
    echo "libopus: OK"
  else
    echo "libopus: missing (run: make ensure-libopus)"
  fi

  if [[ -f "$results" ]]; then
    ensure_results_header "$results" "$focus"
    echo "results ledger: $results"
  else
    echo "results ledger: missing (run: make autoresearch-init FOCUS=$focus)"
  fi

  echo "performance parity regex: $PARITY_REGEX_DEFAULT"
  echo "quality target: make $QUALITY_TEST_TARGET_DEFAULT"
  echo "unimplemented lane: $(unimplemented_lane_summary)"
  echo "bench harness sample: $BENCH_SAMPLE_DEFAULT"
  print_claim_status "$(current_branch_name)"
  print_best_summary "$results" "$focus"
}

cmd_best() {
  local focus="$AUTORESEARCH_FOCUS_DEFAULT"
  local results=""
  while [[ $# -gt 0 ]]; do
    case "$1" in
    --focus)
      focus="$(normalize_focus "$2")"
      shift 2
      ;;
    --results)
      results="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
    esac
  done

  results="$(resolve_results_path "$focus" "$results")"
  ensure_results_header "$results" "$focus"
  print_best_summary "$results" "$focus"
}

cmd_eval() {
  local focus="$AUTORESEARCH_FOCUS_DEFAULT"
  local results=""
  local description=""
  local sample="$BENCH_SAMPLE_DEFAULT"
  local iters="$BENCH_ITERS_DEFAULT"
  local warmup="$BENCH_WARMUP_DEFAULT"
  local bitrate="$BENCH_BITRATE_DEFAULT"
  local complexity="$BENCH_COMPLEXITY_DEFAULT"

  while [[ $# -gt 0 ]]; do
    case "$1" in
    --focus)
      focus="$(normalize_focus "$2")"
      shift 2
      ;;
    --results)
      results="$2"
      shift 2
      ;;
    --description)
      description="$2"
      shift 2
      ;;
    --sample)
      sample="$2"
      shift 2
      ;;
    --iters)
      iters="$2"
      shift 2
      ;;
    --warmup)
      warmup="$2"
      shift 2
      ;;
    --bitrate)
      bitrate="$2"
      shift 2
      ;;
    --complexity)
      complexity="$2"
      shift 2
      ;;
    *)
      usage
      exit 1
      ;;
    esac
  done

  results="$(resolve_results_path "$focus" "$results")"
  ensure_results_header "$results" "$focus"
  mkdir -p "$LOG_DIR_DEFAULT"

  if [[ -z "$description" ]]; then
    description="$(git -C "$ROOT_DIR" log -1 --pretty=%s)"
  fi
  description="$(sanitize_description "$description")"
  [[ -n "$description" ]] || description="experiment"

  local commit log_file gate_status benchguard metric_a metric_b score status
  local best_row best_commit best_score
  local quality_log_file focus_note input_path
  local quality_mean_gap quality_min_gap decoder_transition_min_snr backlog_count

  commit="$(git_commit_label)"
  log_file="$LOG_DIR_DEFAULT/$(date -u +%Y%m%dT%H%M%SZ)-${commit//+/_}.log"
  gate_status="FAIL"
  benchguard="SKIP"
  metric_a="0.000000"
  metric_b="0.000000"
  score="0.000000"
  status="crash"
  best_row="$(best_success_row "$results")"
  best_commit=""
  best_score="0.000000"
  focus_note="$(focus_description "$focus")"
  quality_mean_gap="0.000000"
  quality_min_gap="0.000000"
  decoder_transition_min_snr="0.000000"
  backlog_count="0"
  if [[ -n "$best_row" ]]; then
    IFS=$'\t' read -r best_commit _ _ _ _ best_score _ _ <<<"$best_row"
  fi

  echo "autoresearch: writing log to $log_file"
  echo "autoresearch: focus=$focus ($focus_note)"

  if ! run_logged "$log_file" make ensure-libopus; then
    append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
    die "ensure-libopus failed; see $log_file"
  fi

  case "$focus" in
  performance)
    if ! run_logged "$log_file" env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run "$PARITY_REGEX_DEFAULT" -count=1; then
      status="discard"
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus log=$log_file"
      [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit score=$best_score"
      return 0
    fi
    gate_status="PASS"

    if ! run_logged "$log_file" make bench-guard; then
      benchguard="FAIL"
      status="discard"
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus log=$log_file"
      [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit score=$best_score"
      return 0
    fi
    benchguard="PASS"

    input_path="$(ensure_cached_input "$sample")"
    if ! run_logged "$log_file" env GOWORK=off go run ./examples/bench-encode -in "$input_path" -iters "$iters" -warmup "$warmup" -mode both -bitrate "$bitrate" -complexity "$complexity"; then
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      die "bench harness failed; see $log_file"
    fi

    metric_a="$(extract_avg_rt 'gopus' "$log_file")" || {
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      die "failed to parse gopus avg realtime from $log_file"
    }
    metric_b="$(extract_avg_rt 'libopus\(opus_demo\)' "$log_file")" || {
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      die "failed to parse libopus avg realtime from $log_file"
    }
    score="$(awk -v g="$metric_a" -v l="$metric_b" 'BEGIN { if (l <= 0) { print "0.000000"; exit 0 } printf "%.6f\n", g / l }')"
    ;;
  quality|unimplemented|mixed)
    quality_log_file="$log_file.quality"
    if ! run_logged "$quality_log_file" make "$QUALITY_TEST_TARGET_DEFAULT"; then
      status="discard"
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus log=$quality_log_file"
      [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit score=$best_score"
      return 0
    fi
    gate_status="PASS"

    if ! run_logged "$log_file" make bench-guard; then
      benchguard="FAIL"
      status="discard"
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus log=$log_file"
      [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit score=$best_score"
      return 0
    fi
    benchguard="PASS"

    if [[ "$focus" == "unimplemented" || "$focus" == "mixed" ]]; then
      if ! run_unimplemented_lane_checks "$log_file"; then
        status="discard"
        append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
        echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus log=$log_file"
        [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit score=$best_score"
        return 0
      fi
    fi

    IFS=$'\t' read -r quality_mean_gap quality_min_gap < <(extract_quality_stats "$quality_log_file") || {
      append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
      die "failed to parse quality score from $quality_log_file"
    }

    decoder_transition_min_snr="$(extract_decoder_transition_min_snr "$quality_log_file")"
    backlog_count="$(count_allowlisted_unimplemented_items)"
    metric_a="$quality_mean_gap"
    metric_b="$quality_min_gap"
    score="$(quality_score_for_focus "$focus" "$quality_mean_gap" "$quality_min_gap" "$decoder_transition_min_snr" "$backlog_count")"
    ;;
  *)
    die "invalid focus '$focus' (valid: performance, quality, unimplemented, mixed)"
    ;;
  esac

  if [[ -z "$best_row" ]]; then
    status="baseline"
  elif awk -v current="$score" -v best="$best_score" 'BEGIN { exit !(current > best) }'; then
    status="keep"
  else
    status="discard"
  fi

  append_result_row "$results" "$commit" "$gate_status" "$benchguard" "$metric_a" "$metric_b" "$score" "$status" "$description"
  if [[ "$focus" == "performance" ]]; then
    echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus rt_ratio=$score gopus_avg_rt=$metric_a libopus_avg_rt=$metric_b log=$log_file"
  else
    echo "result: status=$status gate=$gate_status benchguard=$benchguard focus=$focus score=$score quality_mean_gap_db=$metric_a quality_min_gap_db=$metric_b decoder_transition_min_snr_db=$decoder_transition_min_snr feature_backlog=$backlog_count log=$log_file"
  fi
  if [[ -n "$best_commit" ]]; then
    echo "best_success_before: commit=$best_commit score=$best_score"
  fi
}

cmd_loop() {
  local focus="$AUTORESEARCH_FOCUS_DEFAULT"
  local results=""
  local max_iterations=""
  local model=""
  local verbose=0
  local dry_run=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
    --focus)
      focus="$(normalize_focus "$2")"
      shift 2
      ;;
    --results)
      results="$2"
      shift 2
      ;;
    --max-iterations)
      max_iterations="$2"
      shift 2
      ;;
    --model)
      model="$2"
      shift 2
      ;;
    --verbose)
      verbose=1
      shift
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    *)
      usage
      exit 1
      ;;
    esac
  done

  results="$(resolve_results_path "$focus" "$results")"
  ensure_results_header "$results" "$focus"
  require_clean_worktree
  command -v codex >/dev/null 2>&1 || die "codex CLI not found in PATH"
  require_shared_claim "$(current_branch_name)"

  local iteration=0
  while :; do
    iteration=$((iteration + 1))
    if [[ -n "$max_iterations" ]] && (( iteration > max_iterations )); then
      echo "autoresearch: loop finished after $((iteration - 1)) iteration(s)"
      break
    fi

    local start_commit start_branch start_count best_summary prompt_file agent_log agent_msg codex_status
    local row end_commit current_branch status

    start_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
    start_branch="$(git -C "$ROOT_DIR" symbolic-ref -q --short HEAD || true)"
    start_count="$(results_row_count "$results")"
    best_summary="$(best_success_row "$results")"
    if [[ -z "$best_summary" ]]; then
      best_summary="none"
    fi
    local best_summary_human
    best_summary_human="$(format_best_summary "$best_summary" "$focus")"

    prompt_file="$(mktemp)"
    agent_log="$LOG_DIR_DEFAULT/loop-$(date -u +%Y%m%dT%H%M%SZ)-${iteration}.log"
    agent_msg="$LOG_DIR_DEFAULT/loop-$(date -u +%Y%m%dT%H%M%SZ)-${iteration}-last.txt"
    mkdir -p "$LOG_DIR_DEFAULT"
    build_loop_prompt "$start_commit" "$best_summary" "$focus" "$start_branch" >"$prompt_file"

    echo "autoresearch: starting loop iteration $iteration from $(git -C "$ROOT_DIR" rev-parse --short "$start_commit")"
    echo "autoresearch: focus=$focus ($(focus_description "$focus"))"
    echo "autoresearch: best before iteration: $best_summary_human"
    echo "autoresearch: codex log: $agent_log"
    echo "autoresearch: codex last message: $agent_msg"
    if [[ "$dry_run" -eq 1 ]]; then
      cat "$prompt_file"
      rm -f "$prompt_file"
      echo "autoresearch: dry-run only; stopping before codex exec"
      break
    fi

    codex_status=0
    echo "autoresearch: launching codex exec"
    if [[ -n "$model" ]]; then
      if [[ "$verbose" -eq 1 ]]; then
        codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -m "$model" -o "$agent_msg" - <"$prompt_file" 2>&1 | tee "$agent_log" || codex_status=${PIPESTATUS[0]}
      else
        run_codex_with_heartbeat "$agent_log" codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -m "$model" -o "$agent_msg" - <"$prompt_file" || codex_status=$?
      fi
    else
      if [[ "$verbose" -eq 1 ]]; then
        codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -o "$agent_msg" - <"$prompt_file" 2>&1 | tee "$agent_log" || codex_status=${PIPESTATUS[0]}
      else
        run_codex_with_heartbeat "$agent_log" codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -o "$agent_msg" - <"$prompt_file" || codex_status=$?
      fi
    fi
    rm -f "$prompt_file"

    if (( codex_status != 0 )); then
      die "codex exec failed in iteration $iteration; see $agent_log"
    fi
    echo "autoresearch: codex exec finished"

    if [[ "$(results_row_count "$results")" -le "$start_count" ]]; then
      die "iteration $iteration did not append a results row; see $agent_log"
    fi

    row="$(latest_result_row "$results")"
    IFS=$'\t' read -r _ _ _ _ _ _ status _ <<<"$row"
    end_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
    current_branch="$(git -C "$ROOT_DIR" symbolic-ref -q --short HEAD || true)"
    echo "autoresearch: latest results row: $row"

    if [[ "$status" == "discard" || "$status" == "crash" ]]; then
      if [[ -n "$start_branch" && "$current_branch" != "$start_branch" ]]; then
        git -C "$ROOT_DIR" branch -f "$start_branch" "$start_commit" >/dev/null
        git -C "$ROOT_DIR" switch "$start_branch" >/dev/null
        echo "autoresearch: restored branch $start_branch to $(git -C "$ROOT_DIR" rev-parse --short "$start_commit") after $status"
      elif [[ "$end_commit" != "$start_commit" ]]; then
        git -C "$ROOT_DIR" reset --hard "$start_commit" >/dev/null
        echo "autoresearch: reset to $(git -C "$ROOT_DIR" rev-parse --short "$start_commit") after $status"
      fi
    else
      if [[ -n "$start_branch" && "$current_branch" != "$start_branch" ]]; then
        git -C "$ROOT_DIR" branch -f "$start_branch" "$end_commit" >/dev/null
        git -C "$ROOT_DIR" switch "$start_branch" >/dev/null
        echo "autoresearch: reattached branch $start_branch at $(git -C "$ROOT_DIR" rev-parse --short "$end_commit")"
      else
        echo "autoresearch: keeping commit $(git -C "$ROOT_DIR" rev-parse --short "$end_commit")"
      fi
    fi

    echo "autoresearch: iteration $iteration finished with status=$status"
  done
}

main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
  init)
    cmd_init "$@"
    ;;
  preflight)
    cmd_preflight "$@"
    ;;
  best)
    cmd_best "$@"
    ;;
  eval)
    cmd_eval "$@"
    ;;
  loop)
    cmd_loop "$@"
    ;;
  *)
    usage
    exit 1
    ;;
  esac
}

main "$@"
