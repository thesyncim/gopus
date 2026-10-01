#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT_DIR}/tmp_check"
LIBOPUS_VERSION="${LIBOPUS_VERSION:-1.6.1}"
TARBALL="${TMP_DIR}/opus-${LIBOPUS_VERSION}.tar.gz"
LIBOPUS_ENABLE_QEXT="${LIBOPUS_ENABLE_QEXT:-0}"
LIBOPUS_ENABLE_QEXT_SCALAR="${LIBOPUS_ENABLE_QEXT_SCALAR:-0}"
LIBOPUS_ENABLE_QEXT_SIMD="${LIBOPUS_ENABLE_QEXT_SIMD:-0}"
LIBOPUS_ENABLE_DRED_QEXT_SCALAR="${LIBOPUS_ENABLE_DRED_QEXT_SCALAR:-0}"
LIBOPUS_ENABLE_DRED_QEXT_SIMD="${LIBOPUS_ENABLE_DRED_QEXT_SIMD:-0}"
LIBOPUS_ENABLE_FIXED_SCALAR="${LIBOPUS_ENABLE_FIXED_SCALAR:-0}"
LIBOPUS_ENABLE_FIXED_SIMD="${LIBOPUS_ENABLE_FIXED_SIMD:-0}"
LIBOPUS_ENABLE_FIXED_QEXT_SCALAR="${LIBOPUS_ENABLE_FIXED_QEXT_SCALAR:-0}"
LIBOPUS_ENABLE_FIXED_QEXT_SIMD="${LIBOPUS_ENABLE_FIXED_QEXT_SIMD:-0}"
LIBOPUS_ENABLE_CUSTOM="${LIBOPUS_ENABLE_CUSTOM:-0}"
LIBOPUS_ENABLE_CUSTOM_QEXT_SCALAR="${LIBOPUS_ENABLE_CUSTOM_QEXT_SCALAR:-0}"
LIBOPUS_ENABLE_CUSTOM_QEXT_SIMD="${LIBOPUS_ENABLE_CUSTOM_QEXT_SIMD:-0}"
LIBOPUS_ENABLE_CUSTOM_FIXED_SCALAR="${LIBOPUS_ENABLE_CUSTOM_FIXED_SCALAR:-0}"
LIBOPUS_ENABLE_CUSTOM_FIXED_SIMD="${LIBOPUS_ENABLE_CUSTOM_FIXED_SIMD:-0}"
LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SCALAR="${LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SCALAR:-0}"
LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SIMD="${LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SIMD:-0}"
LIBOPUS_ENABLE_SIMD="${LIBOPUS_ENABLE_SIMD:-0}"
LIBOPUS_ENABLE_SCALAR="${LIBOPUS_ENABLE_SCALAR:-0}"
LIBOPUS_ENABLE_CUSTOM_SCALAR="${LIBOPUS_ENABLE_CUSTOM_SCALAR:-0}"
LIBOPUS_CFLAGS_WAS_SET=0
if [[ "${LIBOPUS_CFLAGS+x}" == "x" ]]; then
  LIBOPUS_CFLAGS_WAS_SET=1
fi
LIBOPUS_CFLAGS="${LIBOPUS_CFLAGS:--O3 -DNDEBUG}"
LIBOPUS_CPPFLAGS="${LIBOPUS_CPPFLAGS:-}"
GOPUS_LIBOPUS_AMD64_TARGET="${GOPUS_LIBOPUS_AMD64_TARGET:-}"

normalize_bool() {
  case "$1" in
    1|true|TRUE|yes|YES|on|ON) echo 1 ;;
    0|false|FALSE|no|NO|off|OFF) echo 0 ;;
    *) echo "error: $2 must be 0/1, true/false, yes/no, or on/off" >&2; return 1 ;;
  esac
}

ENABLE_QEXT="$(normalize_bool "${LIBOPUS_ENABLE_QEXT}" LIBOPUS_ENABLE_QEXT)"
ENABLE_QEXT_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_QEXT_SCALAR}" LIBOPUS_ENABLE_QEXT_SCALAR)"
ENABLE_QEXT_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_QEXT_SIMD}" LIBOPUS_ENABLE_QEXT_SIMD)"
ENABLE_DRED_QEXT_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_DRED_QEXT_SCALAR}" LIBOPUS_ENABLE_DRED_QEXT_SCALAR)"
ENABLE_DRED_QEXT_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_DRED_QEXT_SIMD}" LIBOPUS_ENABLE_DRED_QEXT_SIMD)"
ENABLE_FIXED_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_FIXED_SCALAR}" LIBOPUS_ENABLE_FIXED_SCALAR)"
ENABLE_FIXED_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_FIXED_SIMD}" LIBOPUS_ENABLE_FIXED_SIMD)"
ENABLE_FIXED_QEXT_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_FIXED_QEXT_SCALAR}" LIBOPUS_ENABLE_FIXED_QEXT_SCALAR)"
ENABLE_FIXED_QEXT_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_FIXED_QEXT_SIMD}" LIBOPUS_ENABLE_FIXED_QEXT_SIMD)"
ENABLE_CUSTOM="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM}" LIBOPUS_ENABLE_CUSTOM)"
ENABLE_CUSTOM_QEXT_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_QEXT_SCALAR}" LIBOPUS_ENABLE_CUSTOM_QEXT_SCALAR)"
ENABLE_CUSTOM_QEXT_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_QEXT_SIMD}" LIBOPUS_ENABLE_CUSTOM_QEXT_SIMD)"
ENABLE_CUSTOM_FIXED_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_FIXED_SCALAR}" LIBOPUS_ENABLE_CUSTOM_FIXED_SCALAR)"
ENABLE_CUSTOM_FIXED_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_FIXED_SIMD}" LIBOPUS_ENABLE_CUSTOM_FIXED_SIMD)"
ENABLE_CUSTOM_FIXED_QEXT_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SCALAR}" LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SCALAR)"
ENABLE_CUSTOM_FIXED_QEXT_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SIMD}" LIBOPUS_ENABLE_CUSTOM_FIXED_QEXT_SIMD)"
ENABLE_SIMD="$(normalize_bool "${LIBOPUS_ENABLE_SIMD}" LIBOPUS_ENABLE_SIMD)"
ENABLE_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_SCALAR}" LIBOPUS_ENABLE_SCALAR)"
ENABLE_CUSTOM_SCALAR="$(normalize_bool "${LIBOPUS_ENABLE_CUSTOM_SCALAR}" LIBOPUS_ENABLE_CUSTOM_SCALAR)"

VARIANT_COUNT=$((ENABLE_QEXT + ENABLE_QEXT_SCALAR + ENABLE_QEXT_SIMD + ENABLE_DRED_QEXT_SCALAR + ENABLE_DRED_QEXT_SIMD + ENABLE_FIXED_SCALAR + ENABLE_FIXED_SIMD + ENABLE_FIXED_QEXT_SCALAR + ENABLE_FIXED_QEXT_SIMD + ENABLE_CUSTOM + ENABLE_CUSTOM_QEXT_SCALAR + ENABLE_CUSTOM_QEXT_SIMD + ENABLE_CUSTOM_FIXED_SCALAR + ENABLE_CUSTOM_FIXED_SIMD + ENABLE_CUSTOM_FIXED_QEXT_SCALAR + ENABLE_CUSTOM_FIXED_QEXT_SIMD + ENABLE_SIMD + ENABLE_SCALAR + ENABLE_CUSTOM_SCALAR))
if [[ "${VARIANT_COUNT}" -gt 1 ]]; then
  echo "error: libopus build variants are mutually exclusive" >&2
  exit 1
fi

AMD64_TARGET_SUFFIX=""
AMD64_TARGET_CFLAGS=""
if [[ -n "${GOPUS_LIBOPUS_AMD64_TARGET}" ]]; then
  case "${GOPUS_LIBOPUS_AMD64_TARGET}" in
    v1) AMD64_TARGET_CFLAGS="-march=x86-64 -mtune=generic" ;;
    v2) AMD64_TARGET_CFLAGS="-march=x86-64-v2 -mtune=generic" ;;
    v3) AMD64_TARGET_CFLAGS="-march=x86-64-v3 -mtune=generic" ;;
    *) echo "error: GOPUS_LIBOPUS_AMD64_TARGET must be v1, v2, or v3" >&2; exit 1 ;;
  esac
  if [[ "${ENABLE_QEXT}" == "1" || "${ENABLE_QEXT_SCALAR}" == "1" || "${ENABLE_QEXT_SIMD}" == "1" ||
        "${ENABLE_DRED_QEXT_SCALAR}" == "1" || "${ENABLE_DRED_QEXT_SIMD}" == "1" ||
        "${ENABLE_FIXED_SCALAR}" == "1" || "${ENABLE_FIXED_SIMD}" == "1" ||
        "${ENABLE_FIXED_QEXT_SCALAR}" == "1" || "${ENABLE_FIXED_QEXT_SIMD}" == "1" ||
        "${ENABLE_CUSTOM}" == "1" || "${ENABLE_CUSTOM_QEXT_SCALAR}" == "1" || "${ENABLE_CUSTOM_QEXT_SIMD}" == "1" ||
        "${ENABLE_CUSTOM_FIXED_SCALAR}" == "1" || "${ENABLE_CUSTOM_FIXED_SIMD}" == "1" ||
        "${ENABLE_CUSTOM_FIXED_QEXT_SCALAR}" == "1" || "${ENABLE_CUSTOM_FIXED_QEXT_SIMD}" == "1" ||
        "${ENABLE_CUSTOM_SCALAR}" == "1" ]]; then
    echo "error: GOPUS_LIBOPUS_AMD64_TARGET supports only the default float-core scalar or SIMD reference" >&2
    exit 1
  fi
  if [[ "${ENABLE_SCALAR}" != "1" && "${ENABLE_SIMD}" != "1" ]]; then
    echo "error: GOPUS_LIBOPUS_AMD64_TARGET requires one default float-core scalar or SIMD reference" >&2
    exit 1
  fi
  if [[ "${LIBOPUS_CFLAGS_WAS_SET}" == "1" || -n "${LIBOPUS_CPPFLAGS}" || -n "${LDFLAGS:-}" || -n "${CPPFLAGS:-}" ]]; then
    echo "error: GOPUS_LIBOPUS_AMD64_TARGET does not allow custom CFLAGS, CPPFLAGS, or LDFLAGS" >&2
    exit 1
  fi
  AMD64_TARGET_SUFFIX="-amd64-${GOPUS_LIBOPUS_AMD64_TARGET}"
fi

# Force libopus onto its generic-C kernels and disable compiler loop and SLP
# vectorization. Normal FMA contraction stays enabled. This pairs the scalar C
# reference with the default and -tags nosimd Go scalar builds.
SCALAR_CONFIGURE_FLAGS=(--disable-asm --disable-rtcd --disable-intrinsics)
SCALAR_C_VECTOR_FLAGS=(-fno-tree-vectorize -fno-tree-slp-vectorize)

CONFIGURE_FLAGS=(--enable-static --disable-shared)
if [[ "${ENABLE_QEXT}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-qext"
  CONFIGURE_FLAGS+=(--enable-qext)
elif [[ "${ENABLE_QEXT_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-qext-scalar"
  CONFIGURE_FLAGS+=(--enable-qext "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_QEXT_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-qext-simd"
  CONFIGURE_FLAGS+=(--enable-qext --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_DRED_QEXT_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-dred-qext-scalar"
  CONFIGURE_FLAGS+=(--enable-qext --enable-dred "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_DRED_QEXT_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-dred-qext-simd"
  CONFIGURE_FLAGS+=(--enable-qext --enable-dred --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_FIXED_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-fixed-scalar"
  CONFIGURE_FLAGS+=(--enable-fixed-point "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_FIXED_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-fixed-simd"
  CONFIGURE_FLAGS+=(--enable-fixed-point --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_FIXED_QEXT_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-fixed-qext-scalar"
  CONFIGURE_FLAGS+=(--enable-fixed-point --enable-qext "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_FIXED_QEXT_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-fixed-qext-simd"
  CONFIGURE_FLAGS+=(--enable-fixed-point --enable-qext --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_CUSTOM}" == "1" ]]; then
  # --enable-custom-modes defines CUSTOM_MODES and exposes the Opus Custom API
  # (opus_custom_mode_create / opus_custom_encoder_create / ...). This is the
  # only build that can serve as an oracle for non-standard-rate custom modes.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_CUSTOM_SCALAR}" == "1" ]]; then
  # Opus Custom API on the scalar (generic-C) kernels, paired with scalar Go.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-scalar"
  CONFIGURE_FLAGS+=(--enable-custom-modes "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_CUSTOM_QEXT_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-qext-scalar"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-qext "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_CUSTOM_QEXT_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-qext-simd"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-qext --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_CUSTOM_FIXED_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-fixed-scalar"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-fixed-point "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_CUSTOM_FIXED_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-fixed-simd"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-fixed-point --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_CUSTOM_FIXED_QEXT_SCALAR}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-fixed-qext-scalar"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-fixed-point --enable-qext "${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_CUSTOM_FIXED_QEXT_SIMD}" == "1" ]]; then
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}-custom-fixed-qext-simd"
  CONFIGURE_FLAGS+=(--enable-custom-modes --enable-fixed-point --enable-qext --enable-rtcd --enable-intrinsics)
elif [[ "${ENABLE_SCALAR}" == "1" ]]; then
  # Scalar (generic-C) parity reference for scalar Go builds. See the
  # SCALAR_CONFIGURE_FLAGS comment above.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}${AMD64_TARGET_SUFFIX}-scalar"
  CONFIGURE_FLAGS+=("${SCALAR_CONFIGURE_FLAGS[@]}")
elif [[ "${ENABLE_SIMD}" == "1" ]]; then
  # Native SIMD/RTCD reference paired with the Go SIMD build: NEON on arm64 and
  # SSE/AVX dispatch on amd64. Enable these explicitly so config.h records the
  # intended instruction path even if autotools defaults change.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}${AMD64_TARGET_SUFFIX}-simd"
  CONFIGURE_FLAGS+=(--enable-rtcd --enable-intrinsics)
else
  # Unqualified autotools configuration for tooling that explicitly requests the
  # default tree. Paired oracle callers select the -scalar or -simd tree instead.
  SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}"
fi

if [[ "${ENABLE_SCALAR}" == "1" || "${ENABLE_CUSTOM_SCALAR}" == "1" || "${ENABLE_CUSTOM_QEXT_SCALAR}" == "1" || "${ENABLE_CUSTOM_FIXED_SCALAR}" == "1" || "${ENABLE_CUSTOM_FIXED_QEXT_SCALAR}" == "1" || "${ENABLE_QEXT_SCALAR}" == "1" || "${ENABLE_FIXED_SCALAR}" == "1" || "${ENABLE_FIXED_QEXT_SCALAR}" == "1" ]]; then
  LIBOPUS_CFLAGS="${LIBOPUS_CFLAGS} ${SCALAR_C_VECTOR_FLAGS[*]}"
fi
if [[ -n "${GOPUS_LIBOPUS_AMD64_TARGET}" ]]; then
  LIBOPUS_CFLAGS="${LIBOPUS_CFLAGS} ${AMD64_TARGET_CFLAGS}"
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
if [[ -n "${GOPUS_LIBOPUS_AMD64_TARGET}" ]]; then
  case "${HOST_ARCH}" in
    x86_64|amd64) ;;
    *) echo "error: GOPUS_LIBOPUS_AMD64_TARGET requires an amd64 host, got ${HOST_ARCH}" >&2; exit 1 ;;
  esac
  if [[ "${HOST_OS}" == "Darwin" ]] && command -v sysctl >/dev/null 2>&1 && [[ "$(sysctl -n hw.optional.arm64 2>/dev/null || echo 0)" == "1" ]]; then
    echo "error: GOPUS_LIBOPUS_AMD64_TARGET requires an amd64 host, not Apple Silicon under translation" >&2
    exit 1
  fi
  if [[ "${HOST_BITS}" != "64" ]]; then
    echo "error: GOPUS_LIBOPUS_AMD64_TARGET requires a 64-bit amd64 host, got ${HOST_BITS}-bit" >&2
    exit 1
  fi
  case "${CC_TARGET}" in
    x86_64-*|amd64-*) ;;
    *) echo "error: GOPUS_LIBOPUS_AMD64_TARGET requires an amd64 C compiler target, got ${CC_TARGET:-unknown}" >&2; exit 1 ;;
  esac
fi
CONFIGURE_STAMP="${CONFIGURE_FLAGS[*]}"
CUSTOM_STAMP=$((ENABLE_CUSTOM + ENABLE_CUSTOM_SCALAR + ENABLE_CUSTOM_QEXT_SCALAR + ENABLE_CUSTOM_QEXT_SIMD + ENABLE_CUSTOM_FIXED_SCALAR + ENABLE_CUSTOM_FIXED_SIMD + ENABLE_CUSTOM_FIXED_QEXT_SCALAR + ENABLE_CUSTOM_FIXED_QEXT_SIMD))
QEXT_STAMP=$((ENABLE_QEXT + ENABLE_QEXT_SCALAR + ENABLE_QEXT_SIMD + ENABLE_DRED_QEXT_SCALAR + ENABLE_DRED_QEXT_SIMD + ENABLE_FIXED_QEXT_SCALAR + ENABLE_FIXED_QEXT_SIMD + ENABLE_CUSTOM_QEXT_SCALAR + ENABLE_CUSTOM_QEXT_SIMD + ENABLE_CUSTOM_FIXED_QEXT_SCALAR + ENABLE_CUSTOM_FIXED_QEXT_SIMD))
FIXED_STAMP=$((ENABLE_FIXED_SCALAR + ENABLE_FIXED_SIMD + ENABLE_FIXED_QEXT_SCALAR + ENABLE_FIXED_QEXT_SIMD + ENABLE_CUSTOM_FIXED_SCALAR + ENABLE_CUSTOM_FIXED_SIMD + ENABLE_CUSTOM_FIXED_QEXT_SCALAR + ENABLE_CUSTOM_FIXED_QEXT_SIMD))
BUILD_STAMP=$'gopus libopus helper build v5\nversion='"${LIBOPUS_VERSION}"$'\nqext='"${QEXT_STAMP}"$'\nfixed='"${FIXED_STAMP}"$'\ncustom='"${CUSTOM_STAMP}"$'\nhost_os='"${HOST_OS}"$'\nhost_arch='"${HOST_ARCH}"$'\nhost_bits='"${HOST_BITS}"$'\ncc='"${LIBOPUS_CC}"$'\ncc_path='"${CC_PATH}"$'\ncc_target='"${CC_TARGET}"$'\ncc_version='"${CC_VERSION}"$'\nconfigure='"${CONFIGURE_STAMP}"$'\nCFLAGS='"${LIBOPUS_CFLAGS}"$'\nCPPFLAGS='"${LIBOPUS_CPPFLAGS}"$'\nLDFLAGS='"${LIBOPUS_LDFLAGS}"$'\n'
if [[ -n "${GOPUS_LIBOPUS_AMD64_TARGET}" ]]; then
  BUILD_STAMP+="amd64_target=${GOPUS_LIBOPUS_AMD64_TARGET}"$'\n'
fi
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
DRED_MODEL_SOURCE_HASHES=""
if [[ "${ENABLE_DRED_QEXT_SCALAR}" == "1" || "${ENABLE_DRED_QEXT_SIMD}" == "1" ]]; then
  case "${LIBOPUS_VERSION}" in
    1.6.1)
      PITCHDNN_DATA_SHA256="921b6157ff7a6200741c8b3e0c6d0183f2c34567297d63b065486be2bbf995ac"
      DRED_RDOVAE_ENCODER_DATA_SHA256="3bf6d5cbfa3b1fee99a0e65253eeecaa92f861533b9c3926b494e2776c922e5e"
      ;;
    *)
      echo "error: unsupported DRED model source hashes for libopus ${LIBOPUS_VERSION}" >&2
      exit 1
      ;;
  esac
  DRED_MODEL_SOURCE_HASHES="pitchdnn_data.c=${PITCHDNN_DATA_SHA256};dred_rdovae_enc_data.c=${DRED_RDOVAE_ENCODER_DATA_SHA256}"
  BUILD_STAMP+="dnn_model_sources=${DRED_MODEL_SOURCE_HASHES}"$'\n'
fi

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

dred_model_sources_are_pinned() {
  [[ -n "${DRED_MODEL_SOURCE_HASHES}" ]] || return 0
  local pitchdnn="${SRC_DIR}/dnn/pitchdnn_data.c"
  local encoder="${SRC_DIR}/dnn/dred_rdovae_enc_data.c"
  [[ -f "${pitchdnn}" && -f "${encoder}" ]] || return 1
  verify_sha256 "${pitchdnn}" "${PITCHDNN_DATA_SHA256}" || return 1
  verify_sha256 "${encoder}" "${DRED_RDOVAE_ENCODER_DATA_SHA256}"
}

build_stamp_is_current() {
  local stamp="${SRC_DIR}/${BUILD_STAMP_FILE}"
  [[ -f "${stamp}" ]] && [[ "$(cat "${stamp}")"$'\n' == "${BUILD_STAMP}" ]]
}

build_outputs_are_current() {
  OPUS_DEMO_PATH="$(find_built_tool opus_demo)" || return 1
  OPUS_COMPARE_PATH="$(find_built_tool opus_compare)" || return 1
  LIBOPUS_STATIC_PATH="$(find_static_lib)" || return 1
  dred_model_sources_are_pinned || return 1
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

if setup_error="$(LC_ALL=C mkdir -p "${TMP_DIR}" 2>&1)"; then
  :
else
  echo "error: cannot prepare libopus reference directory ${TMP_DIR}: ${setup_error}" >&2
  exit 1
fi

while :; do
  if lock_error="$(LC_ALL=C mkdir "${LOCK_DIR}" 2>&1)"; then
    break
  fi
  case "${lock_error}" in
    *": File exists")
      if [[ -d "${LOCK_DIR}" && ! -L "${LOCK_DIR}" ]]; then
        sleep 1
        continue
      fi
      if [[ -e "${LOCK_DIR}" || -L "${LOCK_DIR}" ]]; then
        echo "error: libopus lock path exists but is not a directory: ${LOCK_DIR} (${lock_error})" >&2
        exit 1
      fi
      # The holder can release the lock between mkdir and the directory check.
      continue
      ;;
    *)
      echo "error: cannot create libopus lock ${LOCK_DIR}: ${lock_error}" >&2
      exit 1
      ;;
  esac
done
trap 'rmdir "${LOCK_DIR}" 2>/dev/null || true' EXIT

if build_outputs_are_current; then
  echo "${OPUS_DEMO_PATH}"
  exit 0
fi

if [[ -n "${DRED_MODEL_SOURCE_HASHES}" && -d "${SRC_DIR}" ]] && ! dred_model_sources_are_pinned; then
  echo "Re-extracting isolated libopus ${LIBOPUS_VERSION} DRED-QEXT tree with unpinned DNN model data" >&2
  rm -rf "${SRC_DIR}"
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
