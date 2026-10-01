package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/qualitycompare"
)

// API-rate decoded-PCM quality diagnostics.
//
// These helpers compare gopus output with the selected libopus reference. They
// choose a useful waveform metric for each sample rate and signal type, and
// supplement exact PCM assertions in their callers. A quality pass alone does
// not establish sample equality.
//
// Gate selection follows the opus_compare applicability rule:
//   - 48 kHz output with >=480 samples/channel (>=10 ms) of mostly real decoded
//     content: opus_compare applies, so the quality helper checks the
//     QualityBarNearExact Q and waveform bars and logs the measured metrics.
//   - sub-48 kHz (8/12/16/24 kHz) or short (2.5/5 ms) output: opus_compare returns
//     -Inf, so the quality helper checks waveform correlation and RMS ratio;
//     opus_compare Q does not gate these cases.
//   - PLC-dominated 48 kHz streams (a short real frame followed by a longer
//     requested/overlong PLC tail): opus_compare's psychoacoustic Q is not a valid
//     quality metric on extrapolated (concealed) audio -- e.g. the multistream
//     hybrid requested-PLC stream contains a substantial concealed tail. For
//     these streams the quality helper checks correlation and RMS and logs Q
//     when opus_compare can still calculate it.
//
// The waveform bar uses corr >= 0.997 and RMS ratio in [0.98, 1.02]. Exact
// libopus comparisons remain separate assertions; these thresholds measure
// waveform similarity and do not authorize a sample mismatch.
var apiRateSubRateBar = qualitycompare.QualityBar{
	MinQ:    math.Inf(-1), // opus_compare N/A here; Q is logged, not gated.
	MinCorr: 0.997,
	RMSLo:   0.98,
	RMSHi:   1.02,
	Desc:    "near-exact vs libopus (sub-48k/short/PLC-dominated: corr/RMS, opus_compare N/A)",
}

// opusCompareApplies reports whether opus_compare (and thus the Q gate) is valid
// for an api-rate decoded stream: 48 kHz with at least 480 samples per channel.
func opusCompareApplies(sampleRate, channels, totalSamples int) bool {
	return sampleRate == 48000 && channels > 0 && totalSamples/channels >= 480
}

// assertAPIRateQualityFloat32 checks the quality metrics for a decoded PCM
// stream with no PLC-dominated tail.
func assertAPIRateQualityFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

// assertAPIRateQualityFloat32PLC checks decoded-PCM quality metrics. got/want are interleaved
// 48 kHz-or-lower PCM that are sample-aligned vs libopus. plcDominated must be
// true when the requested output is mostly packet-loss concealment (a short real
// frame followed by a longer PLC tail), in which case the quality check uses
// correlation and RMS instead of opus_compare Q (see file header).
func assertAPIRateQualityFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	if opusCompareApplies(sampleRate, channels, len(got)) && !plcDominated {
		// 48 kHz, >=10 ms, real content: opus_compare applies. Check its quality
		// bar and log the measured metrics.
		cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0)
		if err != nil {
			t.Fatalf("%s CompareDecodedFloat32: %v", label, err)
		}
		qualitycompare.AssertQuality(t, cmp, qualitycompare.QualityBarNearExact, label)
		return
	}
	// Sub-48 kHz, short, or PLC-dominated output uses the rate-independent
	// waveform corr/RMS metrics. For 48 kHz PLC-dominated streams, also measure
	// Q for the log; it does not affect the quality bar.
	corr, rms := apiRateWaveformCorrelationRMS(got, want)
	q := 0.0
	if opusCompareApplies(sampleRate, channels, len(got)) {
		if cmp, err := qualitycompare.CompareDecodedFloat32(got, want, sampleRate, channels, 0); err == nil {
			q = cmp.Q
		}
	}
	cmp := qualitycompare.QualityComparison{Q: q, BestDelay: 0, Corr: corr, RMSRatio: rms}
	qualitycompare.AssertQuality(t, cmp, apiRateSubRateBar, label)
}

// apiRateWaveformCorrelationRMS mirrors qualitycompare's internal (unexported)
// waveform diagnostics: Pearson correlation and RMS(got)/RMS(want) over the
// common prefix. Used only for the sub-48k/short gate where opus_compare cannot
// run; the 48k path uses CompareDecodedFloat32 directly.
func apiRateWaveformCorrelationRMS(a, b []float32) (corr, rmsRatio float64) {
	n := min(len(b), len(a))
	if n == 0 {
		return 0, 0
	}
	var sumA, sumB, sumASq, sumBSq float64
	for i := range n {
		fa, fb := float64(a[i]), float64(b[i])
		sumA += fa
		sumB += fb
		sumASq += fa * fa
		sumBSq += fb * fb
	}
	meanA, meanB := sumA/float64(n), sumB/float64(n)
	var varA, varB, cov float64
	for i := range n {
		da, db := float64(a[i])-meanA, float64(b[i])-meanB
		cov += da * db
		varA += da * da
		varB += db * db
	}
	if varA > 0 && varB > 0 {
		corr = cov / math.Sqrt(varA*varB)
	} else if varA == 0 && varB == 0 {
		corr = 1
	}
	rmsA := math.Sqrt(sumASq / float64(n))
	rmsB := math.Sqrt(sumBSq / float64(n))
	if rmsB > 0 {
		rmsRatio = rmsA / rmsB
	} else if rmsA == 0 {
		rmsRatio = 1
	}
	return corr, rmsRatio
}

// assertAPIRateQualityInt16 converts both int16 streams to float32, scaled by
// 1/32768, and checks their waveform quality. Call assertAPIRateInt16Exact when
// the test also requires exact sample equality.
func assertAPIRateQualityInt16(t *testing.T, got, want []int16, sampleRate, channels int, label string) {
	t.Helper()
	assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, false, label)
}

func assertAPIRateQualityInt16PLC(t *testing.T, got, want []int16, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	gotF := int16SliceToFloat32(got)
	wantF := int16SliceToFloat32(want)
	assertAPIRateQualityFloat32PLC(t, gotF, wantF, sampleRate, channels, plcDominated, label)
}

func int16SliceToFloat32(in []int16) []float32 {
	out := make([]float32, len(in))
	for i, v := range in {
		out[i] = float32(v) / 32768.0
	}
	return out
}

// assertAPIRateInt16Exact compares integer PCM samples with the selected C
// reference. Quality metrics alone do not establish sample equality.
func assertAPIRateInt16Exact(t *testing.T, got, want []int16, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s sample[%d]=%d want %d", label, i, got[i], want[i])
		}
	}
}

func assertAPIRateFloat32BitsExact(t *testing.T, got, want []float32, label string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len=%d want %d", label, len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("%s sample[%d]=%08x want %08x", label, i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

// The public API-rate reference helper selects the active libopus feature and
// instruction lane, then checks both quality metrics and every output bit.
// Call this only when want comes from decodeWithLibopusReferenceAPIRateFloat32.
func assertSelectedPublicAPIRateFloat32(t *testing.T, got, want []float32, sampleRate, channels int, label string) {
	t.Helper()
	assertSelectedPublicAPIRateFloat32PLC(t, got, want, sampleRate, channels, false, label)
}

func assertSelectedPublicAPIRateFloat32PLC(t *testing.T, got, want []float32, sampleRate, channels int, plcDominated bool, label string) {
	t.Helper()
	assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, plcDominated, label)
	assertAPIRateFloat32BitsExact(t, got, want, label)
}
