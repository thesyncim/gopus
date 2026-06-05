package testvectors

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus"
)

// decoderRateParityBar returns the quality bar for a per-rate parity case.
//
// At 48 kHz, opus_compare is applicable and all three modes meet the
// near-exact bar (QualityBarNearExact, Q>=20, corr>=0.997). At sub-48k rates,
// opus_compare is not applicable (it is hardcoded to 48 kHz), so comparison
// uses waveform correlation and RMS ratio only; the bar is calibrated from
// measured gopus-vs-libopus waveform correlation on darwin/arm64, which
// consistently exceeds 0.997 for all modes and rates.
func decoderRateParityBar(apiRate int, mode string) QualityBar {
	if apiRate == 48000 {
		return QualityBarForMode(mode, 1) // channel count does not change the bar
	}
	// Sub-48k: opus_compare N/A; gate on waveform corr+RMS only.
	// MinQ=0 ensures AssertQuality does not fail on the Q field.
	return QualityBar{
		MinQ:    0.0,
		MinCorr: 0.997,
		RMSLo:   0.97,
		RMSHi:   1.03,
		Desc:    fmt.Sprintf("sub-48k waveform parity bar (rate=%d)", apiRate),
	}
}

// decodeWithInternalDecoderAtRate decodes packets using gopus at the given API
// sample rate and returns the float32 interleaved PCM.
func decodeWithInternalDecoderAtRate(t *testing.T, packets [][]byte, channels, apiRate int) []float32 {
	t.Helper()
	dec, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(apiRate, channels))
	if err != nil {
		t.Fatalf("create decoder at %d Hz: %v", apiRate, err)
	}

	// maxPacketSamples at the API rate: 120 ms packet at the API rate.
	maxSamples := apiRate * 120 / 1000
	if maxSamples < 1 {
		maxSamples = 5760
	}
	outBuf := make([]float32, maxSamples*channels)
	var decoded []float32
	for i, pkt := range packets {
		n, err := dec.Decode(pkt, outBuf)
		if err != nil {
			t.Fatalf("decode frame %d at %d Hz: %v", i, apiRate, err)
		}
		if n == 0 {
			continue
		}
		decoded = append(decoded, outBuf[:n*channels]...)
	}
	return decoded
}

// TestDecoderParityRateMatrix asserts gopus-vs-libopus parity for each
// (encode-config, api_rate) row in the rate-matrix fixture. At 48 kHz the
// canonical opus_compare Q metric is used (same bar as TestDecoderParityLibopusMatrix).
// At 8/12/16/24 kHz, where opus_compare is not applicable, waveform
// correlation and RMS ratio are used instead; the thresholds are set to the
// same values that the 48 kHz rows consistently achieve in practice.
func TestDecoderParityRateMatrix(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)

	fixture, err := loadLibopusDecoderRateMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder rate matrix fixture: %v", err)
	}
	if fixture.Version != 1 {
		t.Fatalf("unsupported decoder rate matrix fixture version: %d", fixture.Version)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder rate matrix fixture has no cases")
	}

	for _, c := range fixture.Cases {
		t.Run(fmt.Sprintf("%s/rate%d", c.Name, c.APIRate), func(t *testing.T) {
			t.Parallel()

			mode := decoderMatrixCaseMode(libopusDecoderMatrixCaseFile{
				Name:          c.Name,
				ModeHistogram: c.ModeHistogram,
			})
			bar := decoderRateParityBar(c.APIRate, mode)

			packets, err := decodeLibopusDecoderRateMatrixPackets(c)
			if err != nil {
				t.Fatalf("decode fixture packets: %v", err)
			}
			refDecoded, err := decodeLibopusDecoderRateMatrixSamples(c)
			if err != nil {
				t.Fatalf("decode fixture f32 samples: %v", err)
			}
			internalDecoded := decodeWithInternalDecoderAtRate(t, packets, c.Channels, c.APIRate)

			if len(refDecoded) == 0 || len(internalDecoded) == 0 {
				t.Fatalf("decoded streams empty: ref=%d internal=%d", len(refDecoded), len(internalDecoded))
			}

			compareLen := len(refDecoded)
			if len(internalDecoded) < compareLen {
				compareLen = len(internalDecoded)
			}

			// maxDelay scales with frame size converted to the API rate.
			// Use at least 4 frames at the API rate, minimum 20 ms worth.
			apiFrameSize := c.FrameSize * c.APIRate / 48000
			if apiFrameSize < 1 {
				apiFrameSize = c.APIRate / 50
			}
			maxDelay := 4 * apiFrameSize * c.Channels
			minDelay := c.APIRate / 50 * c.Channels // 20 ms at API rate
			if maxDelay < minDelay {
				maxDelay = minDelay
			}

			var cmp QualityComparison
			if c.APIRate == 48000 {
				// At 48 kHz, opus_compare is applicable. Use the canonical comparator.
				var err error
				cmp, err = CompareDecodedFloat32(
					internalDecoded[:compareLen],
					refDecoded[:compareLen],
					c.APIRate,
					c.Channels,
					maxDelay,
				)
				if err != nil {
					t.Fatalf("compare decoded quality at 48 kHz: %v", err)
				}
			} else {
				// Below 48 kHz, opus_compare is not applicable (hardcoded to 48 kHz).
				// Use delay-searched waveform correlation and RMS ratio instead.
				// Q is set to 100 (perfect) so MinQ=0 gates pass and the meaningful
				// bars are corr and RMS.
				candidate := internalDecoded[:compareLen]
				reference := refDecoded[:compareLen]
				bestDelay, stats := bestWaveformDelayByCorrelation(candidate, reference, maxDelay)
				cmp = QualityComparison{
					Q:         100.0,
					BestDelay: bestDelay,
					Corr:      stats.Correlation,
					RMSRatio:  stats.RMSRatio,
				}
			}
			AssertQuality(t, cmp, bar, fmt.Sprintf("%s@%dHz", c.Name, c.APIRate))
		})
	}
}

// TestDecoderParityMatrixRateCoverage extends the base coverage assertion
// (TestDecoderParityMatrixCoverage) to also verify that each of the five
// supported API sample rates (8000, 12000, 16000, 24000, 48000 Hz) is
// represented in the rate-matrix fixture across all three codec modes (SILK,
// Hybrid, CELT) with both mono and stereo cases.
func TestDecoderParityMatrixRateCoverage(t *testing.T) {
	t.Parallel()
	fixture, err := loadLibopusDecoderRateMatrixFixture()
	if err != nil {
		t.Fatalf("load decoder rate matrix fixture: %v", err)
	}
	if len(fixture.Cases) == 0 {
		t.Fatal("decoder rate matrix fixture has no cases")
	}

	wantRates := []int{8000, 12000, 16000, 24000, 48000}
	wantModes := []string{"silk", "hybrid", "celt"}

	type coverage struct {
		modes  map[string]bool
		stereo bool
	}
	rateCov := make(map[int]*coverage, len(wantRates))
	for _, r := range wantRates {
		rateCov[r] = &coverage{modes: map[string]bool{}}
	}

	for _, c := range fixture.Cases {
		cov, ok := rateCov[c.APIRate]
		if !ok {
			continue
		}
		for mode, count := range c.ModeHistogram {
			if count > 0 {
				cov.modes[mode] = true
			}
		}
		if c.Channels == 2 {
			cov.stereo = true
		}
	}

	for _, rate := range wantRates {
		cov := rateCov[rate]
		for _, mode := range wantModes {
			if !cov.modes[mode] {
				t.Errorf("rate %d Hz missing mode %q coverage", rate, mode)
			}
		}
		if !cov.stereo {
			t.Errorf("rate %d Hz missing stereo coverage", rate)
		}
	}
}
