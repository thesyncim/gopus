#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RESULTS_FILE_DEFAULT="$ROOT_DIR/results.tsv"
LOG_DIR_DEFAULT="$ROOT_DIR/reports/autoresearch"
PROGRAM_FILE="$ROOT_DIR/program.md"
BENCH_INPUT_DIR="$LOG_DIR_DEFAULT"
BENCH_SAMPLE_DEFAULT="${AUTORESEARCH_SAMPLE:-speech}"
BENCH_ITERS_DEFAULT="${AUTORESEARCH_ITERS:-2}"
BENCH_WARMUP_DEFAULT="${AUTORESEARCH_WARMUP:-1}"
BENCH_BITRATE_DEFAULT="${AUTORESEARCH_BITRATE:-64000}"
BENCH_COMPLEXITY_DEFAULT="${AUTORESEARCH_COMPLEXITY:-10}"
PARITY_REGEX_DEFAULT="${AUTORESEARCH_PARITY_REGEX:-TestSILKParamTraceAgainstLibopus|TestEncoderComplianceSummary}"
RESULTS_HEADER=$'commit\tparity\tbenchguard\tgopus_avg_rt\tlibopus_avg_rt\trt_ratio\tstatus\tdescription'

cd "$ROOT_DIR"

usage() {
  cat <<'EOF'
Usage:
  tools/autoresearch.sh init [--results path]
  tools/autoresearch.sh preflight [--results path]
  tools/autoresearch.sh best [--results path]
  tools/autoresearch.sh eval [--results path] [--description text] [--sample speech|stereo] [--iters N] [--warmup N] [--bitrate bps] [--complexity N]
  tools/autoresearch.sh loop [--results path] [--max-iterations N] [--model MODEL] [--dry-run]
EOF
}

die() {
  echo "autoresearch: $*" >&2
  exit 1
}

sanitize_description() {
  tr '\t\r\n' '   ' <<<"${1:-}" | sed -E 's/[[:space:]]+/ /g; s/^ //; s/ $//'
}

ensure_results_header() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    printf "%s\n" "$RESULTS_HEADER" >"$path"
    return 0
  fi

  local header
  header="$(head -n 1 "$path" || true)"
  if [[ "$header" != "$RESULTS_HEADER" ]]; then
    die "unexpected results.tsv header in $path"
  fi
}

require_file() {
  local path="$1"
  [[ -f "$path" ]] || die "missing required file: $path"
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
  local row
  row="$(best_success_row "$path")"
  if [[ -z "$row" ]]; then
    echo "best: none"
    return 0
  fi

  IFS=$'\t' read -r commit parity benchguard gopus libopus ratio status description <<<"$row"
  echo "best: commit=$commit status=$status rt_ratio=$ratio gopus_avg_rt=$gopus libopus_avg_rt=$libopus desc=$description"
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
  cat <<EOF
You are running exactly one autonomous autoresearch iteration in this repository.

Read and follow these files first:
- program.md
- AGENTS.md
- README.md

Mandatory rules for this one iteration:
1. Work on exactly one small experiment in one editable surface.
2. Make the code change.
3. Commit the experiment before evaluation using a conventional commit message.
4. Run: make autoresearch-eval DESCRIPTION='<short experiment note>'
5. Inspect the appended row in results.tsv.
6. If the result status is discard or crash, reset the repository back to START_COMMIT.
7. If the result status is keep or baseline, stay on the new commit.
8. Do not edit results.tsv or reports/autoresearch/ manually.
9. Finish with exactly one summary line in this format:
   outcome=<status> commit=<short> description=<description>

Context:
- START_COMMIT=$start_commit
- CURRENT_BEST=$best_summary

Do not start a second experiment in this session.
EOF
}

cmd_init() {
  local results="$RESULTS_FILE_DEFAULT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  ensure_results_header "$results"
  mkdir -p "$LOG_DIR_DEFAULT"
  echo "initialized results ledger: $results"
}

cmd_preflight() {
  local results="$RESULTS_FILE_DEFAULT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  require_file "$PROGRAM_FILE"
  require_file "$ROOT_DIR/AGENTS.md"
  require_file "$ROOT_DIR/README.md"
  require_file "$ROOT_DIR/tools/benchguard/main.go"
  require_file "$ROOT_DIR/tools/bench_guardrails.json"

  echo "== gopus autoresearch preflight =="
  echo "repo: $(basename "$ROOT_DIR")"
  echo "branch: $(git -C "$ROOT_DIR" rev-parse --abbrev-ref HEAD)"
  echo "head: $(git -C "$ROOT_DIR" rev-parse --short HEAD)"

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
    ensure_results_header "$results"
    echo "results ledger: $results"
  else
    echo "results ledger: missing (run: make autoresearch-init)"
  fi

  echo "fixed parity regex: $PARITY_REGEX_DEFAULT"
  echo "bench harness sample: $BENCH_SAMPLE_DEFAULT"
  print_best_summary "$results"
}

cmd_best() {
  local results="$RESULTS_FILE_DEFAULT"
  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  ensure_results_header "$results"
  print_best_summary "$results"
}

cmd_eval() {
  local results="$RESULTS_FILE_DEFAULT"
  local description=""
  local sample="$BENCH_SAMPLE_DEFAULT"
  local iters="$BENCH_ITERS_DEFAULT"
  local warmup="$BENCH_WARMUP_DEFAULT"
  local bitrate="$BENCH_BITRATE_DEFAULT"
  local complexity="$BENCH_COMPLEXITY_DEFAULT"

  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  ensure_results_header "$results"
  mkdir -p "$LOG_DIR_DEFAULT"

  if [[ -z "$description" ]]; then
    description="$(git -C "$ROOT_DIR" log -1 --pretty=%s)"
  fi
  description="$(sanitize_description "$description")"
  [[ -n "$description" ]] || description="experiment"

  local commit log_file input_path parity benchguard gopus_avg_rt libopus_avg_rt rt_ratio status
  local best_row best_commit best_ratio

  commit="$(git_commit_label)"
  log_file="$LOG_DIR_DEFAULT/$(date -u +%Y%m%dT%H%M%SZ)-${commit//+/_}.log"
  input_path="$(ensure_cached_input "$sample")"
  parity="FAIL"
  benchguard="SKIP"
  gopus_avg_rt="0.000000"
  libopus_avg_rt="0.000000"
  rt_ratio="0.000000"
  status="crash"
  best_row="$(best_success_row "$results")"
  best_commit=""
  best_ratio="0.000000"
  if [[ -n "$best_row" ]]; then
    IFS=$'\t' read -r best_commit _ _ _ _ best_ratio _ _ <<<"$best_row"
  fi

  echo "autoresearch: writing log to $log_file"

  if ! run_logged "$log_file" make ensure-libopus; then
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    die "ensure-libopus failed; see $log_file"
  fi

  if ! run_logged "$log_file" env GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 go test ./testvectors -run "$PARITY_REGEX_DEFAULT" -count=1; then
    status="discard"
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    echo "result: status=$status parity=$parity benchguard=$benchguard log=$log_file"
    [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit rt_ratio=$best_ratio"
    return 0
  fi
  parity="PASS"

  if ! run_logged "$log_file" make bench-guard; then
    benchguard="FAIL"
    status="discard"
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    echo "result: status=$status parity=$parity benchguard=$benchguard log=$log_file"
    [[ -n "$best_commit" ]] && echo "best_success_before: commit=$best_commit rt_ratio=$best_ratio"
    return 0
  fi
  benchguard="PASS"

  if ! run_logged "$log_file" env GOWORK=off go run ./examples/bench-encode -in "$input_path" -iters "$iters" -warmup "$warmup" -mode both -bitrate "$bitrate" -complexity "$complexity"; then
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    die "bench harness failed; see $log_file"
  fi

  gopus_avg_rt="$(extract_avg_rt 'gopus' "$log_file")" || {
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    die "failed to parse gopus avg realtime from $log_file"
  }
  libopus_avg_rt="$(extract_avg_rt 'libopus\(opus_demo\)' "$log_file")" || {
    append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
    die "failed to parse libopus avg realtime from $log_file"
  }
  rt_ratio="$(awk -v g="$gopus_avg_rt" -v l="$libopus_avg_rt" 'BEGIN { if (l <= 0) { print "0.000000"; exit 0 } printf "%.6f\n", g / l }')"

  if [[ -z "$best_row" ]]; then
    status="baseline"
  elif awk -v current="$rt_ratio" -v best="$best_ratio" 'BEGIN { exit !(current > best) }'; then
    status="keep"
  else
    status="discard"
  fi

  append_result_row "$results" "$commit" "$parity" "$benchguard" "$gopus_avg_rt" "$libopus_avg_rt" "$rt_ratio" "$status" "$description"
  echo "result: status=$status parity=$parity benchguard=$benchguard gopus_avg_rt=$gopus_avg_rt libopus_avg_rt=$libopus_avg_rt rt_ratio=$rt_ratio log=$log_file"
  if [[ -n "$best_commit" ]]; then
    echo "best_success_before: commit=$best_commit rt_ratio=$best_ratio"
  fi
}

cmd_loop() {
  local results="$RESULTS_FILE_DEFAULT"
  local max_iterations=""
  local model=""
  local dry_run=0

  while [[ $# -gt 0 ]]; do
    case "$1" in
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

  ensure_results_header "$results"
  require_clean_worktree
  command -v codex >/dev/null 2>&1 || die "codex CLI not found in PATH"

  local iteration=0
  while :; do
    iteration=$((iteration + 1))
    if [[ -n "$max_iterations" ]] && (( iteration > max_iterations )); then
      echo "autoresearch: loop finished after $((iteration - 1)) iteration(s)"
      break
    fi

    local start_commit start_count best_summary prompt_file agent_log agent_msg codex_status
    local row end_commit status

    start_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"
    start_count="$(results_row_count "$results")"
    best_summary="$(best_success_row "$results")"
    if [[ -z "$best_summary" ]]; then
      best_summary="none"
    fi

    prompt_file="$(mktemp)"
    agent_log="$LOG_DIR_DEFAULT/loop-$(date -u +%Y%m%dT%H%M%SZ)-${iteration}.log"
    agent_msg="$LOG_DIR_DEFAULT/loop-$(date -u +%Y%m%dT%H%M%SZ)-${iteration}-last.txt"
    mkdir -p "$LOG_DIR_DEFAULT"
    build_loop_prompt "$start_commit" "$best_summary" >"$prompt_file"

    echo "autoresearch: starting loop iteration $iteration from $(git -C "$ROOT_DIR" rev-parse --short "$start_commit")"
    if [[ "$dry_run" -eq 1 ]]; then
      cat "$prompt_file"
      rm -f "$prompt_file"
      echo "autoresearch: dry-run only; stopping before codex exec"
      break
    fi

    codex_status=0
    if [[ -n "$model" ]]; then
      codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -m "$model" -o "$agent_msg" - <"$prompt_file" >"$agent_log" 2>&1 || codex_status=$?
    else
      codex exec --dangerously-bypass-approvals-and-sandbox -C "$ROOT_DIR" -o "$agent_msg" - <"$prompt_file" >"$agent_log" 2>&1 || codex_status=$?
    fi
    rm -f "$prompt_file"

    if (( codex_status != 0 )); then
      die "codex exec failed in iteration $iteration; see $agent_log"
    fi

    if [[ "$(results_row_count "$results")" -le "$start_count" ]]; then
      die "iteration $iteration did not append a results row; see $agent_log"
    fi

    row="$(latest_result_row "$results")"
    IFS=$'\t' read -r _ _ _ _ _ _ status _ <<<"$row"
    end_commit="$(git -C "$ROOT_DIR" rev-parse HEAD)"

    if [[ "$status" == "discard" || "$status" == "crash" ]]; then
      if [[ "$end_commit" != "$start_commit" ]]; then
        git -C "$ROOT_DIR" reset --hard "$start_commit" >/dev/null
        echo "autoresearch: reset to $(git -C "$ROOT_DIR" rev-parse --short "$start_commit") after $status"
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
