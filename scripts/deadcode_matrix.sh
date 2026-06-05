#!/usr/bin/env bash
# Mechanical, re-runnable genuinely-dead-code detector for the multi-build-tag
# gopus tree.
#
# Problem this solves: a single-config `deadcode ./...` run is false-positive
# dominated here because gopus is heavily build-tag- and GOARCH-gated and
# oracle-test-heavy. A symbol that looks unreachable on the host/default config
# is routinely live in some other build (e.g. the gopus_dred setDNNBlob, the
# gopus_libopus_oracle cross-package probes, GOARCH-specific kernels).
#
# Method:
#   1. Run two static dead-code analyzers under EVERY shipped/tested build
#      configuration (feature-tag combo x GOARCH). The configs are derived from
#      the CI matrices (lint-tag-matrix LINT_TAG_CONFIGS, build-config-matrix)
#      plus the oracle/arch overlays the parity tests actually exercise.
#        - golang.org/x/tools/cmd/deadcode -test  (RTA reachability; functions)
#        - staticcheck U1000                        (unused funcs/types/consts/...)
#      -test / oracle tags make the analyzers trace the test executables, so
#      cross-package oracle probes are counted live in the configs that use them.
#   2. INTERSECT the per-config flagged sets. A symbol is a candidate only if it
#      is flagged dead in EVERY config. Live in even one config => NOT dead.
#   3. Print the candidate set as stable file:symbol keys. A separate grep-based
#      caller cross-check (see --grepcheck) rules out refl/string/cgo/generated
#      references across the whole tree before anything is removed.
#
# Cross-GOARCH configs are analyzed statically (the analyzers do not execute the
# target), which is exactly what reachability/unused analysis needs.
#
# Usage:
#   scripts/deadcode_matrix.sh            # run full matrix + print dead set
#   scripts/deadcode_matrix.sh --quick    # arm64/amd64 native+cross only, no extras
#   scripts/deadcode_matrix.sh --grepcheck# also run the whole-tree caller scan on candidates
#   scripts/deadcode_matrix.sh --keep     # keep per-config raw JSON under .tmp/deadcode
#
# Requires (auto-checked): go, deadcode, staticcheck on PATH.
#   GOWORK=off go install golang.org/x/tools/cmd/deadcode@latest
#   GOWORK=off go install honnef.co/go/tools/cmd/staticcheck@latest
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

export GOWORK=off
export GOFLAGS="${GOFLAGS:-} -mod=readonly"

QUICK=0
GREPCHECK=0
KEEP=0
for arg in "$@"; do
  case "${arg}" in
    --quick) QUICK=1 ;;
    --grepcheck) GREPCHECK=1 ;;
    --keep) KEEP=1 ;;
    -h|--help) sed -n '2,40p' "${BASH_SOURCE[0]}"; exit 0 ;;
    *) echo "unknown flag: ${arg}" >&2; exit 2 ;;
  esac
done

command -v go >/dev/null 2>&1 || { echo "go not found on PATH" >&2; exit 1; }
command -v deadcode >/dev/null 2>&1 || {
  echo "deadcode not found. Install: GOWORK=off go install golang.org/x/tools/cmd/deadcode@latest" >&2; exit 1; }
command -v staticcheck >/dev/null 2>&1 || {
  echo "staticcheck not found. Install: GOWORK=off go install honnef.co/go/tools/cmd/staticcheck@latest" >&2; exit 1; }

OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/gopus_deadcode.XXXXXX")"
if [ "${KEEP}" = "1" ]; then
  OUT_DIR="${ROOT_DIR}/.tmp/deadcode"
  rm -rf "${OUT_DIR}"
  mkdir -p "${OUT_DIR}"
fi
cleanup() { if [ "${KEEP}" != "1" ]; then rm -rf "${OUT_DIR}"; fi; }
trap cleanup EXIT

# ---------------------------------------------------------------------------
# Config matrix.
#
# Each entry is:  <label>;<GOARCH>;<comma-separated build tags>
#
# Feature tags come from Makefile LINT_TAG_CONFIGS
#   (purego gopus_dred gopus_extra_controls gopus_qext gopus_fixedpoint gopus_custom)
# plus the default (no tags) build. The build-config-matrix CI gate runs the
# whole suite under `purego`, and the parity/oracle gates run under
# gopus_libopus_oracle (which also pulls in the non-test allocation probe and the
# internal/libopustest Probe* exports). gopus_custom carries real non-test CELT
# custom-mode source, so it must be analyzed too.
#
# GOARCH: arm64 is the dev host (native, also exercises arm64 && !purego asm
# paths); amd64 is analyzed cross (the amd64 && !purego kernels). Both archs are
# shipped/tested in CI (macos-latest + ubuntu-24.04-arm are arm64; ubuntu-latest
# + windows-latest are amd64).
#
# The oracle overlay is combined with each feature tag because the parity tests
# that reference cross-package probes are themselves tag-gated; a probe is only
# "live" when its oracle test compiles under that tag. We therefore add an
# oracle variant for the configs whose parity tests use it.
# ---------------------------------------------------------------------------
declare -a CONFIGS

add_cfg() { CONFIGS+=("$1"); }

# Core feature builds, both arches.
for arch in arm64 amd64; do
  add_cfg "default;${arch};"
  add_cfg "purego;${arch};purego"
  add_cfg "dred;${arch};gopus_dred"
  add_cfg "extra_controls;${arch};gopus_extra_controls"
  add_cfg "qext;${arch};gopus_qext"
  add_cfg "fixedpoint;${arch};gopus_fixedpoint"
  add_cfg "custom;${arch};gopus_custom"
done

if [ "${QUICK}" = "0" ]; then
  # Oracle overlays: make the analyzers trace the tag-gated parity tests so the
  # cross-package Probe* helpers and the gopus_libopus_oracle non-test probe are
  # counted live. One per feature surface that has oracle tests, both arches.
  for arch in arm64 amd64; do
    add_cfg "oracle;${arch};gopus_libopus_oracle"
    add_cfg "oracle_dred;${arch};gopus_dred,gopus_libopus_oracle"
    add_cfg "oracle_extra;${arch};gopus_extra_controls,gopus_libopus_oracle"
    add_cfg "oracle_qext;${arch};gopus_qext,gopus_libopus_oracle"
    add_cfg "oracle_fixed;${arch};gopus_fixedpoint,gopus_libopus_oracle"
    add_cfg "oracle_custom;${arch};gopus_custom,gopus_libopus_oracle"
    add_cfg "oracle_purego;${arch};purego,gopus_libopus_oracle"
  done
  # Composite optional-feature builds the public-API contract tests cover, plus
  # niche source-bearing tags (silk trace, neon tone LPC corr, libopus bench).
  for arch in arm64 amd64; do
    add_cfg "dred_qext;${arch};gopus_dred,gopus_qext"
    add_cfg "dred_extra;${arch};gopus_dred,gopus_extra_controls"
    add_cfg "extra_qext;${arch};gopus_extra_controls,gopus_qext"
    add_cfg "all_feature;${arch};gopus_dred,gopus_extra_controls,gopus_qext"
    add_cfg "silk_trace;${arch};gopus_silk_trace"
    add_cfg "libopus_bench;${arch};gopus_libopus_bench"
  done
  # arm64-only opt-in NEON tone/LPC kernel.
  add_cfg "neon_tone;arm64;gopus_neon_tone_lpc_corr"
fi

echo "deadcode-matrix: ${#CONFIGS[@]} configurations" >&2

# Run both analyzers for one config, emit normalized "file\tsymbol" lines.
run_config() {
  local label="$1" arch="$2" tags="$3"
  local tagflag=()
  [ -n "${tags}" ] && tagflag=(-tags "${tags}")
  local keys_file="${OUT_DIR}/${label}.${arch}.keys"
  : > "${keys_file}"

  # deadcode (RTA reachability; functions/methods). -test traces test exes.
  local dc_json="${OUT_DIR}/${label}.${arch}.deadcode.json"
  if GOARCH="${arch}" deadcode -test -json "${tagflag[@]}" ./... >"${dc_json}" 2>"${dc_json}.err"; then
    python3 "${OUT_DIR}/parse_deadcode.py" "${dc_json}" "${ROOT_DIR}" >>"${keys_file}"
  else
    echo "  [warn] deadcode failed for ${label}/${arch}: $(head -1 "${dc_json}.err")" >&2
    # A hard analyzer failure must NOT silently shrink a config's flagged set to
    # empty (which would make the intersection wrongly keep dead symbols out).
    # Mark the config invalid so intersection drops it explicitly.
    echo "__ANALYZER_FAILED__" >>"${keys_file}"
    return
  fi

  # staticcheck U1000 (unused funcs/types/consts/vars/fields). Package-path form.
  local sc_json="${OUT_DIR}/${label}.${arch}.staticcheck.json"
  # staticcheck exits non-zero when it reports findings; that is expected.
  GOARCH="${arch}" staticcheck -f json "${tagflag[@]}" ./... >"${sc_json}" 2>"${sc_json}.err" || true
  if [ -s "${sc_json}" ]; then
    python3 "${OUT_DIR}/parse_staticcheck.py" "${sc_json}" "${ROOT_DIR}" >>"${keys_file}"
  fi
}

# --- embedded python helpers (robust JSON parsing) ---
cat > "${OUT_DIR}/parse_deadcode.py" <<'PY'
import json, os, sys
path, root = sys.argv[1], sys.argv[2].rstrip("/") + "/"
try:
    data = json.load(open(path))
except Exception:
    sys.exit(0)
for pkg in data:
    for fn in pkg.get("Funcs", []):
        if fn.get("Generated"):
            continue  # generated files are not hand-maintained dead code
        pos = fn.get("Position", {})
        f = pos.get("File", "")
        if f.startswith(root):
            f = f[len(root):]
        name = fn.get("Name", "")
        if f and name:
            print(f + "\t" + name)
PY

cat > "${OUT_DIR}/parse_staticcheck.py" <<'PY'
import json, os, re, sys
path, root = sys.argv[1], sys.argv[2].rstrip("/") + "/"
# "func foo is unused", "type Bar is unused", "field x is unused",
# "const C is unused", "var v is unused", "method T.m is unused"
pat = re.compile(r"^(?:func|type|field|const|var|method)\s+([A-Za-z0-9_.]+)\s+is unused")
for line in open(path):
    line = line.strip()
    if not line.startswith("{"):
        continue
    try:
        o = json.loads(line)
    except Exception:
        continue
    if o.get("code") != "U1000":
        continue
    m = pat.match(o.get("message", ""))
    if not m:
        continue
    sym = m.group(1)
    loc = o.get("location", {})
    f = loc.get("file", "")
    if f.startswith(root):
        f = f[len(root):]
    if f and sym:
        print(f + "\t" + sym)
PY

# --- run every config ---
i=0
for entry in "${CONFIGS[@]}"; do
  IFS=';' read -r label arch tags <<< "${entry}"
  i=$((i+1))
  echo "  [${i}/${#CONFIGS[@]}] ${label} (GOARCH=${arch} tags='${tags}')" >&2
  run_config "${label}" "${arch}" "${tags}"
done

# --- intersect: a key dead in EVERY valid config is a candidate ---
python3 - "${OUT_DIR}" <<'PY'
import glob, os, sys
out_dir = sys.argv[1]
files = sorted(glob.glob(os.path.join(out_dir, "*.keys")))
valid = []
invalid = []
sets = []
for kf in files:
    label = os.path.basename(kf)[:-5]
    keys = set()
    failed = False
    for line in open(kf):
        line = line.rstrip("\n")
        if line == "__ANALYZER_FAILED__":
            failed = True
            break
        if line:
            keys.add(line)
    if failed:
        invalid.append(label)
        continue
    valid.append(label)
    sets.append(keys)

if not sets:
    print("NO VALID CONFIGS ANALYZED", file=sys.stderr)
    sys.exit(1)

inter = set.intersection(*sets) if sets else set()

print("# deadcode-matrix intersection")
print("# valid configs ({}): {}".format(len(valid), ", ".join(valid)))
if invalid:
    print("# INVALID configs (analyzer failed, excluded): {}".format(", ".join(invalid)))
print("# genuinely-dead candidates (flagged in EVERY valid config): {}".format(len(inter)))
print()
for key in sorted(inter):
    f, sym = key.split("\t", 1)
    print("{}:{}".format(f, sym))
PY

# Persist the candidate list for the optional grep cross-check.
python3 - "${OUT_DIR}" > "${OUT_DIR}/candidates.txt" <<'PY'
import glob, os, sys
out_dir = sys.argv[1]
sets = []
for kf in sorted(glob.glob(os.path.join(out_dir, "*.keys"))):
    keys = set(); failed = False
    for line in open(kf):
        line = line.rstrip("\n")
        if line == "__ANALYZER_FAILED__":
            failed = True; break
        if line: keys.add(line)
    if not failed:
        sets.append(keys)
inter = set.intersection(*sets) if sets else set()
for key in sorted(inter):
    print(key.replace("\t", "::"))
PY

if [ "${GREPCHECK}" = "1" ]; then
  echo ""
  echo "# whole-tree caller cross-check (zero refs anywhere but the definition => truly dead)"
  while IFS='::' read -r file sym _rest; do
    [ -z "${file}" ] && continue
    # Symbol may be "Type.method"; grep the bare identifier across all .go files.
    ident="${sym##*.}"
    # Count references excluding the definition file's own decl line is hard in
    # pure bash; instead report total hits and let a human eyeball the defn line.
    hits="$(grep -rEn --include='*.go' "\\b${ident}\\b" . 2>/dev/null | grep -v '/\.git/' | wc -l | tr -d ' ')"
    printf '%-70s refs=%s\n' "${file}:${sym}" "${hits}"
  done < "${OUT_DIR}/candidates.txt"
fi
