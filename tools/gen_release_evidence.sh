#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="${1:-${ROOT_DIR}/reports/release}"
STAMP="$(date -u +%Y%m%d-%H%M%SZ)"
BUNDLE_NAME="release-evidence-${STAMP}"
BUNDLE_DIR="${OUT_DIR}/${BUNDLE_NAME}"
LOG_DIR="${BUNDLE_DIR}/logs"
SUMMARY_FILE="${OUT_DIR}/${BUNDLE_NAME}.md"
COMMANDS_FILE="${BUNDLE_DIR}/commands.tsv"
CHECKSUM_FILE="${BUNDLE_DIR}/checksums.txt"
GO_ENV_FILE="${BUNDLE_DIR}/go-env.txt"
MODULES_FILE="${BUNDLE_DIR}/go-modules.json"
RESULTS_FILE="${BUNDLE_DIR}/results.tsv"
FAILED=0

mkdir -p "${LOG_DIR}"

cd "${ROOT_DIR}"

quote_cmd() {
  printf '%q ' "$@"
}

slugify() {
  printf '%s' "$1" |
    tr '[:upper:]' '[:lower:]' |
    sed -E 's/[^a-z0-9]+/-/g; s/^-//; s/-$//'
}

compute_sha256() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
    return 0
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
    return 0
  fi
  if command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$file" | awk '{print $NF}'
    return 0
  fi
  echo "unavailable"
  return 1
}

expected_libopus_sha256() {
  case "${LIBOPUS_VERSION:-1.6.1}" in
    1.6.1) echo "6ffcb593207be92584df15b32466ed64bbec99109f007c82205f0194572411a1" ;;
    *) echo "unknown" ;;
  esac
}

write_static_artifacts() {
  {
    echo "# SHA256 checksums"
    echo
    for file in go.mod go.sum tools/ensure_libopus.sh Makefile; do
      if [[ -f "${file}" ]]; then
        echo "$(compute_sha256 "${file}")  ${file}"
      fi
    done

    local libopus_tarball="tmp_check/opus-${LIBOPUS_VERSION:-1.6.1}.tar.gz"
    if [[ -f "${libopus_tarball}" ]]; then
      echo "$(compute_sha256 "${libopus_tarball}")  ${libopus_tarball}"
    else
      echo "missing  ${libopus_tarball}"
    fi
  } > "${CHECKSUM_FILE}"

  env GOWORK=off go env > "${GO_ENV_FILE}"
  env GOWORK=off go list -m -json all > "${MODULES_FILE}"

  {
    echo -e "category\ttitle\tstatus\tduration_seconds\tlog\tcommand"
  } > "${RESULTS_FILE}"
  {
    echo -e "title\tcommand"
  } > "${COMMANDS_FILE}"
}

run_cmd() {
  local category="$1"
  local title="$2"
  shift 2
  local slug
  local log_file
  local start
  local end
  local duration
  local status
  local result
  local command

  slug="$(slugify "${title}")"
  log_file="${LOG_DIR}/${slug}.log"
  command="$(quote_cmd "$@")"

  printf '%s\t%s\n' "${title}" "${command}" >> "${COMMANDS_FILE}"

  {
    echo "$ ${command}"
    echo
  } > "${log_file}"

  start="$(date +%s)"
  set +e
  "$@" >> "${log_file}" 2>&1
  status=$?
  set -e
  end="$(date +%s)"
  duration="$((end - start))"

  if [[ "${status}" -eq 0 ]]; then
    result="PASS"
  else
    result="FAIL(${status})"
    FAILED=1
    echo "error: command failed for section '${title}' (status=${status})" >&2
  fi

  printf '%s\t%s\t%s\t%s\t%s\t%s\n' \
    "${category}" \
    "${title}" \
    "${result}" \
    "${duration}" \
    "${BUNDLE_NAME}/logs/${slug}.log" \
    "${command}" >> "${RESULTS_FILE}"
}

write_summary() {
  local commit_sha
  local dirty_state
  local libopus_version
  local libopus_tarball
  local libopus_actual_sha
  local libopus_expected_sha
  local overall
  local release_tag

  commit_sha="$(git rev-parse --verify HEAD 2>/dev/null || echo unknown)"
  if git diff --quiet --ignore-submodules -- && git diff --cached --quiet --ignore-submodules --; then
    dirty_state="clean"
  else
    dirty_state="dirty"
  fi
  libopus_version="${LIBOPUS_VERSION:-1.6.1}"
  libopus_tarball="tmp_check/opus-${libopus_version}.tar.gz"
  if [[ -f "${libopus_tarball}" ]]; then
    libopus_actual_sha="$(compute_sha256 "${libopus_tarball}")"
  else
    libopus_actual_sha="missing"
  fi
  libopus_expected_sha="$(expected_libopus_sha256)"
  release_tag="${GITHUB_REF_NAME:-${TAG:-not-set}}"
  if [[ "${FAILED}" -eq 0 ]]; then
    overall="PASS"
  else
    overall="FAIL"
  fi

  {
    echo "# Release Evidence"
    echo
    echo "- Overall result: ${overall}"
    echo "- Generated (UTC): $(date -u +"%Y-%m-%dT%H:%M:%SZ")"
    echo "- Release tag: ${release_tag}"
    echo "- Commit SHA: ${commit_sha}"
    echo "- Working tree: ${dirty_state}"
    echo "- Go version: $(go version)"
    echo "- OS/platform: $(uname -s) $(uname -m); GOOS=$(env GOWORK=off go env GOOS) GOARCH=$(env GOWORK=off go env GOARCH)"
    echo "- libopus reference: ${libopus_version}"
    echo "- libopus tarball SHA256: ${libopus_actual_sha}"
    echo "- Expected libopus SHA256: ${libopus_expected_sha}"
    echo "- Go module: github.com/thesyncim/gopus"
    echo
    echo "No public release exists until both the signed/tagged Git ref and the GitHub Release are published. This file is evidence for the commit above; it is not a release by itself."
    echo
    echo "## Command Results"
    echo
    echo "| Category | Command | Result | Duration | Log |"
    echo "| --- | --- | --- | ---: | --- |"
    tail -n +2 "${RESULTS_FILE}" | while IFS=$'\t' read -r category title status duration log command; do
      echo "| ${category} | \`${command}\` | ${status} | ${duration}s | [${title}](${log}) |"
    done
    echo
    echo "## Required Summaries"
    echo
    echo "- Pass/fail summaries: see the command table above and per-command logs."
    echo "- Benchmark guardrail result: see \`make bench-guard\` and encoder/decoder \`make bench-libopus-guard\` in the Performance rows."
    echo "- Fuzz/safety summary: see \`make test-fuzz-smoke\`, \`make test-fuzz-safety\`, and \`make test-soak-safety\` in the Safety rows."
    echo "- Parity summary: see \`make test-quality\` and \`make verify-production-exhaustive\` in the Parity and Release gate rows."
    echo "- Consumer-smoke result: see \`make test-consumer-smoke\` in the Consumer row."
    echo
    echo "## Bundle Files"
    echo
    echo "- [Command manifest](${BUNDLE_NAME}/commands.tsv)"
    echo "- [Checksums](${BUNDLE_NAME}/checksums.txt)"
    echo "- [Go environment](${BUNDLE_NAME}/go-env.txt)"
    echo "- [Go module inventory](${BUNDLE_NAME}/go-modules.json)"
    echo "- [Command logs](${BUNDLE_NAME}/logs/)"
    echo
  } > "${SUMMARY_FILE}"
}

write_static_artifacts

run_cmd "API" "Package test suite" env GOWORK=off go test ./... -count=1
run_cmd "Docs" "Documentation contract" make test-doc-contract
run_cmd "Consumer" "External consumer smoke" make test-consumer-smoke
run_cmd "Parity" "Focused libopus parity gate" make test-quality
run_cmd "Performance" "Benchmark guardrails" make bench-guard
run_cmd "Performance" "Libopus-relative benchmark guardrails" make bench-libopus-guard
run_cmd "Safety" "Fuzz smoke gate" make test-fuzz-smoke
run_cmd "Release gate" "Production exhaustive gate" make verify-production-exhaustive
run_cmd "Safety" "Assembly safety matrix" make test-assembly-safety
run_cmd "Safety" "Fuzz safety gate" make test-fuzz-safety
run_cmd "Safety" "Soak safety gate" make test-soak-safety
run_cmd "Performance" "Hot-path benchmark sample" env GOWORK=off go test -run '^$' -bench '^Benchmark(DecoderDecode_CELT|DecoderDecodeInt16|EncoderEncode_CallerBuffer|EncoderEncodeInt16)$' -benchmem -count=1 .

write_summary

echo "wrote ${SUMMARY_FILE}"
echo "wrote ${BUNDLE_DIR}"

if [[ "${FAILED}" -ne 0 ]]; then
  exit 1
fi
