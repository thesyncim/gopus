#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CLAIMS_FILE="$ROOT_DIR/.planning/WORK_CLAIMS.md"

usage() {
  cat <<'EOF'
Usage:
  tools/agent_claim.sh list
  tools/agent_claim.sh claim <agent> <paths_csv> [note] [hours] [claim_id]
  tools/agent_claim.sh release <claim_id>

Examples:
  tools/agent_claim.sh claim codex "silk/,testvectors/" "SILK quality tuning"
  tools/agent_claim.sh release codex-20260212-190000
EOF
}

utc_now() {
  date -u +"%Y-%m-%dT%H:%M:%SZ"
}

utc_plus_hours() {
  local hours="$1"
  if date -u -d "+${hours} hours" +"%Y-%m-%dT%H:%M:%SZ" >/dev/null 2>&1; then
    date -u -d "+${hours} hours" +"%Y-%m-%dT%H:%M:%SZ"
    return 0
  fi
  date -u -v+"${hours}"H +"%Y-%m-%dT%H:%M:%SZ"
}

trim() {
  echo "$1" | xargs
}

extract_claim_field() {
  local line="$1"
  local key="$2"
  sed -n "s/.*${key}=\\([^;]*\\);.*/\\1/p" <<<"$line" | xargs
}

require_claims_file() {
  if [[ ! -f "$CLAIMS_FILE" ]]; then
    echo "ERROR: missing claims file: $CLAIMS_FILE"
    exit 1
  fi
}

active_claim_lines() {
  grep -E '^- claim: .*status=active;' "$CLAIMS_FILE" || true
}

warn_or_fail_overlap() {
  local new_claim_id="$1"
  local new_paths_csv="$2"
  local overlap=0

  local active
  active="$(active_claim_lines)"
  if [[ -z "$active" ]]; then
    return 0
  fi

  IFS=',' read -r -a new_paths <<<"$new_paths_csv"
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    local existing_id existing_paths_csv
    existing_id="$(extract_claim_field "$line" "id")"
    existing_paths_csv="$(extract_claim_field "$line" "paths")"
    IFS=',' read -r -a existing_paths <<<"$existing_paths_csv"

    for new_raw in "${new_paths[@]}"; do
      local new_path
      new_path="$(trim "$new_raw")"
      [[ -z "$new_path" ]] && continue
      for existing_raw in "${existing_paths[@]}"; do
        local existing_path
        existing_path="$(trim "$existing_raw")"
        [[ -z "$existing_path" ]] && continue
        if [[ "$new_path" == "$existing_path" ]]; then
          echo "WARN: claim overlap on '$new_path' with active claim '$existing_id'"
          overlap=1
        fi
      done
    done
  done <<<"$active"

  if [[ "$overlap" -eq 1 ]]; then
    if [[ "${ALLOW_OVERLAP:-0}" == "1" ]]; then
      echo "Proceeding because ALLOW_OVERLAP=1."
      return 0
    fi
    echo "ERROR: overlapping active claims found. Resolve/release first or run with ALLOW_OVERLAP=1."
    echo "Hint: make agent-claims"
    return 1
  fi

  return 0
}

cmd_list() {
  require_claims_file
  awk '
    /^## Active Claims/ {in_active=1; next}
    /^## / && in_active {in_active=0}
    in_active && /^- claim:/ {print}
  ' "$CLAIMS_FILE"
}

cmd_claim() {
  require_claims_file
  local agent="${1:-}"
  local paths_csv="${2:-}"
  local note="${3:-working session}"
  local hours="${4:-4}"
  local claim_id="${5:-}"

  if [[ -z "$agent" || -z "$paths_csv" ]]; then
    usage
    exit 1
  fi

  note="${note//;/,}"
  if [[ -z "$claim_id" ]]; then
    claim_id="${agent}-$(date -u +%Y%m%d-%H%M%S)"
  fi

  if rg -n "id=${claim_id};" "$CLAIMS_FILE" >/dev/null 2>&1; then
    echo "ERROR: claim id already exists: $claim_id"
    exit 1
  fi

  warn_or_fail_overlap "$claim_id" "$paths_csv"

  local updated expires line
  updated="$(utc_now)"
  expires="$(utc_plus_hours "$hours")"
  line="- claim: id=${claim_id}; agent=${agent}; status=active; paths=${paths_csv}; updated=${updated}; expires=${expires}; note=${note}"

  printf "%s\n" "$line" >>"$CLAIMS_FILE"
  echo "Added claim:"
  echo "$line"
}

cmd_release() {
  require_claims_file
  local claim_id="${1:-}"
  if [[ -z "$claim_id" ]]; then
    usage
    exit 1
  fi

  local now tmp found
  now="$(utc_now)"
  tmp="$(mktemp)"
  found=0

  if ! awk -v id="$claim_id" -v now="$now" '
    BEGIN {found=0}
    $0 ~ "^- claim: " && $0 ~ ("id=" id ";") {
      found=1
      line=$0
      gsub(/status=[^;]*/, "status=released", line)
      gsub(/updated=[^;]*/, "updated=" now, line)
      gsub(/expires=[^;]*/, "expires=" now, line)
      print line
      next
    }
    {print}
    END {
      if (found == 0) {
        exit 3
      }
    }
  ' "$CLAIMS_FILE" >"$tmp"; then
    local status=$?
    rm -f "$tmp"
    if [[ "$status" -eq 3 ]]; then
      echo "ERROR: claim id not found: $claim_id"
      exit 1
    fi
    exit "$status"
  fi

  mv "$tmp" "$CLAIMS_FILE"
  echo "Released claim: $claim_id"
}

main() {
  local cmd="${1:-}"
  shift || true
  case "$cmd" in
  list)
    cmd_list "$@"
    ;;
  claim)
    cmd_claim "$@"
    ;;
  release)
    cmd_release "$@"
    ;;
  *)
    usage
    exit 1
    ;;
  esac
}

main "$@"
