// Package cgo provides detailed qi value tracing.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestDetailedQITrace traces qi values step by step.
func TestDetailedQITrace(t *testing.T) {
	frameSize := 960
	sampleRate := 48000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		pcm64[i] = 0.5 * math.Sin(2*math.Pi*440*ti)
	}

	t.Log("=== Detailed QI Value Trace ===")
	t.Log("")

	// Encode with gopus
	enc := celt.NewEncoder(1)
	enc.SetBitrate(64000)

	packet, err := enc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}

	t.Logf("Encoded packet: %d bytes", len(packet))
	showLen := 10
	if showLen > len(packet) {
		showLen = len(packet)
	}
	t.Logf("First %d bytes: %X", showLen, packet[:showLen])
	t.Log("")

	// Decode step by step
	rd := &rangecoding.Decoder{}
	rd.Init(packet)

	t.Log("=== Decoding Header ===")
	t.Logf("Initial: tell=%d, rng=0x%08X", rd.Tell(), rd.Range())

	silence := rd.DecodeBit(15)
	t.Logf("After silence=%d: tell=%d, rng=0x%08X", silence, rd.Tell(), rd.Range())

	if silence == 1 {
		t.Log("Silence frame, stopping")
		return
	}

	postfilter := rd.DecodeBit(1)
	t.Logf("After postfilter=%d: tell=%d, rng=0x%08X", postfilter, rd.Tell(), rd.Range())

	mode := celt.GetModeConfig(frameSize)
	var transient int
	if mode.LM > 0 {
		transient = rd.DecodeBit(3)
	}
	t.Logf("After transient=%d: tell=%d, rng=0x%08X", transient, rd.Tell(), rd.Range())

	intra := rd.DecodeBit(3)
	t.Logf("After intra=%d: tell=%d, rng=0x%08X", intra, rd.Tell(), rd.Range())
	t.Log("")

	// Now decode first few coarse energy values
	t.Log("=== Decoding Coarse Energy (first 5 bands) ===")

	probModel := celt.GetEProbModel()
	prob := probModel[mode.LM][0] // inter mode
	if intra == 1 {
		prob = probModel[mode.LM][1] // intra mode
	}

	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)

	// Decode Laplace for each band
	for band := 0; band < 5; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6

		tellBefore := rd.Tell()
		rngBefore := rd.Range()

		qi := dec.DecodeLaplaceTest(fs, decay)

		tellAfter := rd.Tell()
		rngAfter := rd.Range()

		t.Logf("Band %d: qi=%d (fs=%d, decay=%d) tell: %d->%d, rng: 0x%08X->0x%08X",
			band, qi, fs, decay, tellBefore, tellAfter, rngBefore, rngAfter)
	}

	t.Log("")

	// Now let's trace what the ENCODER produced
	t.Log("=== Tracing Encoder Output ===")

	// Compute energies
	preemph := enc.ApplyPreemphasisWithScaling(pcm64)
	history := make([]float64, celt.Overlap)
	mdct := celt.ComputeMDCTWithHistory(preemph, history, 1)
	energies := enc.ComputeBandEnergies(mdct, mode.EffBands, frameSize)

	t.Logf("Band energies (first 5):")
	for i := 0; i < 5; i++ {
		t.Logf("  Band %d: %.4f", i, energies[i])
	}
	t.Log("")

	// Manually encode and trace
	t.Log("=== Manual Encoding Trace ===")
	buf := make([]byte, 4096)
	reEnc := &rangecoding.Encoder{}
	reEnc.Init(buf)

	// Encode header
	reEnc.EncodeBit(0, 15) // silence
	reEnc.EncodeBit(0, 1)  // postfilter
	reEnc.EncodeBit(0, 3)  // transient
	reEnc.EncodeBit(0, 3)  // intra (matching what the full encoder did)
	t.Logf("After header: tell=%d, rng=0x%08X", reEnc.Tell(), reEnc.Range())

	// Create encoder for Laplace
	enc2 := celt.NewEncoder(1)
	enc2.SetRangeEncoder(reEnc)
	enc2.SetFrameBitsForTest(len(buf) * 8)

	// Compute and encode qi values
	alphaCoef := []float64{0.75, 0.822727, 0.857143, 0.875}
	betaCoefInter := []float64{0.039062, 0.070313, 0.101563, 0.132813}
	lm := mode.LM
	coef := alphaCoef[lm]
	beta := betaCoefInter[lm]
	DB6 := 1.0
	prevBandEnergy := 0.0
	prevEnergy := make([]float64, 21)

	for band := 0; band < 5; band++ {
		x := energies[band]
		oldE := prevEnergy[band]
		minEnergy := -9.0 * DB6
		if oldE < minEnergy {
			oldE = minEnergy
		}

		f := x - coef*oldE - prevBandEnergy
		qi := int(math.Floor(f/DB6 + 0.5))

		// Get Laplace params
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6

		tellBefore := reEnc.Tell()
		rngBefore := reEnc.Range()

		encodedQi := enc2.TestEncodeLaplace(qi, fs, decay)

		tellAfter := reEnc.Tell()
		rngAfter := reEnc.Range()

		t.Logf("Band %d: x=%.4f, f=%.4f, qi=%d, encoded=%d, tell: %d->%d, rng: 0x%08X->0x%08X",
			band, x, f, qi, encodedQi, tellBefore, tellAfter, rngBefore, rngAfter)

		// Update predictor
		q := float64(encodedQi) * DB6
		prevBandEnergy = prevBandEnergy + q - beta*q
	}

	manualBytes := reEnc.Done()
	t.Logf("Manual encoded bytes (first 5): %X", manualBytes[:5])
	t.Logf("Actual packet bytes (first 5): %X", packet[:5])
}
