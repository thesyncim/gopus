#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="${ROOT_DIR}/tmp_check"
LIBOPUS_VERSION="${LIBOPUS_VERSION:-1.6.1}"
SRC_DIR="${TMP_DIR}/opus-${LIBOPUS_VERSION}"
TARBALL="${TMP_DIR}/opus-${LIBOPUS_VERSION}.tar.gz"
DOWNLOAD_URL="https://downloads.xiph.org/releases/opus/opus-${LIBOPUS_VERSION}.tar.gz"

if [[ -x "${SRC_DIR}/opus_demo" ]]; then
  echo "${SRC_DIR}/opus_demo"
  exit 0
fi

mkdir -p "${TMP_DIR}"

if [[ ! -d "${SRC_DIR}" ]]; then
  echo "Fetching libopus ${LIBOPUS_VERSION} from ${DOWNLOAD_URL}"
  if command -v curl >/dev/null 2>&1; then
    curl -fL "${DOWNLOAD_URL}" -o "${TARBALL}"
  elif command -v wget >/dev/null 2>&1; then
    wget -O "${TARBALL}" "${DOWNLOAD_URL}"
  else
    echo "error: neither curl nor wget is available to download libopus" >&2
    exit 1
  fi
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
