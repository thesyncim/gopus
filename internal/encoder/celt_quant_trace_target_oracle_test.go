//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// buildCELTQuantTraceOracleAtFrameBand builds the existing GQTR helper with
// source-bound frame and band selectors. The caller supplies a cache dedicated
// to this selector pair so helpers for different frames cannot alias.
func buildCELTQuantTraceOracleAtFrameBand(t *testing.T, frame, band int, coderRangeTrace bool, cache *libopustest.HelperCache) string {
	t.Helper()
	libopustest.RequireOracle(t)
	if frame < 0 || frame >= celtOnlyCBRFrames {
		t.Fatalf("CELT quant trace frame %d outside selected CBR stream [0,%d)", frame, celtOnlyCBRFrames)
	}
	if band < 0 || band >= celtTraceBandCount {
		t.Fatalf("CELT quant trace band %d outside [0,%d)", band, celtTraceBandCount)
	}
	if cache == nil {
		t.Fatal("CELT quant trace helper requires an explicit selector-scoped cache")
	}

	path, err := cache.Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		quantSource, entropyIncludeDir, entropySourceHash := writeCELTQuantTraceTargetSources(t, root, frame, band)
		patchedBands := writeCELTQuantTraceBandsSource(t, libopustest.RefPath("celt", "bands.c"))
		preemphasisSource := writeCELTPreemphasisTraceSource(t)

		bandsSourceHash, err := hashCELTTraceSource(patchedBands)
		if err != nil {
			return "", fmt.Errorf("hash generated CELT bands trace source: %w", err)
		}
		preemphasisSourceHash, err := hashCELTTraceSource(preemphasisSource)
		if err != nil {
			return "", fmt.Errorf("hash generated CELT preemphasis trace source: %w", err)
		}

		outputBase := fmt.Sprintf("gopus_libopus_celt_quant_trace_f%d_b%d", frame, band)
		linkMapPath := filepath.Join(t.TempDir(), outputBase+".map")
		config := libopustest.CHelperConfig{
			Label:      fmt.Sprintf("CELT quant trace frame %d band %d", frame, band),
			OutputBase: outputBase,
			SourceFile: quantSource,
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_CELT_QUANT_TRACE_FRAME=%d", frame),
				fmt.Sprintf("-DGOPUS_CELT_QUANT_TRACE_BAND=%d", band),
				fmt.Sprintf("-DGOPUS_CELT_ENTROPY_SOURCE_SHA256=%q", entropySourceHash),
				fmt.Sprintf("-DGOPUS_CELT_BANDS_TRACE_SOURCE_SHA256=%q", bandsSourceHash),
				fmt.Sprintf("-DGOPUS_CELT_PREEMPHASIS_TRACE_SOURCE_SHA256=%q", preemphasisSourceHash),
			},
			RefIncludes: []string{"celt", "silk", "src"},
			IncludeDirs: []string{entropyIncludeDir, filepath.Join(root, "tools", "csrc")},
			Sources:     []string{preemphasisSource, patchedBands},
			LDFlags: []string{
				"-Wl,--wrap=opus_encode_float",
				"-Wl,--wrap=comb_filter",
				"-Wl,--wrap=compute_band_energies",
				"-Wl,--wrap=amp2Log2",
				"-Wl,--wrap=normalise_bands",
				"-Wl,--wrap=quant_coarse_energy",
				"-Wl,--wrap=quant_all_bands",
				"-Wl,--wrap=clt_mdct_forward_c",
				"-Wl,--wrap=ec_enc_bits",
				"-Wl,--wrap=ec_enc_done",
				"-Wl,--wrap=alg_quant",
				"-Wl,-Map," + linkMapPath,
			},
		}
		if coderRangeTrace {
			config.CFlags = append(config.CFlags, "-DGOPUS_CELT_CODER_RANGE_TRACE")
		}
		helperPath, err := libopustest.BuildPublicAPIHelper(config)
		if err != nil {
			return "", err
		}
		if err := validateCELTTraceLinkMap(linkMapPath); err != nil {
			return "", err
		}
		if err := validateCELTQuantTraceLinkMap(linkMapPath); err != nil {
			return "", err
		}
		return helperPath, nil
	})
	if err != nil {
		libopustest.HelperUnavailable(t, fmt.Sprintf("CELT quant trace frame %d band %d", frame, band), err)
	}
	return path
}

func writeCELTQuantTraceTargetSources(t *testing.T, root string, frame, band int) (quantSource, entropyIncludeDir, entropySourceHash string) {
	t.Helper()
	quantPath := filepath.Join(root, "tools", "csrc", "libopus_encode_diff_celt_quant_trace.c")
	quantBytes, err := os.ReadFile(quantPath)
	if err != nil {
		t.Fatalf("read CELT quant trace source %s: %v", quantPath, err)
	}
	quantText := string(quantBytes)
	for _, replacement := range []struct {
		from string
		to   string
	}{
		{"if (band != 17) return -1;", "if (band != GOPUS_CELT_QUANT_TRACE_BAND) return -1;"},
		{"gqtr.events[previous_theta].header.band != 17", "gqtr.events[previous_theta].header.band != GOPUS_CELT_QUANT_TRACE_BAND"},
		{"gqtr.events[gqtr.current_theta].header.band != 17", "gqtr.events[gqtr.current_theta].header.band != GOPUS_CELT_QUANT_TRACE_BAND"},
		{"gqtr.events[theta_ordinal].header.band == 17", "gqtr.events[theta_ordinal].header.band == GOPUS_CELT_QUANT_TRACE_BAND"},
		{"!trace_selected_frame() || band != 17 || selected_theta_ordinal < 0 ||", "!trace_selected_frame() || band != GOPUS_CELT_QUANT_TRACE_BAND || selected_theta_ordinal < 0 ||"},
	} {
		quantText = replaceCELTQuantTraceSource(t, quantText, replacement.from, replacement.to)
	}
	quantDir := t.TempDir()
	quantSource = filepath.Join(quantDir, "libopus_encode_diff_celt_quant_trace.c")
	if err := os.WriteFile(quantSource, []byte(quantText), 0o600); err != nil {
		t.Fatalf("write selected-frame CELT quant trace source: %v", err)
	}

	entropyPath := filepath.Join(root, "tools", "csrc", "libopus_encode_diff_celt_entropy_trace.c")
	entropyBytes, err := os.ReadFile(entropyPath)
	if err != nil {
		t.Fatalf("read CELT entropy trace source %s: %v", entropyPath, err)
	}
	const originalMain = "int main(void) {\n  int result = gopus_encode_diff_main();"
	const renamedMain = "int gopus_encode_diff_celt_entropy_trace_main(void) {\n  int result = gopus_encode_diff_main();"
	const originalFrame = "#define TRACE_FRAME 1u"
	const selectedFrame = "#define TRACE_FRAME GOPUS_CELT_QUANT_TRACE_FRAME"
	entropyText := replaceCELTQuantTraceSource(t, string(entropyBytes), originalMain, renamedMain)
	entropyText = replaceCELTQuantTraceSource(t, entropyText, originalFrame, selectedFrame)
	entropyIncludeDir = t.TempDir()
	entropySource := filepath.Join(entropyIncludeDir, "libopus_encode_diff_celt_entropy_trace_included.c")
	if err := os.WriteFile(entropySource, []byte(entropyText), 0o600); err != nil {
		t.Fatalf("write selected-frame CELT entropy trace source: %v", err)
	}
	entropyHash := sha256.Sum256([]byte(entropyText))
	entropySourceHash = fmt.Sprintf("%x", entropyHash[:])
	return quantSource, entropyIncludeDir, entropySourceHash
}

func hashCELTTraceSource(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:]), nil
}
