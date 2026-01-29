package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestFullEncodeStageTrace traces the range encoder state after each major encoding stage.
// This helps identify exactly where gopus diverges from libopus.
func TestFullEncodeStageTrace(t *testing.T) {
	t.Log("=== Full Encode Stage Trace ===")
	t.Log("Tracing range encoder state after each encoding stage")
	t.Log("")

	frameSize := 960
	channels := 1
	sampleRate := 48000
	bitrate := 64000

	// Generate 440 Hz sine wave
	samples := make([]float64, frameSize)
	amplitude := 0.5
	freq := 440.0
	for i := range samples {
		samples[i] = amplitude * math.Sin(2*math.Pi*freq*float64(i)/float64(sampleRate))
	}

	// === Stage 0: Full encode with gopus ===
	encoder := celt.NewEncoder(channels)
	encoder.Reset()
	encoder.SetBitrate(bitrate)

	gopusBytes, err := encoder.EncodeFrame(samples, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}
	t.Logf("Gopus encoded: %d bytes", len(gopusBytes))
	t.Logf("First 20 bytes: %02x", gopusBytes[:minIntFull(20, len(gopusBytes))])

	// === Encode same signal with libopus for comparison ===
	samples32 := make([]float32, frameSize)
	for i, v := range samples {
		samples32[i] = float32(v)
	}

	libEnc, err := NewLibopusEncoder(sampleRate, channels, OpusApplicationAudio)
	if err != nil || libEnc == nil {
		t.Fatalf("NewLibopusEncoder failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetComplexity(10)

	libBytes, n := libEnc.EncodeFloat(samples32, frameSize)
	if n < 0 {
		t.Fatalf("libopus encode failed: %d", n)
	}
	t.Logf("Libopus encoded: %d bytes", len(libBytes))
	t.Logf("First 20 bytes: %02x", libBytes[:minIntFull(20, len(libBytes))])

	// === Stage-by-stage manual encoding ===
	t.Log("")
	t.Log("=== Manual Stage-by-Stage Encoding ===")

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	t.Logf("Mode: frameSize=%d, LM=%d, nbBands=%d", frameSize, lm, nbBands)

	// Pre-emphasis
	encoder2 := celt.NewEncoder(channels)
	encoder2.Reset()
	encoder2.SetBitrate(bitrate)
	preemph := encoder2.ApplyPreemphasisWithScaling(samples)

	// Transient detection
	overlap := celt.Overlap
	if overlap > frameSize {
		overlap = frameSize
	}
	transientInput := make([]float64, (overlap+frameSize)*channels)
	copy(transientInput[overlap*channels:], preemph)
	transientResult := encoder2.TransientAnalysis(transientInput, frameSize+overlap, false)
	transient := transientResult.IsTransient
	t.Logf("Transient: %v (tfEstimate=%.4f)", transient, transientResult.TfEstimate)

	shortBlocks := 1
	if transient {
		shortBlocks = mode.ShortBlocks
	}

	// Compute MDCT
	mdctCoeffs := celt.ComputeMDCTWithHistory(preemph, encoder2.OverlapBuffer(), shortBlocks)
	t.Logf("MDCT: %d coefficients", len(mdctCoeffs))

	// Compute band energies
	energies := encoder2.ComputeBandEnergies(mdctCoeffs, nbBands, frameSize)
	t.Logf("Energies band 0-4: %.4f, %.4f, %.4f, %.4f, %.4f",
		energies[0], energies[1], energies[2], energies[3], energies[4])

	// Initialize range encoder
	targetBits := bitrate * frameSize / sampleRate
	bufSize := (targetBits + 7) / 8
	if bufSize < 256 {
		bufSize = 256
	}
	buf := make([]byte, bufSize)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	t.Log("")
	t.Log("=== Range Encoder State Trace ===")
	t.Logf("Initial: rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode silence flag
	re.EncodeBit(0, 15)
	t.Logf("After silence(0): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode postfilter flag
	re.EncodeBit(0, 1)
	t.Logf("After postfilter(0): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// Encode transient flag
	transientBit := 0
	if transient {
		transientBit = 1
	}
	re.EncodeBit(transientBit, 3)
	t.Logf("After transient(%d): rng=0x%08X, val=0x%08X, tell=%d", transientBit, re.Range(), re.Val(), re.Tell())

	// Encode intra flag (first frame = intra)
	re.EncodeBit(1, 3)
	t.Logf("After intra(1): rng=0x%08X, val=0x%08X, tell=%d", re.Range(), re.Val(), re.Tell())

	// At this point, compare with libopus header encoding
	headerStates, _, headerBytes := TraceHeaderPlusLaplace(
		[]int{0, 0, transientBit, 1}, // silence, postfilter, transient, intra
		[]int{15, 1, 3, 3},           // logps
		[]int{},                      // no Laplace values yet
		[]int{},
		[]int{},
	)
	t.Log("")
	t.Log("Libopus header trace:")
	for i, s := range headerStates {
		name := []string{"initial", "silence", "postfilter", "transient", "intra"}[i]
		t.Logf("  %s: rng=0x%08X, val=0x%08X, tell=%d", name, s.Rng, s.Val, s.Tell)
	}
	t.Logf("Header bytes from libopus: %02x", headerBytes)

	// Compare states
	t.Log("")
	t.Log("=== State Comparison After Header ===")
	gopusRng := re.Range()
	gopusVal := re.Val()
	gopusTell := re.Tell()
	libopusRng := headerStates[4].Rng
	libopusVal := headerStates[4].Val
	libopusTell := headerStates[4].Tell

	rngMatch := gopusRng == libopusRng
	valMatch := gopusVal == libopusVal
	tellMatch := gopusTell == libopusTell

	t.Logf("rng:  gopus=0x%08X, libopus=0x%08X - %s", gopusRng, libopusRng, matchStr(rngMatch))
	t.Logf("val:  gopus=0x%08X, libopus=0x%08X - %s", gopusVal, libopusVal, matchStr(valMatch))
	t.Logf("tell: gopus=%d, libopus=%d - %s", gopusTell, libopusTell, matchStr(tellMatch))

	if rngMatch && valMatch && tellMatch {
		t.Log("HEADER STATE MATCHES!")
	} else {
		t.Log("HEADER STATE DIVERGES!")
	}

	// === First byte comparison ===
	t.Log("")
	t.Log("=== Final Byte Comparison ===")
	t.Logf("Gopus first byte: 0x%02X", gopusBytes[0])
	t.Logf("Libopus first byte: 0x%02X (TOC)", libBytes[0])
	t.Logf("Libopus second byte: 0x%02X (first payload)", libBytes[1])

	// Compare gopus payload with libopus payload (skipping TOC)
	minLen := minIntFull(len(gopusBytes), len(libBytes)-1)
	divergeIdx := -1
	for i := 0; i < minLen; i++ {
		if gopusBytes[i] != libBytes[i+1] {
			divergeIdx = i
			break
		}
	}

	if divergeIdx < 0 {
		t.Logf("First %d bytes MATCH between gopus and libopus (excluding TOC)", minLen)
	} else {
		t.Logf("First divergence at byte %d: gopus=0x%02X, libopus=0x%02X",
			divergeIdx, gopusBytes[divergeIdx], libBytes[divergeIdx+1])
	}
}

func matchStr(match bool) string {
	if match {
		return "MATCH"
	}
	return "DIFFER"
}

func minIntFull(a, b int) int {
	if a < b {
		return a
	}
	return b
}
