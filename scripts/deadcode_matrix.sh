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
#      configuration (feature-tag combo x GOARCH x source-selection lane).
#      The configs are derived from the CI matrices (lint-tag-matrix
#      LINT_TAG_CONFIGS, build-config-matrix)
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
# Base entries are: <label>;<GOARCH>;<comma-separated build tags>. Each base
# entry expands to scalar and SIMD lanes where `nosimd` does not force scalar.
# amd64 entries also expand to GOAMD64=v3, which selects additional source.
#
# Feature tags come from Makefile LINT_TAG_CONFIGS
#   (nosimd gopus_dred gopus_osce gopus_qext gopus_fixed_point gopus_custom_modes)
# plus the default (no tags) build. The build-config-matrix CI gate runs the
# whole suite under `nosimd`, and the parity/oracle gates run under
# gopus_libopus_oracle (which also pulls in the non-test allocation probe and the
# internal/libopustest Probe* exports). gopus_custom_modes carries real non-test CELT
# custom-mode source, so it must be analyzed too.
#
# GOARCH: arm64 is the dev host and amd64 is analyzed cross. Source selection
# follows GOARCH, build tags, GOEXPERIMENT, and GOAMD64: ordinary builds select
# scalar Go, while GOEXPERIMENT=simd opts into archsimd files and `nosimd`
# forces scalar selection. amd64 v3 source is also analyzed explicitly. Both
# architectures are shipped/tested in CI
# (macos-latest + ubuntu-24.04-arm are arm64; ubuntu-latest + windows-latest are
# amd64).
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
  add_cfg "nosimd;${arch};nosimd"
  add_cfg "dred;${arch};gopus_dred"
  add_cfg "extra_controls;${arch};gopus_osce"
  add_cfg "qext;${arch};gopus_qext"
  add_cfg "fixedpoint;${arch};gopus_fixed_point"
  add_cfg "custom;${arch};gopus_custom_modes"
done

if [ "${QUICK}" = "0" ]; then
  # Oracle overlays: make the analyzers trace the tag-gated parity tests so the
  # cross-package Probe* helpers and the gopus_libopus_oracle non-test probe are
  # counted live. One per feature surface that has oracle tests, both arches.
  for arch in arm64 amd64; do
    add_cfg "oracle;${arch};gopus_libopus_oracle"
    add_cfg "oracle_dred;${arch};gopus_dred,gopus_libopus_oracle"
    add_cfg "oracle_extra;${arch};gopus_osce,gopus_libopus_oracle"
    add_cfg "oracle_qext;${arch};gopus_qext,gopus_libopus_oracle"
    add_cfg "oracle_fixed;${arch};gopus_fixed_point,gopus_libopus_oracle"
    add_cfg "oracle_custom;${arch};gopus_custom_modes,gopus_libopus_oracle"
    add_cfg "oracle_nosimd;${arch};nosimd,gopus_libopus_oracle"
  done
  # Composite optional-feature builds the public-API contract tests cover, plus
  # niche source-bearing tags (silk trace, neon tone LPC corr, libopus bench).
  for arch in arm64 amd64; do
    add_cfg "dred_qext;${arch};gopus_dred,gopus_qext"
    add_cfg "dred_extra;${arch};gopus_dred,gopus_osce"
    add_cfg "extra_qext;${arch};gopus_osce,gopus_qext"
    add_cfg "all_feature;${arch};gopus_dred,gopus_osce,gopus_qext"
    add_cfg "silk_trace;${arch};gopus_silk_trace"
    add_cfg "libopus_bench;${arch};gopus_libopus_bench"
  done
  # arm64-only opt-in NEON tone/LPC kernel.
  add_cfg "neon_tone;arm64;gopus_neon_tone_lpc_corr"
fi

BASE_CONFIGS=("${CONFIGS[@]}")
CONFIGS=()

# Expand source-selection axes explicitly. A caller's GOEXPERIMENT or GOAMD64
# must not silently change which lane a config represents.
for entry in "${BASE_CONFIGS[@]}"; do
  IFS=';' read -r label arch tags <<< "${entry}"
  goamd64=""
  if [ "${arch}" = "amd64" ]; then goamd64="v1"; fi
  CONFIGS+=("${label};${arch};${tags};scalar;${goamd64}")
done
for entry in "${BASE_CONFIGS[@]}"; do
  IFS=';' read -r label arch tags <<< "${entry}"
  case ",${tags}," in
    *,nosimd,*) continue ;;
  esac
  goamd64=""
  if [ "${arch}" = "amd64" ]; then goamd64="v1"; fi
  CONFIGS+=("simd_${label};${arch};${tags};simd;${goamd64}")
done
for entry in "${BASE_CONFIGS[@]}"; do
  IFS=';' read -r label arch tags <<< "${entry}"
  [ "${arch}" = "amd64" ] || continue
  CONFIGS+=("amd64v3_${label};amd64;${tags};scalar;v3")
done
for entry in "${BASE_CONFIGS[@]}"; do
  IFS=';' read -r label arch tags <<< "${entry}"
  [ "${arch}" = "amd64" ] || continue
  case ",${tags}," in
    *,nosimd,*) continue ;;
  esac
  CONFIGS+=("amd64v3_simd_${label};amd64;${tags};simd;v3")
done

echo "deadcode-matrix: ${#CONFIGS[@]} configurations" >&2

run_analyzer() {
  local arch="$1" goamd64="$2" experiment_lane="$3"
  shift 3
  local experiment=""
  if [ "${experiment_lane}" = "simd" ]; then experiment="simd"; fi
  if [ "${arch}" = "amd64" ]; then
    GOARCH="${arch}" GOAMD64="${goamd64}" GOEXPERIMENT="${experiment}" "$@"
  else
    env -u GOAMD64 GOARCH="${arch}" GOEXPERIMENT="${experiment}" "$@"
  fi
}

mark_config_failed() {
  local keys_file="$1" label="$2" arch="$3" goamd64="$4" experiment="$5" reason="$6"
  echo "__ANALYZER_FAILED__" >> "${keys_file}"
  echo "  [error] ${label} (GOARCH=${arch} GOAMD64=${goamd64:-default} lane=${experiment}): ${reason}" >&2
}

# Run both analyzers for one config, emit normalized "file\tsymbol" lines.
run_config() {
  local label="$1" arch="$2" tags="$3" experiment="$4" goamd64="$5"
  local keys_file="${OUT_DIR}/${label}.${arch}.keys"
  : > "${keys_file}"

  # deadcode (RTA reachability; functions/methods). -test traces test exes.
  local dc_json="${OUT_DIR}/${label}.${arch}.deadcode.json"
  local dc_keys="${OUT_DIR}/${label}.${arch}.deadcode.findings"
  local dc_status=0
  run_analyzer "${arch}" "${goamd64}" "${experiment}" deadcode -test -json -tags "${tags}" ./... >"${dc_json}" 2>"${dc_json}.err" || dc_status=$?
  if [ "${dc_status}" -eq 0 ]; then
    if ! python3 "${OUT_DIR}/parse_deadcode.py" "${dc_json}" "${ROOT_DIR}" >"${dc_keys}"; then
      mark_config_failed "${keys_file}" "${label}" "${arch}" "${goamd64}" "${experiment}" "deadcode emitted invalid JSON"
      return
    fi
    cat "${dc_keys}" >>"${keys_file}"
  else
    mark_config_failed "${keys_file}" "${label}" "${arch}" "${goamd64}" "${experiment}" "deadcode exited ${dc_status}: $(head -1 "${dc_json}.err")"
    return
  fi

  # staticcheck U1000 (unused funcs/types/consts/vars/fields). Package-path form.
  local sc_json="${OUT_DIR}/${label}.${arch}.staticcheck.json"
  local sc_keys="${OUT_DIR}/${label}.${arch}.staticcheck.findings"
  local sc_status=0
  # Exit 1 means U1000 findings; exit 0 means none. Higher statuses indicate a
  # failed analyzer run. Restrict checks so an unrelated diagnostic cannot be
  # mistaken for an expected unused-symbol result.
  run_analyzer "${arch}" "${goamd64}" "${experiment}" staticcheck -f json -checks U1000 -tags "${tags}" ./... >"${sc_json}" 2>"${sc_json}.err" || sc_status=$?
  if [ "${sc_status}" -gt 1 ]; then
    mark_config_failed "${keys_file}" "${label}" "${arch}" "${goamd64}" "${experiment}" "staticcheck exited ${sc_status}: $(head -1 "${sc_json}.err")"
    return
  fi
  if ! python3 "${OUT_DIR}/parse_staticcheck.py" "${sc_json}" "${ROOT_DIR}" >"${sc_keys}"; then
    mark_config_failed "${keys_file}" "${label}" "${arch}" "${goamd64}" "${experiment}" "staticcheck emitted invalid JSON"
    return
  fi
  if [ "${sc_status}" -eq 1 ] && [ ! -s "${sc_keys}" ]; then
    mark_config_failed "${keys_file}" "${label}" "${arch}" "${goamd64}" "${experiment}" "staticcheck exited 1 without U1000 findings"
    return
  fi
  cat "${sc_keys}" >>"${keys_file}"
}

# --- embedded python helpers (robust JSON parsing) ---
cat > "${OUT_DIR}/parse_deadcode.py" <<'PY'
import json, sys
path, root = sys.argv[1], sys.argv[2].rstrip("/") + "/"
try:
    with open(path) as source:
        data = json.load(source)
except Exception as exc:
    print("invalid deadcode JSON: {}".format(exc), file=sys.stderr)
    sys.exit(1)
if data is None:  # deadcode encodes an empty package slice as top-level JSON null
    sys.exit(0)
if not isinstance(data, list):
    print("invalid deadcode JSON: expected a package list", file=sys.stderr)
    sys.exit(1)
for pkg in data:
    if not isinstance(pkg, dict):
        print("invalid deadcode JSON: package has no Funcs list", file=sys.stderr)
        sys.exit(1)
    funcs = pkg.get("Funcs")
    if funcs is None:  # nil slices encode as JSON null when the package has no findings
        continue
    if not isinstance(funcs, list):
        print("invalid deadcode JSON: Funcs is not a list", file=sys.stderr)
        sys.exit(1)
    for fn in funcs:
        if not isinstance(fn, dict):
            print("invalid deadcode JSON: function entry is not an object", file=sys.stderr)
            sys.exit(1)
        if fn.get("Generated"):
            continue  # generated files are not hand-maintained dead code
        pos = fn.get("Position", {})
        if not isinstance(pos, dict):
            print("invalid deadcode JSON: function position is not an object", file=sys.stderr)
            sys.exit(1)
        f = pos.get("File", "")
        name = fn.get("Name", "")
        if not isinstance(f, str) or not isinstance(name, str):
            print("invalid deadcode JSON: function name or file is not a string", file=sys.stderr)
            sys.exit(1)
        if f.startswith(root):
            f = f[len(root):]
        if f and name:
            print(f + "\t" + name)
PY

cat > "${OUT_DIR}/parse_staticcheck.py" <<'PY'
import json, sys
path, root = sys.argv[1], sys.argv[2].rstrip("/") + "/"
# Staticcheck reports methods as `func (*T).m is unused`. Normalize receiver
# spelling to deadcode's `T.m` key so the analyzers can intersect the symbol.
with open(path) as source:
    for line_number, line in enumerate(source, 1):
        line = line.strip()
        if not line:
            continue
        try:
            o = json.loads(line)
        except Exception as exc:
            print("invalid staticcheck JSON on line {}: {}".format(line_number, exc), file=sys.stderr)
            sys.exit(1)
        if not isinstance(o, dict) or o.get("code") != "U1000":
            print("unexpected staticcheck diagnostic on line {}".format(line_number), file=sys.stderr)
            sys.exit(1)
        message = o.get("message", "")
        if not isinstance(message, str) or not message.endswith(" is unused"):
            print("unrecognized U1000 diagnostic on line {}".format(line_number), file=sys.stderr)
            sys.exit(1)
        body = message[:-len(" is unused")]
        kind = ""
        name = ""
        for candidate_kind in ("type param", "func", "type", "field", "const", "var"):
            prefix = candidate_kind + " "
            if body.startswith(prefix):
                kind = candidate_kind
                name = body[len(prefix):]
                break
        if not kind or not name:
            print("unrecognized U1000 diagnostic on line {}".format(line_number), file=sys.stderr)
            sys.exit(1)
        sym = name
        if kind == "func" and name.startswith("(") and ")." in name:
            receiver, method = name[1:].split(").", 1)
            receiver = receiver.lstrip("*").split("[", 1)[0]
            if not receiver or not method:
                print("invalid U1000 method name on line {}".format(line_number), file=sys.stderr)
                sys.exit(1)
            sym = receiver + "." + method
        loc = o.get("location", {})
        if not isinstance(loc, dict):
            print("invalid staticcheck location on line {}".format(line_number), file=sys.stderr)
            sys.exit(1)
        f = loc.get("file", "")
        if not isinstance(f, str) or not f:
            print("missing staticcheck file location on line {}".format(line_number), file=sys.stderr)
            sys.exit(1)
        if f.startswith(root):
            f = f[len(root):]
        if f and sym:
            print(f + "\t" + sym)
PY

# --- run every config ---
i=0
for entry in "${CONFIGS[@]}"; do
  IFS=';' read -r label arch tags experiment goamd64 <<< "${entry}"
  i=$((i+1))
  echo "  [${i}/${#CONFIGS[@]}] ${label} (GOARCH=${arch} GOAMD64=${goamd64:-default} lane=${experiment} tags='${tags}')" >&2
  run_config "${label}" "${arch}" "${tags}" "${experiment}" "${goamd64}"
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

if invalid:
    print("DEADCODE MATRIX INCOMPLETE: analyzer failures prevent candidate output", file=sys.stderr)
    print("# INVALID configs (analyzer failed): {}".format(", ".join(invalid)), file=sys.stderr)
    sys.exit(1)

if not sets:
    print("NO CONFIGS ANALYZED", file=sys.stderr)
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
    print(key)
PY

if [ "${GREPCHECK}" = "1" ]; then
  echo ""
  echo "# whole-tree caller cross-check (zero refs anywhere but the definition => truly dead)"
  while IFS=$'\t' read -r file sym; do
    [ -z "${file}" ] && continue
    # Symbol may be "Type.method"; grep the bare identifier across all .go files.
    ident="${sym##*.}"
    # Count references excluding the definition file's own decl line is hard in
    # pure bash; instead report total hits and let a human eyeball the defn line.
    hits="$(grep -rEn --include='*.go' "\\b${ident}\\b" . 2>/dev/null | grep -v '/\.git/' | wc -l | tr -d ' ' || true)"
    printf '%-70s refs=%s\n' "${file}:${sym}" "${hits}"
  done < "${OUT_DIR}/candidates.txt"
fi
