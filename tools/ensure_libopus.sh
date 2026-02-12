#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT_DIR}/tmp_check"
LIBOPUS_VERSION="${LIBOPUS_VERSION:-1.6.1}"
SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}"
TARBALL="${TMP_DIR}/opus-${LIBOPUS_VERSION}.tar.gz"

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

if [[ -x "${SRC_DIR}/opus_demo" ]]; then
  echo "${SRC_DIR}/opus_demo"
  exit 0
fi

mkdir -p "${TMP_DIR}"

if [[ ! -d "${SRC_DIR}" ]]; then
  if [[ ! -f "${TARBALL}" ]]; then
    download_tarball "${TARBALL}" "${LIBOPUS_VERSION}"
  fi
  verify_sha256 "${TARBALL}" "${EXPECTED_SHA256}"
  tar -xzf "${TARBALL}" -C "${TMP_DIR}"
fi

if [[ ! -f "${SRC_DIR}/configure" ]]; then
  echo "error: missing ${SRC_DIR}/configure (unexpected source layout)" >&2
  exit 1
fi

cd "${SRC_DIR}"
if [[ ! -f Makefile ]]; then
  ./configure --enable-static --disable-shared
fi

if command -v getconf >/dev/null 2>&1; then
  JOBS="$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)"
elif command -v sysctl >/dev/null 2>&1; then
  JOBS="$(sysctl -n hw.ncpu 2>/dev/null || echo 4)"
else
  JOBS=4
fi

make -j"${JOBS}"

if [[ ! -x "${SRC_DIR}/opus_demo" ]]; then
  echo "error: expected executable not produced: ${SRC_DIR}/opus_demo" >&2
  exit 1
fi

echo "${SRC_DIR}/opus_demo"
