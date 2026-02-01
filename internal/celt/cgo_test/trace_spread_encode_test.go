//go:build trace
// +build trace

// Package cgo traces spread encoding differences between gopus and libopus.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceSpreadEncode traces the exact spread encoding difference.
func TestTraceSpreadEncode(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	// Get mode config
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM

	spreadICDF := []uint8{25, 23, 2, 0}

	// === LIBOPUS ===
	t.Log("=== LIBOPUS spread decode ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	// Skip to spread
	rdLib.DecodeBit(15) // silence
	rdLib.DecodeBit(1)  // postfilter
	transientLib := rdLib.DecodeBit(3)
	intraLib := rdLib.DecodeBit(3)
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intraLib == 1, lm)

	// TF
	for i := 0; i < nbBands; i++ {
		rdLib.DecodeBit(1)
	}
	rdLib.DecodeBit(1) // tf_select

	tellBeforeSpreadLib := rdLib.Tell()
	rngBeforeSpreadLib := rdLib.Range()
	valBeforeSpreadLib := rdLib.Val()
	t.Logf("Tell before spread: %d", tellBeforeSpreadLib)
	t.Logf("Range before spread: 0x%08X", rngBeforeSpreadLib)
	t.Logf("Val before spread: 0x%08X", valBeforeSpreadLib)

	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)
	tellAfterSpreadLib := rdLib.Tell()
	rngAfterSpreadLib := rdLib.Range()
	valAfterSpreadLib := rdLib.Val()
	t.Logf("Spread decoded: %d", spreadLib)
	t.Logf("Tell after spread: %d (used %d bits)", tellAfterSpreadLib, tellAfterSpreadLib-tellBeforeSpreadLib)
	t.Logf("Range after spread: 0x%08X", rngAfterSpreadLib)
	t.Logf("Val after spread: 0x%08X", valAfterSpreadLib)
	t.Logf("Transient: %d", transientLib)

	// === GOPUS ===
	t.Log("")
	t.Log("=== GOPUS spread decode ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	// Skip to spread
	rdGo.DecodeBit(15) // silence
	rdGo.DecodeBit(1)  // postfilter
	transientGo := rdGo.DecodeBit(3)
	intraGo := rdGo.DecodeBit(3)
	celt.NewDecoder(1).DecodeCoarseEnergyWithDecoder(rdGo, nbBands, intraGo == 1, lm)

	// TF
	for i := 0; i < nbBands; i++ {
		rdGo.DecodeBit(1)
	}
	rdGo.DecodeBit(1) // tf_select

	tellBeforeSpreadGo := rdGo.Tell()
	rngBeforeSpreadGo := rdGo.Range()
	valBeforeSpreadGo := rdGo.Val()
	t.Logf("Tell before spread: %d", tellBeforeSpreadGo)
	t.Logf("Range before spread: 0x%08X", rngBeforeSpreadGo)
	t.Logf("Val before spread: 0x%08X", valBeforeSpreadGo)

	spreadGo := rdGo.DecodeICDF(spreadICDF, 5)
	tellAfterSpreadGo := rdGo.Tell()
	rngAfterSpreadGo := rdGo.Range()
	valAfterSpreadGo := rdGo.Val()
	t.Logf("Spread decoded: %d", spreadGo)
	t.Logf("Tell after spread: %d (used %d bits)", tellAfterSpreadGo, tellAfterSpreadGo-tellBeforeSpreadGo)
	t.Logf("Range after spread: 0x%08X", rngAfterSpreadGo)
	t.Logf("Val after spread: 0x%08X", valAfterSpreadGo)
	t.Logf("Transient: %d", transientGo)

	// Compare
	t.Log("")
	t.Log("=== Comparison ===")
	t.Logf("Tell before spread: lib=%d go=%d", tellBeforeSpreadLib, tellBeforeSpreadGo)
	t.Logf("Range before spread: lib=0x%08X go=0x%08X (match=%v)",
		rngBeforeSpreadLib, rngBeforeSpreadGo, rngBeforeSpreadLib == rngBeforeSpreadGo)
	t.Logf("Val before spread: lib=0x%08X go=0x%08X (match=%v)",
		valBeforeSpreadLib, valBeforeSpreadGo, valBeforeSpreadLib == valBeforeSpreadGo)
	t.Logf("Spread: lib=%d go=%d (match=%v)", spreadLib, spreadGo, spreadLib == spreadGo)

	// Let's also check what bytes have been output so far
	// Range decoder reads from the front, so bytes up to tell/8 are "consumed"
	t.Log("")
	t.Log("=== Bytes comparison ===")
	for i := 0; i <= 10; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %d: lib=0x%02X go=0x%02X%s", i, libPayload[i], goPacket[i], marker)
		}
	}

	// Let's trace the ENCODING side to see what's different
	t.Log("")
	t.Log("=== Re-encode with tracing ===")

	// Create fresh encoders and trace
	buf := make([]byte, 256)
	reLib := &rangecoding.Encoder{}
	reLib.Init(buf)
	reLib.Shrink(159)

	reGo := &rangecoding.Encoder{}
	reGo.Init(buf)
	reGo.Shrink(159)

	// Encode the same header bits
	reLib.EncodeBit(0, 15) // silence
	reLib.EncodeBit(0, 1)  // postfilter
	reLib.EncodeBit(1, 3)  // transient
	reLib.EncodeBit(0, 3)  // intra

	reGo.EncodeBit(0, 15) // silence
	reGo.EncodeBit(0, 1)  // postfilter
	reGo.EncodeBit(1, 3)  // transient
	reGo.EncodeBit(0, 3)  // intra

	t.Logf("After header: lib tell=%d go tell=%d", reLib.Tell(), reGo.Tell())
	t.Logf("After header: lib rng=0x%08X go rng=0x%08X", reLib.Range(), reGo.Range())

	// The key insight: the encoder state should be IDENTICAL at this point
	// If it's not, something is wrong in the header encoding
	if reLib.Range() != reGo.Range() {
		t.Log("ERROR: Range differs after header encoding!")
	}
	if reLib.Val() != reGo.Val() {
		t.Log("ERROR: Val differs after header encoding!")
	}
}
