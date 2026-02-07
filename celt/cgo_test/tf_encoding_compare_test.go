//go:build cgo_libopus
// +build cgo_libopus

// Package cgo provides TF encoding comparison tests between gopus and libopus.
package cgo

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/celt"
	"github.com/thesyncim/gopus/rangecoding"
)

// TestTFEncodingDetailedCompare performs a detailed byte-by-byte comparison
// focusing on the TF encoding and what comes before it.
func TestTFEncodingDetailedCompare(t *testing.T) {
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	t.Logf("gopus packet len=%d, libopus packet len=%d", len(goPacket), len(libPacket))

	// Decode headers from both packets
	libPayload := libPacket[1:] // Skip TOC byte
	goPayload := goPacket       // gopus encoder doesn't add TOC

	t.Log("\n=== Decoding gopus packet ===")
	goState := decodePacketState(t, goPayload, nbBands, lm, "gopus")

	t.Log("\n=== Decoding libopus packet ===")
	libState := decodePacketState(t, libPayload, nbBands, lm, "libopus")

	// Compare
	t.Log("\n=== Comparison ===")
	t.Logf("Transient: gopus=%v libopus=%v match=%v",
		goState.transient, libState.transient, goState.transient == libState.transient)
	t.Logf("Intra: gopus=%v libopus=%v match=%v",
		goState.intra, libState.intra, goState.intra == libState.intra)
	t.Logf("Tell after coarse energy: gopus=%d libopus=%d",
		goState.tellAfterCoarse, libState.tellAfterCoarse)

	// Compare coarse energies
	t.Log("\nCoarse energy comparison:")
	for i := 0; i < nbBands && i < 10; i++ {
		goE := "N/A"
		libE := "N/A"
		if i < len(goState.coarseEnergy) {
			goE = formatFloat(goState.coarseEnergy[i])
		}
		if i < len(libState.coarseEnergy) {
			libE = formatFloat(libState.coarseEnergy[i])
		}
		match := ""
		if i < len(goState.coarseEnergy) && i < len(libState.coarseEnergy) {
			if goState.coarseEnergy[i] == libState.coarseEnergy[i] {
				match = " MATCH"
			} else {
				match = " DIFFER"
			}
		}
		t.Logf("  Band %2d: gopus=%-10s libopus=%-10s%s", i, goE, libE, match)
	}

	// Compare TF
	t.Log("\nTF comparison:")
	tfMismatch := 0
	for i := 0; i < nbBands; i++ {
		if i < len(goState.tfRes) && i < len(libState.tfRes) {
			if goState.tfRes[i] != libState.tfRes[i] {
				tfMismatch++
				if tfMismatch <= 5 {
					t.Logf("  Band %d: gopus=%d libopus=%d DIFFER", i, goState.tfRes[i], libState.tfRes[i])
				}
			}
		}
	}
	if tfMismatch == 0 {
		t.Log("  TF values match for all bands")
	} else {
		t.Logf("  Total TF mismatches: %d", tfMismatch)
	}
}

type packetState struct {
	silence         int
	postfilter      int
	transient       bool
	intra           bool
	tellAfterFlags  int
	tellAfterCoarse int
	coarseEnergy    []float64
	tfRes           []int
}

func decodePacketState(t *testing.T, payload []byte, nbBands, lm int, name string) packetState {
	var state packetState
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	// Silence flag (logp=15)
	state.silence = rd.DecodeBit(15)
	t.Logf("[%s] silence=%d, tell=%d", name, state.silence, rd.Tell())

	// Postfilter flag (logp=1)
	state.postfilter = rd.DecodeBit(1)
	t.Logf("[%s] postfilter=%d, tell=%d", name, state.postfilter, rd.Tell())
	if state.postfilter == 1 {
		octave := int(rd.DecodeUniform(6))
		_ = rd.DecodeRawBits(uint(4 + octave))
		_ = rd.DecodeRawBits(3)
		_ = rd.DecodeICDF([]uint8{2, 1, 0}, 2)
	}

	// Transient flag (logp=3) - for LM>0
	if lm > 0 {
		state.transient = rd.DecodeBit(3) == 1
		t.Logf("[%s] transient=%v, tell=%d", name, state.transient, rd.Tell())
	}

	// Intra flag (logp=3)
	state.intra = rd.DecodeBit(3) == 1
	t.Logf("[%s] intra=%v, tell=%d", name, state.intra, rd.Tell())

	state.tellAfterFlags = rd.Tell()

	// Decode coarse energy
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	state.coarseEnergy = dec.DecodeCoarseEnergy(nbBands, state.intra, lm)
	state.tellAfterCoarse = rd.Tell()
	t.Logf("[%s] After coarse energy: tell=%d (byte ~%d.%d)",
		name, state.tellAfterCoarse, state.tellAfterCoarse/8, state.tellAfterCoarse%8)

	// Decode TF
	state.tfRes = make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, state.transient, state.tfRes, lm, rd)
	t.Logf("[%s] After TF: tell=%d (byte ~%d.%d)",
		name, rd.Tell(), rd.Tell()/8, rd.Tell()%8)

	return state
}

func formatFloat(f float64) string {
	return fmt.Sprintf("%.4f", f)
}

// TestPostTFEncoding traces the encoding after TF to find divergence point
func TestPostTFEncoding(t *testing.T) {
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	// Decode headers from both packets to get to the post-TF state
	t.Log("=== Decoding post-TF elements ===")

	// Decode gopus packet
	t.Log("\n--- gopus ---")
	decodePostTF(t, goPacket, nbBands, lm, len(goPacket)*8, "gopus")

	// Decode libopus packet (skip TOC)
	t.Log("\n--- libopus ---")
	decodePostTF(t, libPacket[1:], nbBands, lm, len(libPacket[1:])*8, "libopus")
}

// spreadICDF for decoding - MUST match tables.go
var spreadICDFDecode = []byte{25, 23, 2, 0}

// trimICDFDecode for decoding allocation trim
// IMPORTANT: Must use the full 11-element ICDF table (indices 0-10), not truncated
var trimICDFDecode = []byte{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}

func decodePostTF(t *testing.T, payload []byte, nbBands, lm, totalBits int, name string) {
	rd := &rangecoding.Decoder{}
	rd.Init(payload)

	// Decode header flags
	_ = rd.DecodeBit(15) // silence
	postfilter := rd.DecodeBit(1)
	if postfilter == 1 {
		octave := int(rd.DecodeUniform(6))
		_ = rd.DecodeRawBits(uint(4 + octave))
		_ = rd.DecodeRawBits(3)
		_ = rd.DecodeICDF([]uint8{2, 1, 0}, 2)
	}

	transient := false
	if lm > 0 {
		transient = rd.DecodeBit(3) == 1
	}
	intra := rd.DecodeBit(3) == 1

	t.Logf("[%s] transient=%v, intra=%v", name, transient, intra)

	// Decode coarse energy
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	_ = dec.DecodeCoarseEnergy(nbBands, intra, lm)
	tellAfterCoarse := rd.Tell()
	t.Logf("[%s] After coarse energy: tell=%d", name, tellAfterCoarse)

	// Decode TF
	tfRes := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transient, tfRes, lm, rd)
	tellAfterTF := rd.Tell()
	t.Logf("[%s] After TF: tell=%d (byte ~%.1f)", name, tellAfterTF, float64(tellAfterTF)/8)

	// Now decode what comes after TF
	// 1. Spread decision (ICDF)
	spread := rd.DecodeICDF(spreadICDFDecode, 5)
	tellAfterSpread := rd.Tell()
	t.Logf("[%s] spread=%d, tell=%d (byte ~%.1f)", name, spread, tellAfterSpread, float64(tellAfterSpread)/8)

	// 2. Dynamic allocation (need to decode the full dynalloc loop properly)
	// First band uses logp=6, if flag=1 then logp=1 for subsequent, until flag=0
	t.Logf("[%s] Decoding dynalloc for ALL bands:", name)
	dynallocLogp := 6
	for band := 0; band < nbBands; band++ {
		loopLogp := dynallocLogp
		boost := 0
		for j := 0; j < 10; j++ { // Max 10 iterations per band
			flag := rd.DecodeBit(uint(loopLogp))
			if flag == 0 {
				break
			}
			boost++
			loopLogp = 1 // Switch to logp=1 after first 1-bit
		}
		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		if boost > 0 || band < 5 {
			t.Logf("[%s]   band %d: boost=%d, tell=%d", name, band, boost, rd.Tell())
		}
	}
	tellAfterDynalloc := rd.Tell()
	t.Logf("[%s] After dynalloc (all %d bands): tell=%d (byte ~%.1f)", name, nbBands, tellAfterDynalloc, float64(tellAfterDynalloc)/8)

	// 3. Allocation trim (ICDF with ftb=7)
	allocTrim := rd.DecodeICDF(trimICDFDecode, 7)
	tellAfterTrim := rd.Tell()
	t.Logf("[%s] allocTrim=%d, tell=%d (byte ~%.1f)", name, allocTrim, tellAfterTrim, float64(tellAfterTrim)/8)

	// At this point we're at bit 65, which is byte 8.125
	// The remaining content is fine energy and PVQ data
	// Let's show the range encoder state
	t.Logf("[%s] Range decoder state: range=0x%08X, val=0x%08X",
		name, rd.Range(), rd.Val())

	// Show raw bytes around the divergence point (byte 9)
	t.Logf("[%s] Bytes 6-14: %02X", name, payload[6:minTF(15, len(payload))])
	t.Logf("[%s] Bytes 8-12: %02X (divergence area)", name, payload[8:minTF(13, len(payload))])
}

func minTF(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestTFBitstreamPosition checks where TF encoding starts and ends in the bitstream
func TestTFBitstreamPosition(t *testing.T) {
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

	libPacket, libLen := libEnc.EncodeFloat(pcm32, frameSize)
	if libLen <= 0 {
		t.Fatalf("libopus encode failed: length=%d", libLen)
	}

	// Parse libopus packet to get bit positions
	libPayload := libPacket[1:]
	rd := &rangecoding.Decoder{}
	rd.Init(libPayload)

	// Decode header flags
	_ = rd.DecodeBit(15) // silence
	_ = rd.DecodeBit(1)  // postfilter

	transient := false
	if lm > 0 {
		transient = rd.DecodeBit(3) == 1
	}
	intra := rd.DecodeBit(3) == 1

	tellAfterFlags := rd.Tell()
	t.Logf("After flags: tell=%d", tellAfterFlags)

	// Decode coarse energy
	dec := celt.NewDecoder(1)
	dec.SetRangeDecoder(rd)
	_ = dec.DecodeCoarseEnergy(nbBands, intra, lm)
	tellAfterCoarse := rd.Tell()
	t.Logf("After coarse energy: tell=%d (byte ~%.1f)", tellAfterCoarse, float64(tellAfterCoarse)/8)

	// Decode TF
	tfRes := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transient, tfRes, lm, rd)
	tellAfterTF := rd.Tell()
	t.Logf("After TF: tell=%d (byte ~%.1f)", tellAfterTF, float64(tellAfterTF)/8)
	t.Logf("TF uses %d bits", tellAfterTF-tellAfterCoarse)

	// Show TF values
	t.Log("\nTF values from libopus packet:")
	for i := 0; i < nbBands && i < 21; i++ {
		t.Logf("  Band %2d: tf_change=%d", i, tfRes[i])
	}
}
