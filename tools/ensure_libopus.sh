#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT_DIR}/tmp_check"
LIBOPUS_VERSION="${LIBOPUS_VERSION:-1.6.1}"
TARBALL="${TMP_DIR}/opus-${LIBOPUS_VERSION}.tar.gz"
LIBOPUS_ENABLE_QEXT="${LIBOPUS_ENABLE_QEXT:-0}"
LIBOPUS_ENABLE_FIXED="${LIBOPUS_ENABLE_FIXED:-0}"
LIBOPUS_ENABLE_CUSTOM="${LIBOPUS_ENABLE_CUSTOM:-0}"
LIBOPUS_ENABLE_SIMD="${LIBOPUS_ENABLE_SIMD:-0}"
LIBOPUS_ENABLE_SCALAR="${LIBOPUS_ENABLE_SCALAR:-0}"
LIBOPUS_ENABLE_CUSTOM_SCALAR="${LIBOPUS_ENABLE_CUSTOM_SCALAR:-0}"
LIBOPUS_CFLAGS="${LIBOPUS_CFLAGS:--O3 -DNDEBUG}"
LIBOPUS_CPPFLAGS="${LIBOPUS_CPPFLAGS:-}"

normalize_bool() {
  case "$1" in
    1|true|TRUE|yes|YES|on|ON) echo 1 ;;
    0|false|FALSE|no|NO|off|OFF) echo 0 ;;
    *) echo "error: $2 must be 0/1, true/false, yes/no, or on/off" >&2; return 1 ;;
  esac
}

ENABLE_QEXT="$(normalize_bool "${LIBOPUS_ENABLE_QEXT}" LIBOPUS_ENABLE_QEXT)"
ENABLE_FIXED="$(normalize_bool "${LIBOPUS_ENABLE_FIXED}" LIBOPUS_ENABLE_FIXED)"
ENABLE_CUSTOM="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM}" LIBOPUS_ENABLE_CUSTOM)"
ENABLE_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_SIMD}" LIBOPUS_ENABLE_SIMD)"
ENABLE_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_SCALAR}" LIBOPUS_ENABLE_SCALAR)"
ENABLE_CUSTOM_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_SCALAR}" LIBOPUS_ENABLE_CUSTOM_SCALAR)"

VARIANT_COUNT=$((ENABLE_QEXT + ENABLE_FIXED + ENABLE_CUSTOM + ENABLE_SIMD + ENABLE_SCALAR + ENABLE_CUSTOM_SCALAR))
if [[ "${VARIANT_COUNT}" -gt 1 ]]; then
  echo "error: LIBOPUS_ENABLE_QEXT, LIBOPUS_ENABLE_FIXED, LIBOPUS_ENABLE_CUSTOM, LIBOPUS_ENABLE_SIMD, LIBOPUS_ENABLE_SCALAR, and LIBOPUS_ENABLE_CUSTOM_SCALAR are mutually exclusive" >&2
  exit 1
fi

# Force libopus onto its scalar (generic-C) kernels: no inline/external assembly,
# no run-time CPU dispatch, no SIMD intrinsics. This is the bit-reproducible
# reference for the pure-Go (-tags purego) gopus build, which itself has no
# assembly/SIMD. The autotools default on amd64 (and Linux arm64) turns RTCD +
# intrinsics ON, so a default-configured opus-1.6.1 is NOT scalar there; the
# pure-Go-vs-C parity oracles need this explicit scalar build to compare
# like-with-like instead of pure-Go-scalar vs C-SIMD.
SCALAR_CONFIGURE_FLAGS=(--disable-asm --disable-rtcd --disable-intrinsics)

CONFIGURE_FLAGS=(--enable-static --disable-shared)
if [[ "${ENABLE_QEXT}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-qext"
  CONFIGURE_FLAGS+=(--enable-qext)
elif [[ "${ENABLE_FIXED}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-fixed"
  CONFIGURE_FLAGS+=(--enable-fixed-point)
elif [[ "${ENABLE_CUSTOM}" == "1" ]]; then
  # --enable-custom-modes defines CUSTOM_MODES and exposes the Opus Custom API
  # (opus_custom_mode_create / opus_custom_encoder_create / ...). This is the
  # only build that can serve as an oracle for non-standard-rate custom modes.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom"
  CONFIGURE_FLAGS+=(--enable-custom-modes)
elif [[ "${ENABLE_CUSTOM_SCALAR}" == "1" ]]; then
  # Opus Custom API on the scalar (generic-C) kernels: the bit-reproducible
  # custom-modes oracle for the pure-Go celt/custom parity gate. The plain custom
  # build keeps libopus's SIMD default ON (so it stays the asm-tier custom oracle).
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-scalar"
  CONFIGURE_FLAGS+=(--enable-custom-modes "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_SCALAR}" == "1" ]]; then
  # Scalar (generic-C) parity reference for the pure-Go build. See the
  # SCALAR_CONFIGURE_FLAGS comment above.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-scalar"
  CONFIGURE_FLAGS+=("${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_SIMD}" == "1" ]]; then
  # SIMD/RTCD-enabled PERFORMANCE reference. This is libopus's native default
  # (intrinsics + run-time CPU detection ON): NEON on arm64, SSE/AVX RTCD on
  # amd64. It is explicitly NOT bit-reproducible across hosts, so it must NEVER
  # be used as a pure-Go parity oracle — the scalar parity reference
  # (opus-${LIBOPUS_VERSION}-scalar, built via LIBOPUS_ENABLE_SCALAR=1 with
  # --disable-asm/--disable-rtcd/--disable-intrinsics) is the bit-reproducible lib
  # for the pure-Go build. This variant exists so the perf scoreboard and the
  # asm-tier quality oracles can compare gopus asm kernels against a SIMD libopus
  # (fair asm-vs-SIMD). We pass the enabling flags explicitly (rather than relying
  # on autoconf defaults) so the produced config.h reliably DEFINES the SIMD
  # macros even if a future autotools change alters the default.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-simd"
  CONFIGURE_FLAGS+=(--enable-rtcd --enable-intrinsics)
else
  # Default reference (no SIMD flags passed): autotools picks the native config,
  # which turns RTCD + intrinsics ON on amd64 and Linux arm64. The asm gopus build
  # (default, no -tags purego) ships SSE/NEON kernels tuned to match this, so the
  # asm-tier C oracles link this tree. The pure-Go build must instead link the
  # opus-${LIBOPUS_VERSION}-scalar tree (LIBOPUS_ENABLE_SCALAR=1).
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}"
fi

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
# The SIMD variant is disambiguated from other variants by its own SRC_DIR and by
# the configure= line (it carries --enable-rtcd --enable-intrinsics), so it is NOT
# added to the stamp body here: changing the shared stamp format would mark the
# already-built scalar parity reference stale and force a reconfigure (which would
# turn SIMD back ON and destroy the bit-exact parity build). The stamp format is
# therefore frozen for the existing variants.
BUILD_STAMP=$'gopus libopus helper build v5\nversion='"${LIBOPUS_VERSION}"$'\nqext='"${ENABLE_QEXT}"$'\nfixed='"${ENABLE_FIXED}"$'\ncustom='"${ENABLE_CUSTOM}"$'\nhost_os='"${HOST_OS}"$'\nhost_arch='"${HOST_ARCH}"$'\nhost_bits='"${HOST_BITS}"$'\ncc='"${LIBOPUS_CC}"$'\ncc_path='"${CC_PATH}"$'\ncc_target='"${CC_TARGET}"$'\ncc_version='"${CC_VERSION}"$'\nconfigure='"${CONFIGURE_STAMP}"$'\nCFLAGS='"${LIBOPUS_CFLAGS}"$'\nCPPFLAGS='"${LIBOPUS_CPPFLAGS}"$'\nLDFLAGS='"${LIBOPUS_LDFLAGS}"$'\n'
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
