#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

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
done < <(GOWORK=off go list -e -f '{{if not .Error}}{{.ImportPath}}{{end}}' ./...)

if [[ ${#packages[@]} -eq 0 ]]; then
  echo "error: no runnable Go packages found under ${ROOT_DIR}" >&2
  exit 1
fi

GOWORK=off go test "$@" "${packages[@]}"
