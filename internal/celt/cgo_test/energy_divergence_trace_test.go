// Package cgo traces the exact point of energy divergence between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestEnergyDivergenceTrace traces where the energy computation diverges
// between gopus standalone computation and libopus actual encoding.
func TestEnergyDivergenceTrace(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	pcm32 := make([]float32, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
		pcm32[i] = float32(val)
	}

	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	t.Log("=== Energy Divergence Trace ===")
	t.Logf("FrameSize=%d, LM=%d, nbBands=%d", frameSize, lm, nbBands)
	t.Log("")

	// Step 1: Encode with libopus and extract QI values
	t.Log("=== Step 1: Libopus Encoding ===")
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}
	t.Logf("Libopus packet: %d bytes", len(libPacket))

	// Decode libopus flags and QI values
	libPayload := libPacket[1:] // Skip TOC
	libRd := &rangecoding.Decoder{}
	libRd.Init(libPayload)

	libSilence := libRd.DecodeBit(15)
	libPostfilter := libRd.DecodeBit(1)
	var libTransient int
	if lm > 0 {
		libTransient = libRd.DecodeBit(3)
	}
	libIntra := libRd.DecodeBit(3)

	t.Logf("Libopus flags: silence=%d, postfilter=%d, transient=%d, intra=%d",
		libSilence, libPostfilter, libTransient, libIntra)

	// Decode libopus QI values
	probModel := celt.GetEProbModel()
	prob := probModel[lm][0]
	if libIntra == 1 {
		prob = probModel[lm][1]
	}

	libDec := celt.NewDecoder(1)
	libDec.SetRangeDecoder(libRd)

	libQIs := make([]int, nbBands)
	for band := 0; band < nbBands; band++ {
		pi := 2 * band
		if pi > 40 {
			pi = 40
		}
		fs := int(prob[pi]) << 7
		decay := int(prob[pi+1]) << 6
		libQIs[band] = libDec.DecodeLaplaceTest(fs, decay)
	}

	// Step 2: Reverse-engineer libopus band energies from QI values
	t.Log("")
	t.Log("=== Step 2: Reverse Engineer Libopus Energies ===")

	coef := celt.AlphaCoef[lm]
	beta := celt.BetaCoefInter[lm]
	DB6 := 1.0

	libImpliedEnergies := make([]float64, nbBands)
	prev := 0.0
	for band := 0; band < nbBands; band++ {
		qi := libQIs[band]
		oldE := 0.0 // First frame
		if oldE < -9.0*DB6 {
			oldE = -9.0 * DB6
		}
		// Implied energy that would produce this QI:
		// qi = round((x - coef*oldE - prev) / DB6)
		// x = qi*DB6 + coef*oldE + prev
		impliedX := float64(qi)*DB6 + coef*oldE + prev
		libImpliedEnergies[band] = impliedX

		q := float64(qi) * DB6
		prev = prev + q - beta*q
	}

	// Step 3: Compute gopus band energies using the EXACT same process as EncodeFrame
	t.Log("")
	t.Log("=== Step 3: Gopus Internal Band Energies ===")

	// Create a fresh encoder
	gopusEnc := celt.NewEncoder(1)
	gopusEnc.Reset()
	gopusEnc.SetBitrate(bitrate)

	// Apply pre-emphasis (same as EncodeFrame does)
	gopusPreemph := gopusEnc.ApplyPreemphasisWithScaling(pcm64)

	// Determine shortBlocks based on transient detection
	// First, build transient analysis input
	overlap := celt.Overlap
	preemphBufSize := overlap
	preemphBuffer := make([]float64, preemphBufSize)
	transientInput := make([]float64, overlap+frameSize)
	copy(transientInput[:preemphBufSize], preemphBuffer)
	copy(transientInput[preemphBufSize:], gopusPreemph)

	transientResult := gopusEnc.TransientAnalysis(transientInput, frameSize+overlap, false)
	shortBlocks := 1
	if transientResult.IsTransient {
		shortBlocks = mode.ShortBlocks
	}
	t.Logf("Gopus transient=%v, shortBlocks=%d", transientResult.IsTransient, shortBlocks)

	// Compute MDCT using overlap buffer (same as EncodeFrame)
	gopusMDCT := celt.ComputeMDCTWithHistory(gopusPreemph, gopusEnc.OverlapBuffer(), shortBlocks)

	// Compute band energies (same as EncodeFrame)
	gopusEnergies := gopusEnc.ComputeBandEnergies(gopusMDCT, nbBands, frameSize)

	// Step 4: Compare energies
	t.Log("")
	t.Log("=== Step 4: Energy Comparison ===")
	t.Log("Band | Gopus Energy | Lib Implied | Delta | Gopus QI | Lib QI | Match")
	t.Log("-----+--------------+-------------+-------+----------+--------+------")

	totalDiff := 0.0
	mismatchCount := 0
	prevGopus := 0.0

	for band := 0; band < nbBands && band < 12; band++ {
		gopusE := gopusEnergies[band]
		libE := libImpliedEnergies[band]
		delta := gopusE - libE

		// Compute what QI gopus would produce
		oldE := 0.0
		if oldE < -9.0 {
			oldE = -9.0
		}
		f := gopusE - coef*oldE - prevGopus
		gopusQI := int(math.Floor(f/DB6 + 0.5))

		match := "YES"
		if gopusQI != libQIs[band] {
			match = "NO"
			mismatchCount++
		}

		t.Logf("%4d | %12.4f | %11.4f | %5.2f | %8d | %6d | %s",
			band, gopusE, libE, delta, gopusQI, libQIs[band], match)

		// Update prev for next band
		q := float64(gopusQI) * DB6
		prevGopus = prevGopus + q - beta*q

		totalDiff += math.Abs(delta)
	}

	t.Log("")
	t.Logf("Total absolute energy difference: %.4f", totalDiff)
	t.Logf("Mismatch count: %d bands", mismatchCount)

	// Step 5: Test hypothesis - does DC reject matter?
	t.Log("")
	t.Log("=== Step 5: DC Reject Filter Analysis ===")

	// Apply DC reject filter like libopus does
	dcRejected := applyDCReject(pcm64, 3, 48000)

	// Re-encode with DC-rejected signal
	gopusEnc2 := celt.NewEncoder(1)
	gopusEnc2.Reset()
	gopusEnc2.SetBitrate(bitrate)

	gopusPreemph2 := gopusEnc2.ApplyPreemphasisWithScaling(dcRejected)
	gopusMDCT2 := celt.ComputeMDCTWithHistory(gopusPreemph2, gopusEnc2.OverlapBuffer(), shortBlocks)
	gopusEnergies2 := gopusEnc2.ComputeBandEnergies(gopusMDCT2, nbBands, frameSize)

	t.Log("Band | Without DC Rej | With DC Rej | Delta")
	t.Log("-----+----------------+-------------+------")
	for band := 0; band < 5; band++ {
		t.Logf("%4d | %14.4f | %11.4f | %.4f",
			band, gopusEnergies[band], gopusEnergies2[band],
			gopusEnergies2[band]-gopusEnergies[band])
	}

	if mismatchCount > 0 {
		t.Log("")
		t.Log("=== Conclusion ===")
		t.Log("Band energies computed by gopus don't match what libopus encoded.")
		t.Log("This suggests libopus uses a different computation path during actual encoding.")
		t.Log("Possible causes:")
		t.Log("1. DC reject filter (tested above)")
		t.Log("2. secondMdct feature at complexity >= 8")
		t.Log("3. Different pre-emphasis state handling")
		t.Log("4. Analysis pass modifying energies")
	}
}

// applyDCReject applies the same DC reject filter as libopus opus_encoder.c
func applyDCReject(pcm []float64, cutoffHz int, fs int) []float64 {
	out := make([]float64, len(pcm))
	coef := 6.3 * float64(cutoffHz) / float64(fs)
	coef2 := 1 - coef

	m := 0.0
	for i := range pcm {
		x := pcm[i]
		out[i] = x - m
		m = coef*x + 1e-30 + coef2*m
	}
	return out
}
