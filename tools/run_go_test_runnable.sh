#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_BIN="${GO:-go}"
GO_WORK_ENV="${GO_WORK_ENV:-GOWORK=off}"
go_env=()
if [[ -n "${GO_WORK_ENV}" ]]; then
  # Makefile passes GO_WORK_ENV as simple KEY=VALUE assignments.
  # shellcheck disable=SC2206
  go_env=(${GO_WORK_ENV})
fi

cd "${ROOT_DIR}"

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
done < <(env "${go_env[@]}" "${GO_BIN}" list ./...)

if [[ ${#packages[@]} -eq 0 ]]; then
  echo "error: no runnable Go packages found under ${ROOT_DIR}" >&2
  exit 1
fi

env "${go_env[@]}" "${GO_BIN}" test "$@" "${packages[@]}"
