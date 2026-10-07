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

# Use normal go test flags before -- and package patterns after it. The marker
# keeps positional flag values such as -coverprofile ./coverage.out unambiguous.
# The standalone tokens -- and -args are wrapper delimiters; use the attached
# -flag=value form when either token is itself an option value. Existing callers
# that pass package patterns positionally should use the -- delimiter.
test_args=()
package_patterns=()
test_binary_args=()
section=test_args
explicit_package_patterns=false
test_arg_count=0
has_package_pattern=false
while (($#)); do
  arg="$1"
  shift

  case "${section}" in
    test_args)
      case "${arg}" in
        --)
          section=package_patterns
          explicit_package_patterns=true
          ;;
        -args)
          test_binary_args=(-args)
          test_binary_args+=("$@")
          break
          ;;
        *)
          test_args+=("${arg}")
          test_arg_count=$((test_arg_count + 1))
          ;;
      esac
      ;;
    package_patterns)
      if [[ "${arg}" == "-args" ]]; then
        test_binary_args=(-args)
        test_binary_args+=("$@")
        break
      fi
      package_patterns+=("${arg}")
      has_package_pattern=true
      ;;
  esac
done

if [[ "${explicit_package_patterns}" == true && "${has_package_pattern}" != true ]]; then
  echo "error: package patterns are required after --" >&2
  exit 2
fi

if [[ "${explicit_package_patterns}" != true ]]; then
  package_patterns=(./...)
fi

# These build options can change the files or packages selected by go list.
list_args=()
for ((i = 0; i < test_arg_count; i++)); do
  option="${test_args[i]}"
  if [[ "${option}" == -test.* ]]; then
    option="-${option#-test.}"
  fi
  case "${option}" in
    -tags|-overlay|-modfile|-compiler)
      if ((i + 1 < test_arg_count)); then
        next_index=$((i + 1))
        list_args+=("${test_args[i]}" "${test_args[next_index]}")
        i=$((i + 1))
      else
        echo "error: ${test_args[i]} requires a value before package discovery" >&2
        exit 2
      fi
      ;;
    -tags=*|-overlay=*|-modfile=*|-compiler=*|-race=*|-msan=*|-asan=*)
      list_args+=("${test_args[i]}")
      ;;
    -race|-msan|-asan)
      list_args+=("${test_args[i]}")
      ;;
    # Skip separate values for common go test and go build flags. Otherwise a
    # value such as `-coverprofile -tags=gopus_dred` could be mistaken for a
    # package-selection flag and change the package inventory.
    -run|-bench|-benchtime|-fuzz|-fuzztime|-fuzzminimizetime|-skip|-shuffle|\
    -count|-parallel|-timeout|-cpu|\
    -list|-coverprofile|-covermode|-coverpkg|-outputdir|-exec|-o|-C|-p|\
    -pkgdir|-toolexec|-gcflags|-asmflags|-ldflags|-gccgoflags|-buildmode|-mod|\
    -installsuffix|-vet|-pgo|-blockprofile|-blockprofilerate|-cpuprofile|\
    -memprofile|-memprofilerate|-mutexprofile|-mutexprofilefraction|-trace)
      if ((i + 1 < test_arg_count)); then
        i=$((i + 1))
      else
        echo "error: ${test_args[i]} requires a value before package discovery" >&2
        exit 2
      fi
      ;;
  esac
done

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
  for arg in "${test_args[@]+"${test_args[@]}"}" "${test_binary_args[@]+"${test_binary_args[@]}"}"; do
    shard_args+=("--test-arg=$arg")
  done
  for arg in "${list_args[@]+"${list_args[@]}"}"; do
    shard_args+=("--go-list-arg=$arg")
  done
  for pattern in "${package_patterns[@]}"; do
    shard_args+=("--package=$pattern")
  done
  exec python3 "${ROOT_DIR}/tools/run_go_test_sharded.py" "${shard_args[@]}"
fi

# Process substitution hides the status of go list. Capture output first so a
# partial package list cannot turn an enumeration error into a passing test.
package_list_file="$(mktemp)"
trap 'rm -f "${package_list_file}"' EXIT
if env "${go_env[@]+"${go_env[@]}"}" "${GO_COMMAND[@]}" list "${list_args[@]+"${list_args[@]}"}" "${package_patterns[@]+"${package_patterns[@]}"}" >"${package_list_file}"; then
  :
else
  status=$?
  echo "error: failed to list Go test packages" >&2
  exit "${status}"
fi

packages=()
package_count=0
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
  package_count=$((package_count + 1))
done < "${package_list_file}"

if [[ "${package_count}" -eq 0 ]]; then
  echo "error: no runnable Go packages found under ${ROOT_DIR}" >&2
  exit 1
fi

env "${go_env[@]+"${go_env[@]}"}" "${GO_COMMAND[@]}" test "${test_args[@]+"${test_args[@]}"}" "${packages[@]}" "${test_binary_args[@]+"${test_binary_args[@]}"}"
