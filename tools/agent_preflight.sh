#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"

require_file() {
  local path="$1"
  if [[ ! -f "$path" ]]; then
    echo "ERROR: required file missing: $path"
    exit 1
  fi
}

extract_section_line() {
  local file="$1"
  local header="$2"
  awk -v h="$header" '
    $0 == h {in_section=1; next}
    /^## / && in_section {in_section=0}
    in_section && NF {print; exit}
  ' "$file"
}

require_file "AGENTS.md"
require_file ".planning/ACTIVE.md"
require_file ".planning/DECISIONS.md"
require_file ".planning/WORK_CLAIMS.md"

echo "== Gopus Agent Preflight =="
echo "repo: $(basename "$ROOT_DIR")"
echo "branch: $(git rev-parse --abbrev-ref HEAD)"
echo

objective="$(extract_section_line ".planning/ACTIVE.md" "## Objective")"
hypothesis="$(extract_section_line ".planning/ACTIVE.md" "## Current Hypothesis")"

echo "Active objective: ${objective:-<missing>}"
echo "Current hypothesis: ${hypothesis:-<missing>}"
echo

echo "Snapshot parity/compliance markers:"
rg -n "TestSILKParamTraceAgainstLibopus|TestEncoderComplianceSummary" AGENTS.md || true
echo

echo "Open debug investigations:"
open_debug=""
if [[ -d ".planning/debug" ]]; then
  open_debug="$(
    rg -l '^status:\s*(investigating|verifying|diagnosed|active|blocked)\b' .planning/debug -g '*.md' -g '!**/resolved/**' || true
  )"
fi
if [[ -z "$open_debug" ]]; then
  echo "- none"
else
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    echo "- $line"
  done <<<"$open_debug"
fi
echo

echo "Do-not-repeat decisions:"
awk '
  /^## Current Decisions/ {in_current=1; next}
  /^## / && in_current {in_current=0}
  in_current && (/^topic: / || /^do_not_repeat_until: /) {print NR ":" $0}
' .planning/DECISIONS.md
echo

echo "Active work claims:"
active_claims="$(grep -E '^- claim: .*status=active;' .planning/WORK_CLAIMS.md || true)"
if [[ -z "$active_claims" ]]; then
  echo "- none"
else
  echo "$active_claims"
fi
echo

declare -A seen_paths=()
conflicts=0

if [[ -n "$active_claims" ]]; then
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    claim_id="$(sed -n 's/.*id=\([^;]*\);.*/\1/p' <<<"$line" | xargs)"
    paths_csv="$(sed -n 's/.*paths=\([^;]*\);.*/\1/p' <<<"$line")"
    IFS=',' read -r -a paths <<<"$paths_csv"
    for raw_path in "${paths[@]}"; do
      path="$(echo "$raw_path" | xargs)"
      [[ -z "$path" ]] && continue
      if [[ -n "${seen_paths[$path]:-}" ]]; then
        echo "WARN: overlapping claim on '$path' between '$claim_id' and '${seen_paths[$path]}'"
        conflicts=1
      else
        seen_paths["$path"]="$claim_id"
      fi
    done
  done <<<"$active_claims"
fi

if [[ "$conflicts" -eq 1 ]]; then
  echo
  echo "Preflight result: WARN (overlapping active claims found)"
  echo "Tip: use 'make agent-claims' and 'make agent-release CLAIM_ID=<id>' to resolve overlaps."
  exit 0
fi

echo "Preflight result: OK"
echo "Tip: claim scope with 'make agent-claim AGENT=<name> PATHS=\"silk/,testvectors/\" NOTE=\"short note\"'"
