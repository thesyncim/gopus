#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT_DIR}/tmp_check"
LIBOPUS_VERSION="${LIBOPUS_VERSION:-1.6.1}"
TARBALL="${TMP_DIR}/opus-${LIBOPUS_VERSION}.tar.gz"
LIBOPUS_ENABLE_QEXT="${LIBOPUS_ENABLE_QEXT:-0}"
LIBOPUS_CFLAGS="${LIBOPUS_CFLAGS:--O3 -DNDEBUG}"
LIBOPUS_CPPFLAGS="${LIBOPUS_CPPFLAGS:-}"

case "${LIBOPUS_ENABLE_QEXT}" in
  1|true|TRUE|yes|YES|on|ON)
    ENABLE_QEXT=1
    SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-qext"
    CONFIGURE_FLAGS=(--enable-static --disable-shared --enable-qext)
    ;;
  0|false|FALSE|no|NO|off|OFF)
    ENABLE_QEXT=0
    SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}"
    CONFIGURE_FLAGS=(--enable-static --disable-shared)
    ;;
  *)
    echo "error: LIBOPUS_ENABLE_QEXT must be 0/1, true/false, yes/no, or on/off" >&2
    exit 1
    ;;
esac

BUILD_STAMP_FILE=".gopus-libopus-build"

select_c_compiler() {
  if [[ -n "${CC:-}" ]]; then
    echo "${CC}"
    return 0
  fi
  local candidate
  for candidate in cc gcc clang; do
    if command -v "${candidate}" >/dev/null 2>&1; then
      echo "${candidate}"
      return 0
    fi
  done
  echo cc
}

HOST_OS="$(uname -s 2>/dev/null || echo unknown)"
HOST_ARCH="$(uname -m 2>/dev/null || echo unknown)"
HOST_BITS="$(getconf LONG_BIT 2>/dev/null || echo unknown)"
LIBOPUS_CC="$(select_c_compiler)"
LIBOPUS_LDFLAGS="${LDFLAGS:-}"
read -r -a LIBOPUS_CC_ARGV <<< "${LIBOPUS_CC}"
LIBOPUS_CC_DRIVER="${LIBOPUS_CC_ARGV[0]:-${LIBOPUS_CC}}"
CC_PATH="$(command -v "${LIBOPUS_CC_DRIVER}" 2>/dev/null || printf "%s" "${LIBOPUS_CC_DRIVER}")"
CC_TARGET="$("${LIBOPUS_CC_ARGV[@]}" -dumpmachine 2>/dev/null || true)"
CC_VERSION="$("${LIBOPUS_CC_ARGV[@]}" --version 2>/dev/null | sed -n '1p' || true)"
CONFIGURE_STAMP="${CONFIGURE_FLAGS[*]}"
BUILD_STAMP=$'gopus libopus helper build v5\nversion='"${LIBOPUS_VERSION}"$'\nqext='"${ENABLE_QEXT}"$'\nhost_os='"${HOST_OS}"$'\nhost_arch='"${HOST_ARCH}"$'\nhost_bits='"${HOST_BITS}"$'\ncc='"${LIBOPUS_CC}"$'\ncc_path='"${CC_PATH}"$'\ncc_target='"${CC_TARGET}"$'\ncc_version='"${CC_VERSION}"$'\nconfigure='"${CONFIGURE_STAMP}"$'\nCFLAGS='"${LIBOPUS_CFLAGS}"$'\nCPPFLAGS='"${LIBOPUS_CPPFLAGS}"$'\nLDFLAGS='"${LIBOPUS_LDFLAGS}"$'\n'
LOCK_DIR="${SRC_DIR}.lock"

sha256_for_version() {
  case "$1" in
    1.6.1) echo "6ffcb593207be92584df15b32466ed64bbec99109f007c82205f0194572411a1" ;;
    *)
      echo "error: unsupported LIBOPUS_VERSION=$1 (missing pinned SHA256 in tools/ensure_libopus.sh)" >&2
      return 1
      ;;
  esac
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
  echo "error: sha256 tool not found (need sha256sum, shasum, or openssl)" >&2
  return 1
}

verify_sha256() {
  local file="$1"
  local expected="$2"
  local got
  got="$(compute_sha256 "$file")"
  if [[ "$got" != "$expected" ]]; then
    echo "error: SHA256 mismatch for $file" >&2
    echo "expected: $expected" >&2
    echo "got:      $got" >&2
    return 1
  fi
}

download_tarball() {
  local dest="$1"
  local version="$2"
  local urls=(
    "https://ftp.osuosl.org/pub/xiph/releases/opus/opus-${version}.tar.gz"
    "https://downloads.xiph.org/releases/opus/opus-${version}.tar.gz"
  )
  local url
  for url in "${urls[@]}"; do
    echo "Fetching libopus ${version} from ${url}"
    if command -v curl >/dev/null 2>&1; then
      if curl -fL "$url" -o "$dest"; then
        return 0
      fi
    elif command -v wget >/dev/null 2>&1; then
      if wget -O "$dest" "$url"; then
        return 0
      fi
    else
      echo "error: neither curl nor wget is available to download libopus" >&2
      return 1
    fi
    rm -f "$dest"
  done
  echo "error: failed to download libopus ${version} tarball from known mirrors" >&2
  return 1
}

EXPECTED_SHA256="$(sha256_for_version "${LIBOPUS_VERSION}")"

find_built_tool() {
  local tool="$1"
  local candidate
  for candidate in "${SRC_DIR}/${tool}" "${SRC_DIR}/${tool}.exe"; do
    if [[ -f "${candidate}" && ( -x "${candidate}" || "${candidate}" == *.exe ) ]]; then
      echo "${candidate}"
      return 0
    fi
  done
  return 1
}

find_static_lib() {
  local candidate="${SRC_DIR}/.libs/libopus.a"
  if [[ -f "${candidate}" && -s "${candidate}" ]]; then
    echo "${candidate}"
    return 0
  fi
  return 1
}

build_stamp_is_current() {
  local stamp="${SRC_DIR}/${BUILD_STAMP_FILE}"
  [[ -f "${stamp}" ]] && [[ "$(cat "${stamp}")"$'\n' == "${BUILD_STAMP}" ]]
}

build_outputs_are_current() {
  OPUS_DEMO_PATH="$(find_built_tool opus_demo)" || return 1
  OPUS_COMPARE_PATH="$(find_built_tool opus_compare)" || return 1
  LIBOPUS_STATIC_PATH="$(find_static_lib)" || return 1
  build_stamp_is_current
}

extract_source_to() {
  local dest="$1"
  local extract_dir
  extract_dir="$(mktemp -d "${TMP_DIR}/opus-extract.XXXXXX")"
  tar -xzf "${TARBALL}" -C "${extract_dir}"
  if [[ ! -d "${extract_dir}/opus-${LIBOPUS_VERSION}" ]]; then
    echo "error: unexpected libopus source layout in ${TARBALL}" >&2
    rm -rf "${extract_dir}"
    return 1
  fi
  rm -rf "${dest}"
  mv "${extract_dir}/opus-${LIBOPUS_VERSION}" "${dest}"
  rm -rf "${extract_dir}"
}

if build_outputs_are_current; then
  echo "${OPUS_DEMO_PATH}"
  exit 0
fi

mkdir -p "${TMP_DIR}"

while ! mkdir "${LOCK_DIR}" 2>/dev/null; do
  sleep 1
done
trap 'rmdir "${LOCK_DIR}" 2>/dev/null || true' EXIT

if build_outputs_are_current; then
  echo "${OPUS_DEMO_PATH}"
  exit 0
fi

if [[ ! -d "${SRC_DIR}" ]]; then
  if [[ ! -f "${TARBALL}" ]]; then
    download_tarball "${TARBALL}" "${LIBOPUS_VERSION}"
  fi
  verify_sha256 "${TARBALL}" "${EXPECTED_SHA256}"
  extract_source_to "${SRC_DIR}"
fi

if [[ ! -f "${SRC_DIR}/configure" ]]; then
  echo "error: missing ${SRC_DIR}/configure (unexpected source layout)" >&2
  exit 1
fi

cd "${SRC_DIR}"
if [[ -f Makefile ]] && ! build_stamp_is_current; then
  make distclean >/dev/null 2>&1 || rm -f Makefile config.log config.status
fi

if [[ ! -f Makefile ]]; then
  CC="${LIBOPUS_CC}" CFLAGS="${LIBOPUS_CFLAGS}" CPPFLAGS="${LIBOPUS_CPPFLAGS}" LDFLAGS="${LIBOPUS_LDFLAGS}" ./configure "${CONFIGURE_FLAGS[@]}"
fi

if command -v getconf >/dev/null 2>&1; then
  JOBS="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
elif command -v sysctl >/dev/null 2>&1; then
  JOBS="$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
else
  JOBS=4
fi

make -j"${JOBS}"

if ! OPUS_DEMO_PATH="$(find_built_tool opus_demo)"; then
  echo "error: expected executable not produced: ${SRC_DIR}/opus_demo(.exe)" >&2
  exit 1
fi

if ! OPUS_COMPARE_PATH="$(find_built_tool opus_compare)"; then
  echo "error: expected executable not produced: ${SRC_DIR}/opus_compare(.exe)" >&2
  exit 1
fi

if ! LIBOPUS_STATIC_PATH="$(find_static_lib)"; then
  echo "error: expected static library not produced: ${SRC_DIR}/.libs/libopus.a" >&2
  exit 1
fi

printf "%s" "${BUILD_STAMP}" > "${SRC_DIR}/${BUILD_STAMP_FILE}"
echo "${OPUS_DEMO_PATH}"
