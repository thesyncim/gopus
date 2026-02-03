#!/usr/bin/env python3
"""Compare gopus tables to libopus 1.6.1 C tables.

Usage:
  python tools/table_audit.py

Outputs a mismatch report and exits non-zero if mismatches found.
"""
from __future__ import annotations

import ast
import math
import os
import re
import sys
from typing import Any, Dict, List, Tuple

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
C_ROOT = os.path.join(REPO_ROOT, "tmp_check", "opus-1.6.1")

FLOAT_TOL = 1e-9


def read_text(path: str) -> str:
    with open(path, "r", encoding="utf-8") as f:
        return f.read()


def strip_c_comments(s: str) -> str:
    # Remove /* */ and // comments
    s = re.sub(r"/\*.*?\*/", "", s, flags=re.S)
    s = re.sub(r"//.*", "", s)
    return s


def strip_go_comments(s: str) -> str:
    # Remove /* */ and // comments
    s = re.sub(r"/\*.*?\*/", "", s, flags=re.S)
    s = re.sub(r"//.*", "", s)
    return s


def find_matching_brace(s: str, start: int) -> int:
    # start is index of '{'
    depth = 0
    i = start
    while i < len(s):
        c = s[i]
        if c == '{':
            depth += 1
        elif c == '}':
            depth -= 1
            if depth == 0:
                return i
        i += 1
    raise ValueError("Unmatched brace")


def eval_expr(expr: str) -> Any:
    expr = expr.strip()
    if not expr:
        raise ValueError("empty expression")

    # Remove C/Go numeric suffixes (f, F, u, U, l, L)
    # Handle integers and floats like 1f, 1.0f, .5f, 0.f
    expr = re.sub(r"([0-9]+)([uUlLfF]+)\b", r"\1", expr)
    expr = re.sub(r"([0-9]*\.[0-9]+|[0-9]+\.)[fF]\b", r"\1", expr)

    # Strip explicit casts like (opus_int16)
    expr = re.sub(r"\((?:const\s+)?[A-Za-z_][A-Za-z0-9_\s\*]*\)", "", expr)

    # Replace double negatives
    expr = expr.replace("--", "+")

    # Allow hex literals
    # Define safe eval environment
    def QCONST16(x, y):
        return int(round(float(x) * (1 << int(y))))

    def QCONST32(x, y):
        return int(round(float(x) * (1 << int(y))))

    env = {
        "__builtins__": None,
        "QCONST16": QCONST16,
        "QCONST32": QCONST32,
    }

    # Replace hex literals to avoid 'x' being treated as an identifier
    expr = re.sub(r"0x[0-9A-Fa-f]+", "0", expr)

    # Validate identifiers (only allow known macros)
    allowed_words = {"QCONST16", "QCONST32"}
    words = re.findall(r"[A-Za-z_][A-Za-z0-9_]*", expr)
    for w in words:
        if w not in allowed_words:
            raise ValueError(f"non-numeric expression: {expr}")

    try:
        return eval(expr, env, {})
    except Exception as e:
        raise ValueError(f"failed to eval '{expr}': {e}")


def parse_initializer(init: str) -> Any:
    # init includes outer braces
    s = init.strip()
    if not s.startswith("{"):
        raise ValueError("initializer does not start with '{'")

    i = 0

    def skip_ws():
        nonlocal i
        while i < len(s) and s[i].isspace():
            i += 1

    def parse_list():
        nonlocal i
        if s[i] != '{':
            raise ValueError("expected '{'")
        i += 1
        out = []
        while True:
            skip_ws()
            if i >= len(s):
                raise ValueError("unexpected end")
            if s[i] == '}':
                i += 1
                break
            if s[i] == ',':
                i += 1
                continue
            if s[i] == '{':
                out.append(parse_list())
                skip_ws()
                if i < len(s) and s[i] == ',':
                    i += 1
                continue
            # parse expression until comma or closing brace at same paren depth
            start = i
            paren = 0
            while i < len(s):
                c = s[i]
                if c == '(':
                    paren += 1
                elif c == ')':
                    paren = max(0, paren - 1)
                elif paren == 0 and (c == ',' or c == '}'):
                    break
                i += 1
            expr = s[start:i].strip()
            if expr:
                val = eval_expr(expr)
                out.append(val)
            if i < len(s) and s[i] == ',':
                i += 1
                continue
            if i < len(s) and s[i] == '}':
                i += 1
                break
        return out

    skip_ws()
    return parse_list()


def extract_c_arrays(root: str) -> Dict[str, List[Any]]:
    arrays: Dict[str, List[Any]] = {}
    locations: Dict[str, List[str]] = {}
    for dirpath, _, filenames in os.walk(root):
        for fn in filenames:
            if not (fn.endswith(".c") or fn.endswith(".h")):
                continue
            path = os.path.join(dirpath, fn)
            text = strip_c_comments(read_text(path))
            # Find array definitions: name[...]= {...}
            # This is a heuristic scan
            for m in re.finditer(r"\b([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\]\s*)+\=\s*\{", text):
                name = m.group(1)
                start = m.end() - 1  # at '{'
                try:
                    end = find_matching_brace(text, start)
                except ValueError:
                    continue
                init = text[start:end+1]
                locations.setdefault(name, []).append(path)
                arrays.setdefault(name, []).append((path, init))
    # Resolve duplicates: pick the most float-looking or last occurrence
    resolved: Dict[str, Any] = {}
    for name, inits in arrays.items():
        if len(inits) == 1:
            resolved[name] = inits[0][1]
            continue
        # Prefer initializer containing '.' for float tables
        float_inits = [x for x in inits if '.' in x[1]]
        if float_inits:
            resolved[name] = float_inits[-1][1]
        else:
            resolved[name] = inits[-1][1]
    parsed: Dict[str, Any] = {}
    for name, init in resolved.items():
        try:
            parsed[name] = parse_initializer(init)
        except Exception:
            # Skip arrays that contain non-numeric tokens (e.g., pointer arrays)
            continue
    return parsed


def extract_c_array_from_files(paths: List[str], name: str) -> Any | None:
    for rel in paths:
        path = os.path.join(REPO_ROOT, rel)
        if not os.path.exists(path):
            continue
        text = strip_c_comments(read_text(path))
        for m in re.finditer(r"\b([A-Za-z_][A-Za-z0-9_]*)\s*(?:\[[^\]]*\]\s*)+\=\s*\{", text):
            if m.group(1) != name:
                continue
            start = m.end() - 1
            try:
                end = find_matching_brace(text, start)
            except ValueError:
                continue
            init = text[start:end+1]
            try:
                return parse_initializer(init)
            except Exception:
                return None
    return None


def extract_celt_pvq_u_data() -> Any | None:
    path = os.path.join(REPO_ROOT, "tmp_check", "opus-1.6.1", "celt", "cwrs.c")
    if not os.path.exists(path):
        return None
    text = strip_c_comments(read_text(path))
    # Prefer non-extra rows variant (1272) when CWRS_EXTRA_ROWS not defined
    m = re.search(r"CELT_PVQ_U_DATA\s*\[\s*1272\s*\]\s*=\s*\{", text)
    if not m:
        return None
    start = m.end() - 1
    try:
        end = find_matching_brace(text, start)
    except ValueError:
        return None
    init = text[start:end+1]
    # Drop conditional extra rows inside initializer
    init = re.sub(r"#if\s+defined\(CWRS_EXTRA_ROWS\).*?#endif", "", init, flags=re.S)
    # Remove any remaining preprocessor lines (e.g., the #endif after the definition choice)
    init = re.sub(r"^\s*#.*$", "", init, flags=re.M)
    try:
        return parse_initializer(init)
    except Exception:
        return None


def extract_go_arrays(paths: List[str]) -> Dict[str, Any]:
    arrays: Dict[str, Any] = {}
    for path in paths:
        full = os.path.join(REPO_ROOT, path)
        text = strip_go_comments(read_text(full))
        # Find "var Name = ... {" definitions
        for m in re.finditer(r"\bvar\s+([A-Za-z_][A-Za-z0-9_]*)\s*=", text):
            name = m.group(1)
            # Find next '{' after '='
            eq_pos = m.end()
            brace_pos = text.find('{', eq_pos)
            if brace_pos == -1:
                continue
            # Heuristic: ensure this is a composite literal by checking for '}' later
            try:
                end = find_matching_brace(text, brace_pos)
            except ValueError:
                continue
            init = text[brace_pos:end+1]
            try:
                arrays[name] = parse_initializer(init)
            except Exception:
                # Skip non-numeric or complex initializers
                continue
    return arrays


def flatten(x: Any) -> List[Any]:
    if isinstance(x, list):
        out = []
        for v in x:
            out.extend(flatten(v))
        return out
    return [x]


def reshape(flat: List[Any], shape: List[int]) -> Any:
    if not shape:
        return flat[0]
    total = 1
    for n in shape:
        total *= n
    if len(flat) != total:
        raise ValueError(f"cannot reshape length {len(flat)} into {shape}")
    def build(idx: int, dims: List[int]) -> Tuple[Any, int]:
        if len(dims) == 1:
            return flat[idx:idx+dims[0]], idx + dims[0]
        arr = []
        cur = idx
        for _ in range(dims[0]):
            sub, cur = build(cur, dims[1:])
            arr.append(sub)
        return arr, cur
    arr, _ = build(0, shape)
    return arr


def compare_values(go_val: Any, c_val: Any, path: str, diffs: List[str]) -> None:
    # Compare recursively
    if isinstance(go_val, list) and isinstance(c_val, list):
        if len(go_val) != len(c_val):
            diffs.append(f"{path}: length mismatch go={len(go_val)} c={len(c_val)}")
            return
        for i, (gv, cv) in enumerate(zip(go_val, c_val)):
            compare_values(gv, cv, f"{path}[{i}]", diffs)
        return
    # numeric compare
    if isinstance(go_val, float) or isinstance(c_val, float):
        gv = float(go_val)
        cv = float(c_val)
        if math.isnan(gv) or math.isnan(cv) or abs(gv - cv) > FLOAT_TOL:
            diffs.append(f"{path}: {gv} != {cv}")
        return
    if int(go_val) != int(c_val):
        diffs.append(f"{path}: {go_val} != {c_val}")


def scale_values(val: Any, factor: float) -> Any:
    if isinstance(val, list):
        return [scale_values(v, factor) for v in val]
    return float(val) * factor


def main() -> int:
    c_arrays = extract_c_arrays(C_ROOT)
    # Override arrays that appear in non-core directories or require preprocessor handling
    eband_override = extract_c_array_from_files(
        ["tmp_check/opus-1.6.1/celt/modes.c"], "eband5ms"
    )
    if eband_override is not None:
        c_arrays["eband5ms"] = eband_override
    gains_override = extract_c_array_from_files(
        ["tmp_check/opus-1.6.1/celt/celt.c"], "gains"
    )
    if gains_override is not None:
        c_arrays["gains"] = gains_override
    pvq_override = extract_celt_pvq_u_data()
    if pvq_override is not None:
        c_arrays["CELT_PVQ_U_DATA"] = pvq_override

    go_files = [
        "celt/tables.go",
        "celt/alloc_tables.go",
        "celt/bands_quant.go",
        "celt/postfilter.go",
        "celt/cwrs.go",
        "encoder/hybrid.go",
        "silk/libopus_tables.go",
        "silk/resample_down_fir.go",
        "silk/resample_libopus.go",
        "silk/control_snr.go",
    ]
    go_arrays = extract_go_arrays(go_files)

    # Explicit mappings where names differ or reshape needed
    mappings = [
        # CELT
        {"go": "EBands", "c": "eband5ms"},
        {"go": "BandAlloc", "c": "band_allocation", "reshape_c": [11, 21]},
        {"go": "LogN", "c": "logN400"},
        {"go": "cacheCaps", "c": "cache_caps50"},
        {"go": "cacheIndex50", "c": "cache_index50"},
        {"go": "cacheBits50", "c": "cache_bits50"},
        {"go": "eMeans", "c": "eMeans"},
        {"go": "eProbModel", "c": "e_prob_model"},
        {"go": "smallEnergyICDF", "c": "small_energy_icdf"},
        {"go": "log2FracTable", "c": "LOG2_FRAC_TABLE"},
        {"go": "spreadICDF", "c": "spread_icdf"},
        {"go": "trimICDF", "c": "trim_icdf"},
        {"go": "tapsetICDF", "c": "tapset_icdf"},
        {"go": "tfSelectTable", "c": "tf_select_table"},
        {"go": "AlphaCoef", "c": "pred_coef"},
        {"go": "BetaCoefInter", "c": "beta_coef"},
        {"go": "orderyTable", "c": "ordery_table"},
        {"go": "bitInterleaveTable", "c": "bit_interleave_table"},
        {"go": "bitDeinterleaveTable", "c": "bit_deinterleave_table"},
        {"go": "combFilterGains", "c": "gains", "scale_c": 1.0 / 32768.0},
        {"go": "pvqUData", "c": "CELT_PVQ_U_DATA"},
        # Hybrid
        {"go": "hybridRateTable", "c": "rate_table"},
        # SILK resampler
        {"go": "delayMatrixEnc", "c": "delay_matrix_enc"},
        {"go": "delayMatrixDec", "c": "delay_matrix_dec", "truncate_c_cols": 5},
        {"go": "silkResamplerFracFIR12", "c": "silk_resampler_frac_FIR_12"},
        {"go": "silkResampler34Coefs", "c": "silk_Resampler_3_4_COEFS"},
        {"go": "silkResampler23Coefs", "c": "silk_Resampler_2_3_COEFS"},
        {"go": "silkResampler12Coefs", "c": "silk_Resampler_1_2_COEFS"},
        {"go": "silkResampler13Coefs", "c": "silk_Resampler_1_3_COEFS"},
        {"go": "silkResampler14Coefs", "c": "silk_Resampler_1_4_COEFS"},
        {"go": "silkResampler16Coefs", "c": "silk_Resampler_1_6_COEFS"},
        # SILK SNR tables
        {"go": "silkTargetRateNB21", "c": "silk_TargetRate_NB_21"},
        {"go": "silkTargetRateMB21", "c": "silk_TargetRate_MB_21"},
        {"go": "silkTargetRateWB21", "c": "silk_TargetRate_WB_21"},
        # SILK LTP gain vq reshapes
        {"go": "silk_LTP_gain_vq_0", "c": "silk_LTP_gain_vq_0", "reshape_go": [8, 5]},
        {"go": "silk_LTP_gain_vq_1", "c": "silk_LTP_gain_vq_1", "reshape_go": [16, 5]},
        {"go": "silk_LTP_gain_vq_2", "c": "silk_LTP_gain_vq_2", "reshape_go": [32, 5]},
    ]

    # Auto-map SILK libopus_tables.go where names match
    auto_skip = set([
        "silk_LBRR_flags_iCDF_ptr",
        "silk_LTP_gain_iCDF_ptrs",
        "silk_LTP_gain_BITS_Q5_ptrs",
        "silk_LTP_vq_ptrs_Q7",
        "silk_LTP_vq_gain_ptrs_Q7",
    ])
    for name in sorted(go_arrays.keys()):
        if not name.startswith("silk_"):
            continue
        if name in auto_skip:
            continue
        if any(m["go"] == name for m in mappings):
            continue
        if name in c_arrays:
            mappings.append({"go": name, "c": name})

    diffs: List[str] = []
    missing: List[str] = []

    for m in mappings:
        go_name = m["go"]
        c_name = m["c"]
        if go_name not in go_arrays:
            missing.append(f"GO missing {go_name}")
            continue
        if c_name not in c_arrays:
            missing.append(f"C missing {c_name}")
            continue
        go_val = go_arrays[go_name]
        c_val = c_arrays[c_name]
        if "reshape_go" in m:
            go_val = reshape(flatten(go_val), m["reshape_go"])
        if "reshape_c" in m:
            c_val = reshape(flatten(c_val), m["reshape_c"])
        if "truncate_c_cols" in m and isinstance(c_val, list):
            cols = m["truncate_c_cols"]
            c_val = [row[:cols] if isinstance(row, list) else row for row in c_val]
        if "scale_c" in m:
            c_val = scale_values(c_val, m["scale_c"])
        compare_values(go_val, c_val, go_name, diffs)

    if missing:
        print("Missing tables:")
        for msg in missing:
            print("  -", msg)

    if diffs:
        print("Mismatches:")
        for d in diffs[:200]:
            print("  -", d)
        if len(diffs) > 200:
            print(f"  ... and {len(diffs)-200} more")

    if missing or diffs:
        return 1

    print("All mapped tables match libopus.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
