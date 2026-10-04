#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO:-go}"
# GO may be a simple command plus wrapper, for example project-env go.
# shellcheck disable=SC2206
GO_COMMAND=(${GO_BIN})
GO_WORK_ENV="${GO_WORK_ENV:-GOWORK=off}"
go_env=()
if [[ -n "${GO_WORK_ENV}" ]]; then
  # Makefile passes GO_WORK_ENV as simple KEY=VALUE assignments.
  # shellcheck disable=SC2206
  go_env=(${GO_WORK_ENV})
fi

cd "${ROOT_DIR}"

if [[ -n "${GOPUS_TEST_SHARD:-}" ]]; then
  shard_args=(
    "--go=$GO_BIN"
    "--go-work-env=$GO_WORK_ENV"
    "--root=$ROOT_DIR"
    "--shard=$GOPUS_TEST_SHARD"
  )
  if [[ -n "${GOPUS_TEST_SHARD_REPORT:-}" ]]; then
    shard_args+=("--report=$GOPUS_TEST_SHARD_REPORT")
  fi
  for arg in "$@"; do
    shard_args+=("--test-arg=$arg")
  done
  exec python3 "${ROOT_DIR}/tools/run_go_test_sharded.py" "${shard_args[@]}"
fi

packages=()
while IFS= read -r pkg; do
  if [[ -z "${pkg}" ]]; then
    continue
  fi
  case "${pkg}" in
    github.com/thesyncim/gopus/tmp_check|github.com/thesyncim/gopus/tmp_check/*)
      continue
      ;;
  esac
  packages+=("${pkg}")
done < <(env "${go_env[@]}" "${GO_COMMAND[@]}" list ./...)

if [[ ${#packages[@]} -eq 0 ]]; then
  echo "error: no runnable Go packages found under ${ROOT_DIR}" >&2
  exit 1
fi

env "${go_env[@]}" "${GO_COMMAND[@]}" test "$@" "${packages[@]}"
