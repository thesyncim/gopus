//go:build linux && amd64 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const celtQuantTraceBandsSHA256 = "fc7ddcc7a46135c2cd89016025e4b96d30e9b514565610115467405d78d63ec6"

var celtQuantTraceOracleCache libopustest.HelperCache

// buildCELTVBRQuantTraceOracle builds the single-run VBR stage/entropy/GQTR
// helper. Its copied bands.c remains byte-for-byte pinned outside the exact
// diagnostic hooks below.
func buildCELTVBRQuantTraceOracle(t *testing.T) string {
	t.Helper()
	path, err := celtQuantTraceOracleCache.Path(func() (string, error) {
		bandsSource := libopustest.RefPath("celt", "bands.c")
		patchedBands := writeCELTQuantTraceBandsSource(t, bandsSource)
		_, entropyIncludeDir, entropySourceHash := writeCELTQuantTraceEntropySource(t)
		preemphasisSource := writeCELTPreemphasisTraceSource(t)
		root := celtQuantTraceRepoRoot(t)
		config := libopustest.CHelperConfig{
			Label:      "public VBR CELT quantization trace",
			OutputBase: "gopus_libopus_public_vbr_celt_quant_trace",
			SourceFile: "libopus_encode_diff_celt_quant_trace.c",
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_CELT_ENTROPY_SOURCE_SHA256=%q", entropySourceHash),
			},
			RefIncludes: []string{"celt", "silk", "src"},
			IncludeDirs: []string{entropyIncludeDir, filepath.Join(root, "tools", "csrc")},
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
			},
		}
		config.Sources = []string{preemphasisSource, patchedBands}
		linkMapPath := filepath.Join(t.TempDir(), config.OutputBase+".map")
		config.LDFlags = append(config.LDFlags, "-Wl,-Map,"+linkMapPath)
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
		libopustest.HelperUnavailable(t, "public VBR CELT quantization trace", err)
	}
	return path
}

func writeCELTQuantTraceBandsSource(t *testing.T, sourcePath string) string {
	t.Helper()
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read pinned libopus bands source %s: %v", sourcePath, err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(source)); got != celtQuantTraceBandsSHA256 {
		t.Fatalf("pinned libopus bands.c SHA256=%s, want %s", got, celtQuantTraceBandsSHA256)
	}

	text := string(source)
	text = replaceCELTQuantTraceSource(t, text, `#include "pitch.h"
`, `#include "pitch.h"
#include <stdint.h>

extern void gopus_celt_quant_set_channels(int channels);
extern int gopus_celt_quant_theta_begin(int band, int n, int B, int B0, int LM,
    int encode, int stereo, int theta_round, int b, int fill, int remaining,
    const celt_ener *bandE, int nbEBands, ec_enc *ec,
    const celt_norm *X, const celt_norm *Y);
extern void gopus_celt_quant_theta_meta(int ordinal, int qn, int pulse_cap,
    int offset, int raw_itheta_q30);
extern void gopus_celt_quant_theta_end(int ordinal, int b, int fill, int itheta,
    int itheta_q30, int inv, int imid, int iside, int delta, int qalloc,
    int remaining, const celt_norm *X, const celt_norm *Y, ec_enc *ec);
extern int gopus_celt_quant_current_theta_ordinal(void);
extern int gopus_celt_quant_last_band_output_theta_ordinal(void);
extern void gopus_celt_quant_pvq_context(int B0, int LM);
extern int gopus_celt_quant_merge_begin(int theta_ordinal, int N, opus_val32 mid,
    const celt_norm *X, const celt_norm *Y, ec_enc *ec);
extern void gopus_celt_quant_merge_end(int ordinal, const celt_norm *X,
    const celt_norm *Y, ec_enc *ec);
extern void gopus_celt_quant_band_output(int theta_ordinal, int N, unsigned collapse,
    uint32_t range_before, uint32_t tell_frac_before,
    const celt_norm *X, const celt_norm *Y, ec_enc *ec);
extern void gopus_celt_quant_rdo_select(int band, int N, int B, int B0, int LM,
    int encode, int theta_round, int selected_round, int selected_theta_ordinal,
    opus_val32 dist0, opus_val32 dist1, uint32_t range_before,
    uint32_t tell_frac_before, ec_enc *ec, const celt_norm *X,
    const celt_norm *Y);
`)

	text = replaceCELTQuantTraceSource(t, text, `         /* Finally do the actual quantization */
         if (encode)
         {
            cm = alg_quant(X, N, K, spread, B, ec, gain, ctx->resynth
`, `         /* Finally do the actual quantization */
         if (encode)
         {
            gopus_celt_quant_pvq_context(B0, LM);
            cm = alg_quant(X, N, K, spread, B, ec, gain, ctx->resynth
`)
	text = replaceCELTQuantTraceSource(t, text, `   SAVE_STACK;

   M = 1<<LM;`, `   SAVE_STACK;
   gopus_celt_quant_set_channels(C);

   M = 1<<LM;`)
	text = replaceCELTQuantTraceSource(t, text, `   const celt_ener *bandE;

   encode = ctx->encode;`, `   const celt_ener *bandE;
   int gopus_trace_theta = -1;

   encode = ctx->encode;`)
	text = replaceCELTQuantTraceSource(t, text, `   bandE = ctx->bandE;

   /* Decide on the resolution`, `   bandE = ctx->bandE;
   gopus_trace_theta = gopus_celt_quant_theta_begin(i, N, B, B0, LM,
       encode, stereo, ctx->theta_round, *b, *fill, ctx->remaining_bits,
       bandE, m->nbEBands, ec, X, Y);

   /* Decide on the resolution`)
	text = replaceCELTQuantTraceSource(t, text, `      itheta = itheta_q30>>16;
   }
   tell = ec_tell_frac(ec);`, `      itheta = itheta_q30>>16;
   }
   gopus_celt_quant_theta_meta(gopus_trace_theta, qn, pulse_cap, offset, itheta_q30);
   tell = ec_tell_frac(ec);`)
	text = replaceCELTQuantTraceSource(t, text, `   sctx->itheta = itheta;
#ifdef ENABLE_QEXT
   sctx->itheta_q30 = itheta_q30;
#endif
   sctx->qalloc = qalloc;
}`, `   sctx->itheta = itheta;
#ifdef ENABLE_QEXT
   sctx->itheta_q30 = itheta_q30;
#endif
   sctx->qalloc = qalloc;
   gopus_celt_quant_theta_end(gopus_trace_theta, *b, *fill, itheta, itheta_q30,
       inv, imid, iside, delta, qalloc, ctx->remaining_bits, X, Y, ec);
}`)
	text = replaceCELTQuantTraceSource(t, text, `   int orig_fill;
   int encode;
   ec_ctx *ec;

   encode = ctx->encode;`, `   int orig_fill;
   int encode;
   ec_ctx *ec;
   int gopus_trace_stereo_theta = -1;
   uint32_t gopus_trace_band_range_before = 0;
   uint32_t gopus_trace_band_tell_before = 0;

   encode = ctx->encode;`)
	text = replaceCELTQuantTraceSource(t, text, `   encode = ctx->encode;
   ec = ctx->ec;

   /* Special case for one sample */`, `   encode = ctx->encode;
   ec = ctx->ec;
   gopus_trace_band_range_before = ec->rng;
   gopus_trace_band_tell_before = ec_tell_frac(ec);

   /* Special case for one sample */`)
	text = replaceCELTQuantTraceSource(t, text, `   compute_theta(ctx, &sctx, X, Y, N, &b, B, B, LM, 1, &fill ARG_QEXT(&ext_b));
   inv = sctx.inv;`, `   compute_theta(ctx, &sctx, X, Y, N, &b, B, B, LM, 1, &fill ARG_QEXT(&ext_b));
   gopus_trace_stereo_theta = gopus_celt_quant_current_theta_ordinal();
   inv = sctx.inv;`)
	text = replaceCELTQuantTraceSource(t, text, `      if (N!=2)
         stereo_merge(X, Y, mid, N, ctx->arch);`, `      if (N!=2)
      {
         int gopus_trace_merge = gopus_celt_quant_merge_begin(
             gopus_trace_stereo_theta, N, mid, X, Y, ec);
         stereo_merge(X, Y, mid, N, ctx->arch);
         gopus_celt_quant_merge_end(gopus_trace_merge, X, Y, ec);
      }`)
	text = replaceCELTQuantTraceSource(t, text, `   return cm;
}

#ifndef DISABLE_UPDATE_DRAFT
static void special_hybrid_folding`, `   gopus_celt_quant_band_output(gopus_trace_stereo_theta, N, cm,
       gopus_trace_band_range_before, gopus_trace_band_tell_before, X, Y, ec);
   return cm;
}

#ifndef DISABLE_UPDATE_DRAFT
static void special_hybrid_folding`)
	text = replaceCELTQuantTraceSource(t, text, `               opus_val16 w[2];
               compute_channel_weights(bandE[i], bandE[i+m->nbEBands], w);`, `               opus_val16 w[2];
               int gopus_trace_theta0 = -1;
               int gopus_trace_theta1 = -1;
               compute_channel_weights(bandE[i], bandE[i+m->nbEBands], w);`)
	text = replaceCELTQuantTraceSource(t, text, `               dist0 = MULT16_32_Q15(w[0], celt_inner_prod_norm_shift(X_save, X, N, arch)) + MULT16_32_Q15(w[1], celt_inner_prod_norm_shift(Y_save, Y, N, arch));

               /* Save first result. */`, `               dist0 = MULT16_32_Q15(w[0], celt_inner_prod_norm_shift(X_save, X, N, arch)) + MULT16_32_Q15(w[1], celt_inner_prod_norm_shift(Y_save, Y, N, arch));
               gopus_trace_theta0 = gopus_celt_quant_last_band_output_theta_ordinal();

               /* Save first result. */`)
	text = replaceCELTQuantTraceSource(t, text, `               dist1 = MULT16_32_Q15(w[0], celt_inner_prod_norm_shift(X_save, X, N, arch)) + MULT16_32_Q15(w[1], celt_inner_prod_norm_shift(Y_save, Y, N, arch));
               if (dist0 >= dist1) {`, `               dist1 = MULT16_32_Q15(w[0], celt_inner_prod_norm_shift(X_save, X, N, arch)) + MULT16_32_Q15(w[1], celt_inner_prod_norm_shift(Y_save, Y, N, arch));
               gopus_trace_theta1 = gopus_celt_quant_last_band_output_theta_ordinal();
               if (dist0 >= dist1) {`)
	text = replaceCELTQuantTraceSource(t, text, `               }
            } else {
               ctx.theta_round = 0;`, `               }
               gopus_celt_quant_rdo_select(i, N, B, B, LM, encode,
                   ctx.theta_round, dist0 >= dist1 ? -1 : 1,
                   dist0 >= dist1 ? gopus_trace_theta0 : gopus_trace_theta1,
                   dist0, dist1, ec_save.rng, ec_tell_frac(&ec_save), ec, X, Y);
            } else {
               ctx.theta_round = 0;`)

	path := filepath.Join(t.TempDir(), "celt_bands_quant_trace.c")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write instrumented pinned bands.c: %v", err)
	}
	return path
}

func writeCELTQuantTraceEntropySource(t *testing.T) (string, string, string) {
	t.Helper()
	root := celtQuantTraceRepoRoot(t)
	sourcePath := filepath.Join(root, "tools", "csrc", "libopus_encode_diff_celt_entropy_trace.c")
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("read CELT entropy trace helper %s: %v", sourcePath, err)
	}
	const originalEntry = "int main(void) {\n  int result = gopus_encode_diff_main();"
	const renamedEntry = "int gopus_encode_diff_celt_entropy_trace_main(void) {\n  int result = gopus_encode_diff_main();"
	included := replaceCELTQuantTraceSource(t, string(source), originalEntry, renamedEntry)
	dir := t.TempDir()
	path := filepath.Join(dir, "libopus_encode_diff_celt_entropy_trace_included.c")
	if err := os.WriteFile(path, []byte(included), 0o600); err != nil {
		t.Fatalf("write included CELT entropy trace helper: %v", err)
	}
	return path, dir, fmt.Sprintf("%x", sha256.Sum256([]byte(included)))
}

func celtQuantTraceRepoRoot(t *testing.T) string {
	t.Helper()
	_, sourcePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate CELT quant trace test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(sourcePath), "..", ".."))
}

func replaceCELTQuantTraceSource(t *testing.T, source, old, replacement string) string {
	t.Helper()
	if count := strings.Count(source, old); count != 1 {
		t.Fatalf("CELT quant trace source context occurs %d times, want exactly once: %q", count, old)
	}
	return strings.Replace(source, old, replacement, 1)
}

func validateCELTQuantTraceLinkMap(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read CELT quant trace link map %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		rest := line
		for {
			start := strings.Index(rest, "libopus.a(")
			if start < 0 {
				break
			}
			member, tail, ok := strings.Cut(rest[start+len("libopus.a("):], ")")
			if !ok || member == "" {
				return fmt.Errorf("malformed archive member in quant trace link map: %s", strings.TrimSpace(line))
			}
			// quant_bands.o is a separate, required translation unit. Match
			// the bands.c archive member exactly rather than its name suffix.
			switch filepath.Base(member) {
			case "bands.o", "libopus_la-bands.o":
				return fmt.Errorf("quant trace linked uninstrumented archive bands.c: %s", strings.TrimSpace(line))
			}
			rest = tail
		}
	}
	return nil
}

func TestCELTQuantTraceLinkMapRejectsUninstrumentedBands(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		wantError  bool
	}{
		{"quantization kernels", "ref/.libs/libopus.a(quant_bands.o)\nref/.libs/libopus.a(vq.o)\n", false},
		{"bands", "ref/.libs/libopus.a(bands.o)\n", true},
		{"libtool bands", "ref/.libs/libopus.a(libopus_la-bands.o)\n", true},
		{"object directory", "ref/.libs/libopus.a(celt/bands.o)\n", true},
		{"multiple members", "libopus.a(quant_bands.o) libopus.a(bands.o)\n", true},
		{"unterminated member", "ref/.libs/libopus.a(bands.o\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "quant-trace.map")
			if err := os.WriteFile(path, []byte(tc.text), 0600); err != nil {
				t.Fatal(err)
			}
			if err := validateCELTQuantTraceLinkMap(path); (err != nil) != tc.wantError {
				t.Fatalf("link-map validation error=%v, wantError=%t", err, tc.wantError)
			}
		})
	}
}
